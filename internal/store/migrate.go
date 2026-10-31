package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations
var migrationFiles embed.FS

func embeddedMigrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err) // the path is a constant that the embed directive guarantees
	}
	return sub
}

// Schema migrations
//
// The schema is the sum of the files in migrations/, applied in order:
// 0001_init.sql, 0002_<name>.sql, ... The number is the schema version and is
// stored in PRAGMA user_version. A released migration is never edited; every
// schema change is a new file.
//
// Each migration runs in its own BEGIN IMMEDIATE transaction together with the
// user_version update, so a failure rolls both back and a concurrent opener
// (for example `runova admin create` while the service starts) waits for the
// write lock, then sees the new version and skips what is already done. Files
// contain plain SQL statements only; the runner owns the transaction and the
// pragmas, so a migration file must not contain BEGIN, COMMIT or PRAGMA.
//
// Adding columns, tables and indexes needs nothing special. Changing or
// dropping a column, or any change ALTER TABLE cannot express, rebuilds the
// table (SQLite's 12-step procedure, https://sqlite.org/lang_altertable.html).
// The runner performs the steps that are not SQL of the migration itself:
// PRAGMA foreign_keys is OFF for the whole run (the pragma is a no-op inside a
// transaction, which is why migrations run on one dedicated connection), and
// PRAGMA foreign_key_check runs before commit and aborts the migration on any
// violation. The file does the rest:
//
//	CREATE TABLE x_new(...);                       -- the new definition
//	INSERT INTO x_new(a, b) SELECT a, b FROM x;    -- copy, converting as needed
//	DROP TABLE x;
//	ALTER TABLE x_new RENAME TO x;
//	CREATE INDEX x_a ON x(a);                      -- recreate indexes, triggers, views
//
// With foreign keys off, DROP TABLE x does not cascade into child tables.
// Never rebuild by renaming x away first: that rewrites the children's
// REFERENCES to the renamed table.
//
// Before the first pending migration on a database that already has a schema,
// a copy is written with VACUUM INTO to <db>.bak-v<version>-<unix> (mode 0600)
// and the newest keepBackups copies are kept.

const keepBackups = 3

var (
	migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)
	backupName    = regexp.MustCompile(`^\.bak-v(\d+)-(\d+)$`)
)

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration %q: name must be NNNN_description.sql", e.Name())
		}
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		v, _ := strconv.Atoi(m[1])
		ms = append(ms, migration{version: v, name: e.Name(), sql: string(body)})
	}
	if len(ms) == 0 {
		return nil, errors.New("no migrations found")
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %s: want version %04d (versions must run 1..N without gaps or repeats)", m.name, i+1)
		}
	}
	return ms, nil
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func userVersion(ctx context.Context, q rowQuerier) (int, error) {
	var v int
	if err := q.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err != nil {
		return 0, fmt.Errorf("read user_version: %w", err)
	}
	return v, nil
}

func errNewer(current, latest int) error {
	return fmt.Errorf("database dibuat oleh versi Runova yang lebih baru (skema %d, binary ini %d)", current, latest)
}

