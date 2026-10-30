package api_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"runova/internal/apps"
	"runova/internal/caddy"
	"runova/internal/store"
)

// caddyLoader is a Caddy that keeps the last configuration it was given.
type caddyLoader struct {
	mu     sync.Mutex
	config string
	err    error
}

func (l *caddyLoader) Load(_ context.Context, cfg []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	l.config = string(cfg)
	return nil
}

func (l *caddyLoader) Config(context.Context) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return []byte(l.config), nil
}

func (l *caddyLoader) loaded() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.config
}

func (l *caddyLoader) fail(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.err = err
}

// withCaddy connects the app service to a Syncer that loads into a fake Caddy.
func withCaddy(e *appEnv) *caddyLoader {
	loader := &caddyLoader{}
	e.svc.Caddy = &caddy.Syncer{
		Client: loader,
		State: func(ctx context.Context) (caddy.State, error) {
			return apps.CaddyState(ctx, e.store, "127.0.0.1:7070")
		},
	}
	e.svc.Probe = func(context.Context, string) (bool, string) { return false, "belum" }
	return loader
}

func domainsPath(id int64) string { return appPath(id, "/domains") }

func addDomain(c *client, id int64, host string) resp {
	return c.do("POST", domainsPath(id), obj{"host": host})
}

func TestDomainUniqueness(t *testing.T) {
	e := newAppEnv(t, 0)
	withCaddy(e)
	e.addUser("andi", "andi-password-1", store.RoleUser)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	a := e.login("andi", "andi-password-1")
	b := e.login("budi", "budi-password-1")
	admin := e.login("admin", adminPassword)
	idA, _ := a.createApp("one", nil)
	idB, _ := b.createApp("two", nil)

	if r := addDomain(a, idA, "x.co"); r.status != 201 {
		t.Fatalf("A adds x.co = %d %s", r.status, r.body)
	}
	r := addDomain(b, idB, "X.co.")
	if r.status != 409 || r.errMsg(t) != "Domain sudah dipakai" {
		t.Fatalf("B adds X.co. = %d %s, want 409 Domain sudah dipakai", r.status, r.body)
	}
	if strings.Contains(string(r.body), "andi") || strings.Contains(string(r.body), "one") {
		t.Errorf("conflict names the owner: %s", r.body)
	}

	if r := admin.do("PUT", "/api/settings", withPanel(t, admin, obj{"panel_domain": "p.co"})); r.status != 200 {
		t.Fatalf("set panel domain = %d %s", r.status, r.body)
	}
	if r := addDomain(b, idB, "p.co"); r.status != 409 || r.errMsg(t) != "Domain sudah dipakai" {
		t.Fatalf("adding the panel domain = %d %s", r.status, r.body)
	}
	if r := admin.do("PUT", "/api/settings", withPanel(t, admin, obj{"panel_domain": "x.co"})); r.status != 409 {
		t.Fatalf("panel domain equal to an app domain = %d %s, want 409", r.status, r.body)
	}
	if r := addDomain(b, idB, "203.0.113.5"); r.status != 400 {
		t.Fatalf("IP as domain = %d %s", r.status, r.body)
	}
	if r := addDomain(b, idA, "y.co"); r.status != 404 {
		t.Fatalf("B adds to A's app = %d %s", r.status, r.body)
	}
}

// withPanel returns the current settings with the given fields replaced.
func withPanel(t *testing.T, c *client, fields obj) obj {
	t.Helper()
	r := c.do("GET", "/api/settings", nil)
	var cur obj
	r.into(t, &cur)
	for k, v := range fields {
		cur[k] = v
	}
	return cur
}

func TestDomainLimit(t *testing.T) {
	e := newAppEnv(t, 0)
	withCaddy(e)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id, _ := c.createApp("one", nil)
	for _, h := range []string{"a.co", "b.co", "c.co", "d.co", "e.co"} {
		if r := addDomain(c, id, h); r.status != 201 {
			t.Fatalf("add %s = %d %s", h, r.status, r.body)
		}
	}
	r := addDomain(c, id, "f.co")
	if r.status != 400 || r.errMsg(t) != "Maksimal 5 domain per aplikasi" {
		t.Fatalf("sixth = %d %s", r.status, r.body)
	}
}

