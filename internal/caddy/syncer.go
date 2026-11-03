package caddy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
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
	// PortFree reports why a TCP port cannot be listened on, nil if it can; nil means the PortFree function.
	PortFree func(port int) error

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
	if err := s.checkPorts(ctx, state); err != nil {
		return err
	}
	return s.Client.Load(ctx, cfg)
}

// checkPorts refuses a state that needs a port some other process holds,
// before Caddy is asked to load it. Caddy 2.6 (Ubuntu 26.04) keeps the
// listeners of the old configuration when a load fails while starting, and
// later loads no longer close them, so a refused load must never reach it.
// Ports Caddy holds itself are not checked: its running configuration owns them.
func (s *Syncer) checkPorts(ctx context.Context, state State) error {
	ports := make([]int, 0, len(state.IPListeners)+1)
	if state.PanelIPPort != 0 {
		ports = append(ports, state.PanelIPPort)
	}
	for _, l := range state.IPListeners {
		ports = append(ports, l.Port)
	}
	if len(ports) == 0 {
		return nil
	}
	running, err := s.Client.Config(ctx)
	if err != nil {
		return err
	}
	held := listenPorts(running)
	free := s.PortFree
	if free == nil {
		free = PortFree
	}
	slices.Sort(ports)
	for _, port := range ports {
		if held[port] {
			continue
		}
		if err := free(port); err != nil {
			return fmt.Errorf("port %d sudah dipakai proses lain: %w", port, err)
		}
	}
	return nil
}

// PortFree reports whether port can be listened on the way Caddy does, on every address.
func PortFree(port int) error {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return err
	}
	return ln.Close()
}

// listenPorts returns the ports of every ":port" address that servers in cfg listen on.
func listenPorts(cfg []byte) map[int]bool {
	var c struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Listen []string `json:"listen"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	ports := map[int]bool{}
	if json.Unmarshal(cfg, &c) != nil {
		return ports
	}
	for _, srv := range c.Apps.HTTP.Servers {
		for _, addr := range srv.Listen {
			if _, p, ok := strings.Cut(addr, ":"); ok {
				if n, err := strconv.Atoi(p); err == nil {
					ports[n] = true
				}
			}
		}
	}
	return ports
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
