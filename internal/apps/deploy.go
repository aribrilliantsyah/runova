package apps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"runova/internal/artifact"
	"runova/internal/docker"
	"runova/internal/store"
)

// ErrBusy means another deploy or change of the same app is still running.
var ErrBusy = &userError{store.ErrConflict, "Deploy sedang berjalan"}

func (s *Service) lockOrBusy(appID int64) (func(), error) {
	unlock, ok := s.lockApp(appID)
	if !ok {
		return nil, ErrBusy
	}
	return unlock, nil
}

// Deploy runs a pending deployment of the app: it replaces the running
// container (stop-start), waits until the app accepts TCP connections and
// then makes the deployment the active one. A failed deploy is recorded on
// the deployment and the app; the error returned is the cause.
func (s *Service) Deploy(ctx context.Context, user store.User, appID, deploymentID int64) error {
	release, err := s.Lock(ctx, user, appID)
	if err != nil {
		return err
	}
	defer release()
	return s.DeployLocked(ctx, user, appID, deploymentID)
}

// Lock takes the app's change lock for a caller that must know it holds the
// app before it prepares a deployment: ErrBusy when a deploy or another
// change is running. Run the deployment with DeployLocked, then call release.
func (s *Service) Lock(ctx context.Context, user store.User, appID int64) (release func(), err error) {
	if _, _, err := s.load(ctx, user, appID); err != nil {
		return nil, err
	}
	return s.lockOrBusy(appID)
}

// DeployLocked is Deploy for a caller that already holds the app through Lock.
func (s *Service) DeployLocked(ctx context.Context, user store.User, appID, deploymentID int64) error {
	app, owner, err := s.load(ctx, user, appID)
	if err != nil {
		return err
	}
	return s.deployLocked(ctx, user, owner, app, deploymentID)
}

func (s *Service) deployLocked(ctx context.Context, actor, owner store.User, app store.App, deploymentID int64) error {
	d, err := s.Store.Deployment(ctx, deploymentID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.AppID != app.ID) {
		return invalid("Deployment tidak ditemukan")
	}
	if err != nil {
		return err
	}
	if d.Status != store.DeployPending {
		return invalid("Deployment ini sudah diproses")
	}
	// The app row may have changed since the caller loaded it (settings).
	if app, err = s.Store.App(ctx, app.ID); err != nil {
		return err
	}

	prevStatus := app.Status
	if prevStatus == store.AppStarting {
		// The deploy takes over from the start's wait, which then records nothing.
		prevStatus = store.AppRunning
	}
	if err := s.Store.SetAppStatus(ctx, app.ID, store.AppDeploying); err != nil {
		return err
	}
	running := store.DeployRunning
	if err := s.Store.UpdateDeployment(ctx, d.ID, store.DeploymentPatch{Status: &running}); err != nil {
		return err
	}
	d.Status = running

	old, hasOld := s.servingDeployment(ctx, app)
	zero := app.DeployMode == store.DeployModeZero
	failStatus := store.AppFailed
	if zero && hasOld {
		failStatus = prevStatus
	}
	tr := s.newTracer(ctx, d, hasOld, !zero)

	containerID, err := s.rollout(ctx, owner, app, &d, !zero, tr)
	if err != nil {
		s.failDeployment(ctx, owner, app, d, containerID, err, failStatus, tr)
		return err
	}

	tr.begin(ctx, stepRoute, "")
	if err := s.Store.ActivateDeployment(ctx, app.ID, d.ID); err != nil {
		s.failDeployment(ctx, owner, app, d, containerID, atStep(stepRoute, err), failStatus, tr)
		return err
	}
	if zero && hasOld {
		if err := s.switchTraffic(ctx, app, old, tr); err != nil {
			s.failDeployment(ctx, owner, app, d, containerID, atStep(stepRoute, err), failStatus, tr)
			return err
		}
	} else {
		s.refreshCaddy(ctx, app)
		tr.done(ctx, stepRoute, "")
	}
	finished := s.now().UnixMilli()
	if err := s.Store.UpdateDeployment(ctx, d.ID, store.DeploymentPatch{FinishedAt: &finished}); err != nil {
		slog.Error("set deployment finish time", "deployment", d.ID, "err", err)
	}
	s.prune(ctx, app.ID)
	s.logActivity(ctx, actor, store.ActDeploy, app.Name, fmt.Sprintf("#%d", d.Number))
	return nil
}

