// Package pg runs the PostgreSQL DDL Runova needs to give every user an isolated role and databases.
package pg

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxConns = 4

	pgDuplicateObject = "42710"
)

// ErrInvalid is wrapped by every error caused by an argument that was rejected before reaching PostgreSQL.
var ErrInvalid = errors.New("pg: invalid argument")

type DBInfo struct {
	Name        string
	SizeBytes   int64
	Connections int
}

// Admin is the set of privileged operations Runova performs through its own PostgreSQL role.
type Admin interface {
	Ping(ctx context.Context) error
	// EnsureRole creates the role, or resets password and connection limit if it exists.
	EnsureRole(ctx context.Context, role, password string, connLimit int) error
	SetConnLimit(ctx context.Context, role string, n int) error
	SetPassword(ctx context.Context, role, password string) error
	// DropRole drops every database owned by the role, then the role. A missing role is not an error.
	DropRole(ctx context.Context, role string) error
	CreateDatabase(ctx context.Context, name, owner string) error
	// DropDatabase force-drops the database, disconnecting its sessions. A missing database is not an error.
	DropDatabase(ctx context.Context, name string) error
	Databases(ctx context.Context, owner string) ([]DBInfo, error)
	TerminateConnections(ctx context.Context, db string) error
	ReloadConfig(ctx context.Context) error
}

// PgxAdmin implements Admin over a small pgx connection pool.
type PgxAdmin struct{ pool *pgxpool.Pool }

var _ Admin = (*PgxAdmin)(nil)

// Open prepares the pool. Connections are made lazily; call Ping to check reachability.
func Open(ctx context.Context, dsn string) (*PgxAdmin, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	return &PgxAdmin{pool: pool}, nil
}

func (a *PgxAdmin) Close() { a.pool.Close() }

func (a *PgxAdmin) Ping(ctx context.Context) error {
	if err := a.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	return nil
}

func (a *PgxAdmin) EnsureRole(ctx context.Context, role, password string, connLimit int) error {
	if err := validateRole(role); err != nil {
		return err
	}
	if err := validateRoleSettings(password, connLimit); err != nil {
		return err
	}
	id := ident(role)
	create := roleAttrs(password, connLimit)
	// CREATEROLE cannot alter SUPERUSER, REPLICATION or BYPASSRLS, even to turn them off; those were fixed at creation.
	alter := fmt.Sprintf("CONNECTION LIMIT %d PASSWORD %s", connLimit, quoteLiteral(password))

	var exists bool
	if err := a.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
		return fmt.Errorf("look up role %s: %w", role, err)
	}
	if !exists {
		_, err := a.pool.Exec(ctx, "CREATE ROLE "+id+" "+create)
		var pgErr *pgconn.PgError
		switch {
		case err == nil:
		case errors.As(err, &pgErr) && pgErr.Code == pgDuplicateObject:
			exists = true
		default:
			return fmt.Errorf("create role %s: %w", role, err)
		}
	}
	if exists {
		if _, err := a.pool.Exec(ctx, "ALTER ROLE "+id+" "+alter); err != nil {
			return fmt.Errorf("alter role %s: %w", role, err)
		}
	}
	if _, err := a.pool.Exec(ctx, "GRANT "+id+" TO CURRENT_USER WITH INHERIT TRUE, SET TRUE"); err != nil {
		return fmt.Errorf("grant role %s to admin: %w", role, err)
	}
	return nil
}

func (a *PgxAdmin) SetConnLimit(ctx context.Context, role string, n int) error {
	if err := validateRole(role); err != nil {
		return err
	}
	if n < 0 {
		return fmt.Errorf("%w: negative connection limit", ErrInvalid)
	}
	if _, err := a.pool.Exec(ctx, fmt.Sprintf("ALTER ROLE %s CONNECTION LIMIT %d", ident(role), n)); err != nil {
		return fmt.Errorf("set connection limit of %s: %w", role, err)
	}
	return nil
}

func (a *PgxAdmin) SetPassword(ctx context.Context, role, password string) error {
	if err := validateRole(role); err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	if _, err := a.pool.Exec(ctx, "ALTER ROLE "+ident(role)+" PASSWORD "+quoteLiteral(password)); err != nil {
		return fmt.Errorf("set password of %s: %w", role, err)
	}
	return nil
}

