package apps_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"runova/internal/apps"
	"runova/internal/hostdns"
	"runova/internal/store"
)

type fakeDNS struct {
	res    hostdns.Result
	checks []string
}

func (f *fakeDNS) Check(_ context.Context, domain, _ string) hostdns.Result {
	f.checks = append(f.checks, domain)
	return f.res
}

type hostingEnv struct {
	*testEnv
	admin store.User
	dns   *fakeDNS
	caddy *fakeCaddy
}

func newHostingEnv(t *testing.T) *hostingEnv {
	t.Helper()
	e := &hostingEnv{testEnv: newTestEnv(t), dns: &fakeDNS{res: hostdns.Result{OK: true}}, caddy: &fakeCaddy{}}
	e.svc.DNS = e.dns
	e.svc.Caddy = e.caddy
	e.admin = e.addUser("admin", store.RoleAdmin, roomyQuota)
	return e
}

func (e *hostingEnv) register(name string) apps.HostingView {
	e.t.Helper()
	h, err := e.svc.AddHostingDomain(context.Background(), e.admin, name, "")
	if err != nil {
		e.t.Fatalf("AddHostingDomain(%s): %v", name, err)
	}
	return h
}

func (e *hostingEnv) addLabel(u store.User, appID, hostingID int64, label string) (store.Domain, error) {
	return e.svc.AddDomain(context.Background(), u, appID, apps.DomainInput{HostingDomainID: hostingID, Label: label})
}

func TestHostingAddValidates(t *testing.T) {
	ctx := context.Background()
	e := newHostingEnv(t)
	e.setSettings(func(s *store.Settings) { s.PanelDomain = "runova.prodak.id" })

	h, err := e.svc.AddHostingDomain(ctx, e.admin, " *.Porto.Ex.ID ", "Blog, shop")
	if err != nil {
		t.Fatal(err)
	}
	if h.Name != "porto.ex.id" || h.Reserved != "blog,shop" || h.DNSStatus != store.DNSOK || !h.Enabled || h.DNSCheckedAt == 0 {
		t.Fatalf("added = %+v", h)
	}
	if !slices.Equal(e.dns.checks, []string{"porto.ex.id"}) {
		t.Fatalf("DNS checks = %v, want one right after adding", e.dns.checks)
	}
	acts, _ := e.store.ActivityList(ctx, store.ActivityFilter{Limit: 5})
	if !slices.ContainsFunc(acts, func(a store.Activity) bool { return a.Action == store.ActHostingAdd && a.Target == "porto.ex.id" }) {
		t.Errorf("activity = %+v", acts)
	}

	for _, tc := range []struct {
		name, domain, reserved string
		kind                   error
	}{
		{"panel domain", "runova.prodak.id", "", apps.ErrValidation},
		{"duplicate", "porto.ex.id", "", store.ErrConflict},
		{"nested below", "a.porto.ex.id", "", apps.ErrValidation},
		{"nested above", "ex.id", "", apps.ErrValidation},
		{"bad name", "localhost", "", apps.ErrValidation},
		{"bad reserved", "other.id", "x", apps.ErrValidation},
	} {
		if _, err := e.svc.AddHostingDomain(ctx, e.admin, tc.domain, tc.reserved); !errors.Is(err, tc.kind) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.kind)
		}
	}
	if _, err := e.svc.AddHostingDomain(ctx, e.addUser("budi", store.RoleUser, roomyQuota), "x.id", ""); !errors.Is(err, apps.ErrForbidden) {
		t.Errorf("non-admin: %v, want ErrForbidden", err)
	}
	e.dns.res = hostdns.Result{Detail: "Belum ada record DNS"}
	bad, err := e.svc.AddHostingDomain(ctx, e.admin, "bad.id", "")
	if err != nil || bad.DNSStatus != store.DNSError || bad.DNSDetail != "Belum ada record DNS" {
		t.Fatalf("failing DNS = %+v, %v; the domain is still added, with the reason", bad, err)
	}
}

