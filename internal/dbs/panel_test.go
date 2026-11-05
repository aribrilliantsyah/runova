package dbs_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"runova/internal/dbs"
	"runova/internal/pg"
	"runova/internal/pg/pgfake"
	"runova/internal/store"
)

// panelEnv is newEnv with the fake's user connections and one database for budi.
func panelEnv(t *testing.T) (*env, *pgfake.Users, store.User, store.Database) {
	t.Helper()
	e := newEnv(t)
	users := e.pg.Users()
	e.svc.Users = users
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	if err := e.svc.OnUserCreated(ctx, u); err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.Create(ctx, u, "billing")
	if err != nil {
		t.Fatal(err)
	}
	users.AddTable(d.Name, "invoices", []string{"id", "memo"}, [][]string{{"1", "a"}, {"2", "b"}, {"3", "c"}})
	return e, users, u, d
}

func TestPanelUsesTheUsersOwnRole(t *testing.T) {
	e, users, u, d := panelEnv(t)
	ctx := context.Background()
	if _, err := e.svc.Tables(ctx, u, d.ID); err != nil {
		t.Fatal(err)
	}
	role, _ := e.pg.Role("budi")
	calls := e.pg.CallsWithPrefix("user ")
	if !slices.Equal(calls, []string{"user budi budi_billing tables"}) {
		t.Fatalf("calls = %v", calls)
	}
	// The fake refuses any other password, so the call above logged in with the stored one.
	if _, err := users.Tables(ctx, pg.Creds{Role: "budi", Password: role.Password + "x", Database: d.Name}); !errors.Is(err, pg.ErrConnect) {
		t.Fatalf("wrong password accepted: %v", err)
	}
}

func TestPanelOwnership(t *testing.T) {
	e, _, _, d := panelEnv(t)
	ctx := context.Background()
	andi := e.user(t, "andi", store.RoleUser)
	if err := e.svc.OnUserCreated(ctx, andi); err != nil {
		t.Fatal(err)
	}
	before := len(e.pg.CallsWithPrefix("user "))
	checks := map[string]error{}
	_, checks["tables"] = e.svc.Tables(ctx, andi, d.ID)
	_, checks["rows"] = e.svc.TableRows(ctx, andi, d.ID, "invoices", 0, 100)
	_, checks["query"] = e.svc.Query(ctx, andi, d.ID, "SELECT 1", false)
	checks["truncate"] = e.svc.TruncateTable(ctx, andi, d.ID, "invoices", "invoices")
	checks["drop"] = e.svc.DropTable(ctx, andi, d.ID, "invoices", "invoices")
	checks["restore"] = e.svc.RestoreUpload(ctx, andi, d.ID, d.Name, strings.NewReader("PGDMP"), 1<<20)
	_, checks["export"] = e.svc.Export(ctx, andi, d.ID, func(string) io.Writer { return io.Discard })
	for op, err := range checks {
		if !errors.Is(err, dbs.ErrNotFound) {
			t.Errorf("%s by andi: %v, want ErrNotFound", op, err)
		}
	}
	if after := len(e.pg.CallsWithPrefix("user ")); after != before {
		t.Fatalf("foreign calls reached PostgreSQL: %v", e.pg.CallsWithPrefix("user ")[before:])
	}
}

func TestQueryModes(t *testing.T) {
	e, _, u, d := panelEnv(t)
	ctx := context.Background()

	r, err := e.svc.Query(ctx, u, d.ID, "SELECT * FROM invoices;", false)
	if err != nil || r.Error != "" || len(r.Rows) != 3 || len(r.Columns) != 2 {
		t.Fatalf("select = %+v, %v", r, err)
	}
	if got := e.pg.CallsWithPrefix("user budi budi_billing query"); !slices.Equal(got, []string{"user budi budi_billing query read SELECT * FROM invoices"}) {
		t.Fatalf("calls = %v", got)
	}

	r, err = e.svc.Query(ctx, u, d.ID, "DELETE FROM invoices", false)
	if err != nil || !strings.Contains(r.Error, "mode Tulis") {
		t.Fatalf("delete in read mode = %+v, %v", r, err)
	}
	r, err = e.svc.Query(ctx, u, d.ID, "DELETE FROM invoices", true)
	if err != nil || r.Error != "" || r.Affected != 1 || len(r.Rows) != 0 || r.Command != "DELETE" {
		t.Fatalf("delete = %+v, %v", r, err)
	}
}

func TestQueryRefusalsNeverReachPostgreSQL(t *testing.T) {
	e, _, u, d := panelEnv(t)
	ctx := context.Background()
	for _, sql := range []string{
		"SELECT 1; DROP TABLE invoices",
		"COPY invoices TO '/tmp/x'",
		"SET ROLE runova",
		"CREATE FUNCTION f() RETURNS int LANGUAGE c AS 'x'",
		"CREATE EXTENSION file_fdw",
		"DO $$ BEGIN END $$",
		"GRANT budi TO andi",
		"",
	} {
		r, err := e.svc.Query(ctx, u, d.ID, sql, true)
		if err != nil || r.Error == "" {
			t.Errorf("%q = %+v, %v", sql, r, err)
		}
	}
	if got := e.pg.CallsWithPrefix("user "); len(got) != 0 {
		t.Fatalf("refused queries reached PostgreSQL: %v", got)
	}
}

