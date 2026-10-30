package pg

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func newAdmin(t *testing.T) (*PgxAdmin, string) {
	t.Helper()
	dsn := startPG(t)
	a, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	if err := a.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	return a, dsn
}

// connectAs opens a direct connection to db as role, reusing host and port of the admin DSN.
func connectAs(ctx context.Context, adminDSN, role, password, db string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		return nil, err
	}
	cfg.User, cfg.Password, cfg.Database = role, password, db
	return pgx.ConnectConfig(ctx, cfg)
}

func mustConnect(t *testing.T, dsn, role, password, db string) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	c, err := connectAs(ctx, dsn, role, password, db)
	if err != nil {
		t.Fatalf("connect %s to %s: %v", role, db, err)
	}
	t.Cleanup(func() { c.Close(ctx) })
	return c
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestIsolationBetweenRoles(t *testing.T) {
	a, dsn := newAdmin(t)
	ctx := context.Background()
	for _, r := range []string{"budi", "andi"} {
		if err := a.EnsureRole(ctx, r, r+"pass", 5); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.CreateDatabase(ctx, "budi_app", "budi"); err != nil {
		t.Fatal(err)
	}

	_, err := connectAs(ctx, dsn, "andi", "andipass", "budi_app")
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("andi -> budi_app: err = %v, want permission denied", err)
	}

	c := mustConnect(t, dsn, "budi", "budipass", "budi_app")
	if _, err := c.Exec(ctx, "CREATE TABLE t (id int)"); err != nil {
		t.Fatalf("budi CREATE TABLE: %v", err)
	}
	if _, err := c.Exec(ctx, "INSERT INTO t VALUES (1)"); err != nil {
		t.Fatalf("budi INSERT: %v", err)
	}

	// Neither PUBLIC nor another user's own database leaks access.
	if err := a.CreateDatabase(ctx, "andi_app", "andi"); err != nil {
		t.Fatal(err)
	}
	if _, err := connectAs(ctx, dsn, "budi", "budipass", "andi_app"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("budi -> andi_app: err = %v, want permission denied", err)
	}
	// And the shared maintenance database is no escape route to budi's data.
	pc := mustConnect(t, dsn, "andi", "andipass", "postgres")
	if _, err := pc.Exec(ctx, `SELECT 1`); err != nil {
		t.Fatalf("andi on postgres db: %v", err)
	}
}

func TestRoleCannotEscalate(t *testing.T) {
	a, dsn := newAdmin(t)
	ctx := context.Background()
	if err := a.EnsureRole(ctx, "budi", "budipass", 5); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateDatabase(ctx, "budi_app", "budi"); err != nil {
		t.Fatal(err)
	}
	c := mustConnect(t, dsn, "budi", "budipass", "budi_app")

	for _, stmt := range []string{
		"CREATE DATABASE x",
		"CREATE ROLE y",
		"ALTER ROLE budi CONNECTION LIMIT 100",
		"ALTER ROLE budi SUPERUSER",
		"ALTER ROLE budi CREATEDB",
		"SET ROLE runova",
		"DROP ROLE runova",
	} {
		_, err := c.Exec(ctx, stmt)
		if err == nil {
			t.Errorf("%q succeeded as budi, want error", stmt)
			continue
		}
		if code := pgCode(err); code != "42501" {
			t.Errorf("%q: sqlstate %q (%v), want 42501 insufficient_privilege", stmt, code, err)
		}
	}
}

func TestConnLimitEnforced(t *testing.T) {
	a, dsn := newAdmin(t)
	ctx := context.Background()
	if err := a.EnsureRole(ctx, "budi", "budipass", 2); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateDatabase(ctx, "budi_app", "budi"); err != nil {
		t.Fatal(err)
	}
	mustConnect(t, dsn, "budi", "budipass", "budi_app")
	mustConnect(t, dsn, "budi", "budipass", "budi_app")
	_, err := connectAs(ctx, dsn, "budi", "budipass", "budi_app")
	if err == nil || !strings.Contains(err.Error(), "too many connections") {
		t.Fatalf("third connection: err = %v, want too many connections", err)
	}

	if err := a.SetConnLimit(ctx, "budi", 3); err != nil {
		t.Fatal(err)
	}
	mustConnect(t, dsn, "budi", "budipass", "budi_app")
}

func TestDropRoleDropsDatabases(t *testing.T) {
	a, dsn := newAdmin(t)
	ctx := context.Background()
	if err := a.EnsureRole(ctx, "budi", "budipass", 5); err != nil {
		t.Fatal(err)
	}
	if err := a.EnsureRole(ctx, "andi", "andipass", 5); err != nil {
		t.Fatal(err)
	}
	for _, db := range []string{"budi_one", "budi_two", "andi_one"} {
		owner, _, _ := strings.Cut(db, "_")
		if err := a.CreateDatabase(ctx, db, owner); err != nil {
			t.Fatal(err)
		}
	}
	open := mustConnect(t, dsn, "budi", "budipass", "budi_two")
	idleOnShared := mustConnect(t, dsn, "budi", "budipass", "postgres")

	if err := a.DropRole(ctx, "budi"); err != nil {
		t.Fatalf("DropRole: %v", err)
	}
	if got, err := a.Databases(ctx, "budi"); err != nil || len(got) != 0 {
		t.Fatalf("Databases(budi) = %v, %v; want empty", got, err)
	}
	if _, err := open.Exec(ctx, "SELECT 1"); err == nil {
		t.Error("connection to dropped database still usable")
	}
	if _, err := idleOnShared.Exec(ctx, "SELECT 1"); err == nil {
		t.Error("connection of dropped role still usable")
	}
	if got, err := a.Databases(ctx, "andi"); err != nil || len(got) != 1 || got[0].Name != "andi_one" {
		t.Fatalf("Databases(andi) = %v, %v; want andi_one untouched", got, err)
	}
	if err := a.DropRole(ctx, "budi"); err != nil {
		t.Errorf("DropRole on missing role: %v", err)
	}
	if _, err := connectAs(ctx, dsn, "budi", "budipass", "postgres"); err == nil {
		t.Error("dropped role can still log in")
	}
}

