package apps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"

	"runova/internal/caddy"
	"runova/internal/store"
)

// ErrCaddy marks a change that Caddy refused to load.
var ErrCaddy = errors.New("caddy rejected the configuration")

func caddyError(err error) error {
	return &userError{ErrCaddy, "Caddy menolak konfigurasi: " + err.Error()}
}

// ApplyCaddy rebuilds Caddy's configuration from the database and loads it.
// Without a Caddy (tests, tools) there is nothing to do.
func (s *Service) ApplyCaddy(ctx context.Context) error {
	if s.Caddy == nil {
		return nil
	}
	if err := s.Caddy.Apply(ctx); err != nil {
		return caddyError(err)
	}
	return nil
}

// refreshCaddy applies after a change that already happened and cannot be
// undone (stop, delete, switching deployments). A refusal is logged: the
// next successful Apply brings Caddy back in line with the database.
func (s *Service) refreshCaddy(ctx context.Context, app store.App) {
	if err := s.ApplyCaddy(context.WithoutCancel(ctx)); err != nil {
		slog.Error("update caddy", "app", app.Name, "err", err)
	}
}

// revertApp puts the app's settings back to old after a refused change.
func (s *Service) revertApp(ctx context.Context, old store.App) {
	_, err := s.Store.UpdateApp(context.WithoutCancel(ctx), old.ID, store.AppPatch{
		JavaVersion: old.JavaVersion, JVMOpts: &old.JVMOpts, InternalPort: &old.InternalPort,
		MemoryMB: &old.MemoryMB, CPUMilli: &old.CPUMilli, IPAccess: &old.IPAccess, DeployMode: &old.DeployMode,
	})
	if err != nil {
		slog.Error("revert app settings", "app", old.Name, "err", err)
	}
}

// CaddyState reads what Caddy has to serve. Subdomains of a disabled hosting
// domain are not routed. An app is routed while it runs,
// or while a new deployment replaces the one that still serves; its upstream
// is always the host port of the active deployment. panelUpstream is where
// the panel listens, as an address Caddy can reach.
func CaddyState(ctx context.Context, st *store.Store, panelUpstream string) (caddy.State, error) {
	settings, err := st.Settings(ctx)
	if err != nil {
		return caddy.State{}, err
	}
	list, err := st.AllApps(ctx)
	if err != nil {
		return caddy.State{}, err
	}
	domains, err := st.AllDomains(ctx)
	if err != nil {
		return caddy.State{}, err
	}

	state := caddy.State{
		PanelDomain: settings.PanelDomain, PanelIPPort: settings.PanelPort, PanelUpstream: panelUpstream, PublicIP: settings.PublicIP,
	}
	upstream := map[int64]string{}
	for _, app := range list {
		if (app.Status != store.AppRunning && app.Status != store.AppDeploying) || app.CurrentDeploymentID == nil {
			continue
		}
		d, err := st.Deployment(ctx, *app.CurrentDeploymentID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return caddy.State{}, err
		}
		if d.HostPort == 0 {
			continue
		}
		upstream[app.ID] = net.JoinHostPort("127.0.0.1", strconv.Itoa(d.HostPort))
		if app.IPAccess {
			state.IPListeners = append(state.IPListeners, caddy.IPListener{Port: app.IPPort, Upstream: upstream[app.ID]})
		}
	}
	off, err := disabledHostingOf(ctx, st)
	if err != nil {
		return caddy.State{}, err
	}
	for _, d := range domains {
		if d.HostingDomainID != nil && off[*d.HostingDomainID] {
			continue
		}
		if up, ok := upstream[d.AppID]; ok {
			state.Sites = append(state.Sites, caddy.Site{Host: d.Host, Upstream: up})
		}
	}
	return state, nil
}

// disabledHostingOf is the set of hosting domains the admin turned off.
func disabledHostingOf(ctx context.Context, st *store.Store) (map[int64]bool, error) {
	list, err := st.HostingDomains(ctx)
	if err != nil {
		return nil, err
	}
	off := map[int64]bool{}
	for _, h := range list {
		if !h.Enabled {
			off[h.ID] = true
		}
	}
	return off, nil
}

func (s *Service) disabledHosting(ctx context.Context) (map[int64]bool, error) {
	return disabledHostingOf(ctx, s.Store)
}

// PanelUpstream turns the panel's listen address into one Caddy can dial.
func PanelUpstream(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("panel listen address %q: %w", listen, err)
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}