// rollout does the container work of a deploy. With replaceFirst the active
// container goes before the new one is created (stop-start); without it the
// old one keeps serving. On failure it returns the ID of the new container,
// if one was created, so the caller can collect its logs and remove it. Each
// step is recorded on tr; a failure carries its step (stepError).
func (s *Service) rollout(ctx context.Context, owner store.User, app store.App, d *store.Deployment, replaceFirst bool, tr *tracer) (containerID string, err error) {
	image := imageFor(app, *d)
	tr.begin(ctx, stepImage, image)
	if ok, err := s.Engine.ImageExists(ctx, image); err != nil {
		return "", atStep(stepImage, fmt.Errorf("check image %s: %w", image, err))
	} else if !ok {
		tr.progress(ctx, stepImage, "Mengunduh "+image)
		tr.save(ctx)
		if err := s.Engine.PullImage(ctx, image); err != nil {
			return "", atStep(stepImage, fmt.Errorf("pull image %s: %w", image, err))
		}
		tr.done(ctx, stepImage, "Diunduh: "+image)
	} else {
		tr.done(ctx, stepImage, "Sudah ada: "+image)
	}

	tr.begin(ctx, stepPrepare, "")
	idx, err := s.Store.NetIndex(ctx, owner.ID)
	if errors.Is(err, store.ErrNoNetIndex) {
		return "", atStep(stepPrepare, overQuota("Jaringan pengguna habis"))
	}
	if err != nil {
		return "", atStep(stepPrepare, err)
	}
	subnet := fmt.Sprintf("172.30.%d.0/24", idx)
	if err := s.Engine.EnsureNetwork(ctx, networkName(owner.ID), subnet); err != nil {
		return "", atStep(stepPrepare, fmt.Errorf("ensure network: %w", err))
	}
	if err := s.ensureDataDir(owner, app.Name); err != nil {
		return "", atStep(stepPrepare, err)
	}

	if d.HostPort, err = s.allocHostPort(ctx, d.ID); err != nil {
		return "", atStep(stepPrepare, err)
	}
	env, err := s.openSnapshot(d.EnvSnapshot)
	if err != nil {
		return "", atStep(stepPrepare, err)
	}
	dbEnv, err := s.dbEnv(ctx, app, d.Report, env)
	if err != nil {
		return "", atStep(stepPrepare, fmt.Errorf("database env: %w", err))
	}
	spec := s.ContainerSpec(app, owner, *d, env, dbEnv)
	tr.done(ctx, stepPrepare, fmt.Sprintf("Port aplikasi (di dalam container) %d, port host internal 127.0.0.1:%d", d.InternalPort, d.HostPort))

	if replaceFirst && tr.find(stepStopOld) != nil {
		tr.begin(ctx, stepStopOld, "")
		if err := s.removeActive(ctx, app); err != nil {
			return "", atStep(stepStopOld, err)
		}
		tr.done(ctx, stepStopOld, "")
	}

	tr.begin(ctx, stepCreate, "")
	containerID, err = s.Engine.CreateContainer(ctx, containerName(owner, app, *d), spec)
	if err != nil {
		return "", atStep(stepCreate, fmt.Errorf("create container: %w", err))
	}
	if err := s.Store.UpdateDeployment(ctx, d.ID, store.DeploymentPatch{ContainerID: &containerID}); err != nil {
		return containerID, atStep(stepCreate, err)
	}
	d.ContainerID = containerID
	tr.done(ctx, stepCreate, containerName(owner, app, *d))
	tr.begin(ctx, stepStart, "")
	if err := s.Engine.StartContainer(ctx, containerID); err != nil {
		return containerID, atStep(stepStart, fmt.Errorf("start container: %w", err))
	}
	tr.done(ctx, stepStart, "")
	if err := s.waitHealthy(ctx, app, containerID, d.HostPort, d.InternalPort, tr); err != nil {
		return containerID, err
	}
	return containerID, nil
}

