package dbs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"runova/internal/artifact"
	"runova/internal/dbs"
	"runova/internal/pg/pgfake"
	"runova/internal/secret"
	"runova/internal/store"
)

type env struct {
	svc   *dbs.Service
	st    *store.Store
	pg    *pgfake.Fake
	box   *secret.Box
	dir   string
	clock *time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	keyPath := filepath.Join(dir, "key")
	if err := secret.GenerateKey(keyPath); err != nil {
		t.Fatal(err)
	}
	box, err := secret.LoadKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	e := &env{st: st, pg: pgfake.New(), box: box, dir: dir, clock: &now}
	e.svc = &dbs.Service{
		Store: st, PG: e.pg, Box: box, BackupDir: filepath.Join(dir, "backup"),
		Now: func() time.Time { return *e.clock }, HBAPath: filepath.Join(dir, "runova.conf"),
	}
	return e
}

func (e *env) user(t *testing.T, name string, role store.Role) store.User {
	t.Helper()
	u, err := e.st.CreateUser(context.Background(), store.NewUser{
		Username: name, PasswordHash: "h", Role: role,
		Quota: store.Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 20},
	}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// setPassword stores a known sealed role password for the user.
func (e *env) setPassword(t *testing.T, u store.User, password string) {
	t.Helper()
	sealed, err := e.box.Seal([]byte(password))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.SetPGPassword(context.Background(), u.ID, sealed); err != nil {
		t.Fatal(err)
	}
}

func (e *env) app(t *testing.T, u store.User, name string, port int, dbID *int64) store.App {
	t.Helper()
	a, err := e.st.CreateApp(context.Background(), store.App{
		UserID: u.ID, Name: name, Kind: artifact.KindStatic, InternalPort: 8080, IPPort: port,
		MemoryMB: 256, CPUMilli: 250, Status: store.AppStopped, DeployMode: "stopstart", CreatedAt: 1, DatabaseID: dbID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestCreateQuota(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	for _, s := range []string{"a", "b", "c"} {
		if _, err := e.svc.Create(ctx, u, s); err != nil {
			t.Fatalf("Create(%s): %v", s, err)
		}
	}
	_, err := e.svc.Create(ctx, u, "d")
	if !errors.Is(err, dbs.ErrQuota) || err.Error() != "Batas database tercapai (3 dari 3)" {
		t.Fatalf("fourth: %v", err)
	}
	if e.pg.HasDatabase("budi_d") {
		t.Error("over-quota database created in PostgreSQL")
	}
}

func TestCreateNameRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	for _, bad := range []string{"", "Billing", "1x", "a-b", strings.Repeat("a", 32)} {
		if _, err := e.svc.Create(ctx, u, bad); !errors.Is(err, dbs.ErrValidation) {
			t.Errorf("Create(%q) = %v, want ErrValidation", bad, err)
		}
	}
	if _, err := e.svc.Create(ctx, u, "billing"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(ctx, u, "billing"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	if got := e.pg.CallsWithPrefix("create "); len(got) != 1 {
		t.Fatalf("duplicate reached PostgreSQL: %v", got)
	}
}

func TestCreatePGFailureNoRow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	e.pg.CreateDatabaseErr = errors.New("boom")
	if _, err := e.svc.Create(ctx, u, "billing"); err == nil {
		t.Fatal("Create succeeded")
	}
	if rows, _ := e.st.DatabasesByUser(ctx, u.ID); len(rows) != 0 {
		t.Fatalf("rows = %v", rows)
	}
}

func TestCreatePGDown(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "budi", store.RoleUser)
	e.pg.CreateDatabaseErr = errors.New("connection refused")
	e.pg.PingErr = errors.New("connection refused")
	_, err := e.svc.Create(context.Background(), u, "billing")
	if !errors.Is(err, dbs.ErrPGDown) || err.Error() != "PostgreSQL tidak bisa diakses" {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteInUse(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	d, err := e.svc.Create(ctx, u, "billing")
	if err != nil {
		t.Fatal(err)
	}
	e.app(t, u, "billing-api", 20000, &d.ID)
	e.app(t, u, "invoice-pdf", 20001, &d.ID)
	err = e.svc.Delete(ctx, u, d.ID, d.Name)
	if !errors.Is(err, dbs.ErrInUse) || err.Error() != "Masih dipakai oleh: billing-api, invoice-pdf" {
		t.Fatalf("err = %v", err)
	}
	if !e.pg.HasDatabase(d.Name) {
		t.Error("database dropped despite being in use")
	}
}

func TestDeleteConfirm(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	d, _ := e.svc.Create(ctx, u, "billing")
	backups := filepath.Join(e.svc.BackupDir, d.Name)
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := e.svc.Delete(ctx, u, d.ID, "billing"); !errors.Is(err, dbs.ErrValidation) {
		t.Fatalf("wrong confirm: %v", err)
	}
	if !e.pg.HasDatabase(d.Name) {
		t.Fatal("dropped on wrong confirmation")
	}
	if err := e.svc.Delete(ctx, u, d.ID, d.Name); err != nil {
		t.Fatal(err)
	}
	if e.pg.HasDatabase(d.Name) {
		t.Error("database still in PostgreSQL")
	}
	if _, err := e.st.Database(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Error("row still in store")
	}
	if _, err := os.Stat(backups); !os.IsNotExist(err) {
		t.Errorf("backup folder survived: %v", err)
	}
}

func TestOtherUsersDatabaseIsNotFound(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user(t, "andi", store.RoleUser)
	b := e.user(t, "budi", store.RoleUser)
	d, _ := e.svc.Create(ctx, a, "app")
	if err := e.svc.Delete(ctx, b, d.ID, d.Name); !errors.Is(err, dbs.ErrNotFound) {
		t.Errorf("Delete: %v", err)
	}
	if _, err := e.svc.Connection(ctx, b, d.ID, true); !errors.Is(err, dbs.ErrNotFound) {
		t.Errorf("Connection: %v", err)
	}
	if err := e.svc.Delete(ctx, b, 9999, "x"); !errors.Is(err, dbs.ErrNotFound) {
		t.Errorf("missing id: %v", err)
	}
}

func TestAppEnv(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	e.setPassword(t, u, "p@ss:/w")
	d, err := e.st.CreateDatabase(ctx, store.Database{UserID: u.ID, Name: "budi_billing"})
	if err != nil {
		t.Fatal(err)
	}
	app := e.app(t, u, "billing-api", 20000, &d.ID)

	got, err := e.svc.AppEnv(ctx, app, []string{"DB_URL", "DB_USER", "DB_PASSWORD", "REDIS_URL", "PGHOST"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"DATABASE_URL=postgres://budi:p%40ss%3A%2Fw@host.docker.internal:5432/budi_billing",
		"SPRING_DATASOURCE_URL=jdbc:postgresql://host.docker.internal:5432/budi_billing",
		"SPRING_DATASOURCE_USERNAME=budi",
		"SPRING_DATASOURCE_PASSWORD=p@ss:/w",
		"PGHOST=host.docker.internal",
		"PGPORT=5432",
		"PGUSER=budi",
		"PGPASSWORD=p@ss:/w",
		"PGDATABASE=budi_billing",
		"DB_URL=postgres://budi:p%40ss%3A%2Fw@host.docker.internal:5432/budi_billing",
		"DB_USER=budi",
		"DB_PASSWORD=p@ss:/w",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("env =\n%q\nwant\n%q", got, want)
	}
	if calls := e.pg.Calls(); len(calls) != 0 {
		t.Errorf("AppEnv touched PostgreSQL: %v", calls)
	}

	app.Kind = artifact.KindJar
	got, err = e.svc.AppEnv(ctx, app, []string{"DB_URL", "SPRING_R2DBC_URL"})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"DB_URL=jdbc:postgresql://host.docker.internal:5432/budi_billing", "SPRING_R2DBC_URL=r2dbc:postgresql://host.docker.internal:5432/budi_billing"} {
		if !slices.Contains(got, w) {
			t.Errorf("jar env lacks %q: %q", w, got)
		}
	}

	app.DatabaseID = nil
	if got, err := e.svc.AppEnv(ctx, app, []string{"DB_URL"}); err != nil || got != nil {
		t.Fatalf("no database: %v, %v", got, err)
	}
}

func TestHBAFile(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	s, _ := e.st.Settings(ctx)
	s.PGAllowedNets = []string{"10.20.0.0/16"}
	if err := e.st.SetSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WriteHBA(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(e.svc.HBAPath)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	want := []string{"host all all 172.30.0.0/16 scram-sha-256", "host all all 10.20.0.0/16 scram-sha-256"}
	if !slices.Equal(lines, want) {
		t.Fatalf("lines = %q", lines)
	}
	if got := e.pg.CallsWithPrefix("reload"); len(got) != 1 {
		t.Fatalf("reload calls = %v", got)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(e.svc.HBAPath), ".runova.conf.*"))
	if len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestReconcileRoles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	if err := e.svc.ReconcileRoles(ctx); err != nil {
		t.Fatal(err)
	}
	role, ok := e.pg.Role("budi")
	if !ok || len(role.Password) != 32 || role.ConnLim != 20 {
		t.Fatalf("role = %+v, %v", role, ok)
	}
	sealed, _ := e.st.PGPassword(ctx, u.ID)
	if sealed == "" || strings.Contains(sealed, role.Password) {
		t.Fatalf("stored = %q", sealed)
	}
	if plain, err := e.box.Open(sealed); err != nil || string(plain) != role.Password {
		t.Fatalf("stored password does not match: %q, %v", plain, err)
	}

	// A second run keeps the stored password.
	if err := e.svc.ReconcileRoles(ctx); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.pg.Role("budi"); again.Password != role.Password {
		t.Error("password changed on reconcile")
	}
}

func TestReconcileContinuesAfterFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.user(t, "andi", store.RoleUser)
	e.user(t, "budi", store.RoleUser)
	e.pg.EnsureRoleErr = errors.New("boom")
	if err := e.svc.ReconcileRoles(ctx); err == nil {
		t.Fatal("no error")
	}
	if got := e.pg.CallsWithPrefix("ensure "); len(got) != 2 {
		t.Fatalf("ensure calls = %v", got)
	}
}

func TestOnUserHooks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	if err := e.svc.OnUserCreated(ctx, u); err != nil {
		t.Fatal(err)
	}
	u.Quota.PGConnections = 7
	if err := e.svc.OnQuotaChanged(ctx, u); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.pg.Role("budi"); r.ConnLim != 7 {
		t.Fatalf("limit = %d", r.ConnLim)
	}
	d, err := e.svc.Create(ctx, u, "billing")
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(e.svc.BackupDir, d.Name)
	other := filepath.Join(e.svc.BackupDir, "budiman_x")
	for _, p := range []string{folder, other} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.OnUserDeleted(ctx, "budi"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.pg.Role("budi"); ok {
		t.Error("role survived")
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Errorf("own backups survived: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("another user's backups removed: %v", err)
	}
}

func TestRoleOperationsSerializedPerUser(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	var mu sync.Mutex
	active, peak := 0, 0
	e.svc.PG = &concurrencyProbe{Fake: e.pg, enter: func() func() {
		mu.Lock()
		active++
		peak = max(peak, active)
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		return func() { mu.Lock(); active--; mu.Unlock() }
	}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() { defer wg.Done(); e.svc.OnUserCreated(ctx, u) }()
		go func() { defer wg.Done(); e.svc.OnQuotaChanged(ctx, u) }()
	}
	wg.Wait()
	if peak != 1 {
		t.Fatalf("peak concurrent role operations = %d, want 1", peak)
	}
}

// concurrencyProbe measures how many role-changing calls overlap.
type concurrencyProbe struct {
	*pgfake.Fake
	enter func() func()
}

func (c *concurrencyProbe) EnsureRole(ctx context.Context, role, pw string, n int) error {
	defer c.enter()()
	return c.Fake.EnsureRole(ctx, role, pw, n)
}

func (c *concurrencyProbe) SetConnLimit(ctx context.Context, role string, n int) error {
	defer c.enter()()
	return c.Fake.SetConnLimit(ctx, role, n)
}

func TestListFillsLiveData(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	d, _ := e.svc.Create(ctx, u, "billing")
	e.app(t, u, "billing-api", 20000, &d.ID)

	list, err := e.svc.List(ctx, u)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v", list, err)
	}
	if v := list[0]; v.Name != "budi_billing" || v.SizeBytes != 1000 || v.Connections != 1 || !slices.Equal(v.Apps, []string{"billing-api"}) {
		t.Fatalf("view = %+v", v)
	}

	e.pg.DatabasesErr = errors.New("down")
	list, err = e.svc.List(ctx, u)
	if err != nil || len(list) != 1 || list[0].SizeBytes != 0 || list[0].Connections != 0 {
		t.Fatalf("PG down: %v, %v", list, err)
	}
}

func TestConnectionHidesPasswordUnlessRevealed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	e.setPassword(t, u, "secret-pw")
	s, _ := e.st.Settings(ctx)
	s.PublicIP = "203.0.113.5"
	if err := e.st.SetSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	d, _ := e.svc.Create(ctx, u, "billing")

	info, err := e.svc.Connection(ctx, u, d.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if info != (dbs.ConnInfo{Host: "203.0.113.5", Port: 5432, Database: "budi_billing", User: "budi"}) {
		t.Fatalf("info = %+v", info)
	}
	info, err = e.svc.Connection(ctx, u, d.ID, true)
	if err != nil || info.Password != "secret-pw" {
		t.Fatalf("revealed = %+v, %v", info, err)
	}
}
