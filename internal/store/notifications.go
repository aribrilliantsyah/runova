package store

import (
	"context"
	"fmt"
)

type NotificationLevel string

const (
	LevelInfo    NotificationLevel = "info"
	LevelWarning NotificationLevel = "warning"
	LevelError   NotificationLevel = "error"
)

// Notification is an entry in a user's bell menu.
type Notification struct {
	ID     int64             `json:"id"`
	UserID int64             `json:"user_id"`
	At     int64             `json:"at"`
	Level  NotificationLevel `json:"level"`
	Title  string            `json:"title"`
	Body   string            `json:"body"`
	AppID  *int64            `json:"app_id"`
	Read   bool              `json:"read"`
}

func (s *Store) Notify(ctx context.Context, n Notification) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO notifications(user_id, at, level, title, body, app_id, read) VALUES(?,?,?,?,?,?,0)`,
		n.UserID, n.At, n.Level, n.Title, n.Body, n.AppID); err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	return nil
}

// Notifications returns the user's newest notifications first; limit is clamped to 1–200 (0 means 50).
func (s *Store) Notifications(ctx context.Context, userID int64, limit int) ([]Notification, error) {
	switch {
	case limit == 0:
		limit = 50
	case limit < 1:
		limit = 1
	case limit > 200:
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, at, level, title, body, app_id, read FROM notifications
		 WHERE user_id = ? ORDER BY at DESC, id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.At, &n.Level, &n.Title, &n.Body, &n.AppID, &n.Read); err != nil {
			return nil, fmt.Errorf("list notifications: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	return out, nil
}

func (s *Store) MarkNotificationsRead(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE notifications SET read = 1 WHERE user_id = ? AND read = 0`, userID); err != nil {
		return fmt.Errorf("mark notifications read: %w", err)
	}
	return nil
}

func (s *Store) UnreadCount(ctx context.Context, userID int64) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM notifications WHERE user_id = ? AND read = 0`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return n, nil
}
