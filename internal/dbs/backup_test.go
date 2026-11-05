package dbs_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"runova/internal/dbs"
	"runova/internal/pg"
	"runova/internal/pg/pgtest"
	"runova/internal/store"
)

func TestNextRun(t *testing.T) {
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"before 02:00", time.Date(2026, 10, 30, 1, 59, 0, 0, jakarta), time.Date(2026, 10, 30, 2, 0, 0, 0, jakarta)},
		{"at 02:00", time.Date(2026, 10, 30, 2, 0, 0, 0, jakarta), time.Date(2026, 10, 31, 2, 0, 0, 0, jakarta)},
		{"after 02:00", time.Date(2026, 10, 30, 23, 0, 0, 0, jakarta), time.Date(2026, 10, 31, 2, 0, 0, 0, jakarta)},
		{"month end", time.Date(2026, 10, 31, 12, 0, 0, 0, jakarta), time.Date(2026, 11, 1, 2, 0, 0, 0, jakarta)},
		// Europe/Berlin leaves DST on 2026-10-25: the day is 25 hours long.
		{"berlin day with extra hour", time.Date(2026, 10, 25, 1, 0, 0, 0, berlin), time.Date(2026, 10, 25, 2, 0, 0, 0, berlin)},
		{"berlin after fall back", time.Date(2026, 10, 25, 2, 30, 0, 0, berlin), time.Date(2026, 10, 26, 2, 0, 0, 0, berlin)},
		// Spring forward on 2026-03-29: 02:00 does not exist, Go moves it to 03:00.
		{"berlin before spring forward", time.Date(2026, 3, 29, 0, 30, 0, 0, berlin), time.Date(2026, 3, 29, 3, 0, 0, 0, berlin)},
	}
	for _, c := range cases {
		if got := dbs.NextRun(c.now); !got.Equal(c.want) {
			t.Errorf("%s: NextRun(%v) = %v, want %v", c.name, c.now, got, c.want)
		}
	}
}

