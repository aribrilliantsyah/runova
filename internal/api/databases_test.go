package api_test

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"runova/internal/api"
	"runova/internal/apps"
	"runova/internal/dbs"
	"runova/internal/docker/dockertest"
	"runova/internal/pg/pgfake"
	"runova/internal/secret"
	"runova/internal/store"
)

type dbEnv struct {
	*appEnv
	pg  *pgfake.Fake
	dbs *dbs.Service
}

// newDBEnv is newAppEnv with the database endpoints on a fake PostgreSQL.
func newDBEnv(t *testing.T) *dbEnv {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(filepath.Join(dataDir, "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dataDir, "key")
	if err := secret.GenerateKey(keyPath); err != nil {
		t.Fatal(err)
	}
	box, err := secret.LoadKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)}
	engine := dockertest.New()
	fake := pgfake.New()
	dsvc := &dbs.Service{
		Store: st, PG: fake, Box: box, BackupDir: filepath.Join(dataDir, "backup"), Now: clk.Now,
		HBAPath: filepath.Join(dataDir, "runova.conf"),
	}
	svc := &apps.Service{
		Store: st, Engine: engine, Box: box, DataDir: dataDir, Now: clk.Now,
		PortFree: func(int) bool { return true },
		Chown:    func(string, int) error { return nil },
		Dial: func(string, string, time.Duration) (net.Conn, error) {
			c1, c2 := net.Pipe()
			c2.Close()
			return c1, nil
		},
		HealthTimeout: time.Second, HealthInterval: time.Millisecond,
		DBEnv: dsvc.AppEnv,
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(api.New(api.Config{Store: st, Now: clk.Now, DataDir: dataDir, Apps: svc, DBs: dsvc, Context: ctx}))
	t.Cleanup(func() { cancel(); srv.Close(); st.Close() })
	e := &env{t: t, srv: srv, store: st, clock: clk, dataDir: dataDir}
	e.addUser("admin", adminPassword, store.RoleAdmin)
	return &dbEnv{appEnv: &appEnv{env: e, engine: engine, svc: svc}, pg: fake, dbs: dsvc}
}

// userWithRole adds a user and gives it a PostgreSQL role, as creating it through the API does.
func (e *dbEnv) userWithRole(name string) (store.User, *client) {
	e.t.Helper()
	u := e.addUser(name, name+"-password-1", store.RoleUser)
	if err := e.dbs.OnUserCreated(context.Background(), u); err != nil {
		e.t.Fatal(err)
	}
	return u, e.login(name, name+"-password-1")
}

func (c *client) createDB(suffix string) int64 {
	c.e.t.Helper()
	r := c.do("POST", "/api/databases", obj{"suffix": suffix})
	if r.status != 201 {
		c.e.t.Fatalf("create database: %d %s", r.status, r.body)
	}
	var d store.Database
	r.into(c.e.t, &d)
	return d.ID
}

func dbPath(id int64, rest string) string {
	return "/api/databases/" + strconv.FormatInt(id, 10) + rest
}

func TestDatabaseFlow(t *testing.T) {
	e := newDBEnv(t)
	_, c := e.userWithRole("budi")
	id := c.createDB("billing")

	r := c.do("GET", "/api/databases", nil)
	var list struct {
		Databases []dbs.DBView `json:"databases"`
		Used      int          `json:"used"`
		Limit     int          `json:"limit"`
		PGDown    bool         `json:"pg_down"`
	}
	r.into(t, &list)
	if r.status != 200 || len(list.Databases) != 1 || list.Databases[0].Name != "budi_billing" || list.Used != 1 || list.Limit != 3 || list.PGDown {
		t.Fatalf("list = %d %+v", r.status, list)
	}

	r = c.do("GET", dbPath(id, "/connection"), nil)
	if r.status != 200 || strings.Contains(string(r.body), "password") {
		t.Fatalf("connection = %d %s", r.status, r.body)
	}
	r = c.do("POST", dbPath(id, "/connection/reveal"), nil)
	var info dbs.ConnInfo
	r.into(t, &info)
	role, _ := e.pg.Role("budi")
	if r.status != 200 || info.Password != role.Password || info.User != "budi" || info.Database != "budi_billing" {
		t.Fatalf("reveal = %d %+v", r.status, info)
	}

	if r := c.do("DELETE", dbPath(id, ""), obj{"confirm": "nope"}); r.status != 400 {
		t.Fatalf("delete with wrong confirm = %d", r.status)
	}
	if r := c.do("DELETE", dbPath(id, ""), obj{"confirm": "budi_billing"}); r.status != 200 {
		t.Fatalf("delete = %d %s", r.status, r.body)
	}
	acts := activityActions(t, c, "")
	for _, want := range []string{store.ActDBCreate, store.ActDBReveal, store.ActDBDelete} {
		if !slices.Contains(acts, want) {
			t.Errorf("activity %v lacks %s", acts, want)
		}
	}
}

func TestDatabaseQuotaAndName(t *testing.T) {
	e := newDBEnv(t)
	_, c := e.userWithRole("budi")
	if r := c.do("POST", "/api/databases", obj{"suffix": "Bad-Name"}); r.status != 400 {
		t.Fatalf("bad suffix = %d %s", r.status, r.body)
	}
	for _, s := range []string{"a", "b", "c"} {
		c.createDB(s)
	}
	r := c.do("POST", "/api/databases", obj{"suffix": "d"})
	if r.status != 409 || r.errMsg(t) != "Batas database tercapai (3 dari 3)" {
		t.Fatalf("fourth = %d %s", r.status, r.body)
	}
}

func TestDatabaseOwnership(t *testing.T) {
	e := newDBEnv(t)
	_, a := e.userWithRole("andi")
	_, b := e.userWithRole("budi")
	id := a.createDB("secret")

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", dbPath(id, "/connection"), nil},
		{"POST", dbPath(id, "/connection/reveal"), nil},
		{"DELETE", dbPath(id, ""), obj{"confirm": "andi_secret"}},
	} {
		r := b.do(tc.method, tc.path, tc.body)
		if r.status != 404 || r.errMsg(t) != "Database tidak ditemukan" {
			t.Errorf("%s %s = %d %s", tc.method, tc.path, r.status, r.body)
		}
	}
	if !e.pg.HasDatabase("andi_secret") {
		t.Fatal("foreign delete reached PostgreSQL")
	}
	var list struct{ Databases []dbs.DBView }
	b.do("GET", "/api/databases", nil).into(t, &list)
	if len(list.Databases) != 0 {
		t.Fatalf("budi sees %v", list.Databases)
	}
}

