package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"runova/internal/api"
	"runova/internal/auth"
	"runova/internal/store"
)

const adminPassword = "admin-password-1"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type env struct {
	t       *testing.T
	srv     *httptest.Server
	store   *store.Store
	clock   *clock
	dataDir string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dataDir := t.TempDir()
	s, err := store.Open(filepath.Join(dataDir, "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)}
	srv := httptest.NewServer(api.New(api.Config{Store: s, Now: clk.Now, DataDir: dataDir}))
	t.Cleanup(func() { srv.Close(); s.Close() })
	e := &env{t: t, srv: srv, store: s, clock: clk, dataDir: dataDir}
	e.addUser("admin", adminPassword, store.RoleAdmin)
	return e
}

func (e *env) addUser(username, password string, role store.Role) store.User {
	e.t.Helper()
	h, err := auth.HashPassword(password)
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := e.store.CreateUser(context.Background(), store.NewUser{
		Username: username, PasswordHash: h, Role: role,
		Quota: store.Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 20},
	}, e.clock.Now().UnixMilli())
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

type client struct {
	e   *env
	c   *http.Client
	hdr http.Header
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, c: &http.Client{Jar: jar}, hdr: http.Header{}}
}

func (e *env) login(username, password string) *client {
	e.t.Helper()
	c := e.client()
	if r := c.do("POST", "/api/login", map[string]string{"username": username, "password": password}); r.status != 200 {
		e.t.Fatalf("login %s: %d %s", username, r.status, r.body)
	}
	return c
}

type resp struct {
	status int
	header http.Header
	body   []byte
}

func (r resp) into(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}
}

func (r resp) errMsg(t *testing.T) string {
	t.Helper()
	var b struct{ Error string }
	r.into(t, &b)
	return b.Error
}

func (c *client) raw(method, path, contentType string, body io.Reader, hdr http.Header) resp {
	c.e.t.Helper()
	req, err := http.NewRequest(method, c.e.srv.URL+path, body)
	if err != nil {
		c.e.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range c.hdr {
		req.Header[k] = v
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	res, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, header: res.Header, body: b}
}

func (c *client) do(method, path string, body any) resp {
	c.e.t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.e.t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	return c.raw(method, path, "application/json", r, nil)
}

type obj = map[string]any

func newUserBody(username string) obj {
	return obj{"username": username, "display_name": strings.ToUpper(username), "email": username + "@x.id", "password": "rahasia-panjang", "role": "user"}
}

func activityActions(t *testing.T, c *client, query string) []string {
	t.Helper()
	r := c.do("GET", "/api/activity"+query, nil)
	if r.status != 200 {
		t.Fatalf("activity: %d %s", r.status, r.body)
	}
	var list []store.Activity
	r.into(t, &list)
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = a.Action
	}
	return out
}

func countOf(list []string, s string) int {
	n := 0
	for _, x := range list {
		if x == s {
			n++
		}
	}
	return n
}