func TestQueryPostgreSQLErrors(t *testing.T) {
	e, users, u, d := panelEnv(t)
	ctx := context.Background()
	users.QueryErr = &pgconn.PgError{Severity: "ERROR", Code: "42703", Message: `column "nope" does not exist`, Hint: "Perhaps you meant x.", Position: 16}
	r, err := e.svc.Query(ctx, u, d.ID, "-- first line\nSELECT nope FROM invoices", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Error, `column "nope" does not exist`) || !strings.Contains(r.Error, "42703") || !strings.Contains(r.Error, "Perhaps you meant") || r.ErrorLine != 2 {
		t.Fatalf("result = %+v", r)
	}
	users.QueryErr = &pgconn.PgError{Severity: "ERROR", Code: "57014", Message: "canceling statement due to statement timeout"}
	r, _ = e.svc.Query(ctx, u, d.ID, "SELECT pg_sleep(60)", false)
	if !strings.Contains(r.Error, "10 detik") {
		t.Fatalf("timeout = %q", r.Error)
	}
	r, _ = e.svc.Query(ctx, u, d.ID, "UPDATE invoices SET memo = 'x'", true)
	if !strings.Contains(r.Error, "30 detik") {
		t.Fatalf("write timeout = %q", r.Error)
	}
}

func TestTableOperationsNeedAKnownTable(t *testing.T) {
	e, users, u, d := panelEnv(t)
	ctx := context.Background()
	for _, name := range []string{"nope", "pg_authid", "../invoices", `invoices" CASCADE; --`} {
		if _, err := e.svc.TableRows(ctx, u, d.ID, name, 0, 100); !errors.Is(err, dbs.ErrTableNotFound) {
			t.Errorf("rows %q: %v", name, err)
		}
		if err := e.svc.DropTable(ctx, u, d.ID, name, name); !errors.Is(err, dbs.ErrTableNotFound) {
			t.Errorf("drop %q: %v", name, err)
		}
	}
	if err := e.svc.TruncateTable(ctx, u, d.ID, "invoices", "Invoices"); !errors.Is(err, dbs.ErrValidation) {
		t.Fatalf("truncate with wrong confirmation: %v", err)
	}
	if err := e.svc.TruncateTable(ctx, u, d.ID, "invoices", "invoices"); err != nil {
		t.Fatal(err)
	}
	if res, _ := users.Rows(ctx, creds(t, e, u, d), "invoices", 0, 10); len(res.Rows) != 0 {
		t.Fatalf("rows after truncate: %v", res.Rows)
	}
	if err := e.svc.DropTable(ctx, u, d.ID, "invoices", "invoices"); err != nil {
		t.Fatal(err)
	}
	if tables, _ := e.svc.Tables(ctx, u, d.ID); len(tables) != 0 {
		t.Fatalf("tables after drop: %v", tables)
	}
}

func TestTableRowsPaging(t *testing.T) {
	e, _, u, d := panelEnv(t)
	ctx := context.Background()
	r, err := e.svc.TableRows(ctx, u, d.ID, "invoices", 2, 100)
	if err != nil || len(r.Rows) != 1 || *r.Rows[0][0] != "3" {
		t.Fatalf("page = %+v, %v", r, err)
	}
	for _, bad := range [][2]int{{-1, 100}, {0, 0}, {0, 101}} {
		if _, err := e.svc.TableRows(ctx, u, d.ID, "invoices", bad[0], bad[1]); !errors.Is(err, dbs.ErrValidation) {
			t.Errorf("offset %d limit %d: %v", bad[0], bad[1], err)
		}
	}
}

func creds(t *testing.T, e *env, u store.User, d store.Database) pg.Creds {
	t.Helper()
	role, _ := e.pg.Role(u.Username)
	return pg.Creds{Role: u.Username, Password: role.Password, Database: d.Name}
}

