package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"runova/internal/config"
	"runova/internal/update"
)

// exitUpdateAvailable is what `runova update --check` returns when a newer release exists.
const exitUpdateAvailable = 10

const downloadLimit = 1 << 20

// updateDeps are the things `runova update` takes from the machine it runs on.
type updateDeps struct {
	IsRoot  func() bool
	Console console
	Exec    execFunc
	Version string
}

func prodUpdateDeps() updateDeps {
	return updateDeps{IsRoot: func() bool { return os.Geteuid() == 0 }, Console: stdConsole(), Exec: osExec, Version: version}
}

// updateCmd handles `runova update [--version X.Y.Z] [--check] [--yes]`. It
// does not install anything itself: it fetches get.sh of the target release,
// verifies it against that release's SHA256SUMS and runs it, so there is a
// single installer path (get.sh -> install.sh).
func updateCmd(ctx context.Context, cfg config.Config, args []string, d updateDeps, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	wanted := fs.String("version", "", "")
	checkOnly := fs.Bool("check", false, "")
	yes := fs.Bool("yes", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || (*checkOnly && *wanted != "") {
		fmt.Fprint(stderr, usage)
		return 2
	}
	src := update.Source{GitHub: cfg.GitHub, Repo: cfg.Repo}
	installed := strings.TrimPrefix(d.Version, "v")

	if *checkOnly {
		return checkForUpdate(ctx, src, installed, stdout, stderr)
	}
	if !d.IsRoot() {
		fmt.Fprintln(stderr, "Jalankan dengan sudo: sudo runova update")
		return 1
	}

	target := strings.TrimPrefix(*wanted, "v")
	if *wanted != "" {
		if !update.Valid(target) {
			fmt.Fprintf(stderr, "Versi tidak valid: %s (contoh: --version 0.3.0)\n", *wanted)
			return 1
		}
		if cmp, ok := update.Compare(target, installed); ok && cmp < 0 {
			fmt.Fprintf(stderr, "Versi %s lebih lama dari yang terpasang (%s). Untuk kembali ke versi sebelumnya jalankan: sudo runova rollback\n", target, installed)
			return 1
		}
	} else {
		latest, err := src.Latest(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "Tidak bisa memeriksa rilis terbaru: %v\n", err)
			return 1
		}
		cmp, ok := update.Compare(latest, installed)
		if !ok {
			fmt.Fprintf(stderr, "Versi terpasang (%s) tidak bisa dibandingkan. Pilih versi sendiri: sudo runova update --version %s\n", installed, latest)
			return 1
		}
		if cmp <= 0 {
			fmt.Fprintf(stdout, "Runova %s sudah versi terbaru\n", installed)
			return 0
		}
		target = latest
	}

	fmt.Fprintf(stdout, "Runova %s -> %s\nCatatan rilis: %s\n", installed, target, src.ReleaseURL(target))
	ok, err := d.Console.confirm(stdout, *yes)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintln(stderr, "Dibatalkan")
		return 1
	}

	dir, err := os.MkdirTemp("", "runova-update-")
	if err != nil {
		fmt.Fprintf(stderr, "Tidak bisa membuat folder sementara: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	script, err := fetchInstaller(ctx, src, target, dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	err = d.Exec(ctx, d.Console.In, stdout, stderr, "bash", script, "--version", target, "--yes")
	if err != nil {
		fmt.Fprintf(stderr, "Pembaruan gagal: %v\n", err)
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return exit.ExitCode()
		}
		return 1
	}
	return 0
}

func checkForUpdate(ctx context.Context, src update.Source, installed string, stdout, stderr io.Writer) int {
	latest, err := src.Latest(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "Tidak bisa memeriksa rilis terbaru: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Terpasang: %s\nTerbaru  : %s\n", installed, latest)
	if cmp, ok := update.Compare(latest, installed); ok && cmp > 0 {
		fmt.Fprintf(stdout, "Ada pembaruan. Catatan rilis: %s\nPerbarui dengan: sudo runova update\n", src.ReleaseURL(latest))
		return exitUpdateAvailable
	}
	fmt.Fprintln(stdout, "Sudah versi terbaru")
	return 0
}

// fetchInstaller downloads get.sh of the release into dir and returns its
// path, after checking it against the release's SHA256SUMS.
func fetchInstaller(ctx context.Context, src update.Source, version, dir string) (string, error) {
	script, err := httpGet(ctx, src.AssetURL(version, "get.sh"))
	if err != nil {
		return "", fmt.Errorf("Gagal mengunduh get.sh rilis %s: %w", version, err)
	}
	sums, err := httpGet(ctx, src.AssetURL(version, "SHA256SUMS"))
	if err != nil {
		return "", fmt.Errorf("Gagal mengunduh SHA256SUMS rilis %s: %w", version, err)
	}
	want, ok := checksumOf(sums, "get.sh")
	if !ok {
		return "", fmt.Errorf("SHA256SUMS rilis %s tidak memuat get.sh; pembaruan dibatalkan", version)
	}
	sum := sha256.Sum256(script)
	if hex.EncodeToString(sum[:]) != want {
		return "", errors.New("Checksum get.sh tidak cocok; unduhan rusak atau diubah, pembaruan dibatalkan")
	}
	path := filepath.Join(dir, "get.sh")
	if err := os.WriteFile(path, script, 0o700); err != nil {
		return "", fmt.Errorf("Tidak bisa menyimpan get.sh: %w", err)
	}
	return path, nil
}

// checksumOf finds name in sha256sum output ("<hex>  <name>").
func checksumOf(sums []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		sum, file, found := strings.Cut(sc.Text(), " ")
		if found && strings.TrimLeft(file, " *") == name && len(sum) == sha256.Size*2 {
			return strings.ToLower(sum), true
		}
	}
	return "", false
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, downloadLimit))
}
