package apps

import (
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
)

func (s *Service) probe(ctx context.Context, host string) (bool, string) {
	if s.Probe != nil {
		return s.Probe(ctx, host)
	}
	return caddy.ProbeTLS(ctx, host)
}

// AddDomain routes host to the app. The host is normalized and must be free
// (no app and not the panel); Caddy must accept the new configuration, or the
// domain is removed again.
func (s *Service) AddDomain(ctx context.Context, user store.User, appID int64, host string) (store.Domain, error) {
	app, _, err := s.load(ctx, user, appID)
	if err != nil {
		return store.Domain{}, err
	}
	host, err = caddy.ValidateDomain(host)
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
	case err != nil:
		return store.Domain{}, err
	}
	if err := s.ApplyCaddy(ctx); err != nil {
		if rmErr := s.Store.RemoveDomain(context.WithoutCancel(ctx), app.ID, d.ID); rmErr != nil {
			slog.Error("roll back domain", "host", host, "err", rmErr)
		}
		return store.Domain{}, err
	}
	s.logActivity(ctx, user, store.ActDomainAdd, app.Name, host)
	return d, nil
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
	return s.checkDomain(ctx, app, d)
}

// checkDomain records the outcome of one probe. A domain that fails stays
// pending while Caddy may still be obtaining its certificate and becomes an
// error afterwards; the first success notifies the owner.
func (s *Service) checkDomain(ctx context.Context, app store.App, d store.Domain) (store.Domain, error) {
	ok, detail := s.probe(ctx, d.Host)
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

// CheckDomains probes every domain that is not active yet.
func (s *Service) CheckDomains(ctx context.Context) error {
	list, err := s.Store.AllDomains(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range list {
		if d.Status == store.DomainActive {
			continue
		}
		app, err := s.Store.App(ctx, d.AppID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := s.checkDomain(ctx, app, d); err != nil {
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
