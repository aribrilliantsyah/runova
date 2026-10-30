package apps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"runova/internal/docker"
	"runova/internal/store"
)

const (
	// stopWindow is how long after a stop requested by Runova a die event is
	// attributed to that request instead of counting as a crash.
	stopWindow = 10 * time.Second
	// crashLimit dies within crashWindow mark an app failed.
	crashLimit     = 6
	crashWindow    = 10 * time.Minute
	defaultReconn  = 2 * time.Second
	appLabel       = "runova.app"
	interruptedLog = "Runova berhenti saat deploy berjalan"
)

// noteStop records that Runova is stopping the app's container on purpose.
func (s *Service) noteStop(appID int64) {
	s.monMu.Lock()
	defer s.monMu.Unlock()
	if s.stopped == nil {
		s.stopped = map[int64]time.Time{}
	}
	s.stopped[appID] = s.now()
}

// consumeStop reports whether a die event is the echo of a stop Runova asked
// for within stopWindow; the stop is used up by the first die it explains.
func (s *Service) consumeStop(appID int64) bool {
	s.monMu.Lock()
	defer s.monMu.Unlock()
	at, ok := s.stopped[appID]
	if !ok {
		return false
	}
	delete(s.stopped, appID)
	return s.now().Sub(at) <= stopWindow
}

