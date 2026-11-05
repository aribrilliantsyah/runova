package pg

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// newUsers starts PostgreSQL with roles budi and andi, each owning one
// database with a table, and returns the user executor and budi's credentials.
func newUsers(t *testing.T) (*PgxUsers, *PgxAdmin, Creds) {
	t.Helper()
	a, dsn := newAdmin(t)
	ctx := context.Background()
	for _, r := range []string{"budi", "andi"} {
		if err := a.EnsureRole(ctx, r, r+"pass", 10); err != nil {
			t.Fatal(err)
		}
		if err := a.CreateDatabase(ctx, r+"_app", r); err != nil {
			t.Fatal(err)
		}
	}
	u, err := NewUsers(dsn)
	if err != nil {
		t.Fatal(err)
	}
	budi := Creds{Role: "budi", Password: "budipass", Database: "budi_app"}
	for _, stmt := range []string{
		`CREATE TABLE invoices (id int PRIMARY KEY, memo text NOT NULL DEFAULT 'baru', amount numeric(10,2))`,
		`INSERT INTO invoices SELECT g, 'memo ' || g, g * 1.5 FROM generate_series(1, 250) g`,
		`CREATE TABLE "Weird; Name" (x int)`,
	} {
		if _, err := u.Query(ctx, budi, stmt, true); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	andi := Creds{Role: "andi", Password: "andipass", Database: "andi_app"}
	if _, err := u.Query(ctx, andi, `CREATE TABLE secret (v text)`, true); err != nil {
		t.Fatal(err)
	}
	return u, a, budi
}

func sqlstate(err error) string {
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pe.Code
	}
	return ""
}

func TestNewUsersUsesLoopbackForSocketDSN(t *testing.T) {
	u, err := NewUsers("host=/var/run/postgresql user=runova dbname=postgres")
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "127.0.0.1" || u.Port != 5432 {
		t.Fatalf("host %q port %d, want 127.0.0.1:5432", u.Host, u.Port)
	}
	u, err = NewUsers("host=10.1.2.3 port=55432 user=runova password=x dbname=postgres")
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "10.1.2.3" || u.Port != 55432 {
		t.Fatalf("host %q port %d", u.Host, u.Port)
	}
}

