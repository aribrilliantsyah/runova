package store

import (
	"context"
	"errors"
	"fmt"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func isForeignKey(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	// ON DELETE RESTRICT reports SQLITE_CONSTRAINT_TRIGGER, plain violations SQLITE_CONSTRAINT_FOREIGNKEY.
	return se.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY || se.Code() == sqlite3.SQLITE_CONSTRAINT_TRIGGER
}

// Database is a PostgreSQL database created for a user. Name is the full
// PostgreSQL name, "<username>_<suffix>".
type Database struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

const databaseCols = `id, user_id, name, created_at`

func scanDatabase(r scanner) (Database, error) {
	var d Database
	err := r.Scan(&d.ID, &d.UserID, &d.Name, &d.CreatedAt)
	return d, err
}

// CreateDatabase inserts d; ErrConflict when the name is taken.
func (s *Store) CreateDatabase(ctx context.Context, d Database) (Database, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO databases(user_id, name, created_at) VALUES(?,?,?)`, d.UserID, d.Name, d.CreatedAt)
	if isUnique(err) {
		return Database{}, ErrConflict
	}
	if err != nil {
		return Database{}, fmt.Errorf("create database: %w", err)
	}
	if d.ID, err = res.LastInsertId(); err != nil {
		return Database{}, fmt.Errorf("create database: %w", err)
	}
	return d, nil
}

func (s *Store) Database(ctx context.Context, id int64) (Database, error) {
	d, err := scanDatabase(s.db.QueryRowContext(ctx, `SELECT `+databaseCols+` FROM databases WHERE id = ?`, id))
	return d, notFound(err, "database")
}

func (s *Store) queryDatabases(ctx context.Context, query string, args ...any) ([]Database, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		d, err := scanDatabase(rows)
		if err != nil {
			return nil, fmt.Errorf("list databases: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	return out, nil
}

func (s *Store) DatabasesByUser(ctx context.Context, userID int64) ([]Database, error) {
	return s.queryDatabases(ctx, `SELECT `+databaseCols+` FROM databases WHERE user_id = ? ORDER BY name`, userID)
}

func (s *Store) AllDatabases(ctx context.Context) ([]Database, error) {
	return s.queryDatabases(ctx, `SELECT `+databaseCols+` FROM databases ORDER BY user_id, name`)
}

// DeleteDatabase removes the row; ErrConflict when an app still uses it.
func (s *Store) DeleteDatabase(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM databases WHERE id = ?`, id)
	if isForeignKey(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("delete database: %w", err)
	}
	return nil
}

// DBApp is an app bound to a database.
type DBApp struct {
	DatabaseID int64
	AppID      int64
	AppName    string
}

// DatabaseApps lists the apps bound to any database of the user, ordered by app name.
func (s *Store) DatabaseApps(ctx context.Context, userID int64) ([]DBApp, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.database_id, a.id, a.name FROM apps a JOIN databases d ON d.id = a.database_id
		 WHERE d.user_id = ? ORDER BY a.name`, userID)
	if err != nil {
		return nil, fmt.Errorf("list database apps: %w", err)
	}
	defer rows.Close()
	out := []DBApp{}
	for rows.Next() {
		var a DBApp
		if err := rows.Scan(&a.DatabaseID, &a.AppID, &a.AppName); err != nil {
			return nil, fmt.Errorf("list database apps: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list database apps: %w", err)
	}
	return out, nil
}

// AppDatabaseID returns the database the app is bound to, nil when none.
func (s *Store) AppDatabaseID(ctx context.Context, appID int64) (*int64, error) {
	var id *int64
	err := s.db.QueryRowContext(ctx, `SELECT database_id FROM apps WHERE id = ?`, appID).Scan(&id)
	return id, notFound(err, "app")
}

// SetAppDatabase binds the app to a database, or detaches it when id is nil.
func (s *Store) SetAppDatabase(ctx context.Context, appID int64, id *int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE apps SET database_id = ? WHERE id = ?`, id, appID)
	if err != nil {
		return fmt.Errorf("set app database: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PGPassword returns the user's sealed PostgreSQL role password; empty when
// the role has not been created yet.
func (s *Store) PGPassword(ctx context.Context, userID int64) (string, error) {
	var sealed string
	err := s.db.QueryRowContext(ctx, `SELECT pg_password FROM users WHERE id = ?`, userID).Scan(&sealed)
	return sealed, notFound(err, "user")
}

func (s *Store) SetPGPassword(ctx context.Context, userID int64, sealed string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET pg_password = ? WHERE id = ?`, sealed, userID)
	if err != nil {
		return fmt.Errorf("set pg password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