// removeActive stops and deletes the container of the app's active deployment.
func (s *Service) removeActive(ctx context.Context, app store.App) error {
	if app.CurrentDeploymentID == nil {
		return nil
	}
	cur, err := s.Store.Deployment(ctx, *app.CurrentDeploymentID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && cur.ContainerID == "") {
		return nil
	}
	if err != nil {
		return err
	}
	s.noteStop(app.ID)
	return s.removeContainerOf(ctx, cur)
}

// removeContainerOf stops and deletes the container of deployment d, keeping
// its last output on the deployment first.
func (s *Service) removeContainerOf(ctx context.Context, d store.Deployment) error {
	if err := s.Engine.StopContainer(ctx, d.ContainerID, stopGrace); err != nil && !errors.Is(err, docker.ErrNotFound) {
		return fmt.Errorf("stop old container: %w", err)
	}
	s.keepLog(ctx, d, nil)
	if err := s.Engine.RemoveContainer(ctx, d.ContainerID); err != nil {
		return fmt.Errorf("remove old container: %w", err)
	}
	return nil
}

// waitHealthy polls 127.0.0.1:hostPort (published from the container's
// appPort) until the app answers or healthTimeoutFor(app) passes. Docker's
// port proxy accepts connections even when nothing listens in the container
// and then closes them at once, so a connect alone proves nothing: a healthy
// app stays quiet (the read times out) or sends data, while EOF or a reset
// means nobody is home. Every round also checks that the container still
// runs; one that exited fails at once. A JAR whose log still moves at the
// timeout and that has not logged "Started …" is still starting: the wait
// goes on, up to twice the timeout. Errors name the container port, the one
// the user configures. tr may be nil (Restart, Start).
func (s *Service) waitHealthy(ctx context.Context, app store.App, containerID string, hostPort, appPort int, tr *tracer) error {
	dial := s.Dial
	if dial == nil {
		dial = net.DialTimeout
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(hostPort))
	timeout := s.healthTimeoutFor(app)
	start := s.healthNow()
	deadline, limit := start.Add(timeout), start.Add(2*timeout)
	jvm := app.Kind == artifact.KindJar
	var (
		logMoved, logChecked time.Time
		lastLine             string
		started              bool
	)
	// watchLog notes when the JVM's output last changed and whether it logged "Started".
	watchLog := func(now time.Time) {
		if !jvm || now.Sub(logChecked) < startLogEvery {
			return
		}
		logChecked = now
		lines := s.readLines(ctx, containerID, startLogLines)
		started = startedInLog(lines) != nil
		if len(lines) == 0 {
			return
		}
		last := lines[len(lines)-1]
		if line := last.Time.String() + last.Text; line != lastLine {
			if lastLine != "" {
				logMoved = now
			}
			lastLine = line
		}
	}
	tr.begin(ctx, stepHealth, fmt.Sprintf("Menunggu port %d (paling lama %s)", appPort, plainDuration(timeout)))
	for attempt := 1; ; attempt++ {
		st, err := s.Engine.InspectContainer(ctx, containerID)
		if err != nil {
			return atStep(stepHealth, fmt.Errorf("inspect container: %w", err))
		}
		if !st.Running {
			return &stepError{step: stepHealth, state: &st, err: fmt.Errorf("aplikasi berhenti saat dijalankan (%s, exit code %d)", st.Status, st.ExitCode)}
		}
		if s.accepts(dial, addr) {
			tr.done(ctx, stepHealth, fmt.Sprintf("Port %d menerima koneksi setelah %s", appPort, plainDuration(s.healthNow().Sub(start))))
			return nil
		}
		now := s.healthNow()
		watchLog(now)
		stillStarting := jvm && !started && !logMoved.IsZero() && now.Sub(logMoved) < startStall && now.Before(limit)
		if now.After(deadline) && !stillStarting {
			// The wait begins right after the container starts, so it is also how long the JVM has run.
			return &stepError{step: stepHealth, waited: now.Sub(start), port: appPort,
				err: fmt.Errorf("aplikasi tidak menerima koneksi di port %d dalam %s (%d percobaan)", appPort, timeout.Round(time.Second), attempt)}
		}
		detail := fmt.Sprintf("Menunggu port %d: %s dari paling lama %s, percobaan %d", appPort, plainDuration(now.Sub(start)), plainDuration(timeout), attempt)
		if now.After(deadline) {
			detail = fmt.Sprintf("Menunggu port %d: %s; JVM masih start (log terus bertambah), ditunggu paling lama %s", appPort, plainDuration(now.Sub(start)), plainDuration(2*timeout))
		}
		tr.progress(ctx, stepHealth, detail)
		select {
		case <-ctx.Done():
			return atStep(stepHealth, ctx.Err())
		case <-time.After(s.healthInterval()):
		}
	}
}

