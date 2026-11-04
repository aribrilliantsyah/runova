package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"runova/internal/config"
	"runova/internal/store"
)

func TestFakeBackendsReportAnUpdate(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chk := newUpdateChecker(config.Config{FakeBackends: true, GitHub: "https://github.com", Repo: "me/runova"}, st)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go chk.Run(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for !chk.Status(ctx).UpdateAvailable && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := chk.Status(ctx)
	if !got.UpdateAvailable || got.Current != "0.2.0" || got.Latest != "0.3.0" ||
		got.ReleaseURL != "https://github.com/me/runova/releases/tag/v0.3.0" {
		t.Fatalf("status = %+v", got)
	}

	s, _ := st.Settings(ctx)
	s.UpdateCheck = false
	if err := st.SetSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if got := chk.Status(ctx); got.Enabled || got.UpdateAvailable {
		t.Fatalf("with update_check off: %+v", got)
	}
}

func TestDevBuildNeverChecks(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chk := newUpdateChecker(config.Config{GitHub: "http://127.0.0.1:1", Repo: "me/runova"}, st)
	if chk.Current != version {
		t.Fatalf("Current = %q, want the build version %q", chk.Current, version)
	}
	chk.Check(context.Background())
	if got := chk.Status(context.Background()); got.Latest != "" || got.UpdateAvailable {
		t.Fatalf("status = %+v", got)
	}
}
