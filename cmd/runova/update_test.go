package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"runova/internal/config"
)

const fakeGetSh = "#!/usr/bin/env bash\necho installing\n"

type execCall struct {
	name   string
	args   []string
	script string
}

type updateRig struct {
	cfg     config.Config
	calls   []execCall
	execErr error
	root    bool
	tty     bool
	input   string
	sums    func(script string) string
	getSh   string
	latest  string
}

// newUpdateRig serves a GitHub-like release site for repo me/runova.
func newUpdateRig(t *testing.T, latest string) *updateRig {
	t.Helper()
	r := &updateRig{root: true, getSh: fakeGetSh, latest: latest}
	r.sums = func(script string) string {
		sum := sha256.Sum256([]byte(script))
		return hex.EncodeToString(sum[:]) + "  get.sh\n" + strings.Repeat("0", 64) + "  runova-x-linux-amd64.tar.gz\n"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/me/runova/releases/latest", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/me/runova/releases/tag/v"+r.latest, http.StatusFound)
	})
	mux.HandleFunc("/me/runova/releases/tag/", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("/me/runova/releases/download/", func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/get.sh") && r.getSh != "":
			io.WriteString(w, r.getSh)
		case strings.HasSuffix(req.URL.Path, "/SHA256SUMS"):
			io.WriteString(w, r.sums(r.getSh))
		default:
			http.NotFound(w, req)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	r.cfg = config.Config{GitHub: srv.URL, Repo: "me/runova"}
	return r
}

func (r *updateRig) run(installed string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	d := updateDeps{
		IsRoot:  func() bool { return r.root },
		Console: console{In: strings.NewReader(r.input), Interactive: r.tty},
		Version: installed,
		Exec: func(_ context.Context, _ io.Reader, _, _ io.Writer, name string, a ...string) error {
			c := execCall{name: name, args: a}
			if len(a) > 0 {
				if b, err := os.ReadFile(a[0]); err == nil {
					c.script = string(b)
				}
			}
			r.calls = append(r.calls, c)
			return r.execErr
		},
	}
	code := updateCmd(context.Background(), r.cfg, args, d, &out, &errb)
	return code, out.String(), errb.String()
}

func TestUpdateCheckExitCodes(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	r.root = false // --check needs no root
	for _, tc := range []struct {
		installed string
		want      int
		out       string
	}{
		{"0.2.0", 10, "Terbaru  : 0.3.0"},
		{"0.3.0", 0, "Sudah versi terbaru"},
		{"0.4.0", 0, "Sudah versi terbaru"},
		{"dev", 0, "Terbaru  : 0.3.0"},
	} {
		code, out, errOut := r.run(tc.installed, "--check")
		if code != tc.want || !strings.Contains(out, tc.out) || !strings.Contains(out, "Terpasang: "+tc.installed) {
			t.Errorf("installed %s: code %d, out %q, err %q", tc.installed, code, out, errOut)
		}
	}
	if len(r.calls) != 0 {
		t.Fatalf("--check ran %v", r.calls)
	}

	r.cfg.GitHub = "http://127.0.0.1:1"
	if code, _, errOut := r.run("0.2.0", "--check"); code != 1 || !strings.Contains(errOut, "Tidak bisa memeriksa") {
		t.Fatalf("unreachable: code %d, err %q", code, errOut)
	}
}

func TestUpdateCheckDevBuildClaimsNoUpdate(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	if code, _, _ := r.run("dev", "--check"); code != 0 {
		t.Fatalf("code %d: an unparsable installed version must not claim an update", code)
	}
}

func TestUpdateRequiresRoot(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	r.root = false
	code, _, errOut := r.run("0.2.0", "--yes")
	if code != 1 || !strings.Contains(errOut, "Jalankan dengan sudo: sudo runova update") || len(r.calls) != 0 {
		t.Fatalf("code %d, err %q, calls %v", code, errOut, r.calls)
	}
}

func TestUpdateAlreadyLatest(t *testing.T) {
	r := newUpdateRig(t, "0.2.0")
	code, out, _ := r.run("0.2.0", "--yes")
	if code != 0 || !strings.Contains(out, "Runova 0.2.0 sudah versi terbaru") || len(r.calls) != 0 {
		t.Fatalf("code %d, out %q, calls %v", code, out, r.calls)
	}
}

func TestUpdateRunsVerifiedGetSh(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	code, out, errOut := r.run("0.2.0", "--yes")
	if code != 0 {
		t.Fatalf("code %d, err %q", code, errOut)
	}
	if len(r.calls) != 1 {
		t.Fatalf("calls = %v", r.calls)
	}
	c := r.calls[0]
	if c.name != "bash" || len(c.args) != 4 || c.args[1] != "--version" || c.args[2] != "0.3.0" || c.args[3] != "--yes" || c.script != fakeGetSh {
		t.Fatalf("exec = %+v", c)
	}
	if !strings.Contains(out, "0.2.0 -> 0.3.0") || !strings.Contains(out, r.cfg.GitHub+"/me/runova/releases/tag/v0.3.0") {
		t.Fatalf("summary missing versions or release notes link: %q", out)
	}
	if _, err := os.Stat(c.args[0]); !os.IsNotExist(err) {
		t.Fatalf("temporary get.sh left behind: %v", err)
	}
}

func TestUpdateExplicitVersion(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	if code, _, errOut := r.run("0.2.0", "--version", "v0.2.5", "--yes"); code != 0 || r.calls[0].args[2] != "0.2.5" {
		t.Fatalf("code %d, err %q, calls %v", code, errOut, r.calls)
	}
	// Re-installing the installed version is allowed (repair).
	if code, _, errOut := r.run("0.2.0", "--version", "0.2.0", "--yes"); code != 0 {
		t.Fatalf("code %d, err %q", code, errOut)
	}
}

func TestUpdateRefusesOlderVersion(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	code, _, errOut := r.run("0.2.0", "--version", "0.1.0", "--yes")
	if code != 1 || !strings.Contains(errOut, "runova rollback") || len(r.calls) != 0 {
		t.Fatalf("code %d, err %q, calls %v", code, errOut, r.calls)
	}
}

func TestUpdateRefusesInvalidVersion(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	for _, v := range []string{"latest", "1.2", "../x"} {
		if code, _, errOut := r.run("0.2.0", "--version", v, "--yes"); code != 1 || !strings.Contains(errOut, "Versi tidak valid") {
			t.Errorf("%s: code %d, err %q", v, code, errOut)
		}
	}
	if len(r.calls) != 0 {
		t.Fatalf("calls = %v", r.calls)
	}
}

func TestUpdateWithoutLatestNeedsExplicitVersionForDevBuild(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	code, _, errOut := r.run("dev", "--yes")
	if code != 1 || !strings.Contains(errOut, "--version 0.3.0") || len(r.calls) != 0 {
		t.Fatalf("code %d, err %q, calls %v", code, errOut, r.calls)
	}
}

func TestUpdateConfirmation(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	if code, _, errOut := r.run("0.2.0"); code != 1 || !strings.Contains(errOut, "--yes") || len(r.calls) != 0 {
		t.Fatalf("no terminal: code %d, err %q, calls %v", code, errOut, r.calls)
	}

	r.tty, r.input = true, "n\n"
	if code, out, _ := r.run("0.2.0"); code != 1 || !strings.Contains(out, "Lanjutkan? [y/N]") || len(r.calls) != 0 {
		t.Fatalf("answer n: code %d, out %q, calls %v", code, out, r.calls)
	}
	r.input = "\n"
	if code, _, _ := r.run("0.2.0"); code != 1 || len(r.calls) != 0 {
		t.Fatalf("empty answer: code %d, calls %v", code, r.calls)
	}

	r.input = "y\n"
	if code, _, errOut := r.run("0.2.0"); code != 0 || len(r.calls) != 1 {
		t.Fatalf("answer y: code %d, err %q, calls %v", code, errOut, r.calls)
	}
}

func TestUpdateRefusesBadChecksum(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	r.sums = func(string) string { return strings.Repeat("a", 64) + "  get.sh\n" }
	code, _, errOut := r.run("0.2.0", "--yes")
	if code != 1 || !strings.Contains(errOut, "Checksum get.sh tidak cocok") || len(r.calls) != 0 {
		t.Fatalf("code %d, err %q, calls %v", code, errOut, r.calls)
	}
}

func TestUpdateRefusesSumsWithoutGetSh(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	r.sums = func(string) string { return strings.Repeat("a", 64) + "  runova-0.3.0-linux-amd64.tar.gz\n" }
	code, _, errOut := r.run("0.2.0", "--yes")
	if code != 1 || !strings.Contains(errOut, "tidak memuat get.sh") || len(r.calls) != 0 {
		t.Fatalf("code %d, err %q, calls %v", code, errOut, r.calls)
	}
}

func TestUpdateMissingGetSh(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	r.getSh = ""
	code, _, errOut := r.run("0.2.0", "--yes")
	if code != 1 || !strings.Contains(errOut, "Gagal mengunduh get.sh") || len(r.calls) != 0 {
		t.Fatalf("code %d, err %q, calls %v", code, errOut, r.calls)
	}
}

type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitError) ExitCode() int { return int(e) }

func TestUpdatePassesInstallerFailureOn(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	r.execErr = exitError(3)
	if code, _, errOut := r.run("0.2.0", "--yes"); code != 3 || !strings.Contains(errOut, "Pembaruan gagal") {
		t.Fatalf("code %d, err %q", code, errOut)
	}
	r.execErr = errors.New("bash: not found")
	if code, _, _ := r.run("0.2.0", "--yes"); code != 1 {
		t.Fatalf("code %d", code)
	}
}

func TestUpdateUsage(t *testing.T) {
	r := newUpdateRig(t, "0.3.0")
	for _, args := range [][]string{{"extra"}, {"--nope"}, {"--check", "--version", "0.3.0"}} {
		if code, _, errOut := r.run("0.2.0", args...); code != 2 || !strings.Contains(errOut, "Penggunaan:") {
			t.Errorf("%v: code %d, err %q", args, code, errOut)
		}
	}
}
