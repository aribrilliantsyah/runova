package apps

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"runova/internal/caddy"
	"runova/internal/store"
)

const (
	// domainPendingWindow is how long a domain without a working certificate
	// still counts as pending (Caddy is obtaining one) before it is an error.
	domainPendingWindow = 10 * time.Minute
	domainCheckInterval = 5 * time.Minute
	// After the proxy setting changes, Caddy obtains certificates in the
	// background; a failing domain is probed again this often, this many times.
	defaultRecheckDelay = 2 * time.Second
	recheckAttempts     = 10
)

func (s *Service) probe(ctx context.Context, host string, frontProxy bool) (bool, string) {
	if s.Probe != nil {
		return s.Probe(ctx, host, frontProxy)
	}
	return caddy.ProbeTLS(ctx, host, frontProxy)
}

// frontProxy reports front-proxy mode (see caddy.State.TrustedProxies).
func (s *Service) frontProxy(ctx context.Context) (bool, error) {
	v, err := s.Store.Settings(ctx)
	return len(v.TrustedProxies) > 0, err
}

// AddDomain gives the app an address: a label under a registered hosting
// domain (counted against the owner's subdomain quota) or, for admins, a free
// host name. A free host is normalized and must be free (no app and not the
// panel). Caddy must accept the new configuration, or the domain is removed again.
func (s *Service) AddDomain(ctx context.Context, user store.User, appID int64, in DomainInput) (store.Domain, error) {
	app, owner, err := s.load(ctx, user, appID)
	if err != nil {
		return store.Domain{}, err
	}
	var d store.Domain
	switch {
	case in.HostingDomainID != 0 && in.Host != "":
		return store.Domain{}, invalid("Pilih domain hosting atau isi domain sendiri, tidak keduanya")
	case in.HostingDomainID != 0:
		d, err = s.addHostedDomain(ctx, app, owner, in)
	case in.Host != "":
		if !user.IsAdmin() {
			return store.Domain{}, forbidden("Domain sendiri hanya bisa ditambahkan admin")
		}
		d, err = s.addFreeDomain(ctx, app, in.Host)
	default:
		return store.Domain{}, invalid("Pilih domain hosting dan isi nama alamat")
	}
	if err != nil {
		return store.Domain{}, err
	}
	if err := s.ApplyCaddy(ctx); err != nil {
		if rmErr := s.Store.RemoveDomain(context.WithoutCancel(ctx), app.ID, d.ID); rmErr != nil {
			slog.Error("roll back domain", "host", d.Host, "err", rmErr)
		}
		return store.Domain{}, err
	}
	s.logActivity(ctx, user, store.ActDomainAdd, app.Name, d.Host)
	return d, nil
}

func (s *Service) addFreeDomain(ctx context.Context, app store.App, host string) (store.Domain, error) {
	host, err := caddy.ValidateDomain(host)
	if err != nil {
		return store.Domain{}, invalid("%s", err.Error())
	}
	settings, err := s.Store.Settings(ctx)
	if err != nil {
		return store.Domain{}, err
	}
	if host == settings.PanelDomain {
		return store.Domain{}, conflict("Domain sudah dipakai")
	}
	d, err := s.Store.AddDomain(ctx, app.ID, host, s.now().UnixMilli())
	switch {
	case errors.Is(err, store.ErrConflict):
		return store.Domain{}, conflict("Domain sudah dipakai")
	case errors.Is(err, store.ErrDomainLimit):
		return store.Domain{}, invalid("Maksimal %d domain per aplikasi", store.MaxDomainsPerApp)
	}
	return d, err
}

// RemoveDomain deletes the domain and its route.
func (s *Service) RemoveDomain(ctx context.Context, user store.User, appID, domainID int64) error {
	app, _, err := s.load(ctx, user, appID)
	if err != nil {
		return err
	}
	d, err := s.domainOf(ctx, app, domainID)
	if err != nil {
		return err
	}
	if err := s.Store.RemoveDomain(ctx, app.ID, d.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return invalid("Domain tidak ditemukan")
		}
		return err
	}
	s.logActivity(ctx, user, store.ActDomainRemove, app.Name, d.Host)
	return s.ApplyCaddy(ctx)
}

