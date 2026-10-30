// Package store persists users, sessions, settings and activity in SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrLastAdmin = errors.New("last admin")
)

const schema = `
CREATE TABLE IF NOT EXISTS users(
	id INTEGER PRIMARY KEY,
	username TEXT NOT NULL UNIQUE,
	display_name TEXT NOT NULL DEFAULT '',
	email TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL CHECK(role IN('admin','user')),
	quota_apps INTEGER NOT NULL,
	quota_memory_mb INTEGER NOT NULL,
	quota_cpu_milli INTEGER NOT NULL,
	quota_databases INTEGER NOT NULL,
	quota_pg_connections INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	net_index INTEGER NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS sessions(
	token_hash TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user_id);
CREATE TABLE IF NOT EXISTS settings(
	id INTEGER PRIMARY KEY CHECK(id = 1),
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS login_failures(
	username TEXT NOT NULL,
	ip TEXT NOT NULL,
	at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS login_failures_key ON login_failures(username, ip, at);
CREATE TABLE IF NOT EXISTS activity(
	id INTEGER PRIMARY KEY,
	at INTEGER NOT NULL,
	actor_id INTEGER NULL REFERENCES users ON DELETE SET NULL,
	actor_name TEXT NOT NULL,
	action TEXT NOT NULL,
	target TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT '',
	ip TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS activity_at ON activity(at);
CREATE TABLE IF NOT EXISTS uploads(
	id TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	dir TEXT NOT NULL,
	artifact_path TEXT NOT NULL,
	report TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS uploads_created ON uploads(created_at);
CREATE TABLE IF NOT EXISTS apps(
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	name TEXT NOT NULL,
	kind TEXT NOT NULL,
	java_version INTEGER NULL,
	jvm_opts TEXT NOT NULL DEFAULT '',
	internal_port INTEGER NOT NULL,
	ip_port INTEGER NOT NULL UNIQUE,
	ip_access INTEGER NOT NULL DEFAULT 0,
	memory_mb INTEGER NOT NULL,
	cpu_milli INTEGER NOT NULL,
	status TEXT NOT NULL,
	current_deployment_id INTEGER NULL,
	deploy_mode TEXT NOT NULL DEFAULT 'stopstart',
	created_at INTEGER NOT NULL,
	UNIQUE(user_id, name)
);
CREATE TABLE IF NOT EXISTS app_env(
	app_id INTEGER NOT NULL REFERENCES apps ON DELETE CASCADE,
	name TEXT NOT NULL,
	value TEXT NOT NULL,
	secret INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY(app_id, name)
);
CREATE TABLE IF NOT EXISTS deployments(
	id INTEGER PRIMARY KEY,
	app_id INTEGER NOT NULL REFERENCES apps ON DELETE CASCADE,
	number INTEGER NOT NULL,
	artifact_path TEXT NOT NULL,
	file_name TEXT NOT NULL,
	sha256 TEXT NOT NULL,
	report TEXT NOT NULL,
	env_snapshot TEXT NOT NULL DEFAULT '',
	java_version INTEGER NULL,
	jvm_opts TEXT NOT NULL DEFAULT '',
	internal_port INTEGER NOT NULL DEFAULT 0,
	host_port INTEGER NOT NULL DEFAULT 0,
	container_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	failure_log TEXT NOT NULL DEFAULT '',
	created_by INTEGER NULL REFERENCES users ON DELETE SET NULL,
	created_at INTEGER NOT NULL,
	finished_at INTEGER NOT NULL DEFAULT 0,
	UNIQUE(app_id, number)
);
CREATE TABLE IF NOT EXISTS notifications(
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	at INTEGER NOT NULL,
	level TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	app_id INTEGER NULL,
	read INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS notifications_user ON notifications(user_id, at);
`

type Store struct{ db *sql.DB }

// Open opens (creating with mode 0600 if needed) the database at path. Pragmas
// are part of the DSN so every pooled connection gets foreign keys, WAL and a
// busy timeout; transactions take the write lock up front.
func Open(path string) (*Store, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}
	err = f.Chmod(0o600)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("chmod %s: %w", path, err)
	}
	q := url.Values{
		"_pragma": {"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)"},
		"_txlock": {"immediate"},
	}
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func isUnique(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && (se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || se.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY)
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
