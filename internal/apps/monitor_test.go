package apps_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"runova/internal/docker"
	"runova/internal/docker/dockertest"
	"runova/internal/store"
)

// startMonitor runs Monitor until the test ends and waits until it subscribed
// and finished its start-up Reconcile, which holds every app's lock.
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
	// Events are read only after Reconcile returns, so once a marker is
	// drained from the buffered stream the start-up reconcile is over.
	e.engine.Emit(docker.Event{ContainerID: "marker", Action: "exec_start", Labels: map[string]string{"runova.app": "0"}})
	waitFor(t, "start-up reconcile", func() bool { return e.engine.Pending() == 0 })
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
	// giveUp marks the app failed first, then stops it, then notifies: wait for the last step.
	waitFor(t, "crash-loop notification", func() bool {
		n, _ := e.store.UnreadCount(ctx, u.ID)
		return n > 0
	})
	if got, _ := e.store.App(ctx, app.ID); got.Status != store.AppFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}

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
	u := e.addUser("budi", store.RoleUser, store.Quota{Apps: 10, MemoryMB: 4096, Databases: 3, PGConnections: 20, DiskMB: 4096})

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
		live.ID: store.AppRunning, exited.ID: store.AppRunning, missing.ID: store.AppFailed,
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
	if c, _ := e.engine.Container(activeContainer(t, e, exited)); !c.State.Running {
		t.Error("an exited container of a running app was not started again")
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
	slices.Sort(titles)
	if len(titles) != 2 || titles[0] != "Aplikasi exited dinyalakan lagi setelah server dimulai ulang" || titles[1] != "Container missing hilang; deploy ulang" {
		t.Fatalf("notifications = %v", titles)
	}
	if got, _ := e.store.App(ctx, missing.ID); got.CurrentDeploymentID != nil {
		t.Errorf("missing app still points at deployment %d", *got.CurrentDeploymentID)
	}

	// A second run changes nothing and raises no new notification.
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.store.UnreadCount(ctx, u.ID); n != 2 {
		t.Errorf("second Reconcile notifications = %d, want 2", n)
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
	// The status is written first, the notification last: wait for the latter.
	waitFor(t, "clean-exit notification", func() bool {
		n, _ := e.store.UnreadCount(ctx, u.ID)
		return n > 0
	})
	if got, _ := e.store.App(ctx, app.ID); got.Status != store.AppStopped {
		t.Fatalf("status = %s, want stopped", got.Status)
	}
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
	other, _ := deployFirst(t, e, u, "other")
	otherCID := activeContainer(t, e, other)
	startMonitor(t, e)

	if err := e.svc.Stop(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	die(t, e, app, cid)
	// Events are handled in order: once the other app's clean exit has been
	// applied, the echo of the stop before it has been handled too.
	e.engine.Emit(docker.Event{ContainerID: otherCID, Action: "die", Time: e.clock.Now(),
		Labels: map[string]string{"runova.app": strconv.FormatInt(other.ID, 10), "exitCode": "0"}})
	waitFor(t, "barrier event handled", func() bool {
		got, _ := e.store.App(ctx, other.ID)
		return got.Status == store.AppStopped
	})

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

// A container that stops while the event stream is down produces no event;
// the reconcile after the reconnect has to notice it.
func TestMonitorReconcilesAfterReconnect(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")
	cid := activeContainer(t, e, app)
	e.svc.ReconnectDelay = 300 * time.Millisecond
	startMonitor(t, e)

	e.engine.Disconnect(errors.New("stream cut"))
	// The monitor waits ReconnectDelay before it subscribes again; the
	// container exits meanwhile and no event reports it.
	e.engine.Update(cid, func(c *dockertest.Container) { c.State.Running, c.State.Status = false, "exited" })

	waitFor(t, "app stopped by the reconcile after reconnect", func() bool {
		got, _ := e.store.App(ctx, app.ID)
		return got.Status == store.AppStopped
	})
}

// Reconcile runs next to deploys now: it must leave an app alone while its
// change lock is held, including the container a deploy has just created.
func TestReconcileSkipsBusyApp(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")
	d2 := newVersion(t, e, u, app)

	hold, started := make(chan struct{}), make(chan struct{})
	e.setHealthy(func(string) bool {
		select {
		case <-started:
		default:
			close(started)
		}
		<-hold
		return true
	})
	done := make(chan error, 1)
	go func() { done <- e.svc.Deploy(ctx, u, app.ID, d2.ID) }()
	<-started

	// The new container exists and is not yet recorded as the active one.
	err := e.svc.Reconcile(ctx)
	close(hold)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("deploy: %v (reconcile must not remove the container being deployed)", err)
	}
	if got := len(e.engine.Containers()); got != 1 {
		t.Errorf("containers after deploy = %d, want 1", got)
	}
}

func exitContainer(t *testing.T, e *testEnv, app store.App, code int) string {
	t.Helper()
	cid := activeContainer(t, e, app)
	e.engine.Update(cid, func(c *dockertest.Container) {
		c.State.Running, c.State.Status, c.State.ExitCode = false, "exited", code
	})
	return cid
}

// The server rebooted: Docker stopped the containers (a clean exit on SIGTERM
// is not restarted by on-failure). Apps that ought to run are started again;
// an app the user stopped and one that crash-looped stay down.
func TestReconcileStartsAppsThatShouldRun(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, store.Quota{Apps: 10, MemoryMB: 4096, CPUMilli: 4000, Databases: 3, PGConnections: 20, DiskMB: 4096})

	running, _ := deployFirst(t, e, u, "running")
	starting, _ := deployFirst(t, e, u, "starting")
	userStopped, _ := deployFirst(t, e, u, "userstopped")
	failed, _ := deployFirst(t, e, u, "failed")
	untouched, _ := deployFirst(t, e, u, "untouched")
	if err := e.svc.Stop(ctx, u, userStopped.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetAppStatus(ctx, starting.ID, store.AppStarting); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetAppStatus(ctx, failed.ID, store.AppFailed); err != nil {
		t.Fatal(err)
	}
	runningCID := exitContainer(t, e, running, 0)
	startingCID := exitContainer(t, e, starting, 0)
	stoppedCID := exitContainer(t, e, userStopped, 0)
	failedCID := exitContainer(t, e, failed, 0)
	creates := len(e.engine.CallsWithPrefix("create "))
	starts := len(e.engine.CallsWithPrefix("start "))

	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{runningCID, startingCID} {
		if c, _ := e.engine.Container(id); !c.State.Running {
			t.Errorf("container %s should have been started again", c.Name)
		}
	}
	for _, id := range []string{stoppedCID, failedCID} {
		if c, _ := e.engine.Container(id); c.State.Running {
			t.Errorf("container %s was started although the app must stay down", c.Name)
		}
	}
	if c, _ := e.engine.Container(activeContainer(t, e, untouched)); !c.State.Running {
		t.Error("a running container was disturbed")
	}
	want := map[int64]store.AppStatus{
		running.ID: store.AppRunning, starting.ID: store.AppRunning, userStopped.ID: store.AppStopped,
		failed.ID: store.AppFailed, untouched.ID: store.AppRunning,
	}
	for id, status := range want {
		if got, _ := e.store.App(ctx, id); got.Status != status {
			t.Errorf("app %s status = %s, want %s", got.Name, got.Status, status)
		}
	}
	if n := len(e.engine.CallsWithPrefix("create ")); n != creates {
		t.Errorf("Reconcile created %d containers; it must only start existing ones", n-creates)
	}
	if n := len(e.engine.CallsWithPrefix("start ")) - starts; n != 2 {
		t.Errorf("start calls = %d, want 2 (running, starting)", n)
	}

	list, _ := e.store.Notifications(ctx, u.ID, 20)
	var titles []string
	for _, n := range list {
		titles = append(titles, n.Title)
	}
	slices.Sort(titles)
	wantTitles := []string{
		"Aplikasi running dinyalakan lagi setelah server dimulai ulang",
		"Aplikasi starting dinyalakan lagi setelah server dimulai ulang",
	}
	if !slices.Equal(titles, wantTitles) {
		t.Errorf("notifications = %v, want %v", titles, wantTitles)
	}

	// Everything runs now: another Reconcile starts nothing and notifies nobody.
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(e.engine.CallsWithPrefix("start ")) - starts; n != 2 {
		t.Errorf("second Reconcile started containers: %d calls", n-2)
	}
	if n, _ := e.store.UnreadCount(ctx, u.ID); n != 2 {
		t.Errorf("notifications after second Reconcile = %d, want 2", n)
	}
}

// A container whose start fails ends up failed with a notification, not stuck "starting".
func TestReconcileStartFailureMarksFailed(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")
	exitContainer(t, e, app, 0)
	e.engine.StartErr = errors.New("port is already allocated")

	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.store.App(ctx, app.ID); got.Status != store.AppFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	list, _ := e.store.Notifications(ctx, u.ID, 5)
	if len(list) != 1 || list[0].Level != store.LevelError {
		t.Fatalf("notifications = %+v, want one error", list)
	}
}

// Many JVMs started together fight for the CPU: the revive starts one app
// every ReviveGap, not all at once.
func TestReconcileStaggersStarts(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, store.Quota{Apps: 10, MemoryMB: 4096, CPUMilli: 4000, Databases: 3, PGConnections: 20, DiskMB: 4096})
	const gap = 60 * time.Millisecond
	e.svc.ReviveGap = gap
	for _, name := range []string{"aaa", "bbb", "ccc"} {
		app, _ := deployFirst(t, e, u, name)
		exitContainer(t, e, app, 0)
	}
	var mu sync.Mutex
	var at []time.Time
	e.engine.OnStart = func(dockertest.Container) {
		mu.Lock()
		defer mu.Unlock()
		at = append(at, time.Now())
	}

	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(at) != 3 {
		t.Fatalf("started %d containers, want 3", len(at))
	}
	for i := 1; i < len(at); i++ {
		if d := at[i].Sub(at[i-1]); d < gap-10*time.Millisecond {
			t.Errorf("start %d came %v after the previous one, want at least %v", i, d, gap)
		}
	}
}
