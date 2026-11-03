package api_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"runova/internal/hostdns"
	"runova/internal/store"
)

type stubDNS struct{ res hostdns.Result }

func (d *stubDNS) Check(context.Context, string, string) hostdns.Result { return d.res }

func hostingPath(id int64, rest string) string {
	return "/api/hosting-domains/" + strconv.FormatInt(id, 10) + rest
}

type hostingEnv struct {
	*appEnv
	loader *caddyLoader
	admin  *client
}

func newHosting(t *testing.T) *hostingEnv {
	t.Helper()
	e := newAppEnv(t, 0)
	e.svc.DNS = &stubDNS{res: hostdns.Result{OK: true}}
	loader := withCaddy(e)
	return &hostingEnv{appEnv: e, loader: loader, admin: e.login("admin", adminPassword)}
}

func (h *hostingEnv) register(name string) int64 {
	h.t.Helper()
	r := h.admin.do("POST", "/api/hosting-domains", obj{"name": name})
	var body struct{ ID int64 }
	r.into(h.t, &body)
	if r.status != 201 {
		h.t.Fatalf("register %s = %d %s", name, r.status, r.body)
	}
	return body.ID
}

func TestHostingAdminOnly(t *testing.T) {
	h := newHosting(t)
	id := h.register("porto.ex.id")
	h.addUser("budi", "budi-password-1", store.RoleUser)
	c := h.login("budi", "budi-password-1")
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/hosting-domains"},
		{"PATCH", hostingPath(id, "")},
		{"POST", hostingPath(id, "/check")},
		{"DELETE", hostingPath(id, "")},
	} {
		if r := c.do(tc.method, tc.path, obj{"name": "x.id", "enabled": false}); r.status != 403 {
			t.Errorf("%s %s as a user = %d %s, want 403", tc.method, tc.path, r.status, r.body)
		}
	}
	if r := h.client().do("GET", "/api/hosting-domains", nil); r.status != 401 {
		t.Errorf("anonymous list = %d", r.status)
	}
}

func TestHostingListByRole(t *testing.T) {
	h := newHosting(t)
	h.register("porto.ex.id")
	off := h.register("lama.ex.id")
	if r := h.admin.do("PATCH", hostingPath(off, ""), obj{"enabled": false}); r.status != 200 {
		t.Fatalf("disable = %d %s", r.status, r.body)
	}
	h.addUser("budi", "budi-password-1", store.RoleUser)
	c := h.login("budi", "budi-password-1")

	var user struct {
		Domains []map[string]any
		Quota   struct{ Used, Limit int }
	}
	c.do("GET", "/api/hosting-domains", nil).into(t, &user)
	if len(user.Domains) != 1 || len(user.Domains[0]) != 2 || user.Domains[0]["name"] != "porto.ex.id" || user.Quota.Limit != 3 {
		t.Fatalf("user list = %+v; want only {id,name} of enabled domains and the quota", user)
	}
	var admin struct{ Domains []map[string]any }
	h.admin.do("GET", "/api/hosting-domains", nil).into(t, &admin)
	if len(admin.Domains) != 2 || admin.Domains[0]["dns_status"] == nil || admin.Domains[0]["used"] == nil {
		t.Fatalf("admin list = %+v", admin)
	}
}

func TestHostingLifecycle(t *testing.T) {
	h := newHosting(t)
	id := h.register("porto.ex.id")
	h.addUser("budi", "budi-password-1", store.RoleUser)
	c := h.login("budi", "budi-password-1")
	appID, _ := c.createApp("cv-app", nil)
	c.waitStatus(appID, "running")

	r := c.do("POST", domainsPath(appID), obj{"hosting_domain_id": id, "label": "cv-budi"})
	var d store.Domain
	r.into(t, &d)
	if r.status != 201 || d.Host != "cv-budi.porto.ex.id" || d.Label != "cv-budi" {
		t.Fatalf("add subdomain = %d %s", r.status, r.body)
	}
	if !strings.Contains(h.loader.loaded(), `"site:cv-budi.porto.ex.id"`) {
		t.Fatalf("route missing: %s", h.loader.loaded())
	}

	if r := h.admin.do("DELETE", hostingPath(id, ""), nil); r.status != 409 || r.errMsg(t) != "Masih dipakai 1 subdomain; matikan dulu" {
		t.Fatalf("delete in use = %d %s", r.status, r.body)
	}
	if r := h.admin.do("PATCH", hostingPath(id, ""), obj{"enabled": false}); r.status != 200 {
		t.Fatalf("disable = %d %s", r.status, r.body)
	}
	if strings.Contains(h.loader.loaded(), "cv-budi.porto.ex.id") {
		t.Fatalf("route still loaded after disabling: %s", h.loader.loaded())
	}
	if r := c.do("POST", domainsPath(appID), obj{"hosting_domain_id": id, "label": "lain-lagi"}); r.status != 400 || r.errMsg(t) != "Domain hosting tidak tersedia" {
		t.Fatalf("add on disabled = %d %s", r.status, r.body)
	}
	if r := c.do("DELETE", domainsPath(appID)+"/"+strconv.FormatInt(d.ID, 10), nil); r.status != 200 {
		t.Fatalf("remove subdomain = %d %s", r.status, r.body)
	}
	if r := h.admin.do("DELETE", hostingPath(id, ""), nil); r.status != 200 {
		t.Fatalf("delete = %d %s", r.status, r.body)
	}
	if r := h.admin.do("DELETE", hostingPath(id, ""), nil); r.status != 404 {
		t.Fatalf("delete twice = %d %s", r.status, r.body)
	}
	acts := activityActions(t, h.admin, "")
	for _, want := range []string{store.ActHostingAdd, store.ActHostingUpdate, store.ActHostingRemove, store.ActDomainAdd} {
		if !contains(acts, want) {
			t.Errorf("activity %v lacks %s", acts, want)
		}
	}
}

