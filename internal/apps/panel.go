package apps

import (
	"context"
	"log/slog"
	"slices"

	"runova/internal/store"
)

// SaveSettings stores v and, when the change alters what Caddy serves (panel
// domain or port, public IP, trusted proxies), loads the new configuration. A domain that
// already routes an app cannot become the panel's. If Caddy refuses, the
// previous settings are restored. changed reports a panel address change.
func (s *Service) SaveSettings(ctx context.Context, v store.Settings) (changed bool, err error) {
	old, err := s.Store.Settings(ctx)
	if err != nil {
		return false, err
	}
	changed = old.PanelDomain != v.PanelDomain || old.PanelPort != v.PanelPort
	caddyAffected := changed || old.PublicIP != v.PublicIP || !slices.Equal(old.TrustedProxies, v.TrustedProxies)

	if v.PanelDomain != "" && v.PanelDomain != old.PanelDomain {
		taken, err := s.Store.DomainHostTaken(ctx, v.PanelDomain)
		if err != nil {
			return false, err
		}
		if taken {
			return false, conflict("Domain sudah dipakai")
		}
	}
	if err := s.Store.SetSettings(ctx, v); err != nil {
		return false, err
	}
	if !caddyAffected {
		return changed, nil
	}
	if err := s.ApplyCaddy(ctx); err != nil {
		if restoreErr := s.Store.SetSettings(context.WithoutCancel(ctx), old); restoreErr != nil {
			slog.Error("restore settings", "err", restoreErr)
		}
		return false, err
	}
	return changed, nil
}