func (s *Service) domainOf(ctx context.Context, app store.App, domainID int64) (store.Domain, error) {
	list, err := s.Store.Domains(ctx, app.ID)
	if err != nil {
		return store.Domain{}, err
	}
	for _, d := range list {
		if d.ID == domainID {
			return d, nil
		}
	}
	return store.Domain{}, invalid("Domain tidak ditemukan")
}

// Domains lists the app's domains.
func (s *Service) Domains(ctx context.Context, user store.User, appID int64) ([]store.Domain, error) {
	app, _, err := s.load(ctx, user, appID)
	if err != nil {
		return nil, err
	}
	return s.Store.Domains(ctx, app.ID)
}

// CheckDomain probes the domain's certificate now and records the result.
func (s *Service) CheckDomain(ctx context.Context, user store.User, appID, domainID int64) (store.Domain, error) {
	app, _, err := s.load(ctx, user, appID)
	if err != nil {
		return store.Domain{}, err
	}
	d, err := s.domainOf(ctx, app, domainID)
	if err != nil {
		return store.Domain{}, err
	}
	front, err := s.frontProxy(ctx)
	if err != nil {
		return store.Domain{}, err
	}
	return s.checkDomain(ctx, app, d, front, 1)
}

// checkDomain probes up to attempts times, until one succeeds, and records
// the outcome. A domain that fails stays pending while Caddy may still be
// obtaining its certificate and becomes an error afterwards; the first
// success notifies the owner.
func (s *Service) checkDomain(ctx context.Context, app store.App, d store.Domain, front bool, attempts int) (store.Domain, error) {
	ok, detail := s.probe(ctx, d.Host, front)
	for i := 1; !ok && i < attempts; i++ {
		select {
		case <-ctx.Done():
			return store.Domain{}, ctx.Err()
		case <-time.After(cmp.Or(s.RecheckDelay, defaultRecheckDelay)):
		}
		ok, detail = s.probe(ctx, d.Host, front)
	}
	now := s.now()
	status := store.DomainPending
	switch {
	case ok:
		status, detail = store.DomainActive, ""
	case now.Sub(time.UnixMilli(d.CreatedAt)) > domainPendingWindow:
		status = store.DomainError
	}
	if err := s.Store.SetDomainStatus(ctx, d.ID, status, detail, now.UnixMilli()); err != nil {
		return store.Domain{}, err
	}
	if status == store.DomainActive && d.Status != store.DomainActive {
		s.notify(ctx, app, store.LevelInfo, "HTTPS aktif untuk "+d.Host, "")
	}
	d.Status, d.Detail, d.CheckedAt = status, detail, now.UnixMilli()
	return d, nil
}

// CheckDomains probes every domain that is not active yet, except subdomains
// of a disabled hosting domain (Caddy does not serve them).
func (s *Service) CheckDomains(ctx context.Context) error {
	return s.checkDomains(ctx, false, 1)
}

// RecheckDomains probes every served domain, active ones too, after a change
// of the certificates Caddy uses. A failing domain is retried for a while, as
// Caddy obtains the new certificates in the background.
func (s *Service) RecheckDomains(ctx context.Context) error {
	return s.checkDomains(ctx, true, recheckAttempts)
}

func (s *Service) checkDomains(ctx context.Context, active bool, attempts int) error {
	list, err := s.Store.AllDomains(ctx)
	if err != nil {
		return err
	}
	off, err := s.disabledHosting(ctx)
	if err != nil {
		return err
	}
	front, err := s.frontProxy(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range list {
		if d.Status == store.DomainActive && !active || d.HostingDomainID != nil && off[*d.HostingDomainID] {
			continue
		}
		app, err := s.Store.App(ctx, d.AppID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := s.checkDomain(ctx, app, d, front, attempts); err != nil {
			errs = append(errs, fmt.Errorf("check %s: %w", d.Host, err))
		}
	}
	return errors.Join(errs...)
}

// WatchDomains checks pending and failed domains every five minutes until ctx ends.
func (s *Service) WatchDomains(ctx context.Context) {
	t := time.NewTicker(domainCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.CheckDomains(ctx); err != nil && ctx.Err() == nil {
				slog.Error("check domains", "err", err)
			}
		}
	}
}
