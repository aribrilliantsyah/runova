package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"runova/internal/config"
	"runova/internal/store"
)

func TestDevSeedOnlyWithFakeBackends(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DevSeed: true}
	if err := checkDevSeed(cfg); !errors.Is(err, errSeedNeedsFakes) {
		t.Fatalf("seed without fakes: %v", err)
	}
	cfg.FakeBackends = true
	if err := checkDevSeed(cfg); err != nil {
		t.Fatalf("seed with fakes: %v", err)
	}
	if err := checkDevSeed(config.Config{DataDir: "/var/lib/runova", DevSeed: true, FakeBackends: true}); err == nil {
		t.Fatal("seed on production data must be refused")
	}
	if !config.Load(func(k string) string { return map[string]string{"RUNOVA_DEV_SEED": "1"}[k] }).DevSeed {
		t.Fatal("RUNOVA_DEV_SEED=1 should enable the seed")
	}
}

func TestDevSeedFillsListsOnce(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := devSeed(ctx, st, 1_800_000_000_000); err != nil {
		t.Fatal(err)
	}
	users, _ := st.UserList(ctx, store.UserListFilter{})
	if users.Total < 60 || users.Roles.Admin < 2 {
		t.Fatalf("seeded %d users (%d admins), want ≥60", users.Total, users.Roles.Admin)
	}
	acts, _ := st.SearchActivity(ctx, store.ActivitySearch{WithActors: true})
	if acts.Total < 120 || len(acts.Facets.Actor) < 10 || len(acts.Facets.Action) < 10 || acts.Facets.Outcome.Failed == 0 {
		t.Fatalf("seeded activity = %d entries, %d actors, %d actions, %d failed", acts.Total, len(acts.Facets.Actor), len(acts.Facets.Action), acts.Facets.Outcome.Failed)
	}
	withApps := 0
	for _, u := range users.Items {
		if u.Usage.Apps > 0 {
			withApps++
		}
	}
	if withApps == 0 {
		t.Fatal("no seeded user has apps")
	}
	if err := devSeed(ctx, st, 1_800_000_000_000); err != nil {
		t.Fatal(err)
	}
	again, _ := st.UserList(ctx, store.UserListFilter{})
	if again.Total != users.Total {
		t.Fatalf("second seed added users: %d → %d", users.Total, again.Total)
	}
}