// accepts reports whether addr holds a connection open or sends data.
func (s *Service) accepts(dial func(network, addr string, timeout time.Duration) (net.Conn, error), addr string) bool {
	conn, err := dial("tcp", addr, dialTimeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(s.healthReadWait())); err != nil {
		return false
	}
	var b [1]byte
	_, err = conn.Read(b[:])
	return err == nil || errors.Is(err, os.ErrDeadlineExceeded)
}

// failDeployment records a failed deploy: the step that failed, a plain
// diagnosis, the failed container's last output (kept on the deployment), the
// container is removed, the app gets appStatus (failed, or the status it had
// when the old version still serves) and its owner is notified. The
// deployment that was active before stays on record.
func (s *Service) failDeployment(ctx context.Context, owner store.User, app store.App, d store.Deployment, containerID string, cause error, appStatus store.AppStatus, tr *tracer) {
	ctx = context.WithoutCancel(ctx)
	var lines []docker.LogLine
	log := cause.Error()
	if containerID != "" {
		lines = s.readLines(ctx, containerID, keptLogLines)
		if tail := tailText(lines, failureLogLines); tail != "" {
			log += "\n\n" + tail
		}
		s.noteStop(app.ID)
		if err := s.Engine.RemoveContainer(ctx, containerID); err != nil {
			slog.Error("remove failed container", "container", containerID, "err", err)
		}
	}
	now := s.now().UnixMilli()
	problem := diagnose(app, d, cause, lines, now)
	if appStatus != store.AppFailed {
		problem.Kind = "deploy_kept_old"
	}
	if failed := tr.failRunning(problem.Title); problem.Step == "" {
		problem.Step = failed
	}
	status := store.DeployFailed
	patch := store.DeploymentPatch{Status: &status, FailureLog: &log, FinishedAt: &now, Steps: tr.steps, Problem: &problem}
	if lines != nil {
		saved := encodeLog(lines)
		patch.ContainerLog = &saved
	}
	if err := s.Store.UpdateDeployment(ctx, d.ID, patch); err != nil {
		slog.Error("record failed deployment", "deployment", d.ID, "err", err)
	}
	if err := s.Store.SetAppStatus(ctx, app.ID, appStatus); err != nil {
		slog.Error("set app status after failed deploy", "app", app.Name, "err", err)
	}
	s.notify(ctx, app, store.LevelError, fmt.Sprintf("Deploy %s #%d gagal", app.Name, d.Number), problem.Title)
	slog.Warn("deploy failed", "app", app.Name, "deployment", d.Number, "err", cause)
}

// tailText returns the last n lines as plain text.
func tailText(lines []docker.LogLine, n int) string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return strings.Join(out, "\n")
}

