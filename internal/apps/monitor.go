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
	// defaultReviveGap is the pause between two apps started after a server restart.
	defaultReviveGap = 5 * time.Second
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
//
// Every connection first subscribes and only then reconciles with Docker, so a
// container that dies while the stream was down (or before the first
// subscription) is seen by one of the two and never by neither.
func (s *Service) Monitor(ctx context.Context) error {
	delay := s.ReconnectDelay
	if delay <= 0 {
		delay = defaultReconn
	}
	for {
		events, errc := s.Engine.Events(ctx, map[string]string{appLabel: ""})
		if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
			slog.Error("reconcile with docker", "err", err)
		}
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
	if !app.Status.Up() || app.CurrentDeploymentID == nil {
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
	if ev.Labels["exitCode"] == "0" {
		if err := s.Store.SetAppStatus(ctx, app.ID, store.AppStopped); err != nil {
			slog.Error("monitor: set app stopped", "app", app.Name, "err", err)
			return
		}
		code := 0
		s.setProblem(ctx, d, store.Problem{Kind: "exited_clean", Title: "Aplikasi berhenti sendiri (exit 0)",
			Hint: "Proses selesai tanpa error. Aplikasi server biasanya tidak berhenti sendiri; periksa apakah main() keluar setelah start.", ExitCode: &code, At: s.now().UnixMilli()})
		s.notify(ctx, app, store.LevelInfo, fmt.Sprintf("Aplikasi %s berhenti sendiri (exit 0)", app.Name), "")
		return
	}
	if s.countCrash(app.ID) < crashLimit {
		return
	}
	s.giveUp(ctx, app, d, ev.Labels["exitCode"])
}

// setProblem records why the deployment's container stopped.
func (s *Service) setProblem(ctx context.Context, d store.Deployment, p store.Problem) {
	if err := s.Store.UpdateDeployment(context.WithoutCancel(ctx), d.ID, store.DeploymentPatch{Problem: &p}); err != nil {
		slog.Error("record deployment problem", "deployment", d.ID, "err", err)
	}
}

// giveUp stops a crash-looping app for good: Docker's restart policy would
// otherwise keep starting it. The reason of the last exit is kept on the
// deployment with the container's last output.
func (s *Service) giveUp(ctx context.Context, app store.App, d store.Deployment, exitLabel string) {
	// Status first: the die that our own stop causes must find the app failed.
	if err := s.Store.SetAppStatus(ctx, app.ID, store.AppFailed); err != nil {
		slog.Error("monitor: set app failed", "app", app.Name, "err", err)
	}
	st, _ := s.Engine.InspectContainer(ctx, d.ContainerID)
	if err := s.Engine.StopContainer(ctx, d.ContainerID, stopGrace); err != nil && !errors.Is(err, docker.ErrNotFound) {
		slog.Error("monitor: stop crashing container", "app", app.Name, "err", err)
	}
	s.clearCrashes(app.ID)
	lines := s.readLines(ctx, d.ContainerID, keptLogLines)
	// The die event's exit code is the crash; the restart policy may have
	// started the container again since, resetting the inspected one.
	if n, err := strconv.Atoi(exitLabel); err == nil && st.ExitCode == 0 {
		st.ExitCode = n
	}
	code := st.ExitCode
	p := exitProblem(store.Problem{Kind: "crash_loop", ExitCode: &code, At: s.now().UnixMilli()}, app, st, lines, true)
	p.Detail = fmt.Sprintf("Crash %d kali dalam 10 menit; terakhir exit code %d", crashLimit, st.ExitCode)
	if st.OOMKilled {
		p.Detail += " (OOMKilled)"
	}
	s.setProblem(ctx, d, p)
	s.keepLog(ctx, d, lines)
	s.notify(ctx, app, store.LevelError,
		fmt.Sprintf("Aplikasi %s crash %d kali dalam 10 menit dan dihentikan", app.Name, crashLimit), p.Title)
	slog.Warn("app crash loop, stopped", "app", app.Name)
}

// Reconcile aligns the database with Docker. It runs when Runova starts and
// after every reconnect of Monitor, so it is safe next to deploys: an app
// whose change lock is taken is skipped together with all of its containers.
// It never creates containers: a running container stays as it is and a
// missing one marks the app failed. Containers of Runova that no active
// deployment owns are removed.
//
// A stopped container whose app is running or starting is what a server
// reboot leaves behind (an app that exits 0 on SIGTERM is not restarted by
// the on-failure policy). The first complete pass after Runova starts starts
// those containers again, one every ReviveGap; later passes (after a
// reconnect) only mark them stopped, as a clean exit while Runova watches is
// the app's own decision. Apps stopped by the user or failed stay down.
func (s *Service) Reconcile(ctx context.Context) error {
	revive, err := s.reconcilePass(ctx, !s.revived.Load())
	if err != nil {
		return err
	}
	if !s.revived.Swap(true) && len(revive) > 0 {
		s.background(func() { s.reviveApps(context.WithoutCancel(ctx), revive) })
	}
	return nil
}

// reviveApps starts the stopped containers of apps that ought to run, one
// every ReviveGap so that many JVMs do not fight for the CPU at once. Each
// app goes through the same wait for its port as Start (awaitStart), which
// runs on while the next one is started. An app whose lock is taken or whose
// state changed since the pass (the user stopped or redeployed it) is skipped.
func (s *Service) reviveApps(ctx context.Context, list []store.App) {
	gap := s.ReviveGap
	if gap <= 0 {
		gap = defaultReviveGap
	}
	for i, app := range list {
		if i > 0 {
			time.Sleep(gap)
		}
		s.reviveApp(ctx, app)
	}
}

func (s *Service) reviveApp(ctx context.Context, app store.App) {
	unlock, ok := s.lockApp(app.ID)
	if !ok {
		return
	}
	defer unlock()
	cur, err := s.Store.App(ctx, app.ID)
	if err != nil || cur.Status != store.AppStarting || cur.CurrentDeploymentID == nil ||
		app.CurrentDeploymentID == nil || *cur.CurrentDeploymentID != *app.CurrentDeploymentID {
		return
	}
	id, err := s.activeContainer(ctx, cur)
	if err == nil {
		err = s.startContainer(ctx, cur, id)
	}
	if err != nil {
		slog.Error("start app after server restart", "app", cur.Name, "err", err)
		if err := s.Store.SetAppStatus(ctx, cur.ID, store.AppFailed); err != nil {
			slog.Error("set app failed", "app", cur.Name, "err", err)
		}
		s.markActive(ctx, cur, store.Problem{Kind: "start_failed", Title: "Aplikasi gagal dinyalakan setelah server dimulai ulang", Detail: err.Error(), At: s.now().UnixMilli()})
		s.notify(ctx, cur, store.LevelError, fmt.Sprintf("Aplikasi %s gagal dinyalakan setelah server dimulai ulang", cur.Name), err.Error())
		return
	}
	s.logActivity(ctx, store.User{}, store.ActAutostart, cur.Name, "")
	s.notify(ctx, cur, store.LevelInfo, fmt.Sprintf("Aplikasi %s dinyalakan lagi setelah server dimulai ulang", cur.Name), "")
	s.awaitStart(ctx, cur)
}

// reconcilePass is one reconcile under the apps' locks, which are released
// when it returns. With reviveStopped, the apps that ought to run but whose
// container is stopped are marked starting and returned.
func (s *Service) reconcilePass(ctx context.Context, reviveStopped bool) ([]store.App, error) {
	// Containers first: one created after this list belongs to an app whose
	// row exists by then, so the apps read below always cover the list.
	containers, err := s.Engine.ListContainers(ctx, map[string]string{appLabel: ""})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	list, err := s.Store.AllApps(ctx)
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	busy := map[string]bool{}
	var errs []error
	var revive []store.App
	var unlocks []func()
	defer func() {
		for _, unlock := range unlocks {
			unlock()
		}
	}()
	for _, app := range list {
		unlock, ok := s.lockApp(app.ID)
		if !ok {
			busy[strconv.FormatInt(app.ID, 10)] = true
			continue
		}
		unlocks = append(unlocks, unlock)
		id, again, err := s.reconcileApp(ctx, app, reviveStopped)
		if err != nil {
			errs = append(errs, fmt.Errorf("reconcile %s: %w", app.Name, err))
			continue
		}
		if id != "" {
			keep[id] = true
		}
		if again {
			app.Status = store.AppStarting
			revive = append(revive, app)
		}
	}
	if len(errs) > 0 {
		// Without knowing every app's state, removing "orphans" could kill live containers.
		return nil, errors.Join(errs...)
	}
	for _, c := range containers {
		if keep[c.ID] || busy[c.Labels[appLabel]] {
			continue
		}
		slog.Info("removing orphan container", "container", c.ID, "names", c.Names)
		if err := s.Engine.RemoveContainer(ctx, c.ID); err != nil {
			errs = append(errs, fmt.Errorf("remove orphan %s: %w", c.ID, err))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return revive, nil
}

// reconcileApp fixes one app's state and returns the ID of the container that
// belongs to its active deployment, if there is one. revive reports that the
// container is stopped but the app ought to run: it was marked starting and
// the caller starts the container once the app's lock is released.
func (s *Service) reconcileApp(ctx context.Context, app store.App, reviveStopped bool) (id string, revive bool, err error) {
	ds, err := s.Store.Deployments(ctx, app.ID)
	if err != nil {
		return "", false, err
	}
	for _, d := range ds {
		if d.Status != store.DeployRunning {
			continue
		}
		// The process that was deploying is gone; the deployment cannot finish.
		failed, finished, log := store.DeployFailed, s.now().UnixMilli(), interruptedLog
		if err := s.Store.UpdateDeployment(ctx, d.ID, store.DeploymentPatch{Status: &failed, FailureLog: &log, FinishedAt: &finished}); err != nil {
			return "", false, err
		}
		s.notify(ctx, app, store.LevelError, fmt.Sprintf("Deploy %s #%d gagal", app.Name, d.Number), interruptedLog)
	}

	if app.CurrentDeploymentID == nil {
		if app.Status == store.AppDeploying {
			return "", false, s.Store.SetAppStatus(ctx, app.ID, store.AppFailed)
		}
		return "", false, nil
	}
	cur, err := s.Store.Deployment(ctx, *app.CurrentDeploymentID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", false, err
	}
	var st docker.ContainerState
	missing := errors.Is(err, store.ErrNotFound) || cur.ContainerID == ""
	if !missing {
		st, err = s.Engine.InspectContainer(ctx, cur.ContainerID)
		if errors.Is(err, docker.ErrNotFound) {
			missing = true
		} else if err != nil {
			return "", false, fmt.Errorf("inspect container: %w", err)
		}
	}
	if missing {
		if cur.ID != 0 {
			superseded := store.DeploySuperseded
			lost := store.Problem{Kind: "container_lost", Title: "Container aplikasi hilang dari Docker", Hint: "Container dihapus di luar Runova atau Docker direset. Deploy ulang untuk membuatnya lagi.", At: s.now().UnixMilli()}
			if err := s.Store.UpdateDeployment(ctx, cur.ID, store.DeploymentPatch{Status: &superseded, Problem: &lost}); err != nil {
				return "", false, err
			}
		}
		if err := s.Store.ClearCurrentDeployment(ctx, app.ID, store.AppFailed); err != nil {
			return "", false, err
		}
		s.notify(ctx, app, store.LevelError, fmt.Sprintf("Container %s hilang; deploy ulang", app.Name), "")
		return "", false, nil
	}

	if !st.Running && reviveStopped && app.Status.Up() {
		if app.Status != store.AppStarting {
			if err := s.Store.SetAppStatus(ctx, app.ID, store.AppStarting); err != nil {
				return "", false, err
			}
		}
		return st.ID, true, nil
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
			return "", false, err
		}
	}
	return st.ID, false, nil
}