func TestEnsureRoleIdempotent(t *testing.T) {
	a, dsn := newAdmin(t)
	ctx := context.Background()
	if err := a.EnsureRole(ctx, "budi", "first", 5); err != nil {
		t.Fatal(err)
	}
	if err := a.EnsureRole(ctx, "budi", "se'cond\\", 7); err != nil {
		t.Fatal(err)
	}
	if _, err := connectAs(ctx, dsn, "budi", "first", "postgres"); err == nil {
		t.Error("old password still accepted")
	}
	mustConnect(t, dsn, "budi", "se'cond\\", "postgres")

	var limit int
	var super, createdb, createrole, inherit bool
	err := a.pool.QueryRow(ctx, `SELECT rolconnlimit, rolsuper, rolcreatedb, rolcreaterole, rolinherit FROM pg_roles WHERE rolname = 'budi'`).
		Scan(&limit, &super, &createdb, &createrole, &inherit)
	if err != nil {
		t.Fatal(err)
	}
	if limit != 7 || super || createdb || createrole || inherit {
		t.Errorf("attrs = limit %d super %v createdb %v createrole %v inherit %v", limit, super, createdb, createrole, inherit)
	}
}

func TestSetPassword(t *testing.T) {
	a, dsn := newAdmin(t)
	ctx := context.Background()
	if err := a.EnsureRole(ctx, "budi", "old", 5); err != nil {
		t.Fatal(err)
	}
	if err := a.SetPassword(ctx, "budi", "n'ew"); err != nil {
		t.Fatal(err)
	}
	if _, err := connectAs(ctx, dsn, "budi", "old", "postgres"); err == nil {
		t.Error("old password still accepted")
	}
	mustConnect(t, dsn, "budi", "n'ew", "postgres")
}

func TestPasswordInjectionIsInert(t *testing.T) {
	a, dsn := newAdmin(t)
	ctx := context.Background()
	evil := `x'; CREATE ROLE evil SUPERUSER LOGIN; --`
	if err := a.EnsureRole(ctx, "budi", evil, 5); err != nil {
		t.Fatal(err)
	}
	mustConnect(t, dsn, "budi", evil, "postgres")
	var n int
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM pg_roles WHERE rolname = 'evil'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("evil role count = %d, %v", n, err)
	}
}

func TestDatabasesAndDropDatabase(t *testing.T) {
	a, dsn := newAdmin(t)
	ctx := context.Background()
	if err := a.EnsureRole(ctx, "budi", "budipass", 5); err != nil {
		t.Fatal(err)
	}
	if got, err := a.Databases(ctx, "budi"); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("Databases before create = %#v, %v; want empty non-nil", got, err)
	}
	for _, db := range []string{"budi_b", "budi_a"} {
		if err := a.CreateDatabase(ctx, db, "budi"); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.CreateDatabase(ctx, "budi_a", "budi"); err == nil {
		t.Error("duplicate CreateDatabase succeeded")
	}
	mustConnect(t, dsn, "budi", "budipass", "budi_a")

	got, err := a.Databases(ctx, "budi")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "budi_a" || got[1].Name != "budi_b" {
		t.Fatalf("Databases = %+v, want budi_a, budi_b", got)
	}
	if got[0].SizeBytes <= 0 {
		t.Errorf("SizeBytes = %d, want > 0", got[0].SizeBytes)
	}
	if got[0].Connections != 1 || got[1].Connections != 0 {
		t.Errorf("Connections = %d, %d; want 1, 0", got[0].Connections, got[1].Connections)
	}

	if err := a.DropDatabase(ctx, "budi_a"); err != nil {
		t.Fatalf("DropDatabase with open connection: %v", err)
	}
	if err := a.DropDatabase(ctx, "budi_a"); err != nil {
		t.Errorf("DropDatabase on missing database: %v", err)
	}
	if got, _ := a.Databases(ctx, "budi"); len(got) != 1 || got[0].Name != "budi_b" {
		t.Errorf("Databases after drop = %+v", got)
	}
}

func TestTerminateConnections(t *testing.T) {
	a, dsn := newAdmin(t)
	ctx := context.Background()
	if err := a.EnsureRole(ctx, "budi", "budipass", 5); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateDatabase(ctx, "budi_app", "budi"); err != nil {
		t.Fatal(err)
	}
	c := mustConnect(t, dsn, "budi", "budipass", "budi_app")

	if err := a.TerminateConnections(ctx, "budi_app"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := c.Exec(ctx, "SELECT 1"); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connection survived TerminateConnections")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The database stays usable for new connections.
	mustConnect(t, dsn, "budi", "budipass", "budi_app")
}

func TestReloadConfig(t *testing.T) {
	a, _ := newAdmin(t)
	if err := a.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentEnsureRoleForDifferentUsers(t *testing.T) {
	a, dsn := newAdmin(t)
	ctx := context.Background()
	users := []string{"budi", "andi", "citra", "dewi", "eka", "fajar", "gita", "hadi"}

	var wg sync.WaitGroup
	errs := make([]error, len(users))
	for i, u := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = a.EnsureRole(ctx, u, u+"pass", 5)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("EnsureRole(%s): %v", users[i], err)
		}
	}
	for _, u := range users {
		mustConnect(t, dsn, u, u+"pass", "postgres")
	}
}
