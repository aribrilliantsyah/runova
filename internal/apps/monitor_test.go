package apps_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"runova/internal/docker"
	"runova/internal/docker/dockertest"
	"runova/internal/store"
)

// startMonitor runs Monitor until the test ends and waits until it subscribed.
func startMonitor(t *testing.T, e *testEnv) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.svc.Monitor(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "monitor subscription", func() bool { return e.engine.Subscribers() == 1 })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// die delivers a die event for the app's active container and waits until the
// monitor has handled it (events are handled in order, so a following marker
// event is enough to know the die was processed).
func die(t *testing.T, e *testEnv, app store.App, containerID string) {
	t.Helper()
	labels := map[string]string{"runova.app": strconv.FormatInt(app.ID, 10), "exitCode": "1"}
	e.engine.Emit(docker.Event{ContainerID: containerID, Action: "die", Labels: labels, Time: e.clock.Now()})
}

// settle makes sure every event emitted so far was handled by sending a
// marker through the same stream and waiting for the monitor to drain it.
func settle(t *testing.T, e *testEnv) {
	t.Helper()
	e.engine.Emit(docker.Event{ContainerID: "marker", Action: "exec_start", Labels: map[string]string{"runova.app": "0"}})
	time.Sleep(30 * time.Millisecond)
}

func activeContainer(t *testing.T, e *testEnv, app store.App) string {
	t.Helper()
	cur, err := e.store.App(context.Background(), app.ID)
	if err != nil || cur.CurrentDeploymentID == nil {
		t.Fatalf("app = %+v, %v", cur, err)
	}
	d, _ := e.store.Deployment(context.Background(), *cur.CurrentDeploymentID)
	return d.ContainerID
}

func TestCrashLoopMarksFailed(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")
	cid := activeContainer(t, e, app)
	startMonitor(t, e)

	for i := range 5 {
		die(t, e, app, cid)
		e.clock.Advance(time.Minute)
		settle(t, e)
		if got, _ := e.store.App(ctx, app.ID); got.Status != store.AppRunning {
			t.Fatalf("status after %d dies = %s, want running", i+1, got.Status)
		}
	}
	if len(e.engine.CallsWithPrefix("stop ")) != 0 {
		t.Fatalf("stopped before the 6th die: %v", e.engine.Calls())
	}
	die(t, e, app, cid)
	waitFor(t, "app failed", func() bool {
		got, _ := e.store.App(ctx, app.ID)
		return got.Status == store.AppFailed
	})

	if stops := e.engine.CallsWithPrefix("stop "); len(stops) != 1 {
		t.Fatalf("stop calls = %v, want exactly one", stops)
	}
	if c, _ := e.engine.Container(cid); c.State.Running {
		t.Error("container still running")
	}
	list, _ := e.store.Notifications(ctx, u.ID, 10)
	if len(list) != 1 || list[0].Title != "Aplikasi billing crash 6 kali dalam 10 menit dan dihentikan" || list[0].Level != store.LevelError {
		t.Fatalf("notifications = %+v", list)
	}

	// Our own stop makes Docker report a die as well; it must not count again.
	die(t, e, app, cid)
	settle(t, e)
	if n, _ := e.store.UnreadCount(ctx, u.ID); n != 1 {
		t.Fatalf("notifications after the stop's own die = %d, want still 1", n)
	}
}

func TestCrashWindowSlides(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")
	cid := activeContainer(t, e, app)
	startMonitor(t, e)

	for range 5 {
		die(t, e, app, cid)
		e.clock.Advance(10 * time.Second)
		settle(t, e)
	}
	e.clock.Advance(11 * time.Minute)
	die(t, e, app, cid)
	settle(t, e)

	got, _ := e.store.App(ctx, app.ID)
	if got.Status != store.AppRunning || len(e.engine.CallsWithPrefix("stop ")) != 0 {
		t.Fatalf("status = %s, calls = %v; old dies must have left the window", got.Status, e.engine.Calls())
	}
	if n, _ := e.store.UnreadCount(ctx, u.ID); n != 0 {
		t.Fatalf("notifications = %d", n)
	}
}