func TestAddDomainCaddyRejects(t *testing.T) {
	e := newAppEnv(t, 0)
	loader := withCaddy(e)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id, _ := c.createApp("one", nil)
	c.waitStatus(id, "running")

	loader.fail(errors.New("caddy POST /load: 400 listening on :443: address already in use"))
	r := addDomain(c, id, "x.co")
	if r.status != 502 || r.errMsg(t) != "Caddy menolak konfigurasi: caddy POST /load: 400 listening on :443: address already in use" {
		t.Fatalf("add = %d %s", r.status, r.body)
	}
	var list struct{ Domains []store.Domain }
	c.do("GET", domainsPath(id), nil).into(t, &list)
	if len(list.Domains) != 0 {
		t.Fatalf("domain kept after Caddy refused it: %+v", list.Domains)
	}
	if acts := activityActions(t, c, ""); contains(acts, store.ActDomainAdd) {
		t.Errorf("activity records an add that was rolled back: %v", acts)
	}

	loader.fail(nil)
	if r := addDomain(c, id, "x.co"); r.status != 201 {
		t.Fatalf("add after recovery = %d %s", r.status, r.body)
	}
	if !strings.Contains(loader.loaded(), `"site:x.co"`) {
		t.Errorf("route missing from the loaded config: %s", loader.loaded())
	}
}

func TestDomainEndpoints(t *testing.T) {
	e := newAppEnv(t, 0)
	loader := withCaddy(e)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id, _ := c.createApp("one", nil)
	c.waitStatus(id, "running")

	var d store.Domain
	r := addDomain(c, id, "App.X.co.")
	r.into(t, &d)
	if r.status != 201 || d.Host != "app.x.co" || d.Status != store.DomainPending {
		t.Fatalf("add = %d %s", r.status, r.body)
	}
	if !strings.Contains(loader.loaded(), `"site:app.x.co"`) {
		t.Errorf("route missing from the loaded config: %s", loader.loaded())
	}

	did := strconv.FormatInt(d.ID, 10)
	r = c.do("POST", domainsPath(id)+"/"+did+"/check", nil)
	var checked store.Domain
	r.into(t, &checked)
	if r.status != 200 || checked.Status != store.DomainPending || checked.Detail != "belum" {
		t.Fatalf("check = %d %s", r.status, r.body)
	}
	e.svc.Probe = func(context.Context, string) (bool, string) { return true, "" }
	r = c.do("POST", domainsPath(id)+"/"+did+"/check", nil)
	r.into(t, &checked)
	if r.status != 200 || checked.Status != store.DomainActive {
		t.Fatalf("check after success = %d %s", r.status, r.body)
	}

	if r := c.do("DELETE", domainsPath(id)+"/"+did, nil); r.status != 200 {
		t.Fatalf("delete = %d %s", r.status, r.body)
	}
	if strings.Contains(loader.loaded(), "app.x.co") {
		t.Errorf("route still loaded after delete: %s", loader.loaded())
	}
	if r := c.do("DELETE", domainsPath(id)+"/"+did, nil); r.status != 400 {
		t.Errorf("second delete = %d %s", r.status, r.body)
	}
	acts := activityActions(t, c, "")
	if !contains(acts, store.ActDomainAdd) || !contains(acts, store.ActDomainRemove) {
		t.Errorf("activity = %v", acts)
	}
}

func TestIPAccessToggleAppliesCaddy(t *testing.T) {
	e := newAppEnv(t, 0)
	loader := withCaddy(e)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id, _ := c.createApp("one", obj{"ip_access": true})
	c.waitStatus(id, "running")
	if err := e.svc.ApplyCaddy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loader.loaded(), `"runova-ip-20000"`) {
		t.Fatalf("IP listener missing while ip_access is on: %s", loader.loaded())
	}

	if r := c.do("PATCH", appPath(id, ""), obj{"ip_access": false}); r.status != 200 {
		t.Fatalf("PATCH off = %d %s", r.status, r.body)
	}
	if strings.Contains(loader.loaded(), "runova-ip-20000") {
		t.Fatalf("IP listener still loaded after ip_access off: %s", loader.loaded())
	}
	if r := c.do("PATCH", appPath(id, ""), obj{"ip_access": true}); r.status != 200 {
		t.Fatalf("PATCH on = %d %s", r.status, r.body)
	}
	if !strings.Contains(loader.loaded(), `"runova-ip-20000"`) {
		t.Fatalf("IP listener missing after ip_access on: %s", loader.loaded())
	}

	loader.fail(errors.New("nope"))
	r := c.do("PATCH", appPath(id, ""), obj{"ip_access": false})
	if r.status != 502 {
		t.Fatalf("PATCH while Caddy refuses = %d %s, want 502", r.status, r.body)
	}
	var body struct{ App store.App }
	c.do("GET", appPath(id, ""), nil).into(t, &body)
	if !body.App.IPAccess {
		t.Error("ip_access changed although Caddy refused")
	}
}

