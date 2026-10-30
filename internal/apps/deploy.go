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
	app, owner, err := s.load(ctx, user, appID)
	if err != nil {
		return err
	}
	unlock, err := s.lockOrBusy(appID)
	if err != nil {
		return err
	}
	defer unlock()
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

	containerID, err := s.rollout(ctx, owner, app, &d, !zero)
	if err != nil {
		s.failDeployment(ctx, owner, app, d, containerID, err, failStatus)
		return err
	}

	if err := s.Store.ActivateDeployment(ctx, app.ID, d.ID); err != nil {
		s.failDeployment(ctx, owner, app, d, containerID, err, failStatus)
		return err
	}
	if zero && hasOld {
		if err := s.switchTraffic(ctx, app, old); err != nil {
			s.failDeployment(ctx, owner, app, d, containerID, err, failStatus)
			return err
		}
	} else {
		s.refreshCaddy(ctx, app)
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
// if one was created, so the caller can collect its logs and remove it.
func (s *Service) rollout(ctx context.Context, owner store.User, app store.App, d *store.Deployment, replaceFirst bool) (containerID string, err error) {
	image := imageFor(app, *d)
	if ok, err := s.Engine.ImageExists(ctx, image); err != nil {
		return "", fmt.Errorf("check image %s: %w", image, err)
	} else if !ok {
		if err := s.Engine.PullImage(ctx, image); err != nil {
			return "", fmt.Errorf("pull image %s: %w", image, err)
		}
	}

	idx, err := s.Store.NetIndex(ctx, owner.ID)
	if errors.Is(err, store.ErrNoNetIndex) {
		return "", overQuota("Jaringan pengguna habis")
	}
	if err != nil {
		return "", err
	}
	subnet := fmt.Sprintf("172.30.%d.0/24", idx)
	if err := s.Engine.EnsureNetwork(ctx, networkName(owner.ID), subnet); err != nil {
		return "", fmt.Errorf("ensure network: %w", err)
	}
	if err := s.ensureDataDir(owner, app.Name); err != nil {
		return "", err
	}

	if d.HostPort, err = s.allocHostPort(ctx, d.ID); err != nil {
		return "", err
	}
	env, err := s.openSnapshot(d.EnvSnapshot)
	if err != nil {
		return "", err
	}
	spec := s.ContainerSpec(app, owner, *d, env, nil)

	if replaceFirst {
		if err := s.removeActive(ctx, app); err != nil {
			return "", err
		}
	}

	containerID, err = s.Engine.CreateContainer(ctx, containerName(owner, app, *d), spec)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}
	if err := s.Store.UpdateDeployment(ctx, d.ID, store.DeploymentPatch{ContainerID: &containerID}); err != nil {
		return containerID, err
	}
	d.ContainerID = containerID
	if err := s.Engine.StartContainer(ctx, containerID); err != nil {
		return containerID, fmt.Errorf("start container: %w", err)
	}
	if err := s.waitHealthy(ctx, d.HostPort); err != nil {
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

// removeContainerOf stops and deletes the container of deployment d.
func (s *Service) removeContainerOf(ctx context.Context, d store.Deployment) error {
	if err := s.Engine.StopContainer(ctx, d.ContainerID, stopGrace); err != nil && !errors.Is(err, docker.ErrNotFound) {
		return fmt.Errorf("stop old container: %w", err)
	}
	if err := s.Engine.RemoveContainer(ctx, d.ContainerID); err != nil {
		return fmt.Errorf("remove old container: %w", err)
	}
	return nil
}

// waitHealthy polls 127.0.0.1:port until a TCP connection succeeds or the
// health timeout passes.
func (s *Service) waitHealthy(ctx context.Context, port int) error {
	dial := s.Dial
	if dial == nil {
		dial = net.DialTimeout
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	timeout := s.healthTimeout()
	deadline := time.Now().Add(timeout)
	for {
		conn, err := dial("tcp", addr, dialTimeout)
		if err == nil {
			conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("aplikasi tidak menerima koneksi di port %d dalam %s", port, timeout.Round(time.Millisecond))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.healthInterval()):
		}
	}
}

// failDeployment records a failed deploy: the last log lines of the failed
// container go on the deployment, the container is removed, the app gets
// appStatus (failed, or the status it had when the old version still serves)
// and its owner is notified. The deployment that was active before stays on
// record.
func (s *Service) failDeployment(ctx context.Context, owner store.User, app store.App, d store.Deployment, containerID string, cause error, appStatus store.AppStatus) {
	ctx = context.WithoutCancel(ctx)
	log := cause.Error()
	if containerID != "" {
		if lines := s.tailLogs(ctx, containerID); lines != "" {
			log += "\n\n" + lines
		}
		s.noteStop(app.ID)
		if err := s.Engine.RemoveContainer(ctx, containerID); err != nil {
			slog.Error("remove failed container", "container", containerID, "err", err)
		}
	}
	status, finished := store.DeployFailed, s.now().UnixMilli()
	if err := s.Store.UpdateDeployment(ctx, d.ID, store.DeploymentPatch{Status: &status, FailureLog: &log, FinishedAt: &finished}); err != nil {
		slog.Error("record failed deployment", "deployment", d.ID, "err", err)
	}
	if err := s.Store.SetAppStatus(ctx, app.ID, appStatus); err != nil {
		slog.Error("set app status after failed deploy", "app", app.Name, "err", err)
	}
	s.notify(ctx, app, store.LevelError, fmt.Sprintf("Deploy %s #%d gagal", app.Name, d.Number), cause.Error())
	slog.Warn("deploy failed", "app", app.Name, "deployment", d.Number, "err", cause)
}

// tailLogs returns the last lines of a container's output as plain text.
func (s *Service) tailLogs(ctx context.Context, containerID string) string {
	rc, err := s.Engine.Logs(ctx, containerID, failureLogLines, false)
	if err != nil {
		return ""
	}
	defer rc.Close()
	var lines []string
	if err := docker.ReadLogs(rc, func(l docker.LogLine) error {
		lines = append(lines, l.Text)
		return nil
	}); err != nil {
		slog.Warn("read container logs", "container", containerID, "err", err)
	}
	if len(lines) > failureLogLines {
		lines = lines[len(lines)-failureLogLines:]
	}
	return strings.Join(lines, "\n")
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
	if err := s.Engine.StartContainer(ctx, id); err != nil {
		if errors.Is(err, docker.ErrNotFound) {
			return invalid("Container aplikasi tidak ada; deploy ulang")
		}
		return fmt.Errorf("start container: %w", err)
	}
	return nil
}

// Restart stops and starts the active container.
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
	if err := s.Store.SetAppStatus(ctx, app.ID, store.AppRunning); err != nil {
		return err
	}
	s.logActivity(ctx, user, store.ActRestart, app.Name, "")
	return nil
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
	if err := s.Store.SetAppStatus(ctx, app.ID, store.AppRunning); err != nil {
		return err
	}
	s.refreshCaddy(ctx, app)
	s.logActivity(ctx, user, store.ActStart, app.Name, "")
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
