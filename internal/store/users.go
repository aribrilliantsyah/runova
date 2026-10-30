package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"unicode/utf8"
)

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

type Quota struct {
	Apps          int `json:"apps"`
	MemoryMB      int `json:"memory_mb"`
	CPUMilli      int `json:"cpu_milli"`
	Databases     int `json:"databases"`
	PGConnections int `json:"pg_connections"`
}

func (q Quota) Validate() error {
	for _, f := range []struct {
		name        string
		v, min, max int
	}{
		{"Jumlah aplikasi", q.Apps, 0, 100},
		{"Memori", q.MemoryMB, 0, 1048576},
		{"CPU", q.CPUMilli, 0, 256000},
		{"Jumlah database", q.Databases, 0, 100},
		{"Koneksi PostgreSQL", q.PGConnections, 1, 500},
	} {
		if f.v < f.min || f.v > f.max {
			return fmt.Errorf("%s harus antara %d dan %d", f.name, f.min, f.max)
		}
	}
	return nil
}

type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Role        Role   `json:"role"`
	Quota       Quota  `json:"quota"`
	CreatedAt   int64  `json:"created_at"`
}

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

var (
	usernameRE = regexp.MustCompile(`^[a-z][a-z0-9]{1,30}$`)
	reserved   = []string{"postgres", "runova", "root", "public", "template"}
)

func ValidateUsername(s string) error {
	if !usernameRE.MatchString(s) {
		return errors.New("Username harus 2–31 karakter: huruf kecil dan angka, diawali huruf")
	}
	if slices.Contains(reserved, s) {
		return errors.New("Username ini dicadangkan")
	}
	return nil
}

func ValidatePassword(s string) error {
	if n := utf8.RuneCountInString(s); n < 10 || n > 200 {
		return errors.New("Password harus 10–200 karakter")
	}
	return nil
}

type NewUser struct {
	Username, DisplayName, Email, PasswordHash string
	Role                                       Role
	Quota                                      Quota
}

const userCols = `id, username, display_name, email, role, quota_apps, quota_memory_mb, quota_cpu_milli, quota_databases, quota_pg_connections, created_at`

type scanner interface{ Scan(dest ...any) error }

func scanUser(r scanner, extra ...any) (User, error) {
	var u User
	dest := []any{&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.Role,
		&u.Quota.Apps, &u.Quota.MemoryMB, &u.Quota.CPUMilli, &u.Quota.Databases, &u.Quota.PGConnections, &u.CreatedAt}
	if err := r.Scan(append(dest, extra...)...); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) CreateUser(ctx context.Context, n NewUser, now int64) (User, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users(username, display_name, email, password_hash, role, quota_apps, quota_memory_mb, quota_cpu_milli, quota_databases, quota_pg_connections, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		n.Username, n.DisplayName, n.Email, n.PasswordHash, n.Role,
		n.Quota.Apps, n.Quota.MemoryMB, n.Quota.CPUMilli, n.Quota.Databases, n.Quota.PGConnections, now)
	if isUnique(err) {
		return User{}, ErrConflict
	}
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return User{ID: id, Username: n.Username, DisplayName: n.DisplayName, Email: n.Email, Role: n.Role, Quota: n.Quota, CreatedAt: now}, nil
}

func (s *Store) User(ctx context.Context, id int64) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	return u, notFound(err, "user")
}

// UserByUsername returns the user and its password hash.
func (s *Store) UserByUsername(ctx context.Context, username string) (User, string, error) {
	var hash string
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+`, password_hash FROM users WHERE username = ?`, username), &hash)
	return u, hash, notFound(err, "user by username")
}

func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return out, nil
}

func notFound(err error, what string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	default:
		return fmt.Errorf("query %s: %w", what, err)
	}
}

type UserPatch struct {
	DisplayName, Email *string
	Role               *Role
	Quota              *Quota
}

func (s *Store) UpdateUser(ctx context.Context, id int64, p UserPatch) (User, error) {
	var out User
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
		if err := notFound(err, "user"); err != nil {
			return err
		}
		if p.DisplayName != nil {
			u.DisplayName = *p.DisplayName
		}
		if p.Email != nil {
			u.Email = *p.Email
		}
		if p.Quota != nil {
			u.Quota = *p.Quota
		}
		if p.Role != nil && *p.Role != u.Role {
			if u.Role == RoleAdmin {
				if err := requireOtherAdmin(ctx, tx); err != nil {
					return err
				}
			}
			u.Role = *p.Role
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE users SET display_name=?, email=?, role=?, quota_apps=?, quota_memory_mb=?, quota_cpu_milli=?, quota_databases=?, quota_pg_connections=? WHERE id=?`,
			u.DisplayName, u.Email, u.Role, u.Quota.Apps, u.Quota.MemoryMB, u.Quota.CPUMilli, u.Quota.Databases, u.Quota.PGConnections, id)
		if err != nil {
			return fmt.Errorf("update user: %w", err)
		}
		out = u
		return nil
	})
	return out, err
}

// requireOtherAdmin fails with ErrLastAdmin when exactly one admin exists, i.e.
// the admin about to be demoted or deleted is the last one.
func requireOtherAdmin(ctx context.Context, tx *sql.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&n); err != nil {
		return fmt.Errorf("count admins: %w", err)
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}

// SetPassword replaces the password hash and ends all of the user's sessions.
func (s *Store) SetPassword(ctx context.Context, id int64, hash string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
		if err != nil {
			return fmt.Errorf("set password: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
			return fmt.Errorf("delete sessions: %w", err)
		}
		return nil
	})
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var role Role
		err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, id).Scan(&role)
		if err := notFound(err, "user"); err != nil {
			return err
		}
		if role == RoleAdmin {
			if err := requireOtherAdmin(ctx, tx); err != nil {
				return err
			}
		}
		// Apps go first: they reference the user's databases with ON DELETE RESTRICT.
		if _, err := tx.ExecContext(ctx, `DELETE FROM apps WHERE user_id = ?`, id); err != nil {
			return fmt.Errorf("delete user apps: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		return nil
	})
}
