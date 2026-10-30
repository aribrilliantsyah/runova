package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"runova/internal/api"
	"runova/internal/store"
)

type serverBody struct {
	Docker struct {
		OK             bool
		Version, Error string
	} `json:"docker"`
	Caddy struct {
		OK      bool
		Error   string
		Domains int
	} `json:"caddy"`
	Postgres struct {
		OK             bool
		Version, Error string
		Databases      int
	} `json:"postgres"`
	Host struct {
		Hostname      string `json:"hostname"`
		CPUs          int    `json:"cpus"`
		MemTotalMB    int    `json:"mem_total_mb"`
		MemAllocMB    int    `json:"mem_alloc_mb"`
		CPUAllocMilli int    `json:"cpu_alloc_milli"`
		DiskTotalMB   int    `json:"disk_total_mb"`
		DiskUsedMB    int    `json:"disk_used_mb"`
	} `json:"host"`
	Apps struct{ Total, Running, Stopped int } `json:"apps"`
}

func healthEnv(t *testing.T, h api.Health) (*env, *client) {
	t.Helper()
	dataDir := t.TempDir()
	s, err := store.Open(filepath.Join(dataDir, "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)}
	srv := httptest.NewServer(api.New(api.Config{Store: s, Now: clk.Now, DataDir: dataDir, Health: h}))
	t.Cleanup(func() { srv.Close(); s.Close() })
	e := &env{t: t, srv: srv, store: s, clock: clk, dataDir: dataDir}
	e.addUser("admin", adminPassword, store.RoleAdmin)
	return e, e.login("admin", adminPassword)
}

func ok(detail string) api.Check {
	return func(context.Context) (string, error) { return detail, nil }
}

func TestServerStatus(t *testing.T) {
	_, c := healthEnv(t, api.Health{Docker: ok("28.4.0"), Caddy: ok(""), Postgres: ok("18.6")})
	r := c.do("GET", "/api/server", nil)
	if r.status != http.StatusOK {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	var b serverBody
	r.into(t, &b)
	if !b.Docker.OK || b.Docker.Version != "28.4.0" {
		t.Errorf("docker = %+v", b.Docker)
	}
	if !b.Caddy.OK || !b.Postgres.OK || b.Postgres.Version != "18.6" {
		t.Errorf("caddy = %+v postgres = %+v", b.Caddy, b.Postgres)
	}
	if b.Host.CPUs < 1 || b.Host.MemTotalMB < 1 || b.Host.DiskTotalMB < 1 || b.Host.DiskUsedMB > b.Host.DiskTotalMB {
		t.Errorf("host = %+v", b.Host)
	}
}

func TestServerStatusDependencyDown(t *testing.T) {
	_, c := healthEnv(t, api.Health{
		Docker:   ok("28.4.0"),
		Caddy:    func(context.Context) (string, error) { return "", errors.New("connection refused") },
		Postgres: func(context.Context) (string, error) { return "", errors.New("ping postgres: refused") },
	})
	r := c.do("GET", "/api/server", nil)
	if r.status != http.StatusOK {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	var b serverBody
	r.into(t, &b)
	if !b.Docker.OK {
		t.Errorf("docker should stay ok: %+v", b.Docker)
	}
	if b.Postgres.OK || b.Postgres.Error == "" {
		t.Errorf("postgres = %+v, want down with a reason", b.Postgres)
	}
	if b.Caddy.OK || b.Caddy.Error == "" {
		t.Errorf("caddy = %+v, want down with a reason", b.Caddy)
	}
}

func TestServerStatusAdminOnly(t *testing.T) {
	e, _ := healthEnv(t, api.Health{Docker: ok("x"), Caddy: ok(""), Postgres: ok("")})
	e.addUser("budi", "budi-password-1", store.RoleUser)
	if r := e.login("budi", "budi-password-1").do("GET", "/api/server", nil); r.status != http.StatusForbidden {
		t.Errorf("user: status %d, want 403", r.status)
	}
	if r := e.client().do("GET", "/api/server", nil); r.status != http.StatusUnauthorized {
		t.Errorf("anonymous: status %d, want 401", r.status)
	}
}

func TestInfoForEveryUser(t *testing.T) {
	e, admin := healthEnv(t, api.Health{})
	e.addUser("budi", "budi-password-1", store.RoleUser)
	if r := admin.do("PUT", "/api/settings", obj{
		"default_quota":   obj{"apps": 3, "memory_mb": 1024, "cpu_milli": 1000, "databases": 3, "pg_connections": 20},
		"upload_limit_mb": 200, "max_pending_uploads": 5, "port_min": 20000, "port_max": 20999, "public_ip": "203.0.113.15",
		"runtimes": obj{"java8": false, "java17": true, "static": true},
	}); r.status != 200 {
		t.Fatalf("settings: %d %s", r.status, r.body)
	}
	r := e.login("budi", "budi-password-1").do("GET", "/api/info", nil)
	if r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	var info struct {
		UploadLimitMB int             `json:"upload_limit_mb"`
		PublicIP      string          `json:"public_ip"`
		Runtimes      map[string]bool `json:"runtimes"`
	}
	r.into(t, &info)
	if info.UploadLimitMB != 200 || info.PublicIP != "203.0.113.15" || info.Runtimes["java8"] || !info.Runtimes["java17"] {
		t.Errorf("info = %+v", info)
	}
	if len(r.body) == 0 || strings.Contains(string(r.body), "pg_allowed_nets") || strings.Contains(string(r.body), "default_quota") {
		t.Errorf("info leaks admin settings: %s", r.body)
	}
}

func TestUsageByUser(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	e.addUser("sari", "sari-password-1", store.RoleUser)
	budi := e.login("budi", "budi-password-1")
	id, _ := budi.createApp("billing", obj{"memory_mb": 256, "cpu_milli": 250})
	budi.waitStatus(id, "running")
	budi.createApp("worker", obj{"memory_mb": 128, "cpu_milli": 100})

	admin := e.login("admin", adminPassword)
	r := admin.do("GET", "/api/usage", nil)
	if r.status != http.StatusOK {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	var got map[string]struct {
		Apps      int `json:"apps"`
		MemoryMB  int `json:"memory_mb"`
		CPUMilli  int `json:"cpu_milli"`
		Databases int `json:"databases"`
	}
	r.into(t, &got)
	users, _ := e.store.Users(context.Background())
	idOf := map[string]string{}
	for _, u := range users {
		idOf[u.Username] = strconv.FormatInt(u.ID, 10)
	}
	b := got[idOf["budi"]]
	if b.Apps != 2 || b.MemoryMB != 384 || b.CPUMilli != 350 {
		t.Errorf("budi usage = %+v, want 2 apps, 384 MB, 350 millicores", b)
	}
	if s := got[idOf["sari"]]; s.Apps != 0 || s.MemoryMB != 0 {
		t.Errorf("sari usage = %+v, want zero", s)
	}
	if _, ok := got[idOf["sari"]]; !ok {
		t.Errorf("a user without apps must still have an entry")
	}
	if r := budi.do("GET", "/api/usage", nil); r.status != http.StatusForbidden {
		t.Errorf("non-admin status %d, want 403", r.status)
	}
}
