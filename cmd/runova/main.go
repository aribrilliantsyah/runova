// Command runova serves the Runova panel and provides admin maintenance commands.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"runova/internal/api"
	"runova/internal/apps"
	"runova/internal/caddy"
	"runova/internal/config"
	"runova/internal/dbs"
	"runova/internal/secret"
	"runova/internal/store"
	"runova/web"
)

const usage = `Penggunaan: runova <perintah>

  serve                          jalankan panel
  version                        cetak versi
  check                          periksa Docker, Caddy, PostgreSQL, kunci, dan izin folder
  admin create <username>        buat admin dengan password acak
  admin reset-password <user>    buat password acak baru
  admin exists                   keluar 0 bila sudah ada admin, 1 bila belum
  keygen                         buat kunci enkripsi rahasia
  panel set --domain <domain>    alamat panel di domain (HTTPS otomatis)
  panel set --port <port>        alamat panel di IP:port (HTTPS)
  panel set --public-ip <ip>     IP publik server (boleh bersama --domain/--port)
`

// version is set at build time with -ldflags "-X main.version=<versi>".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cfg := config.Load(getenv)
	switch args[0] {
	case "serve":
		return serve(cfg, stderr)
	case "version":
		fmt.Fprintf(stdout, "runova %s\n", version)
		return 0
	case "check":
		return check(cfg, stdout)
	case "admin":
		return admin(cfg, args[1:], stdout, stderr)
	case "panel":
		return panel(cfg, args[1:], stdout, stderr)
	case "keygen":
		if err := secret.GenerateKey(cfg.SecretKeyPath); err != nil {
			fmt.Fprintf(stderr, "Gagal membuat kunci: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Kunci dibuat: %s\n", cfg.SecretKeyPath)
		return 0
	default:
		fmt.Fprint(stderr, usage)
		return 2
	}
}

func serve(cfg config.Config, stderr io.Writer) int {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	slog.SetDefault(log)

	if err := errors.Join(checkFakeBackends(cfg), checkDevMode(cfg)); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	box, err := secret.LoadKey(cfg.SecretKeyPath)
	if err != nil {
		fmt.Fprintf(stderr, "Tidak bisa memuat kunci rahasia: %v\nBuat dengan `runova keygen`.\n", err)
		return 1
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		fmt.Fprintf(stderr, "Tidak bisa membuka database: %v\n", err)
		return 1
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	engine, dockerCheck := newEngine(cfg)
	caddyClient, caddyCheck := newCaddyLoader(cfg)
	if err := engine.Ping(ctx); err != nil {
		fmt.Fprintf(stderr, "Docker tidak bisa diakses: %v\n", err)
		return 1
	}
	panelUpstream, err := apps.PanelUpstream(cfg.Listen)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	syncer := &caddy.Syncer{
		Client: caddyClient,
		State: func(ctx context.Context) (caddy.State, error) {
			state, err := apps.CaddyState(ctx, st, panelUpstream)
			state.AdminListen = cfg.CaddyAdminListen()
			return state, err
		},
		OnError: func(err error) { log.Error("sync caddy", "err", err) },
	}
	svc := &apps.Service{Store: st, Engine: engine, Box: box, DataDir: cfg.DataDir, Caddy: syncer}
	if cfg.DevChmod {
		devChmod(svc)
	}

	var (
		dbSvc   *dbs.Service
		closePG func()
	)
	if cfg.FakeBackends {
		fakeApps(svc)
		dbSvc, closePG, err = fakeDatabases(ctx, cfg, st, box)
	} else {
		dbSvc, closePG, err = openDatabases(ctx, cfg, st, box)
	}
	if err != nil {
		fmt.Fprintf(stderr, "Konfigurasi PostgreSQL tidak valid: %v\n", err)
		return 1
	}
	defer closePG()
	svc.DBEnv = dbSvc.AppEnv

	go func() {
		if err := svc.Monitor(ctx); err != nil && ctx.Err() == nil {
			log.Error("docker monitor stopped", "err", err)
		}
	}()
	go syncer.Run(ctx)
	go svc.WatchDomains(ctx)
	go cleanupLoop(ctx, st, cfg.DataDir)
	go dbSvc.RunSchedule(ctx)

	var deploys sync.WaitGroup
	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: api.New(api.Config{
			Store: st, Now: time.Now, DataDir: cfg.DataDir, Apps: svc, DBs: dbSvc, Context: ctx, Deploys: &deploys,
			Static: web.FS,
			Health: api.Health{Docker: dockerCheck, Caddy: caddyCheck, Postgres: postgresCheck(dbSvc)},
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.Listen, "db", cfg.DBPath())

	select {
	case err := <-errc:
		log.Error("server stopped", "err", err)
		return 1
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code := 0
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("shutdown", "err", err)
		code = 1
	}
	// ctx is canceled, so running deploys fail fast; give them time to remove
	// the containers they created and record the failure.
	if !waitTimeout(&deploys, deployDrain) {
		log.Warn("background deploys still running; reconcile cleans up at next start", "after", deployDrain)
	}
	return code
}

// deployDrain is how long shutdown waits for background deploys.
const deployDrain = 30 * time.Second

// waitTimeout waits for wg and reports whether it finished within d.
func waitTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// cleanupLoop drops expired sessions, old login failures, expired uploads and
// orphaned upload folders at start and then hourly.
func cleanupLoop(ctx context.Context, st *store.Store, dataDir string) {
	clean := func() {
		now := time.Now()
		if err := st.DeleteExpiredSessions(ctx, now.UnixMilli()); err != nil && ctx.Err() == nil {
			slog.Error("delete expired sessions", "err", err)
		}
		if err := st.PruneLoginFailures(ctx, now.Add(-24*time.Hour).UnixMilli()); err != nil && ctx.Err() == nil {
			slog.Error("prune login failures", "err", err)
		}
		if err := api.CleanupUploads(ctx, st, now); err != nil && ctx.Err() == nil {
			slog.Error("cleanup uploads", "err", err)
		}
		if err := api.SweepUploadDirs(dataDir, st, now); err != nil && ctx.Err() == nil {
			slog.Error("sweep upload dirs", "err", err)
		}
	}
	clean()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			clean()
		}
	}
}