// prune keeps the newest deployments and deletes the files of the rest.
func (s *Service) prune(ctx context.Context, appID int64) {
	removed, err := s.Store.PruneDeployments(ctx, appID, keepDeployments)
	if err != nil {
		slog.Error("prune deployments", "app", appID, "err", err)
		return
	}
	if len(removed) == 0 {
		return
	}
	remaining, err := s.Store.Deployments(ctx, appID)
	if err != nil {
		slog.Error("list deployments", "app", appID, "err", err)
		return
	}
	s.removeArtifacts(removed, remaining)
}

// cloneDeployment inserts a pending deployment that reuses src's artifact,
// with the given settings and env snapshot.
func (s *Service) cloneDeployment(ctx context.Context, actor store.User, src store.Deployment, snapshot string, javaVersion *int, jvmOpts string, port int) (store.Deployment, error) {
	return s.Store.CreateDeployment(ctx, store.Deployment{
		AppID: src.AppID, ArtifactPath: src.ArtifactPath, FileName: src.FileName, SHA256: src.SHA256, Report: src.Report,
		EnvSnapshot: snapshot, JavaVersion: javaVersion, JVMOpts: jvmOpts, InternalPort: port,
		Status: store.DeployPending, CreatedBy: &actor.ID, CreatedAt: s.now().UnixMilli(),
	})
}

// PrepareRollback validates a rollback and creates the pending deployment
// that copies the old one's artifact, environment snapshot, Java version, JVM
// options and port. Run it with Deploy.
func (s *Service) PrepareRollback(ctx context.Context, user store.User, appID, deploymentID int64) (store.Deployment, error) {
	app, _, err := s.load(ctx, user, appID)
	if err != nil {
		return store.Deployment{}, err
	}
	old, err := s.Store.Deployment(ctx, deploymentID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && old.AppID != app.ID) {
		return store.Deployment{}, invalid("Deployment tidak ditemukan")
	}
	if err != nil {
		return store.Deployment{}, err
	}
	if old.Status != store.DeploySuperseded && old.Status != store.DeployActive {
		return store.Deployment{}, invalid("Hanya deployment yang pernah berjalan yang bisa dikembalikan")
	}
	if _, err := os.Stat(old.ArtifactPath); err != nil {
		return store.Deployment{}, invalid("File artefak deployment ini sudah tidak ada")
	}
	nd, err := s.cloneDeployment(ctx, user, old, old.EnvSnapshot, old.JavaVersion, old.JVMOpts, old.InternalPort)
	if err != nil {
		return store.Deployment{}, err
	}
	s.logActivity(ctx, user, store.ActRollback, app.Name, fmt.Sprintf("#%d → #%d", old.Number, nd.Number))
	return nd, nil
}

// Rollback prepares and runs a rollback.
func (s *Service) Rollback(ctx context.Context, user store.User, appID, deploymentID int64) error {
	nd, err := s.PrepareRollback(ctx, user, appID, deploymentID)
	if err != nil {
		return err
	}
	return s.Deploy(ctx, user, appID, nd.ID)
}

// PrepareRedeploy returns the deployment to run for "deploy again": the
// newest one when it is still pending; otherwise a new deployment cloned from
// it with the app's current settings and environment, so changes take effect.
func (s *Service) PrepareRedeploy(ctx context.Context, user store.User, appID int64) (store.Deployment, error) {
	app, _, err := s.load(ctx, user, appID)
	if err != nil {
		return store.Deployment{}, err
	}
	ds, err := s.Store.Deployments(ctx, appID)
	if err != nil {
		return store.Deployment{}, err
	}
	if len(ds) == 0 {
		return store.Deployment{}, invalid("Aplikasi belum punya artefak")
	}
	latest := ds[0]
	if latest.Status == store.DeployPending {
		return latest, nil
	}
	if _, err := os.Stat(latest.ArtifactPath); err != nil {
		return store.Deployment{}, invalid("File artefak sudah tidak ada; unggah versi baru")
	}
	snapshot, err := s.snapshotEnv(ctx, appID)
	if err != nil {
		return store.Deployment{}, err
	}
	return s.cloneDeployment(ctx, user, latest, snapshot, app.JavaVersion, app.JVMOpts, app.InternalPort)
}

