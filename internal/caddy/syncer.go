package caddy

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// DefaultInterval is how often the Syncer checks that Caddy still runs Runova's configuration.
const DefaultInterval = 30 * time.Second

// Loader is the part of the admin API the Syncer uses.
type Loader interface {
	Load(ctx context.Context, cfg []byte) error
	Config(ctx context.Context) ([]byte, error)
}

// Syncer keeps Caddy's configuration equal to the one built from Runova's
// state. Every change is a full rebuild and load, one at a time.
type Syncer struct {
	Client Loader
	// State reads the current state from the database.
	State func(ctx context.Context) (State, error)
	// Interval between restart checks; 0 means DefaultInterval.
	Interval time.Duration
	// OnError receives errors of the background loop; nil drops them.
	OnError func(error)

	mu sync.Mutex
}

// Apply builds the configuration from the current state and loads it. It
// returns Caddy's refusal to the caller, so a change can be rolled back.
func (s *Syncer) Apply(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apply(ctx)
}

func (s *Syncer) apply(ctx context.Context) error {
	state, err := s.State(ctx)
	if err != nil {
		return fmt.Errorf("read caddy state: %w", err)
	}
	cfg, err := Build(state)
	if err != nil {
		return err
	}
	return s.Client.Load(ctx, cfg)
}

// Run loads the configuration, then every Interval reloads it if Caddy lost
// it (restart, reboot), until ctx ends.
func (s *Syncer) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	s.report(s.Apply(ctx))
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.report(s.reloadIfLost(ctx))
		}
	}
}

func (s *Syncer) report(err error) {
	if err != nil && s.OnError != nil {
		s.OnError(err)
	}
}

func (s *Syncer) reloadIfLost(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.Client.Config(ctx)
	if err != nil {
		return err
	}
	if hasRunovaServer(cfg) {
		return nil
	}
	return s.apply(ctx)
}

// hasRunovaServer reports whether cfg defines the HTTPS server Runova always writes.
func hasRunovaServer(cfg []byte) bool {
	var c struct {
		Apps struct {
			HTTP struct {
				Servers map[string]json.RawMessage `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if json.Unmarshal(cfg, &c) != nil {
		return false
	}
	_, ok := c.Apps.HTTP.Servers[HTTPSServer]
	return ok
}