func (a *PgxAdmin) DropRole(ctx context.Context, role string) error {
	if err := validateRole(role); err != nil {
		return err
	}
	rows, err := a.pool.Query(ctx, `SELECT d.datname FROM pg_database d JOIN pg_roles r ON r.oid = d.datdba WHERE r.rolname = $1`, role)
	if err != nil {
		return fmt.Errorf("list databases of %s: %w", role, err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("list databases of %s: %w", role, err)
	}
	for _, name := range names {
		if err := a.DropDatabase(ctx, name); err != nil {
			return err
		}
	}
	// Sessions on shared databases such as "postgres" would otherwise outlive the role.
	if _, err := a.pool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1`, role); err != nil {
		return fmt.Errorf("terminate sessions of %s: %w", role, err)
	}
	if _, err := a.pool.Exec(ctx, "DROP ROLE IF EXISTS "+ident(role)); err != nil {
		return fmt.Errorf("drop role %s: %w", role, err)
	}
	return nil
}

func (a *PgxAdmin) CreateDatabase(ctx context.Context, name, owner string) error {
	if err := validateRole(owner); err != nil {
		return err
	}
	dbOwner, _, err := splitDBName(name)
	if err != nil {
		return err
	}
	if dbOwner != owner {
		return fmt.Errorf("%w: database %q does not belong to %q", ErrInvalid, name, owner)
	}
	id := ident(name)
	if _, err := a.pool.Exec(ctx, "CREATE DATABASE "+id+" OWNER "+ident(owner)); err != nil {
		return fmt.Errorf("create database %s: %w", name, err)
	}
	if _, err := a.pool.Exec(ctx, "REVOKE ALL ON DATABASE "+id+" FROM PUBLIC"); err != nil {
		// A database left open to PUBLIC would break isolation; do not keep it.
		_, dropErr := a.pool.Exec(ctx, "DROP DATABASE IF EXISTS "+id+" WITH (FORCE)")
		return errors.Join(fmt.Errorf("revoke public access on %s: %w", name, err), dropErr)
	}
	return nil
}

func (a *PgxAdmin) DropDatabase(ctx context.Context, name string) error {
	if _, _, err := splitDBName(name); err != nil {
		return err
	}
	if _, err := a.pool.Exec(ctx, "DROP DATABASE IF EXISTS "+ident(name)+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("drop database %s: %w", name, err)
	}
	return nil
}

func (a *PgxAdmin) Databases(ctx context.Context, owner string) ([]DBInfo, error) {
	if err := validateRole(owner); err != nil {
		return nil, err
	}
	rows, err := a.pool.Query(ctx, `
SELECT d.datname,
       pg_database_size(d.oid),
       (SELECT count(*) FROM pg_stat_activity a WHERE a.datid = d.oid)
FROM pg_database d
JOIN pg_roles r ON r.oid = d.datdba
WHERE r.rolname = $1
ORDER BY d.datname`, owner)
	if err != nil {
		return nil, fmt.Errorf("list databases of %s: %w", owner, err)
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (DBInfo, error) {
		var i DBInfo
		err := row.Scan(&i.Name, &i.SizeBytes, &i.Connections)
		return i, err
	})
	if err != nil {
		return nil, fmt.Errorf("list databases of %s: %w", owner, err)
	}
	if list == nil {
		list = []DBInfo{}
	}
	return list, nil
}

func (a *PgxAdmin) TerminateConnections(ctx context.Context, db string) error {
	if _, _, err := splitDBName(db); err != nil {
		return err
	}
	if _, err := a.pool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, db); err != nil {
		return fmt.Errorf("terminate connections to %s: %w", db, err)
	}
	return nil
}

func (a *PgxAdmin) ReloadConfig(ctx context.Context) error {
	if _, err := a.pool.Exec(ctx, `SELECT pg_reload_conf()`); err != nil {
		return fmt.Errorf("reload postgres config: %w", err)
	}
	return nil
}

func ident(name string) string { return pgx.Identifier{name}.Sanitize() }

// quoteLiteral renders s as an escape-string literal, independent of standard_conforming_strings.
func quoteLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `''`)
	return "E'" + s + "'"
}

func roleAttrs(password string, connLimit int) string {
	return fmt.Sprintf("LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT %d PASSWORD %s",
		connLimit, quoteLiteral(password))
}

func validateRoleSettings(password string, connLimit int) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	if connLimit < 0 {
		return fmt.Errorf("%w: negative connection limit", ErrInvalid)
	}
	return nil
}

func validatePassword(p string) error {
	if p == "" {
		return fmt.Errorf("%w: empty password", ErrInvalid)
	}
	if strings.ContainsRune(p, 0) {
		return fmt.Errorf("%w: password contains NUL", ErrInvalid)
	}
	return nil
}