func TestGuideWithoutPublicIP(t *testing.T) {
	e := newAppEnv(t, 0)
	withCaddy(e)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	admin := e.login("admin", adminPassword)
	c := e.login("budi", "budi-password-1")
	id, _ := c.createApp("one", nil)

	guide := func() (string, []string) {
		var body struct {
			PublicIP string   `json:"public_ip"`
			Guide    []string `json:"guide"`
		}
		r := c.do("GET", domainsPath(id), nil)
		if r.status != 200 {
			t.Fatalf("GET domains = %d %s", r.status, r.body)
		}
		r.into(t, &body)
		return body.PublicIP, body.Guide
	}
	if ip, g := guide(); ip != "" || strings.Join(g, ",") != "tunnel" {
		t.Fatalf("without public IP: ip=%q guide=%v, want [tunnel]", ip, g)
	}
	if r := admin.do("PUT", "/api/settings", withPanel(t, admin, obj{"public_ip": "203.0.113.5"})); r.status != 200 {
		t.Fatalf("set public ip = %d %s", r.status, r.body)
	}
	if ip, g := guide(); ip != "203.0.113.5" || strings.Join(g, ",") != "dns,proxy,tunnel" {
		t.Fatalf("with public IP: ip=%q guide=%v", ip, g)
	}
}

func TestPanelAddressSettingsApplyCaddy(t *testing.T) {
	e := newAppEnv(t, 0)
	loader := withCaddy(e)
	admin := e.login("admin", adminPassword)

	if r := admin.do("GET", "/api/settings", nil); !strings.Contains(string(r.body), `"panel_domain":""`) || !strings.Contains(string(r.body), `"panel_port":0`) {
		t.Fatalf("settings lack the panel address: %s", r.body)
	}
	if r := admin.do("PUT", "/api/settings", withPanel(t, admin, obj{"panel_domain": "panel.a.co", "panel_port": 8443})); r.status != 200 {
		t.Fatalf("set panel address = %d %s", r.status, r.body)
	}
	cfg := loader.loaded()
	if !strings.Contains(cfg, `"panel:panel.a.co"`) || !strings.Contains(cfg, `"runova-panel-ip"`) {
		t.Fatalf("panel routes missing: %s", cfg)
	}
	if acts := activityActions(t, admin, ""); !contains(acts, store.ActPanelAddress) {
		t.Errorf("activity = %v, want %s", acts, store.ActPanelAddress)
	}

	loader.fail(errors.New("refused"))
	r := admin.do("PUT", "/api/settings", withPanel(t, admin, obj{"panel_domain": "other.a.co", "panel_port": 0}))
	if r.status != 502 {
		t.Fatalf("refused change = %d %s, want 502", r.status, r.body)
	}
	loader.fail(nil)
	var got store.Settings
	admin.do("GET", "/api/settings", nil).into(t, &got)
	if got.PanelDomain != "panel.a.co" || got.PanelPort != 8443 {
		t.Fatalf("settings after a refused change = %+v, want the previous panel address", got)
	}

	if r := admin.do("PUT", "/api/settings", withPanel(t, admin, obj{"panel_domain": "Bad Domain"})); r.status != 400 {
		t.Fatalf("invalid domain = %d %s", r.status, r.body)
	}
}

func TestSettingsWithoutPanelChangeDoNotTouchCaddy(t *testing.T) {
	e := newAppEnv(t, 0)
	loader := withCaddy(e)
	admin := e.login("admin", adminPassword)
	if r := admin.do("PUT", "/api/settings", withPanel(t, admin, obj{"upload_limit_mb": 400})); r.status != 200 {
		t.Fatalf("PUT = %d %s", r.status, r.body)
	}
	if loader.loaded() != "" {
		t.Errorf("Caddy was loaded for a setting that does not affect it: %s", loader.loaded())
	}
	if acts := activityActions(t, admin, ""); contains(acts, store.ActPanelAddress) {
		t.Errorf("activity = %v", acts)
	}
}
