package api_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"runova/internal/api"
	"runova/internal/store"
	"runova/internal/update"
)

type fakeReleases struct{ calls atomic.Int32 }

func (f *fakeReleases) Latest(context.Context) (string, error) {
	f.calls.Add(1)
	return "0.3.0", nil
}

func (f *fakeReleases) ReleaseURL(v string) string {
	return "https://github.com/me/runova/releases/tag/v" + v
}

func updateEnv(t *testing.T) (*env, *fakeReleases, *update.Checker) {
	t.Helper()
	dataDir := t.TempDir()
	s, err := store.Open(filepath.Join(dataDir, "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	src := &fakeReleases{}
	chk := &update.Checker{
		Current: "0.2.0", Source: src,
		Enabled: func(ctx context.Context) bool {
			v, err := s.Settings(ctx)
			return err == nil && v.UpdateCheck
		},
		FirstDelay: time.Millisecond, Interval: time.Hour,
	}
	clk := &clock{t: time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)}
	srv := httptest.NewServer(api.New(api.Config{Store: s, Now: clk.Now, DataDir: dataDir, Update: chk}))
	t.Cleanup(func() { srv.Close(); s.Close() })
	e := &env{t: t, srv: srv, store: s, clock: clk, dataDir: dataDir}
	e.addUser("admin", adminPassword, store.RoleAdmin)
	return e, src, chk
}

func TestUpdateStatusAdminOnly(t *testing.T) {
	e, _, chk := updateEnv(t)
	chk.Check(context.Background())
	e.addUser("budi", "budi-password-1", store.RoleUser)

	var got update.Status
	r := e.login("admin", adminPassword).do("GET", "/api/update", nil)
	if r.status != 200 {
		t.Fatalf("admin: %d %s", r.status, r.body)
	}
	r.into(t, &got)
	if got.Current != "0.2.0" || got.Latest != "0.3.0" || !got.UpdateAvailable || !got.Enabled ||
		got.ReleaseURL != "https://github.com/me/runova/releases/tag/v0.3.0" || got.CheckedAt == 0 {
		t.Fatalf("status = %+v", got)
	}

	if r := e.login("budi", "budi-password-1").do("GET", "/api/update", nil); r.status != 403 {
		t.Fatalf("regular user: %d %s", r.status, r.body)
	}
	if r := e.client().do("GET", "/api/update", nil); r.status != 401 {
		t.Fatalf("anonymous: %d %s", r.status, r.body)
	}
}

func TestUpdateCheckSetting(t *testing.T) {
	e, src, chk := updateEnv(t)
	chk.Check(context.Background())
	c := e.login("admin", adminPassword)
	put := func(on bool) {
		t.Helper()
		var s store.Settings
		c.do("GET", "/api/settings", nil).into(t, &s)
		s.UpdateCheck = on
		if r := c.do("PUT", "/api/settings", s); r.status != 200 {
			t.Fatalf("PUT settings: %d %s", r.status, r.body)
		}
	}

	put(false)
	var got update.Status
	c.do("GET", "/api/update", nil).into(t, &got)
	if got.Enabled || got.UpdateAvailable {
		t.Fatalf("with checking off: %+v", got)
	}

	// Switching it on checks soon, without waiting for the next interval.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go chk.Run(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for src.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	before := src.calls.Load()
	put(true)
	for src.calls.Load() == before && time.Now().Before(deadline.Add(2*time.Second)) {
		time.Sleep(5 * time.Millisecond)
	}
	if src.calls.Load() == before {
		t.Fatal("enabling update_check did not trigger a check")
	}
	c.do("GET", "/api/update", nil).into(t, &got)
	if !got.Enabled || !got.UpdateAvailable {
		t.Fatalf("after enabling: %+v", got)
	}
}
