package store

import (
	"context"
	"fmt"
)

const (
	ActLogin          = "login"
	ActLoginFailed    = "login_failed"
	ActLogout         = "logout"
	ActUserCreate     = "user.create"
	ActUserUpdate     = "user.update"
	ActUserDelete     = "user.delete"
	ActQuotaUpdate    = "quota.update"
	ActPasswordChange = "password.change"
	ActSettingsUpdate = "settings.update"
	ActUpload         = "upload"
	ActAppCreate      = "app.create"
	ActAppUpdate      = "app.update"
	ActAppDelete      = "app.delete"
	ActDeploy         = "deploy"
	ActRollback       = "rollback"
	ActRestart        = "restart"
	ActStop           = "stop"
	ActStart          = "start"
	ActTerminal       = "terminal.open"
	ActEnvUpdate      = "env.update"
	ActEnvReveal      = "env.reveal"
	ActDomainAdd      = "domain.add"
	ActDomainRemove   = "domain.remove"
	ActPanelAddress   = "panel.address"
)

type Activity struct {
	ID        int64  `json:"id"`
	At        int64  `json:"at"`
	ActorID   *int64 `json:"actor_id"`
	ActorName string `json:"actor_name"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
	IP        string `json:"ip"`
}

type ActivityFilter struct {
	ActorID *int64
	Before  int64
	Limit   int
}

func (s *Store) Log(ctx context.Context, a Activity) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO activity(at, actor_id, actor_name, action, target, detail, ip) VALUES(?,?,?,?,?,?,?)`,
		a.At, a.ActorID, a.ActorName, a.Action, a.Target, a.Detail, a.IP); err != nil {
		return fmt.Errorf("log activity: %w", err)
	}
	return nil
}

// ActivityList returns entries newest first. Before 0 means no upper bound;
// Limit is clamped to 1–200 (0 means 50).
func (s *Store) ActivityList(ctx context.Context, f ActivityFilter) ([]Activity, error) {
	limit := f.Limit
	switch {
	case limit == 0:
		limit = 50
	case limit < 1:
		limit = 1
	case limit > 200:
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, at, actor_id, actor_name, action, target, detail, ip FROM activity
		 WHERE (?1 IS NULL OR actor_id = ?1) AND (?2 = 0 OR at < ?2)
		 ORDER BY at DESC, id DESC LIMIT ?3`, f.ActorID, f.Before, limit)
	if err != nil {
		return nil, fmt.Errorf("list activity: %w", err)
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.At, &a.ActorID, &a.ActorName, &a.Action, &a.Target, &a.Detail, &a.IP); err != nil {
			return nil, fmt.Errorf("list activity: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list activity: %w", err)
	}
	return out, nil
}