func TestAdminDatabases(t *testing.T) {
	e := newDBEnv(t)
	_, a := e.userWithRole("andi")
	a.createDB("one")
	if r := a.do("GET", "/api/admin/databases", nil); r.status != 403 {
		t.Fatalf("user = %d", r.status)
	}
	admin := e.login("admin", adminPassword)
	var list struct {
		Databases []struct{ Name, Owner string }
	}
	r := admin.do("GET", "/api/admin/databases", nil)
	r.into(t, &list)
	if r.status != 200 || len(list.Databases) != 1 || list.Databases[0].Owner != "andi" || list.Databases[0].Name != "andi_one" {
		t.Fatalf("admin list = %d %s", r.status, r.body)
	}
}

func TestAppWithForeignDatabase(t *testing.T) {
	e := newDBEnv(t)
	_, a := e.userWithRole("andi")
	_, b := e.userWithRole("budi")
	id := a.createDB("secret")

	up := b.upload("xyz", staticELF())
	var u uploadResult
	up.into(t, &u)
	r := b.do("POST", "/api/apps", obj{"upload_id": u.ID, "name": "xyz", "memory_mb": 256, "cpu_milli": 250, "database_id": id})
	if r.status != 400 || r.errMsg(t) != "Database tidak ditemukan" {
		t.Fatalf("create = %d %s", r.status, r.body)
	}

	appID, _ := b.createApp("yyy", nil)
	r = b.do("PATCH", appPath(appID, ""), obj{"database_id": id})
	if r.status != 400 || r.errMsg(t) != "Database tidak ditemukan" {
		t.Fatalf("patch = %d %s", r.status, r.body)
	}
}

