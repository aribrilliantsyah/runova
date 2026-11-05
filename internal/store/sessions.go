package store

import (
	"context"
	"fmt"
)

func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID, expiresAt int64) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(token_hash, user_id, expires_at) VALUES(?,?,?)`, tokenHash, userID, expiresAt); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// SessionUser returns the owner of a live session; expired or unknown sessions give ErrNotFound.
func (s *Store) SessionUser(ctx context.Context, tokenHash string, now int64) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+prefixed(userCols, "u.")+`
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, now))
	return u, notFound(err, "session")
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}

func (s *Store) RecordLoginFailure(ctx context.Context, username, ip string, at int64) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO login_failures(username, ip, at) VALUES(?,?,?)`, username, ip, at); err != nil {
		return fmt.Errorf("record login failure: %w", err)
	}
	return nil
}

// LoginFailures counts failures at or after since for (username, ip) and returns the latest one's time (0 if none).
func (s *Store) LoginFailures(ctx context.Context, username, ip string, since int64) (count int, last int64, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT count(*), coalesce(max(at), 0) FROM login_failures WHERE username = ? AND ip = ? AND at >= ?`,
		username, ip, since).Scan(&count, &last)
	if err != nil {
		return 0, 0, fmt.Errorf("count login failures: %w", err)
	}
	return count, last, nil
}

func (s *Store) ClearLoginFailures(ctx context.Context, username, ip string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM login_failures WHERE username = ? AND ip = ?`, username, ip); err != nil {
		return fmt.Errorf("clear login failures: %w", err)
	}
	return nil
}

func (s *Store) PruneLoginFailures(ctx context.Context, before int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM login_failures WHERE at < ?`, before); err != nil {
		return fmt.Errorf("prune login failures: %w", err)
	}
	return nil
}
