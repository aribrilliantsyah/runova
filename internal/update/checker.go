package update

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Latester is what the Checker needs from a release source.
type Latester interface {
	Latest(ctx context.Context) (string, error)
	ReleaseURL(version string) string
}

// Status is the answer of GET /api/update.
type Status struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseURL      string `json:"release_url"`
	CheckedAt       int64  `json:"checked_at"`
	Enabled         bool   `json:"enabled"`
}

// Checker looks for a newer release on a schedule and keeps the outcome in
// memory. A failed check keeps the last result and is only logged at debug
// level. A Current of "dev" never checks.
type Checker struct {
	Current string
	Source  Latester
	// Enabled reports the update_check setting; nil means always on.
	Enabled func(context.Context) bool
	// Now, After, FirstDelay and Interval default to time.Now, time.After,
	// one minute and six hours.
	Now        func() time.Time
	After      func(time.Duration) <-chan time.Time
	FirstDelay time.Duration
	Interval   time.Duration

	mu        sync.Mutex
	latest    string
	checkedAt time.Time
	poke      chan struct{}
	pokeOnce  sync.Once
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Checker) after(d time.Duration) <-chan time.Time {
	if c.After != nil {
		return c.After(d)
	}
	return time.After(d)
}

func (c *Checker) pokes() chan struct{} {
	c.pokeOnce.Do(func() { c.poke = make(chan struct{}, 1) })
	return c.poke
}

func (c *Checker) enabled(ctx context.Context) bool {
	return c.Enabled == nil || c.Enabled(ctx)
}

// Run checks after FirstDelay and then every Interval, and right away after
// Poke, until ctx ends.
func (c *Checker) Run(ctx context.Context) {
	first, every := c.FirstDelay, c.Interval
	if first <= 0 {
		first = time.Minute
	}
	if every <= 0 {
		every = 6 * time.Hour
	}
	wait := first
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.after(wait):
		case <-c.pokes():
		}
		c.Check(ctx)
		wait = every
	}
}

// Poke asks Run for a check now, for example after the setting is switched on.
func (c *Checker) Poke() {
	select {
	case c.pokes() <- struct{}{}:
	default:
	}
}

// Check looks up the latest release once, unless checking is off.
func (c *Checker) Check(ctx context.Context) {
	if c.Current == "dev" || !c.enabled(ctx) {
		return
	}
	latest, err := c.Source.Latest(ctx)
	if err != nil {
		slog.Debug("update check failed", "err", err)
		return
	}
	c.mu.Lock()
	c.latest, c.checkedAt = latest, c.now()
	c.mu.Unlock()
}

// Status reports the installed version, the latest one seen and whether an
// update is available. With checking off it reports no update.
func (c *Checker) Status(ctx context.Context) Status {
	c.mu.Lock()
	latest, at := c.latest, c.checkedAt
	c.mu.Unlock()
	st := Status{Current: c.Current, Latest: latest, Enabled: c.enabled(ctx)}
	if !at.IsZero() {
		st.CheckedAt = at.UnixMilli()
	}
	if latest == "" {
		return st
	}
	st.ReleaseURL = c.Source.ReleaseURL(latest)
	if cmp, ok := Compare(latest, c.Current); ok && cmp > 0 && st.Enabled {
		st.UpdateAvailable = true
	}
	return st
}