func TestAppSpecGetsDBEnv(t *testing.T) {
	e := newDBEnv(t)
	_, c := e.userWithRole("budi")
	dbID := c.createDB("billing")
	id, _ := c.createApp("billing-api", obj{"database_id": dbID})
	c.waitStatus(id, "running")

	role, _ := e.pg.Role("budi")
	var env []string
	for _, ct := range e.engine.Containers() {
		env = ct.Spec.Env
	}
	for _, want := range []string{
		"PGHOST=host.docker.internal", "PGUSER=budi", "PGDATABASE=budi_billing", "PGPASSWORD=" + role.Password,
		"SPRING_DATASOURCE_URL=jdbc:postgresql://host.docker.internal:5432/budi_billing",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %q", want)
		}
	}
	if !slices.ContainsFunc(env, func(s string) bool { return strings.HasPrefix(s, "DATABASE_URL=postgres://budi:") }) {
		t.Errorf("env lacks DATABASE_URL: %v", env)
	}

	// Binding is visible on the app, and the database cannot go while bound.
	if r := c.do("DELETE", dbPath(dbID, ""), obj{"confirm": "budi_billing"}); r.status != 409 || r.errMsg(t) != "Masih dipakai oleh: billing-api" {
		t.Fatalf("delete bound = %d %s", r.status, r.body)
	}
}

func TestPatchAppDatabaseNeedsRedeploy(t *testing.T) {
	e := newDBEnv(t)
	_, c := e.userWithRole("budi")
	dbID := c.createDB("billing")
	id, _ := c.createApp("billing-api", nil)
	c.waitStatus(id, "running")

	r := c.do("PATCH", appPath(id, ""), obj{"database_id": dbID})
	var out struct {
		App              store.App `json:"app"`
		RedeployRequired bool      `json:"redeploy_required"`
	}
	r.into(t, &out)
	if r.status != 200 || out.App.DatabaseID == nil || *out.App.DatabaseID != dbID || !out.RedeployRequired {
		t.Fatalf("patch = %d %s", r.status, r.body)
	}
	r = c.do("PATCH", appPath(id, ""), obj{"database_id": 0})
	r.into(t, &out)
	if r.status != 200 || out.App.DatabaseID != nil || !out.RedeployRequired {
		t.Fatalf("detach = %d %s", r.status, r.body)
	}
	r = c.do("PATCH", appPath(id, ""), obj{"database_id": 0})
	r.into(t, &out)
	if out.RedeployRequired {
		t.Error("no-op detach asked for a redeploy")
	}
}

func TestRotatePasswordOrder(t *testing.T) {
	e := newDBEnv(t)
	u, c := e.userWithRole("budi")
	dbID := c.createDB("billing")
	appID, _ := c.createApp("billing-api", obj{"database_id": dbID})
	c.waitStatus(appID, "running")
	before, _ := e.store.PGPassword(context.Background(), u.ID)

	e.pg.SetFailures(func(f *pgfake.Fake) { f.SetPasswordErr = errors.New("boom") })
	r := c.do("POST", "/api/databases/password", nil)
	if r.status != 502 {
		t.Fatalf("failed rotation = %d %s", r.status, r.body)
	}
	if after, _ := e.store.PGPassword(context.Background(), u.ID); after != before {
		t.Fatal("stored password changed although ALTER ROLE failed")
	}

	e.pg.SetFailures(func(f *pgfake.Fake) { f.SetPasswordErr = nil })
	if r := c.do("POST", "/api/databases/password", nil); r.status != 200 {
		t.Fatalf("rotation = %d %s", r.status, r.body)
	}
	after, _ := e.store.PGPassword(context.Background(), u.ID)
	if after == before {
		t.Fatal("stored password unchanged")
	}
	role, _ := e.pg.Role("budi")
	if plain, err := e.openSealed(after); err != nil || plain != role.Password {
		t.Fatalf("stored %q != PostgreSQL %q (%v)", plain, role.Password, err)
	}

	r = c.do("GET", "/api/notifications", nil)
	var n struct{ Notifications []store.Notification }
	r.into(t, &n)
	if len(n.Notifications) != 1 || n.Notifications[0].Body != "Deploy ulang billing-api agar memakai password baru" {
		t.Fatalf("notifications = %+v", n.Notifications)
	}
}