func TestUserStopNotCounted(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")
	cid := activeContainer(t, e, app)
	startMonitor(t, e)

	// Six user restarts, each followed by the die Docker reports for the stop.
	for range 6 {
		if err := e.svc.Restart(ctx, u, app.ID); err != nil {
			t.Fatal(err)
		}
		die(t, e, app, cid)
		e.clock.Advance(time.Second)
		settle(t, e)
	}
	got, _ := e.store.App(ctx, app.ID)
	if got.Status != store.AppRunning {
		t.Fatalf("status = %s; restarts by the user must not count as crashes", got.Status)
	}

	// Once the grace period is over a die is a crash again.
	e.clock.Advance(time.Minute)
	for range 5 {
		die(t, e, app, cid)
		settle(t, e)
	}
	if got, _ := e.store.App(ctx, app.ID); got.Status != store.AppRunning {
		t.Fatalf("status after 5 real crashes = %s, want running", got.Status)
	}
	die(t, e, app, cid)
	waitFor(t, "app failed after 6 real crashes", func() bool {
		got, _ := e.store.App(ctx, app.ID)
		return got.Status == store.AppFailed
	})
}

func TestMonitorIgnoresOtherContainers(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")
	startMonitor(t, e)
	for range 8 {
		die(t, e, app, "some-old-container")
	}
	settle(t, e)
	if got, _ := e.store.App(ctx, app.ID); got.Status != store.AppRunning {
		t.Fatalf("status = %s; dies of containers that are not the active one must be ignored", got.Status)
	}
}

func TestMonitorReconnects(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")
	cid := activeContainer(t, e, app)
	startMonitor(t, e)

	e.engine.Disconnect(context.DeadlineExceeded)
	waitFor(t, "resubscription", func() bool { return e.engine.Subscribers() == 1 })
	for range 6 {
		die(t, e, app, cid)
	}
	waitFor(t, "app failed after reconnect", func() bool {
		got, _ := e.store.App(ctx, app.ID)
		return got.Status == store.AppFailed
	})
}

func TestReconcileOnStart(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, store.Quota{Apps: 10, MemoryMB: 4096, CPUMilli: 4000, Databases: 3, PGConnections: 20})

	live, _ := deployFirst(t, e, u, "live")
	exited, _ := deployFirst(t, e, u, "exited")
	missing, _ := deployFirst(t, e, u, "missing")
	failed, _ := deployFirst(t, e, u, "failed")
	userStopped, _ := deployFirst(t, e, u, "userstopped")
	never, _ := e.createStatic(u, "never")

	if err := e.svc.Stop(ctx, u, userStopped.ID); err != nil {
		t.Fatal(err)
	}
	e.engine.Update(activeContainer(t, e, exited), func(c *dockertest.Container) {
		c.State.Running, c.State.Status, c.State.ExitCode = false, "exited", 2
	})
	e.engine.Update(activeContainer(t, e, failed), func(c *dockertest.Container) { c.State.Running, c.State.Status = false, "exited" })
	if err := e.store.SetAppStatus(ctx, failed.ID, store.AppFailed); err != nil {
		t.Fatal(err)
	}
	if err := e.engine.RemoveContainer(ctx, activeContainer(t, e, missing)); err != nil {
		t.Fatal(err)
	}
	// Wrong status in the DB for a container that is in fact running.
	if err := e.store.SetAppStatus(ctx, live.ID, store.AppStopped); err != nil {
		t.Fatal(err)
	}

	orphan := e.engine.Add(dockertest.Container{
		Name: "runova-budi-ghost-77", State: docker.ContainerState{Running: true, Status: "running"},
		Spec: docker.ContainerSpec{Labels: map[string]string{"runova.app": "9999", "runova.user": "budi"}},
	})
	// A leftover container of a real app that is not its active deployment's.
	stale := e.engine.Add(dockertest.Container{
		Name: "runova-budi-live-0", State: docker.ContainerState{Running: false, Status: "exited"},
		Spec: docker.ContainerSpec{Labels: map[string]string{"runova.app": strconv.FormatInt(live.ID, 10), "runova.user": "budi"}},
	})
	other := e.engine.Add(dockertest.Container{Name: "unrelated", Spec: docker.ContainerSpec{Labels: map[string]string{"x": "y"}}})
	liveID := activeContainer(t, e, live)
	creates := len(e.engine.CallsWithPrefix("create "))

	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	want := map[int64]store.AppStatus{
		live.ID: store.AppRunning, exited.ID: store.AppStopped, missing.ID: store.AppFailed,
		failed.ID: store.AppFailed, userStopped.ID: store.AppStopped, never.ID: store.AppStopped,
	}
	for id, status := range want {
		if got, _ := e.store.App(ctx, id); got.Status != status {
			t.Errorf("app %s status = %s, want %s", got.Name, got.Status, status)
		}
	}
	if n := len(e.engine.CallsWithPrefix("create ")); n != creates {
		t.Errorf("Reconcile created containers (%d new); it must never redeploy", n-creates)
	}
	if c, ok := e.engine.Container(liveID); !ok || !c.State.Running {
		t.Error("the running container was disturbed")
	}
	if c, _ := e.engine.Container(activeContainer(t, e, exited)); c.State.Running {
		t.Error("an exited container was started again; Docker's restart policy owns that")
	}
	for _, gone := range []dockertest.Container{orphan, stale} {
		if _, ok := e.engine.Container(gone.ID); ok {
			t.Errorf("container %s should have been removed", gone.Name)
		}
	}
	if _, ok := e.engine.Container(other.ID); !ok {
		t.Error("a container without the runova.app label was removed")
	}

	list, _ := e.store.Notifications(ctx, u.ID, 20)
	var titles []string
	for _, n := range list {
		titles = append(titles, n.Title)
	}
	if len(titles) != 1 || titles[0] != "Container missing hilang; deploy ulang" {
		t.Fatalf("notifications = %v", titles)
	}
	if got, _ := e.store.App(ctx, missing.ID); got.CurrentDeploymentID != nil {
		t.Errorf("missing app still points at deployment %d", *got.CurrentDeploymentID)
	}

	// A second run changes nothing and raises no new notification.
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.store.UnreadCount(ctx, u.ID); n != 1 {
		t.Errorf("second Reconcile notifications = %d, want 1", n)
	}
}