func TestRestoreUploadRunsAsTheUser(t *testing.T) {
	e, _, u, d := panelEnv(t)
	ctx := context.Background()
	out := filepath.Join(t.TempDir(), "seen")
	e.svc.PGRestore = writeScript(t, `{ echo "$@"; echo "user=$PGUSER pass=$PGPASSWORD"; cat "$7"; } > `+out)
	dump := "PGDMP" + strings.Repeat("x", 100)
	if err := e.svc.RestoreUpload(ctx, u, d.ID, d.Name, strings.NewReader(dump), 1<<20); err != nil {
		t.Fatal(err)
	}
	seen, _ := os.ReadFile(out)
	lines := strings.SplitN(string(seen), "\n", 3)
	role, _ := e.pg.Role("budi")
	if !strings.HasPrefix(lines[0], "--clean --if-exists --no-owner --role=budi -d budi_billing ") {
		t.Fatalf("args = %q", lines[0])
	}
	if lines[1] != "user=budi pass="+role.Password || lines[2] != dump {
		t.Fatalf("env/content = %q", seen)
	}
	if got := e.pg.CallsWithPrefix("terminate "); !slices.Equal(got, []string{"terminate budi_billing"}) {
		t.Fatalf("terminate = %v", got)
	}
	if left := tempFiles(t, e); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

func tempFiles(t *testing.T, e *env) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(e.dir, func(p string, de os.DirEntry, err error) error {
		if err == nil && !de.IsDir() && strings.Contains(de.Name(), "upload") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestRestoreUploadRefusals(t *testing.T) {
	e, _, u, d := panelEnv(t)
	ctx := context.Background()
	ran := filepath.Join(t.TempDir(), "ran")
	e.svc.PGRestore = writeScript(t, "touch "+ran)
	for name, tc := range map[string]struct {
		confirm, body string
		limit         int64
		want          error
	}{
		"wrong confirmation": {"budi_other", "PGDMPxx", 1 << 20, dbs.ErrValidation},
		"plain SQL":          {d.Name, "DROP TABLE invoices;", 1 << 20, dbs.ErrValidation},
		"empty":              {d.Name, "", 1 << 20, dbs.ErrValidation},
		"too large":          {d.Name, "PGDMP" + strings.Repeat("x", 100), 50, dbs.ErrTooLarge},
	} {
		err := e.svc.RestoreUpload(ctx, u, d.ID, tc.confirm, strings.NewReader(tc.body), tc.limit)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("pg_restore ran for a refused upload")
	}
	if got := e.pg.CallsWithPrefix("terminate "); len(got) != 0 {
		t.Fatalf("terminated connections for a refused upload: %v", got)
	}
	if left := tempFiles(t, e); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

func TestRestoreUploadToolFailureCleansUp(t *testing.T) {
	e, _, u, d := panelEnv(t)
	e.svc.PGRestore = writeScript(t, `echo 'pg_restore: error: unsupported version (1.16) in file header' >&2; exit 1`)
	err := e.svc.RestoreUpload(context.Background(), u, d.ID, d.Name, strings.NewReader("PGDMPx"), 1<<20)
	if te, ok := errors.AsType[*dbs.ToolError](err); !ok || !strings.Contains(te.Stderr, "unsupported version") {
		t.Fatalf("err = %v", err)
	}
	if left := tempFiles(t, e); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

func TestRestoreFromBackupRunsAsTheUser(t *testing.T) {
	e, _, u, d := panelEnv(t)
	ctx := context.Background()
	e.svc.PGDump = writeScript(t, "printf PGDMPx")
	b, err := e.svc.BackupNow(ctx, d, true)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "seen")
	e.svc.PGRestore = writeScript(t, `echo "$PGUSER" > `+out)
	if err := e.svc.Restore(ctx, u, d.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if seen, _ := os.ReadFile(out); string(seen) != "budi\n" {
		t.Fatalf("pg_restore ran as %q", seen)
	}
}

func TestExportStreamsAsTheUser(t *testing.T) {
	e, _, u, d := panelEnv(t)
	ctx := context.Background()
	e.svc.PGDump = writeScript(t, `printf "%s|$PGUSER|" "$*"; head -c 300000 /dev/zero | tr '\0' 'x'`)
	var buf bytes.Buffer
	var named string
	name, err := e.svc.Export(ctx, u, d.ID, func(n string) io.Writer { named = n; return &buf })
	if err != nil {
		t.Fatal(err)
	}
	if name != named || !strings.HasPrefix(name, "budi_billing-20261030-090000") || !strings.HasSuffix(name, ".dump") {
		t.Fatalf("name = %q / %q", name, named)
	}
	if !strings.HasPrefix(buf.String(), "-Fc --no-owner -d budi_billing|budi|") || buf.Len() != len("-Fc --no-owner -d budi_billing|budi|")+300000 {
		t.Fatalf("stream = %q… (%d bytes)", buf.String()[:min(60, buf.Len())], buf.Len())
	}
	if entries, _ := os.ReadDir(e.svc.BackupDir); len(entries) != 0 {
		t.Fatalf("export wrote to the backup folder: %v", entries)
	}
}

func TestExportFailureBeforeOutput(t *testing.T) {
	e, _, u, d := panelEnv(t)
	e.svc.PGDump = writeScript(t, `echo 'pg_dump: error: connection failed' >&2; exit 1`)
	started := false
	_, err := e.svc.Export(context.Background(), u, d.ID, func(string) io.Writer { started = true; return io.Discard })
	if te, ok := errors.AsType[*dbs.ToolError](err); !ok || !strings.Contains(te.Stderr, "connection failed") || started {
		t.Fatalf("err = %v started = %v", err, started)
	}
}

func TestExportFailureAfterOutput(t *testing.T) {
	e, _, u, d := panelEnv(t)
	e.svc.PGDump = writeScript(t, `printf PGDMP; echo 'pg_dump: error: lost' >&2; exit 1`)
	started := false
	_, err := e.svc.Export(context.Background(), u, d.ID, func(string) io.Writer { started = true; return io.Discard })
	if !started || !errors.Is(err, dbs.ErrExportBroken) {
		t.Fatalf("err = %v started = %v", err, started)
	}
}