// migrate brings the database to the newest version in fsys. A database from
// a newer binary is refused without being touched.
func migrate(ctx context.Context, db *sql.DB, fsys fs.FS) (err error) {
	ms, err := loadMigrations(fsys)
	if err != nil {
		return err
	}
	latest := len(ms)

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	defer conn.Close()

	if cur, err := userVersion(ctx, conn); err != nil {
		return err
	} else if cur > latest {
		return errNewer(cur, latest)
	} else if cur == latest {
		return nil
	}

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys: %w", err)
	}
	defer func() {
		if _, rerr := conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`); rerr != nil {
			// Never hand a connection with foreign keys off back to the pool.
			conn.Raw(func(any) error { return driver.ErrBadConn })
			err = errors.Join(err, fmt.Errorf("re-enable foreign keys: %w", rerr))
		}
	}()

	backedUp := false
	for {
		done, err := step(ctx, db, conn, ms, &backedUp)
		if err != nil || done {
			return err
		}
	}
}

// step applies the next pending migration, or adopts a pre-migration
// database, in one transaction. It reports true when nothing is left to do.
func step(ctx context.Context, db *sql.DB, conn *sql.Conn, ms []migration, backedUp *bool) (done bool, err error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin migration: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// The version is read again under the write lock: another opener may have
	// migrated while this one waited for it.
	cur, err := userVersion(ctx, tx)
	if err != nil {
		return false, err
	}
	switch {
	case cur > len(ms):
		return false, errNewer(cur, len(ms))
	case cur == len(ms):
		return true, tx.Rollback()
	case cur == 0:
		legacy, err := hasLegacySchema(ctx, tx)
		if err != nil {
			return false, err
		}
		if legacy {
			if err := adoptLegacy(ctx, tx); err != nil {
				return false, fmt.Errorf("adopt pre-migration database: %w", err)
			}
			*backedUp = false
			return false, tx.Commit()
		}
		// Nothing to lose in a database that starts empty.
		*backedUp = true
	case !*backedUp:
		// The write lock held by tx keeps the copy equal to version cur.
		if err := backup(ctx, db, cur); err != nil {
			return false, err
		}
		*backedUp = true
	}

	m := ms[cur]
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return false, fmt.Errorf("migration %s: %w", m.name, err)
	}
	if err := checkForeignKeys(ctx, tx); err != nil {
		return false, fmt.Errorf("migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
		return false, fmt.Errorf("migration %s: set version: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("migration %s: commit: %w", m.name, err)
	}
	slog.Info("database migrated", "migration", m.name, "version", m.version)
	return false, nil
}

func checkForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("foreign key check: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return rows.Err()
	}
	var table, parent string
	var rowid sql.NullInt64
	var fkid int
	if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
		return fmt.Errorf("foreign key check: %w", err)
	}
	return fmt.Errorf("foreign key violation: row %d of %s has no parent in %s", rowid.Int64, table, parent)
}

// backup writes <db>.bak-v<from>-<unix> and drops copies beyond keepBackups.
// VACUUM cannot run inside a transaction, so it goes through the pool while
// the caller's transaction holds the write lock.
func backup(ctx context.Context, db *sql.DB, from int) error {
	var file string
	if err := db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&file); err != nil {
		return fmt.Errorf("locate database file: %w", err)
	}
	if file == "" {
		return errors.New("backup: database has no file")
	}
	dst := fmt.Sprintf("%s.bak-v%d-%d", file, from, time.Now().Unix())
	// VACUUM INTO accepts an empty file, which gives the copy its mode.
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	f.Close()
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		os.Remove(dst)
		return fmt.Errorf("backup before migration: %w", err)
	}
	slog.Info("database backup written before migration", "path", dst, "version", from)
	pruneBackups(file)
	return nil
}

func pruneBackups(dbFile string) {
	dir, base := filepath.Dir(dbFile), filepath.Base(dbFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("list database backups", "err", err)
		return
	}
	type backupFile struct {
		path        string
		at, version int
	}
	var found []backupFile
	for _, e := range entries {
		suffix, ok := strings.CutPrefix(e.Name(), base)
		if !ok {
			continue
		}
		m := backupName.FindStringSubmatch(suffix)
		if m == nil {
			continue
		}
		v, _ := strconv.Atoi(m[1])
		at, _ := strconv.Atoi(m[2])
		found = append(found, backupFile{filepath.Join(dir, e.Name()), at, v})
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].at != found[j].at {
			return found[i].at > found[j].at
		}
		return found[i].version > found[j].version
	})
	for _, b := range found[min(keepBackups, len(found)):] {
		if err := os.Remove(b.path); err != nil {
			slog.Warn("remove old database backup", "path", b.path, "err", err)
		}
	}
}

// hasLegacySchema reports whether a database with user_version 0 already holds
// Runova tables: one created by a build from before migrations existed.
func hasLegacySchema(ctx context.Context, tx *sql.Tx) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'users'`).Scan(&n); err != nil {
		return false, fmt.Errorf("inspect schema: %w", err)
	}
	return n > 0, nil
}

// adoptLegacy is the only compatibility code for databases that predate
// migrations. Those were created by the old CREATE TABLE IF NOT EXISTS schema,
// which equals 0001_init.sql except that the databases feature (the databases
// table, users.pg_password, apps.database_id) was added in place; add what a
// given file lacks and stamp version 1 without running 0001.
func adoptLegacy(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS databases(
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	name TEXT NOT NULL UNIQUE,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS databases_user ON databases(user_id)`); err != nil {
		return fmt.Errorf("create databases table: %w", err)
	}
	for _, c := range []struct{ table, column, ddl string }{
		{"users", "pg_password", "TEXT NOT NULL DEFAULT ''"},
		{"apps", "database_id", "INTEGER NULL REFERENCES databases ON DELETE RESTRICT"},
	} {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.column).Scan(&n); err != nil {
			return fmt.Errorf("inspect %s: %w", c.table, err)
		}
		if n > 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `ALTER TABLE `+c.table+` ADD COLUMN `+c.column+` `+c.ddl); err != nil {
			return fmt.Errorf("add %s.%s: %w", c.table, c.column, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA user_version = 1`); err != nil {
		return fmt.Errorf("set version: %w", err)
	}
	slog.Info("adopted pre-migration database", "version", 1)
	return nil
}

// SchemaVersion reports the schema version stored in the database at path and
// the newest version this binary knows. It never creates or modifies the file;
// a missing file reports version 0.
func SchemaVersion(path string) (current, latest int, err error) {
	ms, err := loadMigrations(embeddedMigrations())
	if err != nil {
		return 0, 0, err
	}
	latest = len(ms)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return 0, latest, nil
	} else if err != nil {
		return 0, 0, fmt.Errorf("stat %s: %w", path, err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?"+url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)"}}.Encode())
	if err != nil {
		return 0, 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close()
	current, err = userVersion(context.Background(), db)
	return current, latest, err
}