func TestIntegrationUserQueryRunsAsTheUsersRole(t *testing.T) {
	u, _, budi := newUsers(t)
	res, err := u.Query(context.Background(), budi, `SELECT session_user, current_user`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || *res.Rows[0][0] != "budi" || *res.Rows[0][1] != "budi" {
		t.Fatalf("rows = %v", res.Rows)
	}
}

func TestIntegrationUserQueryResult(t *testing.T) {
	u, _, budi := newUsers(t)
	ctx := context.Background()
	res, err := u.Query(ctx, budi, `SELECT id, memo, amount, NULL::text AS nothing FROM invoices ORDER BY id LIMIT 2`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Columns) != 4 || res.Columns[0].Name != "id" || res.Columns[0].Type != "integer" || res.Columns[2].Type != "numeric(10,2)" {
		t.Fatalf("columns = %+v", res.Columns)
	}
	if len(res.Rows) != 2 || *res.Rows[1][0] != "2" || *res.Rows[1][1] != "memo 2" || *res.Rows[1][2] != "3.00" || res.Rows[1][3] != nil {
		t.Fatalf("rows = %v", res.Rows)
	}
	if res.Truncated || res.Command != "SELECT" {
		t.Fatalf("truncated %v command %q", res.Truncated, res.Command)
	}

	res, err = u.Query(ctx, budi, `UPDATE invoices SET memo = 'x' WHERE id <= 3`, true)
	if err != nil || res.Affected != 3 || res.Command != "UPDATE" || len(res.Rows) != 0 {
		t.Fatalf("update = %+v, %v", res, err)
	}
}

func TestIntegrationUserQueryLimitsRows(t *testing.T) {
	u, _, budi := newUsers(t)
	res, err := u.Query(context.Background(), budi, `SELECT g FROM generate_series(1, 5000) g`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != MaxResultRows || !res.Truncated {
		t.Fatalf("%d rows, truncated %v", len(res.Rows), res.Truncated)
	}
	res, err = u.Query(context.Background(), budi, `SELECT g FROM generate_series(1, 1000) g`, false)
	if err != nil || len(res.Rows) != 1000 || res.Truncated {
		t.Fatalf("exactly 1000: %d rows truncated %v, %v", len(res.Rows), res.Truncated, err)
	}
}

func TestIntegrationUserQueryLimitsBytes(t *testing.T) {
	u, _, budi := newUsers(t)
	res, err := u.Query(context.Background(), budi, `SELECT repeat('x', 1000000) FROM generate_series(1, 20)`, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || len(res.Rows) == 0 || len(res.Rows) > 6 {
		t.Fatalf("%d rows, truncated %v", len(res.Rows), res.Truncated)
	}
}

func TestIntegrationUserQueryReadOnlyMode(t *testing.T) {
	u, _, budi := newUsers(t)
	ctx := context.Background()
	for _, sql := range []string{
		`WITH d AS (DELETE FROM invoices RETURNING *) SELECT count(*) FROM d`,
		`SELECT set_config('transaction_read_only', 'off', true)`,
		`SELECT set_config('default_transaction_read_only', 'off', false), (WITH d AS (DELETE FROM invoices RETURNING 1) SELECT count(*) FROM d)`,
		`INSERT INTO invoices VALUES (1000, 'x', 1)`,
		`CREATE TABLE t2 (x int)`,
	} {
		if _, err := u.Query(ctx, budi, sql, false); err == nil {
			t.Errorf("%q succeeded in read mode", sql)
		}
	}
	res, _ := u.Query(ctx, budi, `SELECT count(*) FROM invoices`, false)
	if *res.Rows[0][0] != "250" {
		t.Fatalf("rows changed in read mode: %v", res.Rows)
	}
}

func TestIntegrationUserQueryRejectsMultipleStatements(t *testing.T) {
	u, _, budi := newUsers(t)
	_, err := u.Query(context.Background(), budi, `SELECT 1; DROP TABLE invoices`, true)
	if err == nil {
		t.Fatal("two statements ran")
	}
	res, err := u.Query(context.Background(), budi, `SELECT count(*) FROM invoices`, false)
	if err != nil || *res.Rows[0][0] != "250" {
		t.Fatalf("table damaged: %v %v", res.Rows, err)
	}
}

func TestIntegrationUserQueryTimeout(t *testing.T) {
	u, _, budi := newUsers(t)
	u.ReadTimeout = time.Second
	start := time.Now()
	_, err := u.Query(context.Background(), budi, `SELECT pg_sleep(20)`, false)
	if sqlstate(err) != "57014" {
		t.Fatalf("err = %v (%s), want 57014 query_canceled", err, sqlstate(err))
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
	// The session cannot lift its own timeout for the statement already running.
	_, err = u.Query(context.Background(), budi, `SELECT set_config('statement_timeout', '0', false), pg_sleep(3)`, false)
	if sqlstate(err) != "57014" {
		t.Fatalf("lifted timeout: err = %v", err)
	}
}

// The real boundary: whatever the panel's whitelist lets through or not, the
// user's own role cannot do these. They run straight on the panel's connection.
func TestIntegrationUserRoleCannotEscape(t *testing.T) {
	u, _, budi := newUsers(t)
	ctx := context.Background()
	for _, sql := range []string{
		`CREATE FUNCTION f() RETURNS int LANGUAGE c AS 'libc', 'abs'`,
		`CREATE FUNCTION f() RETURNS int LANGUAGE plpython3u AS 'return 1'`,
		`CREATE FUNCTION f() RETURNS int LANGUAGE internal AS 'int4abs'`,
		`CREATE EXTENSION file_fdw`,
		`CREATE EXTENSION dblink`,
		`CREATE LANGUAGE plperlu`,
		`SET ROLE runova`,
		`SELECT set_config('role', 'runova', false)`,
		`SET SESSION AUTHORIZATION runova`,
		`SELECT set_config('session_authorization', 'runova', false)`,
		`SELECT pg_read_file('/etc/passwd')`,
		`SELECT pg_read_binary_file('/etc/passwd')`,
		`SELECT pg_ls_dir('.')`,
		`SELECT lo_import('/etc/passwd')`,
		`COPY invoices FROM '/etc/passwd'`,
		`COPY invoices TO '/tmp/runova-x'`,
		`COPY (SELECT 1) TO PROGRAM 'id'`,
		`ALTER ROLE budi SUPERUSER`,
		`ALTER ROLE budi CREATEROLE`,
		`ALTER ROLE andi PASSWORD 'x'`,
		`ALTER ROLE budi CONNECTION LIMIT -1`,
		`GRANT budi TO andi`,
		`GRANT runova TO budi`,
		`GRANT andi TO budi`,
		`GRANT pg_read_server_files TO budi`,
		`CREATE ROLE x`,
		`CREATE DATABASE x`,
		`ALTER SYSTEM SET work_mem = '1GB'`,
		`SELECT pg_reload_conf()`,
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = 'runova'`,
		`SELECT * FROM pg_authid`,
		`DROP TABLE andi_app.public.secret`,
	} {
		_, err := u.Query(ctx, budi, sql, true)
		if err == nil {
			t.Errorf("%q succeeded as budi", sql)
		}
	}
	// The role is still what it was.
	res, err := u.Query(ctx, budi, `SELECT rolsuper, rolcreaterole, rolcreatedb FROM pg_roles WHERE rolname = 'budi'`, false)
	if err != nil || *res.Rows[0][0] != "f" || *res.Rows[0][1] != "f" || *res.Rows[0][2] != "f" {
		t.Fatalf("role attributes = %v, %v", res.Rows, err)
	}
}

func TestIntegrationUserOwnsTrustedFunctions(t *testing.T) {
	u, _, budi := newUsers(t)
	ctx := context.Background()
	for _, sql := range []string{
		`CREATE FUNCTION twice(int) RETURNS int LANGUAGE sql AS 'SELECT $1 * 2'`,
		`CREATE FUNCTION thrice(x int) RETURNS int LANGUAGE plpgsql AS $$ BEGIN RETURN x * 3; END $$`,
	} {
		if _, err := u.Query(ctx, budi, sql, true); err != nil {
			t.Errorf("%q: %v", sql, err)
		}
	}
}

func TestIntegrationUserCannotReachOtherDatabases(t *testing.T) {
	u, _, _ := newUsers(t)
	ctx := context.Background()
	for _, db := range []string{"andi_app", "postgres", "template1"} {
		_, err := u.Query(ctx, Creds{Role: "budi", Password: "budipass", Database: db}, `SELECT 1`, false)
		if db == "postgres" || db == "template1" {
			// PUBLIC keeps CONNECT on the shared databases; nothing of other users lives there.
			continue
		}
		if !errors.Is(err, ErrConnect) || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("budi -> %s: err = %v", db, err)
		}
	}
	if _, err := u.Query(ctx, Creds{Role: "budi", Password: "wrong", Database: "budi_app"}, `SELECT 1`, false); !errors.Is(err, ErrConnect) {
		t.Errorf("wrong password: err = %v", err)
	}
}

func TestIntegrationUserTables(t *testing.T) {
	u, _, budi := newUsers(t)
	ctx := context.Background()
	tables, err := u.Tables(ctx, budi)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 || tables[0].Name != "Weird; Name" || tables[1].Name != "invoices" {
		t.Fatalf("tables = %+v", tables)
	}
	inv := tables[1]
	if inv.Rows != 250 || inv.RowsEstimated || inv.SizeBytes <= 0 {
		t.Fatalf("invoices = %+v", inv)
	}
	want := []Column{
		{Name: "id", Type: "integer", Nullable: false, PrimaryKey: true},
		{Name: "memo", Type: "text", Nullable: false, Default: "'baru'::text"},
		{Name: "amount", Type: "numeric(10,2)", Nullable: true},
	}
	if len(inv.Columns) != len(want) {
		t.Fatalf("columns = %+v", inv.Columns)
	}
	for i, c := range want {
		if inv.Columns[i] != c {
			t.Errorf("column %d = %+v, want %+v", i, inv.Columns[i], c)
		}
	}
}

func TestIntegrationUserRowsPaging(t *testing.T) {
	u, _, budi := newUsers(t)
	ctx := context.Background()
	res, err := u.Rows(ctx, budi, "invoices", 200, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 50 || res.Truncated {
		t.Fatalf("last page: %d rows, more %v", len(res.Rows), res.Truncated)
	}
	res, err = u.Rows(ctx, budi, "invoices", 0, 100)
	if err != nil || len(res.Rows) != 100 || !res.Truncated || *res.Rows[0][0] != "1" {
		t.Fatalf("first page: %d rows, more %v, %v", len(res.Rows), res.Truncated, err)
	}
	if _, err := u.Rows(ctx, budi, "Weird; Name", 0, 100); err != nil {
		t.Fatalf("quoted name: %v", err)
	}
}

func TestIntegrationUserTruncateAndDrop(t *testing.T) {
	u, _, budi := newUsers(t)
	ctx := context.Background()
	if err := u.Truncate(ctx, budi, "invoices"); err != nil {
		t.Fatal(err)
	}
	res, _ := u.Query(ctx, budi, `SELECT count(*) FROM invoices`, false)
	if *res.Rows[0][0] != "0" {
		t.Fatalf("after truncate: %v", res.Rows)
	}
	if err := u.DropTable(ctx, budi, "Weird; Name"); err != nil {
		t.Fatal(err)
	}
	tables, _ := u.Tables(ctx, budi)
	if len(tables) != 1 || tables[0].Name != "invoices" {
		t.Fatalf("after drop: %+v", tables)
	}
	if err := u.DropTable(ctx, budi, "nope"); sqlstate(err) != "42P01" {
		t.Fatalf("drop missing: %v", err)
	}
}
