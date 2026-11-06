package api_test

import (
	"net/http"
	"testing"

	"runova/internal/store"
)

type userPage struct {
	Items []store.UserRow `json:"items"`
	Total int             `json:"total"`
	Limit int             `json:"limit"`
	Roles store.RoleCounts
}

func TestListUsersPagingFilterSort(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	e.addUser("sari", "sari-password-1", store.RoleUser)
	e.addUser("tono", "tono-password-1", store.RoleAdmin)
	budi := e.login("budi", "budi-password-1")
	id, _ := budi.createApp("billing", obj{"memory_mb": 256})
	budi.waitStatus(id, "running")
	budi.createApp("worker", obj{"memory_mb": 128})
	admin := e.login("admin", adminPassword)

	get := func(query string) userPage {
		t.Helper()
		r := admin.do("GET", "/api/users"+query, nil)
		if r.status != 200 {
			t.Fatalf("%s: %d %s", query, r.status, r.body)
		}
		var p userPage
		r.into(t, &p)
		return p
	}
	names := func(p userPage) []string {
		var out []string
		for _, u := range p.Items {
			out = append(out, u.Username)
		}
		return out
	}

	p := get("")
	if p.Total != 4 || p.Limit != 25 || p.Roles.Admin != 2 || p.Roles.User != 2 {
		t.Fatalf("default page = total %d limit %d roles %+v", p.Total, p.Limit, p.Roles)
	}
	if b := p.Items[1]; b.Username != "budi" || b.Usage.Apps != 2 || b.Usage.MemoryMB != 384 || b.LastLoginAt == 0 {
		t.Fatalf("budi row = %+v", b)
	}
	if s := p.Items[2]; s.Username != "sari" || s.Usage != (store.Usage{}) || s.LastLoginAt != 0 {
		t.Fatalf("sari row = %+v", s)
	}
	if got := names(get("?sort=memory&order=desc&limit=1")); len(got) != 1 || got[0] != "budi" {
		t.Fatalf("top memory = %v", got)
	}
	if got := names(get("?role=admin&sort=username&order=desc")); len(got) != 2 || got[0] != "tono" || got[1] != "admin" {
		t.Fatalf("admins desc = %v", got)
	}
	if p := get("?q=SAR&role=user"); p.Total != 1 || p.Items[0].Username != "sari" || p.Roles.All != 1 {
		t.Fatalf("q=SAR = %v %+v", names(p), p.Roles)
	}
	if p := get("?limit=2&offset=2"); p.Total != 4 || len(p.Items) != 2 || p.Items[0].Username != "sari" {
		t.Fatalf("page 2 = %v", names(p))
	}
	for _, q := range []string{"?sort=password_hash", "?order=up", "?role=root", "?limit=x", "?offset=-1"} {
		if r := admin.do("GET", "/api/users"+q, nil); r.status != 400 || r.errMsg(t) == "" {
			t.Errorf("%s: %d %s", q, r.status, r.body)
		}
	}
	if r := admin.do("GET", "/api/users?sort=password_hash", nil); string(r.body) == "" {
		t.Fatal("empty error body")
	}
	if r := budi.do("GET", "/api/users", nil); r.status != http.StatusForbidden {
		t.Errorf("non-admin = %d, want 403", r.status)
	}
	if r := admin.do("GET", "/api/usage", nil); r.status != http.StatusNotFound {
		t.Errorf("/api/usage is merged into /api/users, got %d", r.status)
	}
}