func TestHostingAddReportsDNS(t *testing.T) {
	h := newHosting(t)
	h.svc.DNS = &stubDNS{res: hostdns.Result{Detail: "Belum ada record DNS untuk *.bad.id"}}
	r := h.admin.do("POST", "/api/hosting-domains", obj{"name": "*.bad.id", "reserved": "blog"})
	var v struct {
		Name      string
		DNSStatus string `json:"dns_status"`
		DNSDetail string `json:"dns_detail"`
		Reserved  string
	}
	r.into(t, &v)
	if r.status != 201 || v.Name != "bad.id" || v.DNSStatus != "error" || v.DNSDetail == "" || v.Reserved != "blog" {
		t.Fatalf("add = %d %s", r.status, r.body)
	}
	if r := h.admin.do("POST", "/api/hosting-domains", obj{"name": "bad.id"}); r.status != 409 {
		t.Errorf("duplicate = %d %s", r.status, r.body)
	}
	if r := h.admin.do("POST", "/api/hosting-domains", obj{"name": "x"}); r.status != 400 {
		t.Errorf("bad name = %d %s", r.status, r.body)
	}
}

func TestHostingSubdomainRules(t *testing.T) {
	h := newHosting(t)
	id := h.register("porto.ex.id")
	h.addUser("andi", "andi-password-1", store.RoleUser)
	h.addUser("budi", "budi-password-1", store.RoleUser)
	a := h.login("andi", "andi-password-1")
	b := h.login("budi", "budi-password-1")
	idA, _ := a.createApp("one-app", nil)
	idB, _ := b.createApp("two-app", nil)

	if r := b.do("POST", domainsPath(idB), obj{"host": "budi.com"}); r.status != 403 || r.errMsg(t) != "Domain sendiri hanya bisa ditambahkan admin" {
		t.Fatalf("user own domain = %d %s", r.status, r.body)
	}
	if r := a.do("POST", domainsPath(idA), obj{"hosting_domain_id": id, "label": "kopi"}); r.status != 201 {
		t.Fatalf("A add = %d %s", r.status, r.body)
	}
	if r := b.do("POST", domainsPath(idB), obj{"hosting_domain_id": id, "label": "kopi"}); r.status != 409 || r.errMsg(t) != "Alamat sudah dipakai" {
		t.Fatalf("B takes the same label = %d %s", r.status, r.body)
	}
	if r := b.do("POST", domainsPath(idB), obj{"hosting_domain_id": id, "label": "www"}); r.status != 400 {
		t.Fatalf("reserved label = %d %s", r.status, r.body)
	}
	if r := b.do("POST", domainsPath(idA), obj{"hosting_domain_id": id, "label": "curang"}); r.status != 404 {
		t.Fatalf("B on A's app = %d %s", r.status, r.body)
	}
	for _, label := range []string{"b-one", "b-two"} {
		if r := b.do("POST", domainsPath(idB), obj{"hosting_domain_id": id, "label": label}); r.status != 201 {
			t.Fatalf("B add %s = %d %s", label, r.status, r.body)
		}
	}
	if r := b.do("POST", domainsPath(idB), obj{"hosting_domain_id": id, "label": "b-three"}); r.status != 201 {
		t.Fatalf("third subdomain, within the quota of 3 apps = %d %s", r.status, r.body)
	}
	if r := b.do("POST", domainsPath(idB), obj{"hosting_domain_id": id, "label": "b-four"}); r.status != 409 || r.errMsg(t) != "Kuota subdomain habis (3 dari 3)" {
		t.Fatalf("over quota = %d %s", r.status, r.body)
	}
}

func TestHostingAvailable(t *testing.T) {
	h := newHosting(t)
	id := h.register("porto.ex.id")
	h.addUser("budi", "budi-password-1", store.RoleUser)
	c := h.login("budi", "budi-password-1")
	idB, _ := c.createApp("cv-app", nil)
	c.do("POST", domainsPath(idB), obj{"hosting_domain_id": id, "label": "ada"})

	for label, want := range map[string]bool{"baru": true, "ada": false, "ab": false, "www": false} {
		var out struct {
			Available bool
			Reason    string
		}
		r := c.do("GET", hostingPath(id, "/available?label="+label), nil)
		r.into(t, &out)
		if r.status != 200 || out.Available != want || (!want && out.Reason == "") {
			t.Errorf("available(%s) = %d %s", label, r.status, r.body)
		}
	}
	n, _ := h.store.SubdomainCount(context.Background(), 2)
	if n != 1 {
		t.Errorf("subdomains = %d; checking must not store", n)
	}
	if r := c.do("GET", "/api/hosting-domains/999/available?label=baru", nil); r.status != 200 || strings.Contains(string(r.body), `"available":true`) {
		t.Errorf("unknown domain = %d %s", r.status, r.body)
	}
}