// countCrash records a crash now and returns how many fall in the sliding window.
func (s *Service) countCrash(appID int64) int {
	s.monMu.Lock()
	defer s.monMu.Unlock()
	if s.crashes == nil {
		s.crashes = map[int64][]time.Time{}
	}
	now := s.now()
	recent := s.crashes[appID][:0]
	for _, t := range s.crashes[appID] {
		if now.Sub(t) < crashWindow {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	s.crashes[appID] = recent
	return len(recent)
}

// clearCrashes forgets the app's recent crashes, e.g. when the user starts it again.
func (s *Service) clearCrashes(appID int64) {
	s.monMu.Lock()
	defer s.monMu.Unlock()
	delete(s.crashes, appID)
}

// forget drops all monitor state of a deleted app.
func (s *Service) forget(appID int64) {
	s.monMu.Lock()
	defer s.monMu.Unlock()
	delete(s.crashes, appID)
	delete(s.stopped, appID)
}

// Monitor watches Docker events of Runova containers until ctx ends. The
// sixth die of an app's active container within ten minutes stops it, marks
// the app failed and notifies its owner. Dies caused by stops Runova asked for
// do not count. A broken event stream is reopened after ReconnectDelay.
func (s *Service) Monitor(ctx context.Context) error {
	delay := s.ReconnectDelay
	if delay <= 0 {
		delay = defaultReconn
	}
	for {
		events, errc := s.Engine.Events(ctx, map[string]string{appLabel: ""})
		for ev := range events {
			s.handleEvent(ctx, ev)
		}
		if err := <-errc; err != nil && ctx.Err() == nil {
			slog.Warn("docker events stream ended", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func (s *Service) handleEvent(ctx context.Context, ev docker.Event) {
	if ev.Action != "die" {
		return
	}
	appID, err := strconv.ParseInt(ev.Labels[appLabel], 10, 64)
	if err != nil {
		return
	}
	app, err := s.Store.App(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		slog.Error("monitor: load app", "app", appID, "err", err)
		return
	}
	if app.Status != store.AppRunning || app.CurrentDeploymentID == nil {
		// The die that a stop or a deploy caused arrives while the app is
		// stopped or deploying. Use the stop note up here, or it would swallow
		// the first real crash after the app runs again.
		s.consumeStop(app.ID)
		return
	}
	d, err := s.Store.Deployment(ctx, *app.CurrentDeploymentID)
	if err != nil || d.ContainerID != ev.ContainerID {
		return
	}
	if s.consumeStop(app.ID) {
		return
	}
	// The restart policy is on-failure: a clean exit is not restarted, and
	// the app would stay "running" with a dead container.
	if ev.Labels["exitCode"] == "0" {
		if err := s.Store.SetAppStatus(ctx, app.ID, store.AppStopped); err != nil {
			slog.Error("monitor: set app stopped", "app", app.Name, "err", err)
			return
		}
		s.notify(ctx, app, store.LevelInfo, fmt.Sprintf("Aplikasi %s berhenti sendiri (exit 0)", app.Name), "")
		return
	}
	if s.countCrash(app.ID) < crashLimit {
		return
	}
	s.giveUp(ctx, app, d)
}

// giveUp stops a crash-looping app for good: Docker's restart policy would
// otherwise keep starting it.
func (s *Service) giveUp(ctx context.Context, app store.App, d store.Deployment) {
	// Status first: the die that our own stop causes must find the app failed.
	if err := s.Store.SetAppStatus(ctx, app.ID, store.AppFailed); err != nil {
		slog.Error("monitor: set app failed", "app", app.Name, "err", err)
	}
	if err := s.Engine.StopContainer(ctx, d.ContainerID, stopGrace); err != nil && !errors.Is(err, docker.ErrNotFound) {
		slog.Error("monitor: stop crashing container", "app", app.Name, "err", err)
	}
	s.clearCrashes(app.ID)
	s.notify(ctx, app, store.LevelError,
		fmt.Sprintf("Aplikasi %s crash %d kali dalam 10 menit dan dihentikan", app.Name, crashLimit), "")
	slog.Warn("app crash loop, stopped", "app", app.Name)
}

// Reconcile aligns the database with Docker after Runova (re)starts. It never
// creates or starts containers: a running container stays as it is, a stopped
// one is left to Docker's restart policy, and a missing one marks the app
// failed. Containers of Runova that no active deployment owns are removed.
func (s *Service) Reconcile(ctx context.Context) error {
	list, err := s.Store.AllApps(ctx)
	if err != nil {
		return err
	}
	containers, err := s.Engine.ListContainers(ctx, map[string]string{appLabel: ""})
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	keep := map[string]bool{}
	var errs []error
	for _, app := range list {
		id, err := s.reconcileApp(ctx, app)
		if err != nil {
			errs = append(errs, fmt.Errorf("reconcile %s: %w", app.Name, err))
			continue
		}
		if id != "" {
			keep[id] = true
		}
	}
	if len(errs) > 0 {
		// Without knowing every app's state, removing "orphans" could kill live containers.
		return errors.Join(errs...)
	}
	for _, c := range containers {
		if keep[c.ID] {
			continue
		}
		slog.Info("removing orphan container", "container", c.ID, "names", c.Names)
		if err := s.Engine.RemoveContainer(ctx, c.ID); err != nil {
			errs = append(errs, fmt.Errorf("remove orphan %s: %w", c.ID, err))
		}
	}
	return errors.Join(errs...)
}

// reconcileApp fixes one app's state and returns the ID of the container that
// belongs to its active deployment, if there is one.
func (s *Service) reconcileApp(ctx context.Context, app store.App) (string, error) {
	ds, err := s.Store.Deployments(ctx, app.ID)
	if err != nil {
		return "", err
	}
	for _, d := range ds {
		if d.Status != store.DeployRunning {
			continue
		}
		// The process that was deploying is gone; the deployment cannot finish.
		failed, finished, log := store.DeployFailed, s.now().UnixMilli(), interruptedLog
		if err := s.Store.UpdateDeployment(ctx, d.ID, store.DeploymentPatch{Status: &failed, FailureLog: &log, FinishedAt: &finished}); err != nil {
			return "", err
		}
		s.notify(ctx, app, store.LevelError, fmt.Sprintf("Deploy %s #%d gagal", app.Name, d.Number), interruptedLog)
	}

	if app.CurrentDeploymentID == nil {
		if app.Status == store.AppDeploying {
			return "", s.Store.SetAppStatus(ctx, app.ID, store.AppFailed)
		}
		return "", nil
	}
	cur, err := s.Store.Deployment(ctx, *app.CurrentDeploymentID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	var st docker.ContainerState
	missing := errors.Is(err, store.ErrNotFound) || cur.ContainerID == ""
	if !missing {
		st, err = s.Engine.InspectContainer(ctx, cur.ContainerID)
		if errors.Is(err, docker.ErrNotFound) {
			missing = true
		} else if err != nil {
			return "", fmt.Errorf("inspect container: %w", err)
		}
	}
	if missing {
		if cur.ID != 0 {
			superseded := store.DeploySuperseded
			if err := s.Store.UpdateDeployment(ctx, cur.ID, store.DeploymentPatch{Status: &superseded}); err != nil {
				return "", err
			}
		}
		if err := s.Store.ClearCurrentDeployment(ctx, app.ID, store.AppFailed); err != nil {
			return "", err
		}
		s.notify(ctx, app, store.LevelError, fmt.Sprintf("Container %s hilang; deploy ulang", app.Name), "")
		return "", nil
	}

	want := store.AppRunning
	if !st.Running {
		want = store.AppStopped
		if app.Status == store.AppFailed {
			want = store.AppFailed // a crash loop stopped it; stay failed
		}
	}
	if app.Status != want {
		if err := s.Store.SetAppStatus(ctx, app.ID, want); err != nil {
			return "", err
		}
	}
	return st.ID, nil
}