// DeployLatest prepares and runs a redeploy.
func (s *Service) DeployLatest(ctx context.Context, user store.User, appID int64) error {
	nd, err := s.PrepareRedeploy(ctx, user, appID)
	if err != nil {
		return err
	}
	return s.Deploy(ctx, user, appID, nd.ID)
}

// activeContainer returns the container of the app's active deployment.
func (s *Service) activeContainer(ctx context.Context, app store.App) (string, error) {
	if app.CurrentDeploymentID == nil {
		return "", invalid("Aplikasi belum pernah berhasil di-deploy")
	}
	d, err := s.Store.Deployment(ctx, *app.CurrentDeploymentID)
	if err != nil {
		return "", err
	}
	if d.ContainerID == "" {
		return "", invalid("Container aplikasi tidak ada; deploy ulang")
	}
	return d.ContainerID, nil
}

func (s *Service) stopContainer(ctx context.Context, app store.App, id string) error {
	s.noteStop(app.ID)
	if err := s.Engine.StopContainer(ctx, id, stopGrace); err != nil {
		if errors.Is(err, docker.ErrNotFound) {
			return invalid("Container aplikasi tidak ada; deploy ulang")
		}
		return fmt.Errorf("stop container: %w", err)
	}
	return nil
}

func (s *Service) startContainer(ctx context.Context, app store.App, id string) error {
	s.clearCrashes(app.ID)
	s.markActive(ctx, app, store.Problem{})
	if err := s.Engine.StartContainer(ctx, id); err != nil {
		if errors.Is(err, docker.ErrNotFound) {
			return invalid("Container aplikasi tidak ada; deploy ulang")
		}
		return fmt.Errorf("start container: %w", err)
	}
	return nil
}

// Restart stops and starts the active container; the app is "starting" until
// its port opens (awaitStart).
func (s *Service) Restart(ctx context.Context, user store.User, appID int64) error {
	app, _, err := s.load(ctx, user, appID)
	if err != nil {
		return err
	}
	unlock, err := s.lockOrBusy(appID)
	if err != nil {
		return err
	}
	defer unlock()
	id, err := s.activeContainer(ctx, app)
	if err != nil {
		return err
	}
	if err := s.stopContainer(ctx, app, id); err != nil {
		return err
	}
	if err := s.startContainer(ctx, app, id); err != nil {
		return err
	}
	if err := s.Store.SetAppStatus(ctx, app.ID, store.AppStarting); err != nil {
		return err
	}
	s.logActivity(ctx, user, store.ActRestart, app.Name, "")
	s.awaitStart(ctx, app)
	return nil
}

// awaitStart waits in the background until the restarted or started active
// container opens its port, then marks the app running, or failed with the
// reason on the deployment. The app's lock is not held meanwhile, so the user
// can stop a start that hangs; the result is only recorded while the app is
// still starting the same deployment. The container stays as it is either
// way: a JVM that needs longer keeps going and its log stays readable.
func (s *Service) awaitStart(ctx context.Context, app store.App) {
	ctx = context.WithoutCancel(ctx)
	s.background(func() {
		d, err := s.Store.Deployment(ctx, *app.CurrentDeploymentID)
		if err != nil {
			slog.Error("load started deployment", "app", app.Name, "err", err)
			return
		}
		werr := s.waitHealthy(ctx, app, d.ContainerID, d.HostPort, d.InternalPort, nil)
		cur, err := s.Store.App(ctx, app.ID)
		if err != nil || cur.Status != store.AppStarting || cur.CurrentDeploymentID == nil || *cur.CurrentDeploymentID != d.ID {
			return
		}
		if werr == nil {
			if err := s.Store.SetAppStatus(ctx, app.ID, store.AppRunning); err != nil {
				slog.Error("set app running after start", "app", app.Name, "err", err)
			}
			return
		}
		lines := s.readLines(ctx, d.ContainerID, keptLogLines)
		p := diagnose(cur, d, werr, lines, s.now().UnixMilli())
		if err := s.Store.SetAppStatus(ctx, app.ID, store.AppFailed); err != nil {
			slog.Error("set app failed after start", "app", app.Name, "err", err)
		}
		s.markActive(ctx, cur, p)
		s.notify(ctx, cur, store.LevelError, fmt.Sprintf("Aplikasi %s tidak siap setelah dijalankan", app.Name), p.Title)
		slog.Warn("start failed", "app", app.Name, "err", werr)
	})
}