func (e *dbEnv) openSealed(sealed string) (string, error) {
	box, err := secret.LoadKey(filepath.Join(e.dataDir, "key"))
	if err != nil {
		return "", err
	}
	plain, err := box.Open(sealed)
	return string(plain), err
}

func TestPGDownDoesNotBreakDeploy(t *testing.T) {
	e := newDBEnv(t)
	_, c := e.userWithRole("budi")
	dbID := c.createDB("billing")

	down := errors.New("connection refused")
	e.pg.SetFailures(func(f *pgfake.Fake) {
		f.PingErr, f.DatabasesErr, f.CreateDatabaseErr, f.SetPasswordErr = down, down, down, down
	})

	r := c.do("GET", "/api/databases", nil)
	var list struct {
		Databases []dbs.DBView `json:"databases"`
		PGDown    bool         `json:"pg_down"`
	}
	r.into(t, &list)
	if r.status != 200 || !list.PGDown || len(list.Databases) != 1 {
		t.Fatalf("list = %d %s", r.status, r.body)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/databases", obj{"suffix": "other"}},
		{"POST", "/api/databases/password", nil},
	} {
		r := c.do(tc.method, tc.path, tc.body)
		if r.status != 503 || r.errMsg(t) != "PostgreSQL tidak bisa diakses" {
			t.Errorf("%s %s = %d %s", tc.method, tc.path, r.status, r.body)
		}
	}

	id, _ := c.createApp("billing-api", obj{"database_id": dbID})
	c.waitStatus(id, "running")
	var env []string
	for _, ct := range e.engine.Containers() {
		env = ct.Spec.Env
	}
	if !slices.Contains(env, "PGHOST=host.docker.internal") {
		t.Fatalf("env = %v", env)
	}
}

func TestUserHooks(t *testing.T) {
	e := newDBEnv(t)
	admin := e.login("admin", adminPassword)

	r := admin.do("POST", "/api/users", newUserBody("budi"))
	if r.status != 201 {
		t.Fatalf("create user = %d %s", r.status, r.body)
	}
	var u store.User
	r.into(t, &u)
	if role, ok := e.pg.Role("budi"); !ok || role.ConnLim != 20 {
		t.Fatalf("role = %+v, %v", role, ok)
	}
	if sealed, _ := e.store.PGPassword(context.Background(), u.ID); sealed == "" {
		t.Error("role password not stored")
	}

	quota := store.Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 9}
	if r := admin.do("PATCH", "/api/users/"+strconv.FormatInt(u.ID, 10), obj{"quota": quota}); r.status != 200 {
		t.Fatalf("patch quota = %d %s", r.status, r.body)
	}
	if role, _ := e.pg.Role("budi"); role.ConnLim != 9 {
		t.Fatalf("connection limit = %d", role.ConnLim)
	}

	if r := admin.do("DELETE", "/api/users/"+strconv.FormatInt(u.ID, 10), nil); r.status != 200 {
		t.Fatalf("delete user = %d %s", r.status, r.body)
	}
	if _, ok := e.pg.Role("budi"); ok {
		t.Error("role survived user deletion")
	}
}

func TestUserCreatedWhenPGFails(t *testing.T) {
	e := newDBEnv(t)
	admin := e.login("admin", adminPassword)
	e.pg.SetFailures(func(f *pgfake.Fake) { f.EnsureRoleErr = errors.New("down"); f.PingErr = errors.New("down") })

	if r := admin.do("POST", "/api/users", newUserBody("budi")); r.status != 201 {
		t.Fatalf("create user = %d %s", r.status, r.body)
	}
	var n struct{ Notifications []store.Notification }
	admin.do("GET", "/api/notifications", nil).into(t, &n)
	if len(n.Notifications) != 1 || n.Notifications[0].Title != "Role PostgreSQL untuk budi belum dibuat" {
		t.Fatalf("notifications = %+v", n.Notifications)
	}

	// The next start's reconcile repairs it.
	e.pg.SetFailures(func(f *pgfake.Fake) { f.EnsureRoleErr = nil; f.PingErr = nil })
	if err := e.dbs.ReconcileRoles(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.pg.Role("budi"); !ok {
		t.Fatal("reconcile did not create the role")
	}
}