func TestReconcileInterruptedDeploy(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := e.createStatic(u, "half")
	if err := e.store.SetAppStatus(ctx, app.ID, store.AppDeploying); err != nil {
		t.Fatal(err)
	}
	running := store.DeployRunning
	if err := e.store.UpdateDeployment(ctx, d.ID, store.DeploymentPatch{Status: &running}); err != nil {
		t.Fatal(err)
	}

	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := e.store.App(ctx, app.ID)
	gd, _ := e.store.Deployment(ctx, d.ID)
	if got.Status != store.AppFailed || gd.Status != store.DeployFailed {
		t.Fatalf("app=%s deployment=%s; a deploy cut short by a restart must end up failed", got.Status, gd.Status)
	}
	if used, _ := e.store.UsedHostPorts(ctx); len(used) != 0 {
		t.Errorf("host ports still held: %v", used)
	}
}

// restart policy on-failure never restarts a container that exited 0, so the
// panel must not keep showing the app as running.
func TestCleanExitMarksStopped(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")
	cid := activeContainer(t, e, app)
	startMonitor(t, e)

	labels := map[string]string{"runova.app": strconv.FormatInt(app.ID, 10), "exitCode": "0"}
	e.engine.Emit(docker.Event{ContainerID: cid, Action: "die", Labels: labels, Time: e.clock.Now()})
	waitFor(t, "app stopped", func() bool {
		got, _ := e.store.App(ctx, app.ID)
		return got.Status == store.AppStopped
	})
	if n := len(e.engine.CallsWithPrefix("stop ")); n != 0 {
		t.Errorf("stop calls = %d; a clean exit needs no stop", n)
	}
	list, _ := e.store.Notifications(ctx, u.ID, 10)
	if len(list) != 1 || list[0].Level != store.LevelInfo {
		t.Fatalf("notifications = %+v, want one info notice", list)
	}
}

// The die that echoes a user's stop arrives while the app is stopped. It must
// use up the stop note, or the first real crash after Start would go uncounted.
func TestStopEchoDoesNotSwallowLaterCrash(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")
	cid := activeContainer(t, e, app)
	startMonitor(t, e)

	if err := e.svc.Stop(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	die(t, e, app, cid)
	settle(t, e)
	if err := e.svc.Start(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	for range 6 {
		die(t, e, app, cid)
		e.clock.Advance(time.Second)
	}
	waitFor(t, "app failed after 6 real crashes", func() bool {
		got, _ := e.store.App(ctx, app.ID)
		return got.Status == store.AppFailed
	})
}