// writeBackup puts a dump file named for t into the database's folder.
func (e *env) writeBackup(t *testing.T, db string, at time.Time, manual bool, content string) string {
	t.Helper()
	dir := filepath.Join(e.svc.BackupDir, db)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := at.Format("20060102-150405")
	if manual {
		name += "-m"
	}
	name += ".dump"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestBackupPathConfined(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user(t, "budi", store.RoleUser)
	other := e.user(t, "andi", store.RoleUser)
	d1, _ := e.svc.Create(ctx, a, "one")
	d2, _ := e.svc.Create(ctx, a, "two")
	foreign, _ := e.svc.Create(ctx, other, "x")
	at := time.Date(2026, 10, 30, 2, 0, 0, 0, time.UTC)
	own := e.writeBackup(t, d1.Name, at, false, "own")
	theirs := e.writeBackup(t, d2.Name, at.Add(time.Second), false, "two")
	e.writeBackup(t, foreign.Name, at.Add(2*time.Second), false, "foreign")
	if err := os.Symlink(filepath.Join(e.svc.BackupDir, d2.Name, theirs), filepath.Join(e.svc.BackupDir, d1.Name, "20261030-030000.dump")); err != nil {
		t.Fatal(err)
	}

	if p, err := e.svc.BackupPath(ctx, a, d1.ID, own); err != nil || p != filepath.Join(e.svc.BackupDir, d1.Name, own) {
		t.Fatalf("own backup: %q, %v", p, err)
	}
	for _, id := range []string{
		"../budi_two/" + theirs, "../andi_x/20261030-020002.dump", "x.dump", "", ".", "..",
		theirs,                                   // exists, but in another database's folder
		own + "/../" + own,                       // not a plain file name
		"20261030-020000.dump/",                  // trailing slash
		"20261030-030000.dump",                   // symlink out of the folder
		"20261030-040000.dump",                   // does not exist
		strings.Replace(own, ".dump", ".sql", 1), // wrong extension
	} {
		if _, err := e.svc.BackupPath(ctx, a, d1.ID, id); !errors.Is(err, dbs.ErrNotFound) {
			t.Errorf("BackupPath(%q) = %v, want ErrNotFound", id, err)
		}
	}
	if _, err := e.svc.BackupPath(ctx, a, foreign.ID, own); !errors.Is(err, dbs.ErrNotFound) {
		t.Errorf("foreign database: %v", err)
	}
	if err := e.svc.Restore(ctx, a, d1.ID, theirs); !errors.Is(err, dbs.ErrNotFound) {
		t.Errorf("Restore of another database's backup: %v", err)
	}
	if got := e.pg.CallsWithPrefix("terminate"); len(got) != 0 {
		t.Errorf("rejected restore terminated connections: %v", got)
	}
}

func TestBackupsListNewestFirst(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	d, _ := e.svc.Create(ctx, u, "one")
	base := time.Date(2026, 10, 28, 2, 0, 0, 0, time.UTC)
	e.writeBackup(t, d.Name, base, false, "a")
	e.writeBackup(t, d.Name, base.Add(48*time.Hour), true, "bbb")
	e.writeBackup(t, d.Name, base.Add(24*time.Hour), false, "cc")
	os.WriteFile(filepath.Join(e.svc.BackupDir, d.Name, ".tmp-123"), []byte("partial"), 0o600)

	got, err := e.svc.Backups(ctx, u, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, b := range got {
		ids = append(ids, b.ID)
	}
	want := []string{"20261030-020000-m.dump", "20261029-020000.dump", "20261028-020000.dump"}
	if !slices.Equal(ids, want) {
		t.Fatalf("ids = %v", ids)
	}
	if !got[0].Manual || got[1].Manual || got[0].SizeBytes != 3 || got[0].At != base.Add(48*time.Hour).UnixMilli() {
		t.Fatalf("backups = %+v", got)
	}
	if empty, err := e.svc.Backups(ctx, u, d.ID+99); !errors.Is(err, dbs.ErrNotFound) || empty != nil {
		t.Fatalf("unknown database: %v %v", empty, err)
	}
}

func TestPrune(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	old := e.writeBackup(t, "budi_one", now.AddDate(0, 0, -8), false, "old")
	oldManual := e.writeBackup(t, "budi_one", now.AddDate(0, 0, -8).Add(time.Minute), true, "oldm")
	recent := e.writeBackup(t, "budi_one", now.AddDate(0, 0, -6), false, "recent")
	other := e.writeBackup(t, "andi_two", now.AddDate(0, 0, -9), false, "old2")
	stray := filepath.Join(e.svc.BackupDir, "budi_one", "notes.txt")
	os.WriteFile(stray, []byte("x"), 0o600)

	if err := e.svc.Prune(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(e.svc.BackupDir, "budi_one"))
	var left []string
	for _, f := range entries {
		left = append(left, f.Name())
	}
	if want := []string{recent, "notes.txt"}; !slices.Equal(left, want) {
		t.Fatalf("left = %v (removed %s, %s), want %v", left, old, oldManual, want)
	}
	if _, err := os.Stat(filepath.Join(e.svc.BackupDir, "andi_two", other)); !os.IsNotExist(err) {
		t.Errorf("other folder not pruned: %v", err)
	}
}

func TestBackupFailureCleansTemp(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	d, _ := e.svc.Create(ctx, u, "one")
	e.svc.PGDump = "false"
	if _, err := e.svc.BackupNow(ctx, d, false); err == nil {
		t.Fatal("BackupNow succeeded")
	}
	entries, err := os.ReadDir(filepath.Join(e.svc.BackupDir, d.Name))
	if err != nil || len(entries) != 0 {
		t.Fatalf("folder = %v, %v", entries, err)
	}
}

func TestBackupDumpErrorCarriesStderr(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	d, _ := e.svc.Create(ctx, u, "one")
	e.svc.PGDump = writeScript(t, "echo 'pg_dump: connection refused' >&2; head -c 10000 /dev/zero | tr '\\0' x >&2; exit 1")
	_, err := e.svc.BackupNow(ctx, d, false)
	te, ok := errors.AsType[*dbs.ToolError](err)
	if !ok || !strings.Contains(te.Stderr, "connection refused") || len(te.Stderr) > 4096 {
		t.Fatalf("err = %v (stderr %d bytes)", err, len(te.Stderr))
	}
}

func TestBackupNowFileAndEnv(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	d, _ := e.svc.Create(ctx, u, "one")
	argsFile := filepath.Join(t.TempDir(), "args")
	e.svc.PGDump = writeScript(t, `echo "$@" > `+argsFile+`; echo "$PGHOST|$PGUSER" >> `+argsFile+`; printf DUMP`)
	e.svc.PGEnv = []string{"PGHOST=db.example", "PGUSER=runova"}
	*e.clock = time.Date(2026, 10, 30, 2, 0, 5, 0, time.UTC)

	b, err := e.svc.BackupNow(ctx, d, true)
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != "20261030-020005-m.dump" || !b.Manual || b.SizeBytes != 4 {
		t.Fatalf("backup = %+v", b)
	}
	path := filepath.Join(e.svc.BackupDir, d.Name, b.ID)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file = %v, %v", info, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "DUMP" {
		t.Fatalf("content = %q", got)
	}
	if args, _ := os.ReadFile(argsFile); string(args) != "-Fc --no-owner -d budi_one\ndb.example|runova\n" {
		t.Fatalf("pg_dump saw %q", args)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("folder = %v", entries)
	}
}

func TestRestoreCommand(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, "budi", store.RoleUser)
	if err := e.svc.OnUserCreated(ctx, u); err != nil {
		t.Fatal(err)
	}
	d, _ := e.svc.Create(ctx, u, "one")
	id := e.writeBackup(t, d.Name, time.Date(2026, 10, 30, 2, 0, 0, 0, time.UTC), false, "x")
	argsFile := filepath.Join(t.TempDir(), "args")
	e.svc.PGRestore = writeScript(t, `echo "$@" > `+argsFile)

	if err := e.svc.Restore(ctx, u, d.ID, id); err != nil {
		t.Fatal(err)
	}
	want := "--clean --if-exists --no-owner --role=budi -d budi_one " + filepath.Join(e.svc.BackupDir, d.Name, id) + "\n"
	if args, _ := os.ReadFile(argsFile); string(args) != want {
		t.Fatalf("pg_restore saw %q, want %q", args, want)
	}
	if got := e.pg.CallsWithPrefix("terminate "); !slices.Equal(got, []string{"terminate budi_one"}) {
		t.Fatalf("terminate calls = %v", got)
	}

	e.svc.PGRestore = writeScript(t, `echo 'pg_restore: error: could not execute query' >&2; exit 1`)
	err := e.svc.Restore(ctx, u, d.ID, id)
	if te, ok := errors.AsType[*dbs.ToolError](err); !ok || !strings.Contains(te.Stderr, "could not execute query") {
		t.Fatalf("failed restore: %v", err)
	}
}