// Stop stops the active container and marks the app stopped.
func (s *Service) Stop(ctx context.Context, user store.User, appID int64) error {
	app, _, err := s.load(ctx, user, appID)
	if err != nil {
		return err
	}
	unlock, err := s.lockOrBusy(appID)
	if err != nil {
		return err
	}
	defer unlock()
	id, err := s.activeContainer(ctx, app)
	if err != nil {
		return err
	}
	if err := s.stopContainer(ctx, app, id); err != nil {
		return err
	}
	if err := s.Store.SetAppStatus(ctx, app.ID, store.AppStopped); err != nil {
		return err
	}
	s.markActive(ctx, app, store.Problem{Kind: "stopped_user", Title: "Dihentikan oleh " + user.Username, At: s.now().UnixMilli()})
	s.refreshCaddy(ctx, app)
	s.logActivity(ctx, user, store.ActStop, app.Name, "")
	return nil
}

// Start starts the stopped active container again.
func (s *Service) Start(ctx context.Context, user store.User, appID int64) error {
	app, _, err := s.load(ctx, user, appID)
	if err != nil {
		return err
	}
	unlock, err := s.lockOrBusy(appID)
	if err != nil {
		return err
	}
	defer unlock()
	id, err := s.activeContainer(ctx, app)
	if err != nil {
		return err
	}
	if err := s.startContainer(ctx, app, id); err != nil {
		return err
	}
	if err := s.Store.SetAppStatus(ctx, app.ID, store.AppStarting); err != nil {
		return err
	}
	s.refreshCaddy(ctx, app)
	s.logActivity(ctx, user, store.ActStart, app.Name, "")
	s.awaitStart(ctx, app)
	return nil
}

// Delete removes the app: every container labelled with it, its artifacts,
// its data folder and its rows.
func (s *Service) Delete(ctx context.Context, user store.User, appID int64) error {
	app, owner, err := s.load(ctx, user, appID)
	if err != nil {
		return err
	}
	unlock, err := s.lockOrBusy(appID)
	if err != nil {
		return err
	}
	defer unlock()

	s.noteStop(app.ID)
	list, err := s.Engine.ListContainers(ctx, map[string]string{"runova.app": strconv.FormatInt(app.ID, 10)})
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	for _, c := range list {
		if err := s.Engine.RemoveContainer(ctx, c.ID); err != nil {
			return fmt.Errorf("remove container: %w", err)
		}
	}
	for _, dir := range []string{s.artifactsRoot(owner.Username, app.Name), s.appDataDir(owner.Username, app.Name)} {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove %s: %w", dir, err)
		}
	}
	if err := s.Store.DeleteApp(ctx, app.ID); err != nil {
		return err
	}
	s.forget(app.ID)
	s.refreshCaddy(ctx, app)
	s.logActivity(ctx, user, store.ActAppDelete, app.Name, "")
	return nil
}

// markActive sets (or with an empty Kind clears) the problem of the app's
// active deployment: why it is not running right now.
func (s *Service) markActive(ctx context.Context, app store.App, p store.Problem) {
	if app.CurrentDeploymentID == nil {
		return
	}
	if err := s.Store.UpdateDeployment(context.WithoutCancel(ctx), *app.CurrentDeploymentID, store.DeploymentPatch{Problem: &p}); err != nil {
		slog.Error("record deployment problem", "app", app.Name, "err", err)
	}
}
