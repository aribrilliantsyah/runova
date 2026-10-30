package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"runova/internal/api"
	"runova/internal/config"
	"runova/internal/pg"
	"runova/internal/secret"
)

const checkTimeout = 5 * time.Second

// dockerInitDirs are where dockerd looks for docker-init besides $PATH.
var dockerInitDirs = []string{"/usr/local/libexec/docker", "/usr/libexec/docker", "/usr/local/lib/docker", "/usr/lib/docker"}

// check handles `runova check`: it probes everything Runova needs, prints one
// "ok <name> <detail>" or "fail <name>: <reason>" line per probe, and exits 1
// when any probe failed.
func check(cfg config.Config, stdout io.Writer) int {
	_, dockerCheck := newEngine(cfg)
	_, caddyCheck := newCaddyLoader(cfg)

	probes := []struct {
		name string
		run  api.Check
	}{
		{"docker", dockerCheck},
		{"docker-init", func(context.Context) (string, error) { return findDockerInit(cfg) }},
		{"caddy", caddyCheck},
		{"postgres", func(ctx context.Context) (string, error) { return postgresVersion(ctx, cfg) }},
		{"secret", func(context.Context) (string, error) { return cfg.SecretKeyPath, loadKey(cfg) }},
		{"data", func(context.Context) (string, error) { return cfg.DataDir, writable(cfg.DataDir) }},
		{"pg_hba", func(context.Context) (string, error) { return hbaDir(cfg) }},
	}
	code := 0
	for _, p := range probes {
		ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
		detail, err := p.run(ctx)
		cancel()
		if err != nil {
			fmt.Fprintf(stdout, "fail %s: %s\n", p.name, oneLine(err.Error()))
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "ok %s %s\n", p.name, oneLine(detail))
	}
	return code
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// findDockerInit locates the docker-init binary that containers with Init: true need.
func findDockerInit(cfg config.Config) (string, error) {
	if cfg.FakeBackends {
		return "fake", nil
	}
	if p, err := exec.LookPath("docker-init"); err == nil {
		return p, nil
	}
	for _, dir := range dockerInitDirs {
		p := filepath.Join(dir, "docker-init")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("docker-init tidak ditemukan (paket docker.io menyediakannya)")
}

func postgresVersion(ctx context.Context, cfg config.Config) (string, error) {
	if cfg.FakeBackends {
		return "fake", nil
	}
	admin, err := pg.Open(ctx, cfg.PGDSN)
	if err != nil {
		return "", err
	}
	defer admin.Close()
	return admin.Version(ctx)
}

func loadKey(cfg config.Config) error {
	_, err := secret.LoadKey(cfg.SecretKeyPath)
	return err
}

// writable proves dir exists and the current user can create files in it.
func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".runova-check-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

// hbaDir checks the folder Runova writes its pg_hba file into.
func hbaDir(cfg config.Config) (string, error) {
	if cfg.FakeBackends {
		return "fake", nil
	}
	dir := filepath.Dir(cfg.PGHBAPath)
	return dir, writable(dir)
}