func TestBackupAllNotifiesOwnerAndContinues(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user(t, "andi", store.RoleUser)
	b := e.user(t, "budi", store.RoleUser)
	e.svc.Create(ctx, a, "one")
	e.svc.Create(ctx, b, "two")
	// Fails only for andi's database.
	e.svc.PGDump = writeScript(t, `case "$*" in *andi_one*) echo boom >&2; exit 1;; esac; printf ok`)

	e.svc.BackupAll(ctx)

	if _, err := os.Stat(filepath.Join(e.svc.BackupDir, "budi_two")); err != nil {
		t.Fatalf("budi's database was not backed up: %v", err)
	}
	na, _ := e.st.Notifications(ctx, a.ID, 10)
	if len(na) != 1 || na[0].Title != "Backup andi_one gagal" {
		t.Fatalf("andi notifications = %+v", na)
	}
	if nb, _ := e.st.Notifications(ctx, b.ID, 10); len(nb) != 0 {
		t.Fatalf("budi notified: %+v", nb)
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tool.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	dsn := pgtest.StartPG(t)
	for _, bin := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s not on PATH: %v", bin, err)
		}
	}
	ctx := context.Background()
	admin, err := pg.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	env, err := pg.ClientEnv(dsn)
	if err != nil {
		t.Fatal(err)
	}

	e := newEnv(t)
	users, err := pg.NewUsers(dsn)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.PG, e.svc.PGEnv, e.svc.Users = admin, env, users
	e.svc.Now = time.Now
	u := e.user(t, "budi", store.RoleUser)
	if err := e.svc.OnUserCreated(ctx, u); err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.Create(ctx, u, "billing")
	if err != nil {
		t.Fatal(err)
	}

	conn := connectAsUser(t, ctx, e, dsn, u, d.Name)
	if _, err := conn.Exec(ctx, `CREATE TABLE invoices(id int PRIMARY KEY, memo text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO invoices VALUES (1, 'first'), (2, 'second')`); err != nil {
		t.Fatal(err)
	}

	b, err := e.svc.Backup(ctx, u, d.ID)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if !b.Manual || b.SizeBytes == 0 {
		t.Fatalf("backup = %+v", b)
	}

	if _, err := conn.Exec(ctx, `DELETE FROM invoices WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE junk(x int)`); err != nil {
		t.Fatal(err)
	}

	// Restore ends our connection.
	if err := e.svc.Restore(ctx, u, d.ID, b.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT 1`); err == nil {
		t.Error("connection survived the restore")
	}

	conn = connectAsUser(t, ctx, e, dsn, u, d.Name)
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM invoices`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows after restore = %d, %v", n, err)
	}
	var owner string
	if err := conn.QueryRow(ctx, `SELECT tableowner FROM pg_tables WHERE tablename = 'invoices'`).Scan(&owner); err != nil || owner != "budi" {
		t.Fatalf("table owner = %q, %v", owner, err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO invoices VALUES (3, 'third')`); err != nil {
		t.Fatalf("budi cannot write after restore: %v", err)
	}

	// Export streams a dump made as budi; restoring it from an upload brings the rows back.
	var dump bytes.Buffer
	if _, err := e.svc.Export(ctx, u, d.ID, func(string) io.Writer { return &dump }); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if !bytes.HasPrefix(dump.Bytes(), []byte("PGDMP")) {
		t.Fatalf("export is not a custom dump: %q", dump.Bytes()[:min(16, dump.Len())])
	}
	if _, err := conn.Exec(ctx, `DROP TABLE invoices`); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RestoreUpload(ctx, u, d.ID, d.Name, &dump, 1<<30); err != nil {
		t.Fatalf("RestoreUpload: %v", err)
	}
	conn = connectAsUser(t, ctx, e, dsn, u, d.Name)
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM invoices`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("rows after upload restore = %d, %v", n, err)
	}
	tables, err := e.svc.Tables(ctx, u, d.ID)
	if err != nil || len(tables) != 2 || tables[0].Name != "invoices" || tables[0].Rows != 3 || tables[1].Name != "junk" {
		t.Fatalf("Tables = %+v, %v", tables, err)
	}
}

// connectAsUser opens a connection to db as the user's PostgreSQL role, with
// the password Runova stored for it.
func connectAsUser(t *testing.T, ctx context.Context, e *env, adminDSN string, u store.User, db string) *pgx.Conn {
	t.Helper()
	sealed, err := e.st.PGPassword(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := e.box.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	cfg.User, cfg.Password, cfg.Database = u.Username, string(plain), db
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect as %s: %v", u.Username, err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}
