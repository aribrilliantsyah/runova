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
	"syscall"
	"time"

	"runova/internal/api"
	"runova/internal/config"
	"runova/internal/secret"
	"runova/internal/store"
)

const usage = `Penggunaan: runova <perintah>

  serve                          jalankan panel
  admin create <username>        buat admin dengan password acak
  admin reset-password <user>    buat password acak baru
  keygen                         buat kunci enkripsi rahasia
`

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
	case "admin":
		return admin(cfg, args[1:], stdout, stderr)
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

	if _, err := secret.LoadKey(cfg.SecretKeyPath); err != nil {
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

	go cleanupLoop(ctx, st)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.New(api.Config{Store: st, Now: time.Now}),
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
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("shutdown", "err", err)
		return 1
	}
	return 0
}

// cleanupLoop drops expired sessions and old login failures at start and then hourly.
func cleanupLoop(ctx context.Context, st *store.Store) {
	clean := func() {
		now := time.Now()
		if err := st.DeleteExpiredSessions(ctx, now.UnixMilli()); err != nil && ctx.Err() == nil {
			slog.Error("delete expired sessions", "err", err)
		}
		if err := st.PruneLoginFailures(ctx, now.Add(-24*time.Hour).UnixMilli()); err != nil && ctx.Err() == nil {
			slog.Error("prune login failures", "err", err)
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
