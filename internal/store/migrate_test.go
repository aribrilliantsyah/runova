package store

import (
	"context"
	"database/sql"
	_ "embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

//go:embed testdata/legacy_schema.sql
var legacySchema string

func files(m map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, body := range m {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

func rawDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runova.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func runMigrate(t *testing.T, db *sql.DB, fsys fs.FS, _ string) error {
	t.Helper()
	return migrate(context.Background(), db, fsys)
}

func version(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func backups(t *testing.T, path string) []string {
	t.Helper()
	m, err := filepath.Glob(path + ".bak-v*-*")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMigrateAppliesInOrderAndSetsVersion(t *testing.T) {
	db, path := rawDB(t)
	fsys := files(map[string]string{
		"0001_init.sql":    `CREATE TABLE t(a INTEGER);`,
		"0002_add_b.sql":   `ALTER TABLE t ADD COLUMN b TEXT NOT NULL DEFAULT 'x'; INSERT INTO t(a) VALUES(1);`,
		"0003_index.sql":   `CREATE INDEX t_b ON t(b);`,
		"README.md":        `ignored`,
		"notes/0009_x.sql": `ignored`,
	})
	if err := runMigrate(t, db, fsys, path); err != nil {
		t.Fatal(err)
	}
	if v := version(t, db); v != 3 {
		t.Fatalf("user_version = %d, want 3", v)
	}
	if n := count(t, db, `SELECT count(*) FROM t WHERE b = 'x'`); n != 1 {
		t.Fatalf("rows with default b = %d", n)
	}
	if err := runMigrate(t, db, fsys, path); err != nil {
		t.Fatalf("second run must be a no-op: %v", err)
	}
	if b := backups(t, path); len(b) != 0 {
		t.Fatalf("backups without pending migrations: %v", b)
	}
}

func TestMigrateRejectsGapsAndBadNames(t *testing.T) {
	db, path := rawDB(t)
	for name, m := range map[string]map[string]string{
		"gap":        {"0001_a.sql": `SELECT 1;`, "0003_c.sql": `SELECT 1;`},
		"not-from-1": {"0002_b.sql": `SELECT 1;`},
		"duplicate":  {"0001_a.sql": `SELECT 1;`, "0001_b.sql": `SELECT 1;`},
		"bad-name":   {"init.sql": `SELECT 1;`},
	} {
		if err := runMigrate(t, db, files(m), path); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if v := version(t, db); v != 0 {
		t.Fatalf("user_version = %d after rejected sets", v)
	}
}

func TestMigrateRollsBackFailedMigration(t *testing.T) {
	db, path := rawDB(t)
	fsys := files(map[string]string{
		"0001_init.sql": `CREATE TABLE t(a INTEGER);`,
		"0002_bad.sql":  `CREATE TABLE half(a INTEGER); INSERT INTO nope VALUES(1);`,
		"0003_next.sql": `CREATE TABLE never(a INTEGER);`,
	})
	err := runMigrate(t, db, fsys, path)
	if err == nil || !strings.Contains(err.Error(), "0002_bad.sql") {
		t.Fatalf("err = %v, want it to name 0002_bad.sql", err)
	}
	if v := version(t, db); v != 1 {
		t.Fatalf("user_version = %d, want 1 (0001 kept, 0002 rolled back)", v)
	}
	if n := count(t, db, `SELECT count(*) FROM sqlite_master WHERE name IN('half','never')`); n != 0 {
		t.Fatalf("%d tables left behind by failed or later migrations", n)
	}
	if n := count(t, db, `SELECT count(*) FROM sqlite_master WHERE name = 't'`); n != 1 {
		t.Fatal("0001 was not kept")
	}
}

func TestMigrateRejectsNewerDatabase(t *testing.T) {
	db, path := rawDB(t)
	if _, err := db.Exec(`CREATE TABLE keep(a); PRAGMA user_version = 5`); err != nil {
		t.Fatal(err)
	}
	fsys := files(map[string]string{"0001_init.sql": `CREATE TABLE t(a);`, "0002_more.sql": `CREATE TABLE u(a);`})
	err := runMigrate(t, db, fsys, path)
	if err == nil || !strings.Contains(err.Error(), "database dibuat oleh versi Runova yang lebih baru (skema 5, binary ini 2)") {
		t.Fatalf("err = %v", err)
	}
	if v := version(t, db); v != 5 {
		t.Fatalf("user_version = %d, want untouched 5", v)
	}
	if n := count(t, db, `SELECT count(*) FROM sqlite_master WHERE name IN('t','u')`); n != 0 {
		t.Fatal("migrations ran against a newer database")
	}
	if b := backups(t, path); len(b) != 0 {
		t.Fatalf("backup of a newer database: %v", b)
	}
}

func TestMigrateBacksUpPopulatedDatabaseBeforePendingMigration(t *testing.T) {
	db, path := rawDB(t)
	v1 := files(map[string]string{"0001_init.sql": `CREATE TABLE t(a INTEGER);`})
	if err := runMigrate(t, db, v1, path); err != nil {
		t.Fatal(err)
	}
	if b := backups(t, path); len(b) != 0 {
		t.Fatalf("backup of a fresh database: %v", b)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES(7)`); err != nil {
		t.Fatal(err)
	}

	v2 := files(map[string]string{
		"0001_init.sql": `CREATE TABLE t(a INTEGER);`,
		"0002_more.sql": `DELETE FROM t; CREATE TABLE u(a);`,
	})
	if err := runMigrate(t, db, v2, path); err != nil {
		t.Fatal(err)
	}
	b := backups(t, path)
	if len(b) != 1 || !strings.Contains(filepath.Base(b[0]), "runova.db.bak-v1-") {
		t.Fatalf("backups = %v, want one runova.db.bak-v1-<unix>", b)
	}
	if fi, err := os.Stat(b[0]); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	bak, err := sql.Open("sqlite", "file:"+b[0]+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer bak.Close()
	if n := count(t, bak, `SELECT a FROM t`); n != 7 {
		t.Fatalf("backup holds t.a = %d, want the pre-migration 7", n)
	}
	if n := count(t, bak, `SELECT count(*) FROM sqlite_master WHERE name = 'u'`); n != 0 {
		t.Fatal("backup already contains the migration")
	}
	if v := version(t, bak); v != 1 {
		t.Fatalf("backup user_version = %d", v)
	}
}

func TestMigrateKeepsThreeNewestBackups(t *testing.T) {
	db, path := rawDB(t)
	v1 := files(map[string]string{"0001_init.sql": `CREATE TABLE t(a INTEGER);`})
	if err := runMigrate(t, db, v1, path); err != nil {
		t.Fatal(err)
	}
	old := []string{".bak-v1-100", ".bak-v1-200", ".bak-v1-300", ".bak-v1-400"}
	for _, suffix := range old {
		if err := os.WriteFile(path+suffix, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(filepath.Dir(path), "runova.db.bak-notes")
	if err := os.WriteFile(other, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	v2 := files(map[string]string{
		"0001_init.sql": `CREATE TABLE t(a INTEGER);`,
		"0002_more.sql": `CREATE TABLE u(a);`,
	})
	if err := runMigrate(t, db, v2, path); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, b := range backups(t, path) {
		got[strings.TrimPrefix(b, path)] = true
	}
	if len(got) != 3 || got[".bak-v1-100"] || got[".bak-v1-200"] || !got[".bak-v1-300"] || !got[".bak-v1-400"] {
		t.Fatalf("backups left = %v, want 300, 400 and the new one", got)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("unrelated file removed: %v", err)
	}
}

func TestMigrateAdoptsPreMigrationDatabase(t *testing.T) {
	cases := map[string]string{
		"before databases feature": legacySchema,
		"after databases feature": legacySchema + `
CREATE TABLE databases(id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE, name TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL);
CREATE INDEX databases_user ON databases(user_id);
ALTER TABLE users ADD COLUMN pg_password TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN database_id INTEGER NULL REFERENCES databases ON DELETE RESTRICT;`,
	}
	for name, ddl := range cases {
		t.Run(name, func(t *testing.T) {
			db, path := rawDB(t)
			if _, err := db.Exec(ddl); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO users(username, password_hash, role, quota_apps, quota_memory_mb, quota_cpu_milli, quota_databases, quota_pg_connections, created_at)
				VALUES('budi', 'h', 'admin', 1, 1, 1, 1, 1, 1)`); err != nil {
				t.Fatal(err)
			}
			if err := runMigrate(t, db, embeddedMigrations(), path); err != nil {
				t.Fatal(err)
			}
			if v := version(t, db); v != 4 {
				t.Fatalf("user_version = %d, want 4 (adopted as 1, then 0002, 0003 and 0004 applied)", v)
			}
			if n := count(t, db, `SELECT count(*) FROM users WHERE username = 'budi'`); n != 1 {
				t.Fatal("user lost")
			}
			for table, column := range map[string]string{"users": "pg_password", "apps": "database_id"} {
				if n := count(t, db, `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column); n != 1 {
					t.Errorf("%s.%s missing after adoption", table, column)
				}
			}
			if n := count(t, db, `SELECT count(*) FROM sqlite_master WHERE name IN('databases','databases_user')`); n != 2 {
				t.Error("databases table or index missing after adoption")
			}
			if b := backups(t, path); len(b) != 1 || !strings.Contains(b[0], ".bak-v1-") {
				t.Fatalf("backups = %v, want one v1 backup taken before 0002 only", b)
			}
		})
	}
}

func TestMigrateAdoptionThenPendingMigrationBacksUp(t *testing.T) {
	db, path := rawDB(t)
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	fsys := files(map[string]string{
		"0001_init.sql": `CREATE TABLE users(never_run INTEGER);`,
		"0002_more.sql": `CREATE TABLE extra(a INTEGER);`,
	})
	if err := runMigrate(t, db, fsys, path); err != nil {
		t.Fatal(err)
	}
	if v := version(t, db); v != 2 {
		t.Fatalf("user_version = %d, want 2", v)
	}
	if n := count(t, db, `SELECT count(*) FROM pragma_table_info('users') WHERE name = 'never_run'`); n != 0 {
		t.Fatal("0001 ran against an adopted database")
	}
	if b := backups(t, path); len(b) != 1 || !strings.Contains(b[0], ".bak-v1-") {
		t.Fatalf("backups = %v, want one v1 backup before 0002", b)
	}
}

func TestMigrateRebuildKeepsChildRowsAndChecksForeignKeys(t *testing.T) {
	setup := `CREATE TABLE parent(id INTEGER PRIMARY KEY, name TEXT);
		CREATE TABLE child(id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parent ON DELETE CASCADE);`
	rebuild := `CREATE TABLE parent_new(id INTEGER PRIMARY KEY, name TEXT NOT NULL DEFAULT '', extra INTEGER NOT NULL DEFAULT 0);
		INSERT INTO parent_new(id, name) SELECT id, coalesce(name, '') FROM parent;
		DROP TABLE parent;
		ALTER TABLE parent_new RENAME TO parent;`

	t.Run("rebuild keeps cascade children", func(t *testing.T) {
		db, path := rawDB(t)
		if err := runMigrate(t, db, files(map[string]string{"0001_init.sql": setup}), path); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO parent VALUES(1, 'a'); INSERT INTO child VALUES(1, 1), (2, 1)`); err != nil {
			t.Fatal(err)
		}
		fsys := files(map[string]string{"0001_init.sql": setup, "0002_rebuild.sql": rebuild})
		if err := runMigrate(t, db, fsys, path); err != nil {
			t.Fatal(err)
		}
		if n := count(t, db, `SELECT count(*) FROM child`); n != 2 {
			t.Fatalf("child rows = %d after parent rebuild, want 2", n)
		}
	})

	t.Run("violation aborts and rolls back", func(t *testing.T) {
		db, path := rawDB(t)
		if err := runMigrate(t, db, files(map[string]string{"0001_init.sql": setup}), path); err != nil {
			t.Fatal(err)
		}
		fsys := files(map[string]string{
			"0001_init.sql":   setup,
			"0002_orphan.sql": `INSERT INTO child VALUES(1, 99);`,
		})
		err := runMigrate(t, db, fsys, path)
		if err == nil || !strings.Contains(err.Error(), "0002_orphan.sql") || !strings.Contains(err.Error(), "foreign key") {
			t.Fatalf("err = %v", err)
		}
		if v := version(t, db); v != 1 {
			t.Fatalf("user_version = %d, want 1", v)
		}
		if n := count(t, db, `SELECT count(*) FROM child`); n != 0 {
			t.Fatal("orphan row survived rollback")
		}
	})

	t.Run("foreign keys back on afterwards", func(t *testing.T) {
		db, path := rawDB(t)
		db.SetMaxOpenConns(1)
		if err := runMigrate(t, db, files(map[string]string{"0001_init.sql": setup}), path); err != nil {
			t.Fatal(err)
		}
		if n := count(t, db, `PRAGMA foreign_keys`); n != 1 {
			t.Fatalf("foreign_keys = %d on the connection returned to the pool", n)
		}
	})
}

func TestMigrateConcurrentOpensApplyEachMigrationOnce(t *testing.T) {
	fsys := files(map[string]string{
		"0001_init.sql": `CREATE TABLE log(n INTEGER);`,
		"0002_once.sql": `INSERT INTO log VALUES(2);`,
		"0003_once.sql": `INSERT INTO log VALUES(3);`,
	})
	path := filepath.Join(t.TempDir(), "runova.db")
	seed, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	if err := runMigrate(t, seed, files(map[string]string{"0001_init.sql": `CREATE TABLE log(n INTEGER);`}), path); err != nil {
		t.Fatal(err)
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	start := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, err := openDB(path)
			if err != nil {
				errs <- err
				return
			}
			defer db.Close()
			<-start
			errs <- migrate(context.Background(), db, fsys)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if v := version(t, seed); v != 3 {
		t.Fatalf("user_version = %d", v)
	}
	if got := count(t, seed, `SELECT count(*) FROM log`); got != 2 {
		t.Fatalf("log rows = %d, want each of 0002 and 0003 applied once", got)
	}
}

func TestOpenRunsEmbeddedMigrationsAndRejectsNewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runova.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cur, latest, err := SchemaVersion(path)
	if err != nil || cur != latest || latest < 1 {
		t.Fatalf("SchemaVersion = %d, %d, %v", cur, latest, err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	_, err = Open(path)
	if err == nil || !strings.Contains(err.Error(), "lebih baru (skema 99, binary ini ") {
		t.Fatalf("Open of a newer database: err = %v", err)
	}
	if cur, _, err := SchemaVersion(path); err != nil || cur != 99 {
		t.Fatalf("SchemaVersion after rejected Open = %d, %v", cur, err)
	}
}

func TestSchemaVersionOfMissingDatabaseDoesNotCreateIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runova.db")
	cur, latest, err := SchemaVersion(path)
	if err != nil || cur != 0 || latest < 1 {
		t.Fatalf("SchemaVersion = %d, %d, %v", cur, latest, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("SchemaVersion created the database: %v", err)
	}
}

func TestMigration0002KeepsVersion1Data(t *testing.T) {
	db, path := rawDB(t)
	init1, err := fs.ReadFile(embeddedMigrations(), "0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := runMigrate(t, db, files(map[string]string{"0001_init.sql": string(init1)}), path); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO users(id, username, password_hash, role, quota_apps, quota_memory_mb, quota_cpu_milli, quota_databases, quota_pg_connections, created_at)
			VALUES(1, 'budi', 'x', 'user', 3, 1024, 1000, 3, 20, 1);
		INSERT INTO apps(id, user_id, name, kind, internal_port, ip_port, memory_mb, cpu_milli, status, created_at)
			VALUES(1, 1, 'billing', 'static', 8080, 20000, 128, 100, 'running', 1);
		INSERT INTO domains(app_id, host, status, created_at) VALUES(1, 'billing.budi.com', 'active', 5), (1, 'www.budi.com', 'pending', 6);
	`); err != nil {
		t.Fatal(err)
	}

	if err := runMigrate(t, db, embeddedMigrations(), path); err != nil {
		t.Fatal(err)
	}
	if v := version(t, db); v != 4 {
		t.Fatalf("user_version = %d, want 4", v)
	}
	if n := count(t, db, `SELECT count(*) FROM domains WHERE app_id = 1 AND hosting_domain_id IS NULL AND label = '' AND host IN ('billing.budi.com', 'www.budi.com')`); n != 2 {
		t.Fatalf("free domains kept = %d, want 2", n)
	}
	if n := count(t, db, `SELECT count(*) FROM apps`); n != 1 {
		t.Fatalf("apps = %d, want 1", n)
	}
	if n := count(t, db, `SELECT count(*) FROM pragma_foreign_key_check`); n != 0 {
		t.Fatalf("foreign_key_check reports %d violations", n)
	}
	if _, err := db.Exec(`INSERT INTO hosting_domains(name, created_at) VALUES('porto.ex.id', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE domains SET hosting_domain_id = 1, label = 'billing' WHERE host = 'billing.budi.com'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM hosting_domains WHERE id = 1`); err == nil {
		t.Fatal("deleting a hosting domain that subdomains use must fail (ON DELETE RESTRICT)")
	}
}
