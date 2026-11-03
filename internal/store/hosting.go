package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// DNSStatus is the outcome of the last DNS check of a hosting domain.
type DNSStatus string

const (
	DNSUnchecked DNSStatus = "unchecked"
	DNSOK        DNSStatus = "ok"
	DNSError     DNSStatus = "error"
)

// ErrHostingUnavailable means the hosting domain does not exist or is disabled.
var ErrHostingUnavailable = errors.New("hosting domain unavailable")

// ErrSubdomainQuota matches a SubdomainQuotaError.
var ErrSubdomainQuota = errors.New("subdomain quota exceeded")

// SubdomainQuotaError reports a user who already has Used of Limit subdomains.
type SubdomainQuotaError struct{ Used, Limit int }

func (e *SubdomainQuotaError) Error() string {
	return fmt.Sprintf("subdomain quota exceeded (%d of %d)", e.Used, e.Limit)
}
func (e *SubdomainQuotaError) Is(target error) bool { return target == ErrSubdomainQuota }

// HostingInUseError reports a hosting domain that N subdomains still use.
type HostingInUseError struct{ N int }

func (e *HostingInUseError) Error() string {
	return fmt.Sprintf("hosting domain in use by %d subdomains", e.N)
}
func (e *HostingInUseError) Is(target error) bool { return target == ErrConflict }

// HostingDomain is a base domain the admin registered: users may create
// "<label>.<Name>" for their apps.
type HostingDomain struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Enabled      bool      `json:"enabled"`
	Reserved     string    `json:"reserved"`
	DNSStatus    DNSStatus `json:"dns_status"`
	DNSDetail    string    `json:"dns_detail"`
	DNSCheckedAt int64     `json:"dns_checked_at"`
	CreatedAt    int64     `json:"created_at"`
}

const hostingCols = `id, name, enabled, reserved, dns_status, dns_detail, dns_checked_at, created_at`

func scanHosting(r scanner) (HostingDomain, error) {
	var h HostingDomain
	err := r.Scan(&h.ID, &h.Name, &h.Enabled, &h.Reserved, &h.DNSStatus, &h.DNSDetail, &h.DNSCheckedAt, &h.CreatedAt)
	return h, err
}

// AddHostingDomain registers name (already normalized); ErrConflict when it exists.
func (s *Store) AddHostingDomain(ctx context.Context, name, reserved string, now int64) (HostingDomain, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO hosting_domains(name, reserved, created_at) VALUES(?,?,?)`, name, reserved, now)
	if isUnique(err) {
		return HostingDomain{}, ErrConflict
	}
	if err != nil {
		return HostingDomain{}, fmt.Errorf("add hosting domain: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return HostingDomain{}, err
	}
	return s.HostingDomain(ctx, id)
}

// HostingDomain returns one registered domain; ErrNotFound when absent.
func (s *Store) HostingDomain(ctx context.Context, id int64) (HostingDomain, error) {
	h, err := scanHosting(s.db.QueryRowContext(ctx, `SELECT `+hostingCols+` FROM hosting_domains WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return HostingDomain{}, ErrNotFound
	}
	if err != nil {
		return HostingDomain{}, fmt.Errorf("hosting domain: %w", err)
	}
	return h, nil
}

