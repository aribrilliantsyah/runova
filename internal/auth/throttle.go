package auth

import (
	"context"
	"time"

	"runova/internal/store"
)

const (
	MaxFailures = 5
	Window      = 15 * time.Minute
	Lockout     = 15 * time.Minute
)

// Throttle limits failed logins per (username, IP).
type Throttle struct {
	Store *store.Store
	Now   func() time.Time
}

// Locked reports whether the pair is locked out and until when.
func (t *Throttle) Locked(ctx context.Context, username, ip string) (until time.Time, locked bool, err error) {
	now := t.Now()
	count, last, err := t.Store.LoginFailures(ctx, username, ip, now.Add(-Window).UnixMilli())
	if err != nil || count < MaxFailures {
		return time.Time{}, false, err
	}
	until = time.UnixMilli(last).Add(Lockout)
	return until, now.Before(until), nil
}

func (t *Throttle) Fail(ctx context.Context, username, ip string) error {
	return t.Store.RecordLoginFailure(ctx, username, ip, t.Now().UnixMilli())
}

func (t *Throttle) Reset(ctx context.Context, username, ip string) error {
	return t.Store.ClearLoginFailures(ctx, username, ip)
}
