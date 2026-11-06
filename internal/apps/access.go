package apps

import (
	"context"
	"errors"
	"io"
	"strconv"
	"time"

	"runova/internal/artifact"
	"runova/internal/docker"
	"runova/internal/store"
)

// ErrNoShell means the app runs from an image without a shell.
var ErrNoShell = &userError{store.ErrConflict, "Binary statis tidak punya shell"}

// ErrNotRunning means the app has no container to attach to.
var ErrNotRunning = &userError{store.ErrConflict, "Aplikasi tidak sedang berjalan"}

// Busy reports whether a deploy or another change of the app is in progress.
func (s *Service) Busy(appID int64) bool {
	unlock, ok := s.lockApp(appID)
	if ok {
		unlock()
	}
	return !ok
}

// runningContainer returns the container of the app's active deployment when
// the app is running or starting.
func (s *Service) runningContainer(ctx context.Context, user store.User, appID int64) (store.App, store.User, string, error) {
	app, owner, err := s.load(ctx, user, appID)
	if err != nil {
		return store.App{}, store.User{}, "", err
	}
	if !app.Status.Up() || app.CurrentDeploymentID == nil {
		return store.App{}, store.User{}, "", ErrNotRunning
	}
	d, err := s.Store.Deployment(ctx, *app.CurrentDeploymentID)
	if err != nil {
		return store.App{}, store.User{}, "", err
	}
	if d.ContainerID == "" {
		return store.App{}, store.User{}, "", ErrNotRunning
	}
	return app, owner, d.ContainerID, nil
}

// Stats returns the live resource use of the app's container; ok is false
// when the app is not running.
func (s *Service) Stats(ctx context.Context, user store.User, appID int64) (st docker.Stats, ok bool, err error) {
	_, _, id, err := s.runningContainer(ctx, user, appID)
	if errors.Is(err, ErrNotRunning) {
		return docker.Stats{}, false, nil
	}
	if err != nil {
		return docker.Stats{}, false, err
	}
	st, err = s.Engine.Stats(ctx, id)
	if errors.Is(err, docker.ErrNotFound) {
		return docker.Stats{}, false, nil
	}
	return st, err == nil, err
}

// Logs opens the app's container log. With follow it stays open until closed
// (or the container stops). started is when the container last started, so a
// reader that reopens the log can tell that it started again in between.
func (s *Service) Logs(ctx context.Context, user store.User, appID int64, tail int, follow bool) (rc io.ReadCloser, started time.Time, err error) {
	app, _, err := s.load(ctx, user, appID)
	if err != nil {
		return nil, time.Time{}, err
	}
	// Logs stay readable after a crash or stop, so any recorded container will do.
	id, err := s.activeContainer(ctx, app)
	if err != nil {
		return nil, time.Time{}, ErrNotRunning
	}
	st, err := s.Engine.InspectContainer(ctx, id)
	if errors.Is(err, docker.ErrNotFound) {
		return nil, time.Time{}, ErrNotRunning
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	rc, err = s.Engine.Logs(ctx, id, tail, follow)
	if errors.Is(err, docker.ErrNotFound) {
		return nil, time.Time{}, ErrNotRunning
	}
	return rc, st.StartedAt, err
}

// CheckTerminal verifies the actor may open a terminal in the app, without opening one.
func (s *Service) CheckTerminal(ctx context.Context, user store.User, appID int64) error {
	app, _, _, err := s.runningContainer(ctx, user, appID)
	if errors.Is(err, ErrNotRunning) {
		// Distinguish the missing shell first: static apps never have one.
		if a, _, lerr := s.load(ctx, user, appID); lerr == nil && a.Kind == artifact.KindStatic {
			return ErrNoShell
		}
	}
	if err != nil {
		return err
	}
	if app.Kind == artifact.KindStatic {
		return ErrNoShell
	}
	return nil
}

// Terminal starts /bin/sh as the owner's container user inside the app's
// container, on a TTY. Only JAR and glibc apps have a shell.
func (s *Service) Terminal(ctx context.Context, user store.User, appID int64) (docker.ExecSession, store.App, error) {
	if err := s.CheckTerminal(ctx, user, appID); err != nil {
		return nil, store.App{}, err
	}
	app, owner, id, err := s.runningContainer(ctx, user, appID)
	if err != nil {
		return nil, store.App{}, err
	}
	uid := strconv.Itoa(containerUID(owner))
	sess, err := s.Engine.Exec(ctx, id, uid+":"+uid, []string{"/bin/sh"})
	if errors.Is(err, docker.ErrNotFound) {
		return nil, store.App{}, ErrNotRunning
	}
	return sess, app, err
}
