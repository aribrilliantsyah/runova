package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"runova/internal/api"
	"runova/internal/apps"
	"runova/internal/caddy"
	"runova/internal/config"
	"runova/internal/dbs"
	"runova/internal/docker"
	"runova/internal/docker/dockertest"
	"runova/internal/pg"
	"runova/internal/pg/pgfake"
	"runova/internal/secret"
	"runova/internal/store"
)

// productionData is the data directory of a real installation; fake backends never run on it.
const productionData = "/var/lib/runova"

// errFakeOnProduction is returned when RUNOVA_FAKE_BACKENDS=1 meets the production data directory.
var errFakeOnProduction = errors.New("RUNOVA_FAKE_BACKENDS=1 ditolak untuk RUNOVA_DATA=" + productionData)

func checkFakeBackends(cfg config.Config) error {
	if cfg.FakeBackends && filepath.Clean(cfg.DataDir) == productionData {
		return errFakeOnProduction
	}
	return nil
}

func ok(detail string) api.Check {
	return func(context.Context) (string, error) { return detail, nil }
}

// newEngine returns the Docker engine and its health probe.
func newEngine(cfg config.Config) (docker.Engine, api.Check) {
	if cfg.FakeBackends {
		return newFakeEngine(), ok("fake")
	}
	c := docker.New(cfg.DockerHost)
	return c, func(ctx context.Context) (string, error) { return c.Version(ctx) }
}

// newCaddyLoader returns Caddy's admin client and its health probe.
func newCaddyLoader(cfg config.Config) (caddy.Loader, api.Check) {
	if cfg.FakeBackends {
		return &fakeLoader{}, ok("")
	}
	c := caddy.NewClient(cfg.CaddyAdmin)
	return c, func(ctx context.Context) (string, error) {
		_, err := c.Config(ctx)
		return "", err
	}
}

// postgresCheck probes PostgreSQL through the service's admin connection.
func postgresCheck(svc *dbs.Service) api.Check {
	return func(ctx context.Context) (string, error) {
		if v, ok := svc.PG.(interface {
			Version(context.Context) (string, error)
		}); ok {
			return v.Version(ctx)
		}
		return "fake", svc.PG.Ping(ctx)
	}
}

// fakeLoader keeps the last configuration it was given, like a Caddy that never restarts.
type fakeLoader struct {
	mu  sync.Mutex
	cfg []byte
}

func (f *fakeLoader) Load(_ context.Context, cfg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg = append([]byte(nil), cfg...)
	return nil
}

func (f *fakeLoader) Config(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.cfg...), nil
}

// newFakeEngine is an in-memory Docker whose containers log a few lines when
// they start and answer the terminal with a shell prompt.
func newFakeEngine() *dockertest.Fake {
	f := dockertest.New()
	f.StatsResult = docker.Stats{MemoryBytes: 120 << 20, MemoryLimit: 512 << 20, CPUPercent: 18}
	f.OnStart = func(c dockertest.Container) {
		now := time.Now()
		lines := []docker.LogLine{
			{Stream: "stdout", Time: now, Text: "runova  menjalankan " + c.Spec.Image},
			{Stream: "stdout", Time: now, Text: fmt.Sprintf("Listening on port %d", c.Spec.InternalPort)},
			{Stream: "stderr", Time: now, Text: "WARN  slow connection acquire 812ms"},
			{Stream: "stderr", Time: now, Text: "ERROR mailer: SMTP timed out"},
		}
		for _, e := range c.Spec.Env {
			if v, ok := strings.CutPrefix(e, "LOG_ECHO="); ok {
				lines = append(lines, docker.LogLine{Stream: "stdout", Time: now, Text: v})
			}
		}
		f.Update(c.ID, func(c *dockertest.Container) { c.Logs = append(c.Logs, lines...) })
	}
	f.ExecHandler = func(_ string, _ []string, conn io.ReadWriteCloser) {
		defer conn.Close()
		conn.Write([]byte("$ "))
		buf := make([]byte, 1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				conn.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}
	return f
}

// fakeApps adjusts the app service so deploys against the fake engine finish:
// health probes succeed, ownership changes are skipped and domains starting
// with "pending." never get a certificate.
func fakeApps(svc *apps.Service) {
	svc.Chown = func(string, int) error { return nil }
	svc.Dial = func(string, string, time.Duration) (net.Conn, error) {
		a, b := net.Pipe()
		b.Close()
		return a, nil
	}
	svc.Probe = func(_ context.Context, host string) (bool, string) {
		if strings.HasPrefix(host, "pending.") {
			return false, "Caddy belum punya sertifikat untuk domain ini"
		}
		return true, ""
	}
}

// fakeDatabases builds the database service over an in-memory PostgreSQL.
// pg_dump and pg_restore become scripts that write and accept a stub dump.
func fakeDatabases(ctx context.Context, cfg config.Config, st *store.Store, box *secret.Box) (*dbs.Service, func(), error) {
	bin := filepath.Join(cfg.DataDir, "fakebin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return nil, nil, err
	}
	dump := filepath.Join(bin, "pg_dump")
	restore := filepath.Join(bin, "pg_restore")
	if err := os.WriteFile(dump, []byte("#!/bin/sh\nprintf 'PGDMP fake dump\\n'\n"), 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(restore, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		return nil, nil, err
	}
	var admin pg.Admin = pgfake.New()
	svc := &dbs.Service{
		Store: st, PG: admin, Box: box, BackupDir: cfg.BackupDir(),
		HBAPath: filepath.Join(cfg.DataDir, "pg_hba.conf"), PGDump: dump, PGRestore: restore,
	}
	// Like the real startup: users that exist before this process (the installer's admin) get their roles.
	if err := svc.ReconcileRoles(ctx); err != nil {
		return nil, nil, err
	}
	return svc, func() {}, nil
}