// HostingDomains lists every registered domain sorted by name.
func (s *Store) HostingDomains(ctx context.Context) ([]HostingDomain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+hostingCols+` FROM hosting_domains ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list hosting domains: %w", err)
	}
	defer rows.Close()
	out := []HostingDomain{}
	for rows.Next() {
		h, err := scanHosting(rows)
		if err != nil {
			return nil, fmt.Errorf("list hosting domains: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// UpdateHostingDomain changes the fields that are not nil.
func (s *Store) UpdateHostingDomain(ctx context.Context, id int64, enabled *bool, reserved *string) (HostingDomain, error) {
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if enabled != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE hosting_domains SET enabled = ? WHERE id = ?`, *enabled, id); err != nil {
				return fmt.Errorf("update hosting domain: %w", err)
			}
		}
		if reserved != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE hosting_domains SET reserved = ? WHERE id = ?`, *reserved, id); err != nil {
				return fmt.Errorf("update hosting domain: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return HostingDomain{}, err
	}
	return s.HostingDomain(ctx, id)
}

// SetHostingDNS records the result of a DNS check.
func (s *Store) SetHostingDNS(ctx context.Context, id int64, status DNSStatus, detail string, now int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE hosting_domains SET dns_status = ?, dns_detail = ?, dns_checked_at = ? WHERE id = ?`, status, detail, now, id)
	if err != nil {
		return fmt.Errorf("set hosting dns: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveHostingDomain deletes the registration; a *HostingInUseError (an
// ErrConflict) while subdomains still use it.
func (s *Store) RemoveHostingDomain(ctx context.Context, id int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM domains WHERE hosting_domain_id = ?`, id).Scan(&n); err != nil {
			return fmt.Errorf("count subdomains: %w", err)
		}
		if n > 0 {
			return &HostingInUseError{N: n}
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM hosting_domains WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("remove hosting domain: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// AddHostedDomain stores host ("<label>.<hosting name>") for the app as pending.
// In one transaction it enforces the per-app limit (ErrDomainLimit), that the
// hosting domain exists and is enabled (ErrHostingUnavailable), that the app's
// owner has fewer than quota subdomains (*SubdomainQuotaError) and that the
// host is free (ErrConflict).
func (s *Store) AddHostedDomain(ctx context.Context, appID, hostingID int64, label, host string, quota int, now int64) (Domain, error) {
	d := Domain{AppID: appID, Host: host, Status: DomainPending, CreatedAt: now, HostingDomainID: &hostingID, Label: label}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var enabled bool
		err := tx.QueryRowContext(ctx, `SELECT enabled FROM hosting_domains WHERE id = ?`, hostingID).Scan(&enabled)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !enabled {
			return ErrHostingUnavailable
		}
		if err != nil {
			return fmt.Errorf("hosting domain: %w", err)
		}
		var used int
		err = tx.QueryRowContext(ctx,
			`SELECT count(*) FROM domains d JOIN apps a ON a.id = d.app_id
			 WHERE d.hosting_domain_id IS NOT NULL AND a.user_id = (SELECT user_id FROM apps WHERE id = ?)`, appID).Scan(&used)
		if err != nil {
			return fmt.Errorf("count subdomains: %w", err)
		}
		if used >= quota {
			return &SubdomainQuotaError{Used: used, Limit: quota}
		}
		d.ID, err = insertDomain(ctx, tx, d)
		return err
	})
	return d, err
}

// SubdomainCount is how many hosting subdomains the user's apps have.
func (s *Store) SubdomainCount(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM domains d JOIN apps a ON a.id = d.app_id
		 WHERE d.hosting_domain_id IS NOT NULL AND a.user_id = ?`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count subdomains: %w", err)
	}
	return n, nil
}

// HostingUsage counts the subdomains of each hosting domain, in total and
// those created at or after since.
func (s *Store) HostingUsage(ctx context.Context, since int64) (used, recent map[int64]int, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT hosting_domain_id, count(*), coalesce(sum(created_at >= ?), 0) FROM domains
		 WHERE hosting_domain_id IS NOT NULL GROUP BY hosting_domain_id`, since)
	if err != nil {
		return nil, nil, fmt.Errorf("hosting usage: %w", err)
	}
	defer rows.Close()
	used, recent = map[int64]int{}, map[int64]int{}
	for rows.Next() {
		var id int64
		var u, r int
		if err := rows.Scan(&id, &u, &r); err != nil {
			return nil, nil, fmt.Errorf("hosting usage: %w", err)
		}
		used[id], recent[id] = u, r
	}
	return used, recent, rows.Err()
}
