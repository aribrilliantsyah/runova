package api

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"runova/internal/docker"
	"runova/internal/store"
)

// statsTTL is how long one app's resource use is reused: the panel polls
// every few seconds for every open page, and each Docker stats call can take
// up to statsWait.
const statsTTL = 3 * time.Second

type cachedStats struct {
	stats   docker.Stats
	expires time.Time
}

// statsCache remembers the last answer for each running app. Failures are not
// cached, so a flapping Docker is asked again on the next request.
type statsCache struct {
	mu      sync.Mutex
	entries map[int64]cachedStats
}

func (c *statsCache) get(appID int64, now time.Time) (docker.Stats, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, found := c.entries[appID]
	if !found || !now.Before(e.expires) {
		return docker.Stats{}, false
	}
	return e.stats, true
}

func (c *statsCache) put(appID int64, st docker.Stats, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[int64]cachedStats{}
	}
	for id, e := range c.entries {
		if !now.Before(e.expires) {
			delete(c.entries, id)
		}
	}
	c.entries[appID] = cachedStats{stats: st, expires: now.Add(statsTTL)}
}

// appStats returns the app's resource use for GET /api/apps/{id}: nil when the
// app is not running or Docker does not answer in time. The caller has
// already checked that u may see the app, so a cached answer is safe to share.
func (s *server) appStats(ctx context.Context, u store.User, app store.App) *docker.Stats {
	if app.Status != store.AppRunning {
		return nil
	}
	now := s.now()
	if st, ok := s.stats.get(app.ID, now); ok {
		return &st
	}
	sctx, cancel := context.WithTimeout(ctx, statsWait)
	defer cancel()
	st, running, err := s.apps.Stats(sctx, u, app.ID)
	if err != nil {
		slog.Warn("app stats", "app", app.Name, "err", err)
		return nil
	}
	if !running {
		return nil
	}
	s.stats.put(app.ID, st, now)
	return &st
}