func TestAddHostedSubdomain(t *testing.T) {
	ctx := context.Background()
	e := newHostingEnv(t)
	h := e.register("porto.ex.id")
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(u, "cv-app")

	d, err := e.addLabel(u, app.ID, h.ID, " CV-Budi ")
	if err != nil {
		t.Fatal(err)
	}
	if d.Host != "cv-budi.porto.ex.id" || d.Label != "cv-budi" || d.HostingDomainID == nil || *d.HostingDomainID != h.ID || d.Status != store.DomainPending {
		t.Fatalf("domain = %+v", d)
	}
	if e.caddy.applied != 1 {
		t.Errorf("Apply calls = %d, want 1 (registering a hosting domain changes nothing in Caddy)", e.caddy.applied)
	}
	acts, _ := e.store.ActivityList(ctx, store.ActivityFilter{Limit: 1})
	if acts[0].Action != store.ActDomainAdd || acts[0].Detail != "cv-budi.porto.ex.id" {
		t.Errorf("activity = %+v", acts[0])
	}
	if err := e.svc.RemoveDomain(ctx, u, app.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.addLabel(u, app.ID, h.ID, "cv-budi"); err != nil {
		t.Fatalf("label is free again after removal: %v", err)
	}
}

func TestAddHostedLabelRules(t *testing.T) {
	e := newHostingEnv(t)
	e.setSettings(func(s *store.Settings) { s.PanelDomain = "dash.porto.ex.id" })
	h, err := e.svc.AddHostingDomain(context.Background(), e.admin, "porto.ex.id", "blog")
	if err != nil {
		t.Fatal(err)
	}
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	other := e.addUser("citra", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(u, "cv-app")
	otherApp, _ := e.createStatic(other, "cv-two")
	adminApp, _ := e.createStatic(e.admin, "site")
	if _, err := e.svc.AddDomain(context.Background(), e.admin, adminApp.ID, apps.DomainInput{Host: "taken.porto.ex.id"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.addLabel(u, app.ID, h.ID, "kopi-enak"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		label string
		kind  error
	}{
		{"ab", apps.ErrValidation},
		{"Www", apps.ErrValidation},
		{"api", apps.ErrValidation},
		{"blog", apps.ErrValidation},
		{"dash", apps.ErrValidation},
		{"xn--abc", apps.ErrValidation},
		{"a.b.c", apps.ErrValidation},
		{"taken", store.ErrConflict},
		{"kopi-enak", store.ErrConflict},
	} {
		_, err := e.addLabel(other, otherApp.ID, h.ID, tc.label)
		if !errors.Is(err, tc.kind) {
			t.Errorf("label %q: err = %v, want %v", tc.label, err, tc.kind)
		}
	}
	if _, err := e.addLabel(other, otherApp.ID, h.ID, "kopi-enak"); err == nil || err.Error() != "Alamat sudah dipakai" {
		t.Errorf("duplicate message = %v", err)
	}
	if _, err := e.addLabel(other, otherApp.ID, 999, "abc"); !errors.Is(err, apps.ErrValidation) || err.Error() != "Domain hosting tidak tersedia" {
		t.Errorf("unknown hosting domain: %v", err)
	}
}

func TestSubdomainQuotaFollowsAppsQuota(t *testing.T) {
	ctx := context.Background()
	e := newHostingEnv(t)
	h := e.register("porto.ex.id")
	u := e.addUser("budi", store.RoleUser, store.Quota{Apps: 2, MemoryMB: 1024, CPUMilli: 1000, Databases: 1, PGConnections: 5})
	one, _ := e.createStatic(u, "one")
	two, _ := e.createStatic(u, "two")

	if _, err := e.addLabel(u, one.ID, h.ID, "aaa"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.addLabel(u, two.ID, h.ID, "bbb"); err != nil {
		t.Fatal(err)
	}
	_, err := e.addLabel(u, one.ID, h.ID, "ccc")
	if !errors.Is(err, apps.ErrQuota) || err.Error() != "Kuota subdomain habis (2 dari 2)" {
		t.Fatalf("third subdomain: %v", err)
	}
	if used, limit, _ := e.svc.SubdomainQuota(ctx, u); used != 2 || limit != 2 {
		t.Fatalf("SubdomainQuota = %d, %d", used, limit)
	}

	raised := u
	raised.Quota.Apps = 3
	if _, err := e.addLabel(raised, one.ID, h.ID, "ccc"); err != nil {
		t.Fatalf("after raising the quota: %v", err)
	}
	lowered := u
	lowered.Quota.Apps = 1
	if _, err := e.addLabel(lowered, one.ID, h.ID, "ddd"); !errors.Is(err, apps.ErrQuota) {
		t.Fatalf("below usage: %v, want ErrQuota", err)
	}
	if got := mustDomains(t, e.testEnv, one.ID); len(got) != 2 {
		t.Fatalf("existing subdomains must stay when the quota drops: %+v", got)
	}

	off := false
	if _, err := e.svc.UpdateHostingDomain(ctx, e.admin, h.ID, &off, nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.store.SubdomainCount(ctx, u.ID); n != 3 {
		t.Fatalf("subdomains under a disabled base still count: %d", n)
	}
}

func TestUserCannotAddOwnDomain(t *testing.T) {
	ctx := context.Background()
	e := newHostingEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(u, "cv-app")

	_, err := e.svc.AddDomain(ctx, u, app.ID, apps.DomainInput{Host: "budi.com"})
	if !errors.Is(err, apps.ErrForbidden) || err.Error() != "Domain sendiri hanya bisa ditambahkan admin" {
		t.Fatalf("user own domain: %v", err)
	}
	// A free domain the user already has (from before) stays readable and removable.
	if _, err := e.store.AddDomain(ctx, app.ID, "lama.budi.com", 1); err != nil {
		t.Fatal(err)
	}
	list, err := e.svc.Domains(ctx, u, app.ID)
	if err != nil || len(list) != 1 || list[0].Host != "lama.budi.com" {
		t.Fatalf("Domains = %+v, %v", list, err)
	}
	if err := e.svc.RemoveDomain(ctx, u, app.ID, list[0].ID); err != nil {
		t.Fatalf("remove own old domain: %v", err)
	}
	adminApp, _ := e.createStatic(e.admin, "site")
	if _, err := e.svc.AddDomain(ctx, e.admin, adminApp.ID, apps.DomainInput{Host: "budi.com"}); err != nil {
		t.Fatalf("admin own domain: %v", err)
	}
	for _, in := range []apps.DomainInput{{}, {HostingDomainID: 1, Host: "x.co"}} {
		if _, err := e.svc.AddDomain(ctx, e.admin, adminApp.ID, in); !errors.Is(err, apps.ErrValidation) {
			t.Errorf("%+v: %v, want ErrValidation", in, err)
		}
	}
}

func TestHostedOwnership(t *testing.T) {
	ctx := context.Background()
	e := newHostingEnv(t)
	h := e.register("porto.ex.id")
	a := e.addUser("andi", store.RoleUser, roomyQuota)
	b := e.addUser("budi", store.RoleUser, roomyQuota)
	appA, _ := e.createStatic(a, "one")
	d, err := e.addLabel(a, appA.ID, h.ID, "andi-app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.addLabel(b, appA.ID, h.ID, "budi-sneaky"); !errors.Is(err, apps.ErrAppNotFound) {
		t.Errorf("adding to another user's app: %v", err)
	}
	if err := e.svc.RemoveDomain(ctx, b, appA.ID, d.ID); !errors.Is(err, apps.ErrAppNotFound) {
		t.Errorf("removing another user's subdomain: %v", err)
	}
	if got := mustDomains(t, e.testEnv, appA.ID); len(got) != 1 {
		t.Fatalf("domains = %+v", got)
	}
}

func TestHostedCaddyRejectsRollsBack(t *testing.T) {
	ctx := context.Background()
	e := newHostingEnv(t)
	h := e.register("porto.ex.id")
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(u, "cv-app")
	e.caddy.err = errors.New("bad")
	if _, err := e.addLabel(u, app.ID, h.ID, "abc"); !errors.Is(err, apps.ErrCaddy) {
		t.Fatalf("err = %v", err)
	}
	if n, _ := e.store.SubdomainCount(ctx, u.ID); n != 0 {
		t.Fatalf("a refused subdomain still counts against the quota: %d", n)
	}
	e.caddy.err = nil
	if _, err := e.addLabel(u, app.ID, h.ID, "abc"); err != nil {
		t.Fatalf("label not free after rollback: %v", err)
	}
}

func TestDisableHostingRemovesRoutesAndNotifies(t *testing.T) {
	ctx := context.Background()
	e := newHostingEnv(t)
	h := e.register("porto.ex.id")
	a := e.addUser("andi", store.RoleUser, roomyQuota)
	b := e.addUser("budi", store.RoleUser, roomyQuota)
	appA, _ := deployFirst(t, e.testEnv, a, "one")
	appB, _ := deployFirst(t, e.testEnv, b, "two")
	for _, x := range []struct {
		u     store.User
		app   int64
		label string
	}{{a, appA.ID, "aaa"}, {a, appA.ID, "aab"}, {b, appB.ID, "bbb"}} {
		if _, err := e.addLabel(x.u, x.app, h.ID, x.label); err != nil {
			t.Fatal(err)
		}
	}
	hosts := func() []string {
		st, err := apps.CaddyState(ctx, e.store, "127.0.0.1:7070")
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range st.Sites {
			out = append(out, s.Host)
		}
		slices.Sort(out)
		return out
	}
	if got := hosts(); !slices.Equal(got, []string{"aaa.porto.ex.id", "aab.porto.ex.id", "bbb.porto.ex.id"}) {
		t.Fatalf("routes = %v", got)
	}

	off := false
	v, err := e.svc.UpdateHostingDomain(ctx, e.admin, h.ID, &off, nil)
	if err != nil || v.Enabled || v.Used != 3 {
		t.Fatalf("disable = %+v, %v", v, err)
	}
	if got := hosts(); len(got) != 0 {
		t.Fatalf("routes after disabling = %v", got)
	}
	for _, u := range []store.User{a, b} {
		list, _ := e.store.Notifications(ctx, u.ID, 10)
		var n int
		for _, x := range list {
			if x.Level == store.LevelWarning {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s got %d warnings, want exactly one per owner", u.Username, n)
		}
	}
	if got := mustDomains(t, e.testEnv, appA.ID); len(got) != 2 || got[0].Status != store.DomainPending {
		t.Fatalf("subdomains must stay recorded: %+v", got)
	}

	on := true
	if _, err := e.svc.UpdateHostingDomain(ctx, e.admin, h.ID, &on, nil); err != nil {
		t.Fatal(err)
	}
	if got := hosts(); len(got) != 3 {
		t.Fatalf("routes after enabling = %v", got)
	}

	e.caddy.err = errors.New("bad")
	if _, err := e.svc.UpdateHostingDomain(ctx, e.admin, h.ID, &off, nil); !errors.Is(err, apps.ErrCaddy) {
		t.Fatalf("refused disable: %v", err)
	}
	if got, _ := e.store.HostingDomain(ctx, h.ID); !got.Enabled {
		t.Fatal("a refused disable must restore the enabled state")
	}
}

func TestUpdateHostingReserved(t *testing.T) {
	ctx := context.Background()
	e := newHostingEnv(t)
	h := e.register("porto.ex.id")
	rs := " Shop,blog "
	v, err := e.svc.UpdateHostingDomain(ctx, e.admin, h.ID, nil, &rs)
	if err != nil || v.Reserved != "shop,blog" {
		t.Fatalf("update = %+v, %v", v, err)
	}
	bad := "x"
	if _, err := e.svc.UpdateHostingDomain(ctx, e.admin, h.ID, nil, &bad); !errors.Is(err, apps.ErrValidation) {
		t.Errorf("bad reserved: %v", err)
	}
	if _, err := e.svc.UpdateHostingDomain(ctx, e.admin, 999, nil, &rs); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	if _, err := e.svc.UpdateHostingDomain(ctx, e.addUser("budi", store.RoleUser, roomyQuota), h.ID, nil, &rs); !errors.Is(err, apps.ErrForbidden) {
		t.Errorf("non-admin: %v", err)
	}
}

func TestCheckDomainsSkipsDisabledBase(t *testing.T) {
	ctx := context.Background()
	e := newHostingEnv(t)
	h := e.register("porto.ex.id")
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(u, "cv-app")
	probed := 0
	e.svc.Probe = func(context.Context, string, bool) (bool, string) { probed++; return false, "belum" }
	if _, err := e.addLabel(u, app.ID, h.ID, "abc"); err != nil {
		t.Fatal(err)
	}
	off := false
	e.svc.UpdateHostingDomain(ctx, e.admin, h.ID, &off, nil)
	if err := e.svc.CheckDomains(ctx); err != nil || probed != 0 {
		t.Fatalf("probed %d, err %v: a disabled base is not served, so there is nothing to probe", probed, err)
	}
	on := true
	e.svc.UpdateHostingDomain(ctx, e.admin, h.ID, &on, nil)
	if err := e.svc.CheckDomains(ctx); err != nil || probed != 1 {
		t.Fatalf("probed %d, err %v", probed, err)
	}
}

func TestRemoveHostingInUse(t *testing.T) {
	ctx := context.Background()
	e := newHostingEnv(t)
	h := e.register("porto.ex.id")
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(u, "cv-app")
	d, _ := e.addLabel(u, app.ID, h.ID, "abc")

	err := e.svc.RemoveHostingDomain(ctx, e.admin, h.ID)
	if !errors.Is(err, store.ErrConflict) || err.Error() != "Masih dipakai 1 subdomain; matikan dulu" {
		t.Fatalf("remove in use: %v", err)
	}
	if err := e.svc.RemoveDomain(ctx, u, app.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RemoveHostingDomain(ctx, e.admin, h.ID); err != nil {
		t.Fatal(err)
	}
	acts, _ := e.store.ActivityList(ctx, store.ActivityFilter{Limit: 1})
	if acts[0].Action != store.ActHostingRemove {
		t.Errorf("activity = %+v", acts[0])
	}
	if err := e.svc.RemoveHostingDomain(ctx, u, h.ID); !errors.Is(err, apps.ErrForbidden) {
		t.Errorf("non-admin: %v", err)
	}
}

func TestAvailableStoresNothing(t *testing.T) {
	ctx := context.Background()
	e := newHostingEnv(t)
	h := e.register("porto.ex.id")
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(u, "cv-app")
	e.addLabel(u, app.ID, h.ID, "taken")

	for label, wantOK := range map[string]bool{"fresh": true, "taken": false, "ab": false, "www": false} {
		ok, reason, err := e.svc.HostingAvailable(ctx, h.ID, label)
		if err != nil || ok != wantOK || (!ok && reason == "") || (ok && reason != "") {
			t.Errorf("available(%q) = %v %q %v", label, ok, reason, err)
		}
	}
	if ok, reason, _ := e.svc.HostingAvailable(ctx, 999, "fresh"); ok || reason != "Domain hosting tidak tersedia" {
		t.Errorf("unknown base: %v %q", ok, reason)
	}
	off := false
	e.svc.UpdateHostingDomain(ctx, e.admin, h.ID, &off, nil)
	if ok, _, _ := e.svc.HostingAvailable(ctx, h.ID, "fresh"); ok {
		t.Error("a disabled base has nothing available")
	}
	if n, _ := e.store.SubdomainCount(ctx, u.ID); n != 1 {
		t.Fatalf("subdomains = %d; checking availability must not store anything", n)
	}
}

func TestHostingListByRole(t *testing.T) {
	ctx := context.Background()
	e := newHostingEnv(t)
	on := e.register("porto.ex.id")
	off := e.register("lama.ex.id")
	disabled := false
	if _, err := e.svc.UpdateHostingDomain(ctx, e.admin, off.ID, &disabled, nil); err != nil {
		t.Fatal(err)
	}
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(u, "cv-app")
	e.addLabel(u, app.ID, on.ID, "abc")

	user, err := e.svc.HostingDomains(ctx, u)
	if err != nil || len(user) != 1 || user[0].Name != "porto.ex.id" {
		t.Fatalf("user list = %+v, %v; want only the enabled domain", user, err)
	}
	all, err := e.svc.HostingDomains(ctx, e.admin)
	if err != nil || len(all) != 2 {
		t.Fatalf("admin list = %+v, %v", all, err)
	}
	byName := map[string]apps.HostingView{all[0].Name: all[0], all[1].Name: all[1]}
	if byName["porto.ex.id"].Used != 1 || byName["porto.ex.id"].LECount != 1 || byName["lama.ex.id"].Enabled {
		t.Fatalf("admin view = %+v", byName)
	}
	e.setSettings(func(s *store.Settings) { s.TrustedProxies = []string{"10.0.0.5"} })
	all, _ = e.svc.HostingDomains(ctx, e.admin)
	for _, v := range all {
		if v.LECount != 0 || v.LELevel != apps.LEOK {
			t.Errorf("%s behind a front proxy: LE %d %q; Caddy takes no Let's Encrypt certificates then", v.Name, v.LECount, v.LELevel)
		}
	}
}

func TestCheckHostingDNSRecordsResult(t *testing.T) {
	ctx := context.Background()
	e := newHostingEnv(t)
	h := e.register("porto.ex.id")
	e.dns.res = hostdns.Result{Detail: "matikan proxy"}
	v, err := e.svc.CheckHostingDNS(ctx, e.admin, h.ID)
	if err != nil || v.DNSStatus != store.DNSError || v.DNSDetail != "matikan proxy" {
		t.Fatalf("check = %+v, %v", v, err)
	}
	e.dns.res = hostdns.Result{OK: true}
	if v, _ = e.svc.CheckHostingDNS(ctx, e.admin, h.ID); v.DNSStatus != store.DNSOK || v.DNSDetail != "" {
		t.Fatalf("recheck = %+v", v)
	}
	if _, err := e.svc.CheckHostingDNS(ctx, e.admin, 999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}
