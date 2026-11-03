package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type DomainStatus string

const (
	DomainPending DomainStatus = "pending"
	DomainActive  DomainStatus = "active"
	DomainError   DomainStatus = "error"
)

// MaxDomainsPerApp is how many domains one app may have.
const MaxDomainsPerApp = 5

// ErrDomainLimit means the app already has MaxDomainsPerApp domains.
var ErrDomainLimit = errors.New("domain limit reached")

// Domain is a host name routed to an app. Status follows the HTTPS
// certificate: pending until Caddy serves one, then active, or error with
// the reason in Detail.
type Domain struct {
	ID        int64        `json:"id"`
	AppID     int64        `json:"app_id"`
	Host      string       `json:"host"`
	Status    DomainStatus `json:"status"`
	Detail    string       `json:"detail"`
	CheckedAt int64        `json:"checked_at"`
	CreatedAt int64        `json:"created_at"`
	// HostingDomainID and Label are set for a subdomain of a registered hosting
	// domain (Host = Label + "." + its name); nil and empty for a free domain.
	HostingDomainID *int64 `json:"hosting_domain_id"`
	Label           string `json:"label"`
}

const domainCols = `id, app_id, host, status, detail, checked_at, created_at, hosting_domain_id, label`

func scanDomain(r scanner) (Domain, error) {
	var d Domain
	err := r.Scan(&d.ID, &d.AppID, &d.Host, &d.Status, &d.Detail, &d.CheckedAt, &d.CreatedAt, &d.HostingDomainID, &d.Label)
	return d, err
}

// insertDomain adds d inside tx after checking the per-app limit and returns its ID.
func insertDomain(ctx context.Context, tx *sql.Tx, d Domain) (int64, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM domains WHERE app_id = ?`, d.AppID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count domains: %w", err)
	}
	if n >= MaxDomainsPerApp {
		return 0, ErrDomainLimit
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO domains(app_id, host, status, detail, checked_at, created_at, hosting_domain_id, label) VALUES(?,?,?,?,?,?,?,?)`,
		d.AppID, d.Host, d.Status, "", 0, d.CreatedAt, d.HostingDomainID, d.Label)
	if isUnique(err) {
		return 0, ErrConflict
	}
	if err != nil {
		return 0, fmt.Errorf("add domain: %w", err)
	}
	return res.LastInsertId()
}

// AddDomain stores host (already normalized) for the app as pending. It
// returns ErrConflict when any app uses the host and ErrDomainLimit when the
// app has MaxDomainsPerApp already.
func (s *Store) AddDomain(ctx context.Context, appID int64, host string, now int64) (Domain, error) {
	d := Domain{AppID: appID, Host: host, Status: DomainPending, CreatedAt: now}
	err := s.withTx(ctx, func(tx *sql.Tx) (err error) {
		d.ID, err = insertDomain(ctx, tx, d)
		return err
	})
	return d, err
}

func (s *Store) queryDomains(ctx context.Context, query string, args ...any) ([]Domain, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("list domains: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	return out, nil
}

// Domains lists the app's domains sorted by host.
func (s *Store) Domains(ctx context.Context, appID int64) ([]Domain, error) {
	return s.queryDomains(ctx, `SELECT `+domainCols+` FROM domains WHERE app_id = ? ORDER BY host`, appID)
}

// AllDomains lists every domain sorted by host.
func (s *Store) AllDomains(ctx context.Context) ([]Domain, error) {
	return s.queryDomains(ctx, `SELECT `+domainCols+` FROM domains ORDER BY host`)
}

// HostedDomains lists the subdomains of one hosting domain sorted by host.
func (s *Store) HostedDomains(ctx context.Context, hostingID int64) ([]Domain, error) {
	return s.queryDomains(ctx, `SELECT `+domainCols+` FROM domains WHERE hosting_domain_id = ? ORDER BY host`, hostingID)
}

// DomainHostTaken reports whether any app uses host.
func (s *Store) DomainHostTaken(ctx context.Context, host string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM domains WHERE host = ?`, host).Scan(&n); err != nil {
		return false, fmt.Errorf("domain lookup: %w", err)
	}
	return n > 0, nil
}

// RemoveDomain deletes the domain if it belongs to the app; ErrNotFound otherwise.
func (s *Store) RemoveDomain(ctx context.Context, appID, domainID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM domains WHERE id = ? AND app_id = ?`, domainID, appID)
	if err != nil {
		return fmt.Errorf("remove domain: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDomainStatus records the result of a certificate check.
func (s *Store) SetDomainStatus(ctx context.Context, id int64, status DomainStatus, detail string, now int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE domains SET status = ?, detail = ?, checked_at = ? WHERE id = ?`, status, detail, now, id)
	if err != nil {
		return fmt.Errorf("set domain status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
