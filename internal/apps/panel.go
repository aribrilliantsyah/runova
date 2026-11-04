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
// A change of the trusted proxies can switch front-proxy mode and with it the
// issuer of every certificate, so every domain is checked again in the background.
func (s *Service) SaveSettings(ctx context.Context, v store.Settings) (changed bool, err error) {
	old, err := s.Store.Settings(ctx)
	if err != nil {
		return false, err
	}
	changed = old.PanelDomain != v.PanelDomain || old.PanelPort != v.PanelPort
	proxiesChanged := !slices.Equal(old.TrustedProxies, v.TrustedProxies)
	caddyAffected := changed || old.PublicIP != v.PublicIP || proxiesChanged

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
	if proxiesChanged {
		s.background(func() {
			if err := s.RecheckDomains(context.WithoutCancel(ctx)); err != nil {
				slog.Error("recheck domains", "err", err)
			}
		})
	}
	return changed, nil
}

func (s *Service) background(f func()) {
	if s.Go != nil {
		s.Go(f)
		return
	}
	go f()
}
