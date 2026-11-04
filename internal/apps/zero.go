package apps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"runova/internal/store"
)

const defaultDrainDelay = 5 * time.Second

func (s *Service) drainDelay() time.Duration {
	if s.DrainDelay > 0 {
		return s.DrainDelay
	}
	return defaultDrainDelay
}

// servingDeployment returns the active deployment when it has a container
// that can still be serving.
func (s *Service) servingDeployment(ctx context.Context, app store.App) (store.Deployment, bool) {
	if app.CurrentDeploymentID == nil {
		return store.Deployment{}, false
	}
	d, err := s.Store.Deployment(ctx, *app.CurrentDeploymentID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.ContainerID == "") {
		return store.Deployment{}, false
	}
	if err != nil {
		slog.Error("load serving deployment", "app", app.Name, "err", err)
		return store.Deployment{}, false
	}
	return d, true
}

// switchTraffic runs after the new deployment is healthy and active: Caddy
// is pointed at it, connections in flight get DrainDelay to finish, and
// only then the old container goes. If Caddy refuses, the old deployment is
// made the active one again, so nothing changes for visitors.
func (s *Service) switchTraffic(ctx context.Context, app store.App, old store.Deployment, tr *tracer) error {
	if err := s.ApplyCaddy(ctx); err != nil {
		if rbErr := s.Store.ActivateDeployment(context.WithoutCancel(ctx), app.ID, old.ID); rbErr != nil {
			slog.Error("restore previous deployment", "app", app.Name, "deployment", old.ID, "err", rbErr)
		}
		return err
	}
	tr.done(ctx, stepRoute, "")
	tr.begin(ctx, stepRetire, fmt.Sprintf("Menunggu %s untuk koneksi yang masih berjalan", s.drainDelay()))
	select {
	case <-time.After(s.drainDelay()):
	case <-ctx.Done():
	}
	// The old container is no longer the app's active one, so its exit is
	// not a crash for the monitor and needs no stop note.
	if err := s.removeContainerOf(context.WithoutCancel(ctx), old); err != nil {
		slog.Error("remove replaced container", "app", app.Name, "container", old.ContainerID, "err", err)
	}
	tr.done(ctx, stepRetire, fmt.Sprintf("v%d dihentikan", old.Number))
	return nil
}
