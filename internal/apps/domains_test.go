package apps_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"runova/internal/apps"
	"runova/internal/caddy"
	"runova/internal/store"
)

// fakeCaddy records Apply calls and can refuse them.
type fakeCaddy struct {
	err     error
	applied int
}

func (f *fakeCaddy) Apply(context.Context) error {
	f.applied++
	return f.err
}

func TestAddDomainNormalizesAndApplies(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	fc := &fakeCaddy{}
	e.svc.Caddy = fc
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")

	d, err := e.svc.AddDomain(ctx, u, app.ID, "App.Contoh.ID.")
	if err != nil {
		t.Fatal(err)
	}
	if d.Host != "app.contoh.id" || d.Status != store.DomainPending {
		t.Fatalf("domain = %+v", d)
	}
	if fc.applied != 1 {
		t.Errorf("Apply calls = %d, want 1", fc.applied)
	}
	acts, _ := e.store.ActivityList(ctx, store.ActivityFilter{Limit: 1})
	if acts[0].Action != store.ActDomainAdd || acts[0].Target != "billing" || acts[0].Detail != "app.contoh.id" {
		t.Errorf("activity = %+v", acts[0])
	}

	if err := e.svc.RemoveDomain(ctx, u, app.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if fc.applied != 2 {
		t.Errorf("Apply calls after remove = %d, want 2", fc.applied)
	}
	acts, _ = e.store.ActivityList(ctx, store.ActivityFilter{Limit: 1})
	if acts[0].Action != store.ActDomainRemove {
		t.Errorf("activity = %+v", acts[0])
	}
}

func TestAddDomainRejections(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	e.svc.Caddy = &fakeCaddy{}
	a := e.addUser("andi", store.RoleUser, roomyQuota)
	b := e.addUser("budi", store.RoleUser, roomyQuota)
	appA, _ := e.createStatic(a, "one")
	appB, _ := e.createStatic(b, "two")
	e.setSettings(func(s *store.Settings) { s.PanelDomain = "p.co" })

	if _, err := e.svc.AddDomain(ctx, a, appA.ID, "x.co"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		user store.User
		app  int64
		host string
		kind error
		msg  string
	}{
		{"taken by another user", b, appB.ID, "X.co.", store.ErrConflict, "Domain sudah dipakai"},
		{"taken by the same app", a, appA.ID, "x.co", store.ErrConflict, "Domain sudah dipakai"},
		{"panel domain", b, appB.ID, "p.co", store.ErrConflict, "Domain sudah dipakai"},
		{"IP address", b, appB.ID, "203.0.113.5", apps.ErrValidation, ""},
		{"single label", b, appB.ID, "contoh", apps.ErrValidation, ""},
		{"someone else's app", b, appA.ID, "y.co", apps.ErrAppNotFound, ""},
	} {
		_, err := e.svc.AddDomain(ctx, tc.user, tc.app, tc.host)
		if !errors.Is(err, tc.kind) || (tc.msg != "" && err.Error() != tc.msg) {
			t.Errorf("%s: err = %v, want %v %q", tc.name, err, tc.kind, tc.msg)
		}
		if err != nil && (strings.Contains(err.Error(), "andi") || strings.Contains(err.Error(), "one")) {
			t.Errorf("%s: error names the owner: %v", tc.name, err)
		}
	}
}

func TestAddDomainLimit(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	e.svc.Caddy = &fakeCaddy{}
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(u, "billing")
	for _, h := range []string{"a.co", "b.co", "c.co", "d.co", "e.co"} {
		if _, err := e.svc.AddDomain(ctx, u, app.ID, h); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.svc.AddDomain(ctx, u, app.ID, "f.co")
	if !errors.Is(err, apps.ErrValidation) || err.Error() != "Maksimal 5 domain per aplikasi" {
		t.Fatalf("sixth domain: %v", err)
	}
}

func TestAddDomainCaddyRejectsRollsBack(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	fc := &fakeCaddy{err: errors.New("caddy POST /load: 400 bad upstream")}
	e.svc.Caddy = fc
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(u, "billing")

	_, err := e.svc.AddDomain(ctx, u, app.ID, "x.co")
	if !errors.Is(err, apps.ErrCaddy) || err.Error() != "Caddy menolak konfigurasi: caddy POST /load: 400 bad upstream" {
		t.Fatalf("AddDomain = %v", err)
	}
	if got, _ := e.store.Domains(ctx, app.ID); len(got) != 0 {
		t.Fatalf("domain row left behind: %+v", got)
	}
	acts, _ := e.store.ActivityList(ctx, store.ActivityFilter{})
	if slices.ContainsFunc(acts, func(a store.Activity) bool { return a.Action == store.ActDomainAdd }) {
		t.Error("a rolled-back domain was logged as added")
	}
	fc.err = nil
	if _, err := e.svc.AddDomain(ctx, u, app.ID, "x.co"); err != nil {
		t.Fatalf("host not free after rollback: %v", err)
	}
}

func TestCheckDomainStatusTransitions(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	e.svc.Caddy = &fakeCaddy{}
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(u, "billing")
	ok, detail := false, "Sertifikat belum dipercaya"
	e.svc.Probe = func(_ context.Context, host string) (bool, string) {
		if host != "x.co" {
			t.Errorf("probed %q", host)
		}
		return ok, detail
	}
	d, _ := e.svc.AddDomain(ctx, u, app.ID, "x.co")

	got, err := e.svc.CheckDomain(ctx, u, app.ID, d.ID)
	if err != nil || got.Status != store.DomainPending || got.Detail != detail || got.CheckedAt == 0 {
		t.Fatalf("young failing domain = %+v, %v; want pending with the reason", got, err)
	}
	if n, _ := e.store.UnreadCount(ctx, u.ID); n != 0 {
		t.Errorf("notifications = %d before the domain is active", n)
	}

	e.clock.Advance(11 * time.Minute)
	if got, _ = e.svc.CheckDomain(ctx, u, app.ID, d.ID); got.Status != store.DomainError {
		t.Fatalf("old failing domain = %+v, want error", got)
	}

	ok = true
	if got, _ = e.svc.CheckDomain(ctx, u, app.ID, d.ID); got.Status != store.DomainActive || got.Detail != "" {
		t.Fatalf("working domain = %+v", got)
	}
	list, _ := e.store.Notifications(ctx, u.ID, 10)
	if len(list) != 1 || list[0].Title != "HTTPS aktif untuk x.co" || list[0].Level != store.LevelInfo || list[0].AppID == nil || *list[0].AppID != app.ID {
		t.Fatalf("notifications = %+v", list)
	}
	if _, err := e.svc.CheckDomain(ctx, u, app.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.store.UnreadCount(ctx, u.ID); n != 1 {
		t.Errorf("notifications = %d after a second successful check, want still 1", n)
	}

	other := e.addUser("andi", store.RoleUser, roomyQuota)
	if _, err := e.svc.CheckDomain(ctx, other, app.ID, d.ID); !errors.Is(err, apps.ErrAppNotFound) {
		t.Errorf("other user's check: %v", err)
	}
	if _, err := e.svc.CheckDomain(ctx, u, app.ID, 999); !errors.Is(err, apps.ErrValidation) {
		t.Errorf("unknown domain: %v", err)
	}
}

func TestCheckDomainsProbesOnlyInactive(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	e.svc.Caddy = &fakeCaddy{}
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(u, "billing")
	probed := map[string]int{}
	working := map[string]bool{"ok.co": true}
	e.svc.Probe = func(_ context.Context, host string) (bool, string) {
		probed[host]++
		return working[host], "belum"
	}
	for _, h := range []string{"new.co", "ok.co"} {
		if _, err := e.svc.AddDomain(ctx, u, app.ID, h); err != nil {
			t.Fatal(err)
		}
	}

	if err := e.svc.CheckDomains(ctx); err != nil {
		t.Fatal(err)
	}
	working["new.co"] = true
	if err := e.svc.CheckDomains(ctx); err != nil {
		t.Fatal(err)
	}
	if probed["ok.co"] != 1 || probed["new.co"] != 2 {
		t.Fatalf("probes = %v; an active domain is not probed again", probed)
	}
	for _, d := range mustDomains(t, e, app.ID) {
		if d.Status != store.DomainActive {
			t.Errorf("%s = %s", d.Host, d.Status)
		}
	}
	if n, _ := e.store.UnreadCount(ctx, u.ID); n != 2 {
		t.Errorf("notifications = %d, want one per domain that became active", n)
	}
}

func mustDomains(t *testing.T, e *testEnv, appID int64) []store.Domain {
	t.Helper()
	ds, err := e.store.Domains(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func TestCaddyState(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	e.svc.Caddy = &fakeCaddy{}
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	running, d1 := deployFirst(t, e, u, "billing")
	stopped, _ := deployFirst(t, e, u, "reports")
	fresh, _ := e.createStatic(u, "fresh")
	for app, host := range map[int64]string{running.ID: "run.co", stopped.ID: "stop.co", fresh.ID: "fresh.co"} {
		if _, err := e.svc.AddDomain(ctx, u, app, host); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.Stop(ctx, u, stopped.ID); err != nil {
		t.Fatal(err)
	}
	on := true
	for _, id := range []int64{running.ID, fresh.ID} {
		if _, _, err := e.svc.UpdateSettings(ctx, u, id, apps.SettingsInput{IPAccess: &on}); err != nil {
			t.Fatal(err)
		}
	}
	e.setSettings(func(s *store.Settings) { s.PanelDomain = "panel.co"; s.PanelPort = 8443; s.PublicIP = "203.0.113.5" })

	got, err := apps.CaddyState(ctx, e.store, "127.0.0.1:7070")
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := e.store.Deployment(ctx, d1.ID)
	want := caddy.State{
		Sites:         []caddy.Site{{Host: "run.co", Upstream: "127.0.0.1:" + strconv.Itoa(cur.HostPort)}},
		IPListeners:   []caddy.IPListener{{Port: running.IPPort, Upstream: "127.0.0.1:" + strconv.Itoa(cur.HostPort)}},
		PanelDomain:   "panel.co",
		PanelIPPort:   8443,
		PanelUpstream: "127.0.0.1:7070",
		PublicIP:      "203.0.113.5",
	}
	if !slices.Equal(got.Sites, want.Sites) || !slices.Equal(got.IPListeners, want.IPListeners) ||
		got.PanelDomain != want.PanelDomain || got.PanelIPPort != want.PanelIPPort || got.PanelUpstream != want.PanelUpstream || got.PublicIP != want.PublicIP {
		t.Fatalf("state = %+v\nwant   %+v\n(only the running app with an active deployment is routed; a stopped app and an app that never deployed are not)", got, want)
	}
}

func TestIPAccessToggleReverts(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	fc := &fakeCaddy{}
	e.svc.Caddy = fc
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")

	on := true
	if _, _, err := e.svc.UpdateSettings(ctx, u, app.ID, apps.SettingsInput{IPAccess: &on}); err != nil || fc.applied != 1 {
		t.Fatalf("toggle on: %v, applied %d", err, fc.applied)
	}
	mem := 256
	if _, _, err := e.svc.UpdateSettings(ctx, u, app.ID, apps.SettingsInput{MemoryMB: &mem}); err != nil || fc.applied != 1 {
		t.Fatalf("unrelated change: %v, applied %d; Caddy must only be told about IP access changes", err, fc.applied)
	}

	fc.err = errors.New("refused")
	off, mem2 := false, 320
	_, _, err := e.svc.UpdateSettings(ctx, u, app.ID, apps.SettingsInput{IPAccess: &off, MemoryMB: &mem2})
	if !errors.Is(err, apps.ErrCaddy) {
		t.Fatalf("UpdateSettings = %v, want ErrCaddy", err)
	}
	got, _ := e.store.App(ctx, app.ID)
	if !got.IPAccess || got.MemoryMB != 256 {
		t.Fatalf("app = ip_access %v memory %d; a refused change must leave every setting as it was", got.IPAccess, got.MemoryMB)
	}
}