func TestLoginFlowAndMe(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	if r := c.do("GET", "/api/me", nil); r.status != 401 {
		t.Fatalf("me before login: %d", r.status)
	}
	r := c.do("POST", "/api/login", obj{"username": "admin", "password": adminPassword})
	if r.status != 200 {
		t.Fatalf("login: %d %s", r.status, r.body)
	}
	var u store.User
	r.into(t, &u)
	if u.Username != "admin" || u.Role != store.RoleAdmin {
		t.Fatalf("login user = %+v", u)
	}
	if strings.Contains(string(r.body), "password") {
		t.Fatalf("login response leaks password field: %s", r.body)
	}
	r = c.do("GET", "/api/me", nil)
	if r.status != 200 {
		t.Fatalf("me: %d", r.status)
	}
	r.into(t, &u)
	if u.Username != "admin" {
		t.Fatalf("me = %+v", u)
	}
	if r := c.do("POST", "/api/logout", nil); r.status != 200 {
		t.Fatalf("logout: %d", r.status)
	}
	if r := c.do("GET", "/api/me", nil); r.status != 401 {
		t.Fatalf("me after logout: %d", r.status)
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	for name, body := range map[string]obj{
		"wrong password": {"username": "admin", "password": "salah-salah-salah"},
		"unknown user":   {"username": "hantu", "password": "salah-salah-salah"},
		"empty":          {"username": "", "password": ""},
		"huge username":  {"username": strings.Repeat("a", 5000), "password": "x"},
	} {
		r := c.do("POST", "/api/login", body)
		if r.status != 401 || r.errMsg(t) != "Username atau password salah" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
}

func TestLoginLockout(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	for i := range 5 {
		if r := c.do("POST", "/api/login", obj{"username": "admin", "password": "salah-salah-salah"}); r.status != 401 {
			t.Fatalf("attempt %d: %d", i+1, r.status)
		}
	}
	r := c.do("POST", "/api/login", obj{"username": "admin", "password": adminPassword})
	if r.status != 429 {
		t.Fatalf("6th attempt: %d %s", r.status, r.body)
	}
	if got := r.header.Get("Retry-After"); got != "900" {
		t.Fatalf("Retry-After = %q", got)
	}
	want := "Terlalu banyak percobaan. Coba lagi pukul " + e.clock.Now().Add(15*time.Minute).Local().Format("15:04")
	if msg := r.errMsg(t); msg != want {
		t.Fatalf("message = %q, want %q", msg, want)
	}

	other := e.client()
	other.hdr.Set("X-Forwarded-For", "198.51.100.7")
	if r := other.do("POST", "/api/login", obj{"username": "admin", "password": adminPassword}); r.status != 200 {
		t.Fatalf("different client ip must not be locked: %d %s", r.status, r.body)
	}

	e.clock.advance(15 * time.Minute)
	if r := c.do("POST", "/api/login", obj{"username": "admin", "password": adminPassword}); r.status != 200 {
		t.Fatalf("after lockout: %d %s", r.status, r.body)
	}
	actions := activityActions(t, c, "?limit=200")
	if n := countOf(actions, store.ActLoginFailed); n != 5 {
		t.Fatalf("login_failed entries = %d, want 5 (%v)", n, actions)
	}
}

func TestSuccessfulLoginClearsFailures(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	for range 4 {
		c.do("POST", "/api/login", obj{"username": "admin", "password": "salah-salah-salah"})
	}
	if r := c.do("POST", "/api/login", obj{"username": "admin", "password": adminPassword}); r.status != 200 {
		t.Fatalf("login: %d", r.status)
	}
	for range 4 {
		c.do("POST", "/api/login", obj{"username": "admin", "password": "salah-salah-salah"})
	}
	if r := c.do("POST", "/api/login", obj{"username": "admin", "password": adminPassword}); r.status != 200 {
		t.Fatalf("failures before a success still counted: %d", r.status)
	}
}

func TestAdminOnly(t *testing.T) {
	e := newEnv(t)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/users"},
		{"POST", "/api/users"},
		{"PATCH", "/api/users/1"},
		{"DELETE", "/api/users/1"},
		{"POST", "/api/users/1/password"},
		{"GET", "/api/settings"},
		{"PUT", "/api/settings"},
	} {
		if r := c.do(tc.method, tc.path, obj{}); r.status != 403 {
			t.Errorf("%s %s = %d, want 403", tc.method, tc.path, r.status)
		}
	}
	anon := e.client()
	if r := anon.do("GET", "/api/users", nil); r.status != 401 {
		t.Errorf("anonymous GET /api/users = %d", r.status)
	}
	if r := anon.do("GET", "/api/activity", nil); r.status != 401 {
		t.Errorf("anonymous GET /api/activity = %d", r.status)
	}
}

func TestCreateUserGetsDefaultQuota(t *testing.T) {
	e := newEnv(t)
	c := e.login("admin", adminPassword)
	var s store.Settings
	c.do("GET", "/api/settings", nil).into(t, &s)
	s.DefaultQuota.Apps = 7
	if r := c.do("PUT", "/api/settings", s); r.status != 200 {
		t.Fatalf("PUT settings: %d %s", r.status, r.body)
	}
	r := c.do("POST", "/api/users", newUserBody("budi"))
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	var u store.User
	r.into(t, &u)
	if u.Quota.Apps != 7 || u.Username != "budi" || u.Role != store.RoleUser || u.DisplayName != "BUDI" || u.Email != "budi@x.id" || u.ID == 0 {
		t.Fatalf("created = %+v", u)
	}
	var generic obj
	r.into(t, &generic)
	for k := range generic {
		if strings.Contains(k, "password") {
			t.Fatalf("response has %q key", k)
		}
	}
	got := e.login("budi", "rahasia-panjang").do("GET", "/api/me", nil)
	if got.status != 200 {
		t.Fatalf("new user cannot log in: %d", got.status)
	}
	var list []store.User
	c.do("GET", "/api/users", nil).into(t, &list)
	if len(list) != 2 || list[0].Username != "admin" || list[1].Username != "budi" {
		t.Fatalf("users = %+v", list)
	}
}

func TestCreateUserValidation(t *testing.T) {
	e := newEnv(t)
	c := e.login("admin", adminPassword)
	with := func(k string, v any) obj {
		b := newUserBody("budi")
		b[k] = v
		return b
	}
	for name, body := range map[string]obj{
		"uppercase":      with("username", "Budi"),
		"reserved":       with("username", "postgres"),
		"short password": with("password", "pendek"),
		"bad role":       with("role", "superuser"),
		"bad email":      with("email", "bukan email"),
		"long name":      with("display_name", strings.Repeat("x", 101)),
	} {
		if r := c.do("POST", "/api/users", body); r.status != 400 || r.errMsg(t) == "" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := c.do("POST", "/api/users", newUserBody("budi")); r.status != 201 {
		t.Fatalf("valid create: %d %s", r.status, r.body)
	}
	r := c.do("POST", "/api/users", newUserBody("budi"))
	if r.status != 409 || r.errMsg(t) != "Username sudah dipakai" {
		t.Fatalf("duplicate: %d %s", r.status, r.body)
	}
	if r := c.raw("POST", "/api/users", "application/json", strings.NewReader("{"), nil); r.status != 400 {
		t.Fatalf("malformed JSON: %d", r.status)
	}
}

func TestUpdateUserAndQuota(t *testing.T) {
	e := newEnv(t)
	c := e.login("admin", adminPassword)
	var u store.User
	c.do("POST", "/api/users", newUserBody("budi")).into(t, &u)
	path := "/api/users/" + strconv.FormatInt(u.ID, 10)

	r := c.do("PATCH", path, obj{"display_name": "Budi S", "quota": obj{"apps": 5, "memory_mb": 2048, "cpu_milli": 2000, "databases": 4, "pg_connections": 30}})
	if r.status != 200 {
		t.Fatalf("patch: %d %s", r.status, r.body)
	}
	r.into(t, &u)
	if u.DisplayName != "Budi S" || u.Email != "budi@x.id" || u.Quota != (store.Quota{Apps: 5, MemoryMB: 2048, CPUMilli: 2000, Databases: 4, PGConnections: 30}) {
		t.Fatalf("patched = %+v", u)
	}
	if r := c.do("PATCH", path, obj{"quota": obj{"apps": 5, "memory_mb": 1, "cpu_milli": 1, "databases": 1, "pg_connections": 0}}); r.status != 400 {
		t.Fatalf("invalid quota: %d %s", r.status, r.body)
	}
	if r := c.do("PATCH", path, obj{"role": "dewa"}); r.status != 400 {
		t.Fatalf("invalid role: %d", r.status)
	}
	if r := c.do("PATCH", path, obj{"email": "bukan email"}); r.status != 400 {
		t.Fatalf("invalid email: %d", r.status)
	}
	if r := c.do("PATCH", path, obj{"role": "admin"}); r.status != 200 {
		t.Fatalf("promote: %d %s", r.status, r.body)
	}
	if r := c.do("PATCH", "/api/users/9999", obj{"display_name": "x"}); r.status != 404 || r.errMsg(t) != "Pengguna tidak ditemukan" {
		t.Fatalf("missing user: %d %s", r.status, r.body)
	}
	if r := c.do("PATCH", "/api/users/abc", obj{"display_name": "x"}); r.status != 404 {
		t.Fatalf("non numeric id: %d", r.status)
	}
	actions := activityActions(t, c, "?limit=200")
	if countOf(actions, store.ActQuotaUpdate) != 1 || countOf(actions, store.ActUserUpdate) != 2 {
		t.Fatalf("activity = %v", actions)
	}
}

func TestLastAdminProtected(t *testing.T) {
	e := newEnv(t)
	c := e.login("admin", adminPassword)
	var me store.User
	c.do("GET", "/api/me", nil).into(t, &me)
	path := "/api/users/" + strconv.FormatInt(me.ID, 10)

	r := c.do("PATCH", path, obj{"role": "user"})
	if r.status != 409 || r.errMsg(t) != "Minimal harus ada satu admin" {
		t.Fatalf("demote only admin: %d %s", r.status, r.body)
	}
	r = c.do("DELETE", path, nil)
	if r.status != 409 || r.errMsg(t) != "Tidak bisa menghapus akun sendiri" {
		t.Fatalf("delete self: %d %s", r.status, r.body)
	}

	var second store.User
	c.do("POST", "/api/users", func() obj { b := newUserBody("bayu"); b["role"] = "admin"; return b }()).into(t, &second)
	r = c.do("PATCH", path, obj{"role": "user"})
	if r.status != 409 || r.errMsg(t) != "Tidak bisa menurunkan peran akun sendiri" {
		t.Fatalf("self demote with another admin: %d %s", r.status, r.body)
	}
	if r := c.do("GET", "/api/me", nil); r.status != 200 {
		t.Fatalf("me after rejected demote: %d", r.status)
	}
	if r := c.do("PATCH", path, obj{"role": "admin", "display_name": "Root Admin"}); r.status != 200 {
		t.Fatalf("no-op role patch: %d %s", r.status, r.body)
	}
	secondPath := "/api/users/" + strconv.FormatInt(second.ID, 10)
	if r := c.do("PATCH", secondPath, obj{"role": "user"}); r.status != 200 {
		t.Fatalf("demote other admin: %d %s", r.status, r.body)
	}
}

func TestDeleteUser(t *testing.T) {
	e := newEnv(t)
	c := e.login("admin", adminPassword)
	var u store.User
	c.do("POST", "/api/users", newUserBody("budi")).into(t, &u)
	path := "/api/users/" + strconv.FormatInt(u.ID, 10)
	if r := c.do("DELETE", path, nil); r.status != 200 {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := c.do("DELETE", path, nil); r.status != 404 {
		t.Fatalf("delete twice: %d", r.status)
	}
	if actions := activityActions(t, c, ""); countOf(actions, store.ActUserDelete) != 1 {
		t.Fatalf("activity = %v", actions)
	}
}

func TestDeletedUserSessionRejected(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin", adminPassword)
	var u store.User
	admin.do("POST", "/api/users", newUserBody("budi")).into(t, &u)
	budi := e.login("budi", "rahasia-panjang")
	if r := budi.do("GET", "/api/me", nil); r.status != 200 {
		t.Fatalf("me: %d", r.status)
	}
	if r := admin.do("DELETE", "/api/users/"+strconv.FormatInt(u.ID, 10), nil); r.status != 200 {
		t.Fatalf("delete: %d", r.status)
	}
	if r := budi.do("GET", "/api/me", nil); r.status != 401 {
		t.Fatalf("me after delete: %d, want 401", r.status)
	}
	if r := budi.do("GET", "/api/activity", nil); r.status != 401 {
		t.Fatalf("activity after delete: %d, want 401", r.status)
	}
}

func TestPasswordChangeKillsOtherSessions(t *testing.T) {
	e := newEnv(t)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	a := e.login("budi", "budi-password-1")
	b := e.login("budi", "budi-password-1")

	if r := a.do("POST", "/api/me/password", obj{"current": "salah-salah-salah", "new": "password-baru-123"}); r.status != 400 {
		t.Fatalf("wrong current: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/api/me/password", obj{"current": "budi-password-1", "new": "pendek"}); r.status != 400 {
		t.Fatalf("short new: %d %s", r.status, r.body)
	}
	if r := b.do("GET", "/api/me", nil); r.status != 200 {
		t.Fatalf("B after rejected changes: %d", r.status)
	}
	if r := a.do("POST", "/api/me/password", obj{"current": "budi-password-1", "new": "password-baru-123"}); r.status != 200 {
		t.Fatalf("change: %d %s", r.status, r.body)
	}
	if r := a.do("GET", "/api/me", nil); r.status != 200 {
		t.Fatalf("A after change: %d, want 200", r.status)
	}
	if r := b.do("GET", "/api/me", nil); r.status != 401 {
		t.Fatalf("B after change: %d, want 401", r.status)
	}
	if r := e.client().do("POST", "/api/login", obj{"username": "budi", "password": "budi-password-1"}); r.status != 401 {
		t.Fatalf("old password still works: %d", r.status)
	}
	e.login("budi", "password-baru-123")
	if actions := activityActions(t, a, ""); countOf(actions, store.ActPasswordChange) != 1 {
		t.Fatalf("activity = %v", actions)
	}
}

func TestAdminResetsPassword(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin", adminPassword)
	var u store.User
	admin.do("POST", "/api/users", newUserBody("budi")).into(t, &u)
	budi := e.login("budi", "rahasia-panjang")
	path := "/api/users/" + strconv.FormatInt(u.ID, 10) + "/password"

	if r := admin.do("POST", path, obj{"password": "pendek"}); r.status != 400 {
		t.Fatalf("short: %d", r.status)
	}
	if r := admin.do("POST", "/api/users/9999/password", obj{"password": "password-baru-123"}); r.status != 404 {
		t.Fatalf("missing user: %d", r.status)
	}
	if r := admin.do("POST", path, obj{"password": "password-baru-123"}); r.status != 200 {
		t.Fatalf("reset: %d %s", r.status, r.body)
	}
	if r := budi.do("GET", "/api/me", nil); r.status != 401 {
		t.Fatalf("old session after reset: %d", r.status)
	}
	e.login("budi", "password-baru-123")

	var me store.User
	admin.do("GET", "/api/me", nil).into(t, &me)
	if r := admin.do("POST", "/api/users/"+strconv.FormatInt(me.ID, 10)+"/password", obj{"password": "admin-baru-12345"}); r.status != 200 {
		t.Fatalf("self reset: %d", r.status)
	}
	if r := admin.do("GET", "/api/me", nil); r.status != 200 {
		t.Fatalf("admin session after resetting own password: %d", r.status)
	}
}

func TestOriginCheck(t *testing.T) {
	e := newEnv(t)
	c := e.login("admin", adminPassword)
	host := strings.TrimPrefix(e.srv.URL, "http://")
	post := func(origin string) int {
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		return c.raw("POST", "/api/logout", "application/json", strings.NewReader("{}"), h).status
	}
	r := c.raw("POST", "/api/logout", "application/json", strings.NewReader("{}"), http.Header{"Origin": {"https://evil.example"}})
	if r.status != 403 || r.errMsg(t) != "Origin tidak diizinkan" {
		t.Fatalf("evil origin: %d %s", r.status, r.body)
	}
	if got := post("null"); got != 403 {
		t.Fatalf("null origin: %d", got)
	}
	if got := post("http://" + host + ".evil.example"); got != 403 {
		t.Fatalf("suffix origin: %d", got)
	}
	if r := c.do("GET", "/api/me", nil); r.status != 200 {
		t.Fatalf("rejected logout must not end session: %d", r.status)
	}
	if got := post("http://" + host); got != 200 {
		t.Fatalf("matching origin: %d", got)
	}
	c = e.login("admin", adminPassword)
	if got := post(""); got != 200 {
		t.Fatalf("no origin: %d", got)
	}
	c = e.login("admin", adminPassword)
	if r := c.raw("GET", "/api/me", "", nil, http.Header{"Origin": {"https://evil.example"}}); r.status != 200 {
		t.Fatalf("GET with foreign origin must be allowed: %d", r.status)
	}
}

func TestActivityScope(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin", adminPassword)
	var budiU store.User
	admin.do("POST", "/api/users", newUserBody("budi")).into(t, &budiU)
	var adminU store.User
	admin.do("GET", "/api/me", nil).into(t, &adminU)
	e.clock.advance(time.Second)
	budi := e.login("budi", "rahasia-panjang")
	e.clock.advance(time.Second)
	e.client().do("POST", "/api/login", obj{"username": "budi", "password": "salah-salah-salah"})

	var own []store.Activity
	budi.do("GET", "/api/activity", nil).into(t, &own)
	if len(own) != 2 {
		t.Fatalf("user sees %+v", own)
	}
	for _, a := range own {
		if a.ActorID == nil || *a.ActorID != budiU.ID {
			t.Fatalf("user sees foreign entry %+v", a)
		}
	}
	if own[0].Action != store.ActLoginFailed || own[0].ActorName != "budi" || own[0].IP == "" || own[1].Action != store.ActLogin {
		t.Fatalf("own = %+v", own)
	}
	var spoof []store.Activity
	budi.do("GET", "/api/activity?actor="+strconv.FormatInt(adminU.ID, 10), nil).into(t, &spoof)
	if len(spoof) != 2 {
		t.Fatalf("actor param must be ignored for users: %+v", spoof)
	}

	var all, filtered []store.Activity
	admin.do("GET", "/api/activity", nil).into(t, &all)
	if len(all) != 4 {
		t.Fatalf("admin sees %d entries: %+v", len(all), all)
	}
	admin.do("GET", "/api/activity?actor="+strconv.FormatInt(adminU.ID, 10), nil).into(t, &filtered)
	if len(filtered) != 2 {
		t.Fatalf("admin filtered by actor: %+v", filtered)
	}
	var paged []store.Activity
	admin.do("GET", "/api/activity?limit=1&before="+strconv.FormatInt(all[0].At, 10), nil).into(t, &paged)
	if len(paged) != 1 || paged[0].At >= all[0].At {
		t.Fatalf("paged = %+v", paged)
	}
	for _, q := range []string{"?limit=abc", "?before=x", "?actor=x", "?limit=-1"} {
		if r := admin.do("GET", "/api/activity"+q, nil); r.status != 400 {
			t.Errorf("%s: %d", q, r.status)
		}
	}
	empty := admin.do("GET", "/api/activity?actor=9999", nil)
	if strings.TrimSpace(string(empty.body)) != "[]" {
		t.Fatalf("empty list = %s", empty.body)
	}
}

func TestJSONRequired(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	r := c.raw("POST", "/api/login", "text/plain", strings.NewReader(`{"username":"admin","password":"x"}`), nil)
	if r.status != 415 {
		t.Fatalf("text/plain: %d", r.status)
	}
	r = c.raw("POST", "/api/login", "", strings.NewReader(`{}`), nil)
	if r.status != 415 {
		t.Fatalf("no content type: %d", r.status)
	}
	big := `{"username":"` + strings.Repeat("a", 2<<20) + `"}`
	r = c.raw("POST", "/api/login", "application/json", strings.NewReader(big), nil)
	if r.status != 413 {
		t.Fatalf("2 MiB body: %d", r.status)
	}
	r = c.raw("POST", "/api/login", "application/json; charset=utf-8", strings.NewReader(`{"username":"admin","password":"`+adminPassword+`"}`), nil)
	if r.status != 200 {
		t.Fatalf("json with charset: %d %s", r.status, r.body)
	}
}

func TestSettings(t *testing.T) {
	e := newEnv(t)
	c := e.login("admin", adminPassword)
	var s store.Settings
	r := c.do("GET", "/api/settings", nil)
	r.into(t, &s)
	if r.status != 200 || s.UploadLimitMB != 500 || s.PortMin != 20000 || s.PortMax != 20999 || len(s.Runtimes) != len(store.RuntimeIDs) {
		t.Fatalf("defaults: %d %+v", r.status, s)
	}

	bad := s
	bad.PortMin = 80
	if r := c.do("PUT", "/api/settings", bad); r.status != 400 || r.errMsg(t) == "" {
		t.Fatalf("invalid settings: %d %s", r.status, r.body)
	}

	s.UploadLimitMB = 80
	s.PublicIP = "203.0.113.5"
	s.Runtimes = map[string]bool{"java21": true}
	if r := c.do("PUT", "/api/settings", s); r.status != 200 {
		t.Fatalf("PUT: %d %s", r.status, r.body)
	}
	var got store.Settings
	c.do("GET", "/api/settings", nil).into(t, &got)
	if got.UploadLimitMB != 80 || got.PublicIP != "203.0.113.5" || !got.Runtimes["java21"] || got.Runtimes["java8"] || len(got.Runtimes) != len(store.RuntimeIDs) {
		t.Fatalf("after PUT: %+v", got)
	}
	if actions := activityActions(t, c, ""); countOf(actions, store.ActSettingsUpdate) != 1 {
		t.Fatalf("activity = %v", actions)
	}
}

func TestUnknownRoute(t *testing.T) {
	e := newEnv(t)
	r := e.client().do("GET", "/api/nope", nil)
	if r.status != 404 || r.errMsg(t) == "" {
		t.Fatalf("unknown route: %d %s", r.status, r.body)
	}
}

// Concurrent guesses must not slip past the lockout: the check-and-count step
// is atomic, so exactly MaxFailures attempts get an answer and the rest are 429.
func TestLoginLockoutHoldsUnderConcurrency(t *testing.T) {
	e := newEnv(t)
	const n = 40
	body := `{"username":"admin","password":"salah-salah-salah"}`
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := http.Post(e.srv.URL+"/api/login", "application/json", strings.NewReader(body))
			if err != nil {
				codes <- -1
				return
			}
			res.Body.Close()
			codes <- res.StatusCode
		}()
	}
	wg.Wait()
	close(codes)
	got := map[int]int{}
	for c := range codes {
		got[c]++
	}
	if got[401] != auth.MaxFailures || got[429] != n-auth.MaxFailures {
		t.Fatalf("statuses = %v, want %d×401 and %d×429", got, auth.MaxFailures, n-auth.MaxFailures)
	}
}
