package apps_test

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"runova/internal/docker"
	"runova/internal/docker/dockertest"
	"runova/internal/store"
)

// restartLike makes the fake container behave like Docker's restart policy
// after the process died: the container runs again with a new StartedAt and a
// higher RestartCount, while ExitCode and OOMKilled are already reset (this is
// what `docker inspect` shows a moment after an OOM kill under on-failure).
// The die (and, for an OOM kill, oom) events go out first, as Docker sends them.
func restartLike(e *testEnv, app store.App, cid string, exitCode int, oom bool) {
	labels := map[string]string{"runova.app": strconv.FormatInt(app.ID, 10)}
	if oom {
		e.engine.Emit(docker.Event{ContainerID: cid, Action: "oom", Labels: labels, Time: time.Now()})
	}
	die := map[string]string{"runova.app": labels["runova.app"], "exitCode": strconv.Itoa(exitCode)}
	e.engine.Emit(docker.Event{ContainerID: cid, Action: "die", Labels: die, Time: time.Now()})
	e.engine.Update(cid, func(c *dockertest.Container) {
		c.State.Running, c.State.Status, c.State.ExitCode, c.State.OOMKilled = true, "running", 0, false
		c.State.RestartCount++
		c.State.StartedAt = c.State.StartedAt.Add(24 * time.Second)
	})
}

// restartAfter makes the started container restart on the n-th health probe;
// the port never opens.
func restartAfter(e *testEnv, app *store.App, n, exitCode int, oom bool) {
	var cid string
	e.engine.OnStart = func(c dockertest.Container) { cid = c.ID }
	probes := 0
	e.setHealthy(func(string) bool {
		probes++
		if probes == n {
			restartLike(e, *app, cid, exitCode, oom)
		}
		return false
	})
}

// The reported case: the JVM is OOM-killed while it starts, Docker's restart
// policy starts it again before the next inspect, and the deploy kept waiting
// for minutes because the container always looked "running". The deploy must
// fail at once with the OOM diagnosis, remove the new container and keep the
// old version serving.
func TestDeployFailsAtOnceWhenContainerRestarts(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d1 := deployFirst(t, e, u, "starboom")
	first := reload(t, e, d1.ID)
	e.svc.HealthTimeout = 3 * time.Second
	restartAfter(e, &app, 3, 137, true)
	d2 := newVersion(t, e, u, app)

	start := time.Now()
	err := e.svc.Deploy(ctx, u, app.ID, d2.ID)
	if err == nil {
		t.Fatal("deploy succeeded although the container was OOM-killed and restarted")
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("deploy took %s; a restarted container must fail without waiting for the health timeout", took)
	}
	failed := reload(t, e, d2.ID)
	p := failed.Problem
	if p == nil || p.Title != "Memori habis (OOMKilled)" || p.Step != "health" || p.ExitCode == nil || *p.ExitCode != 137 {
		t.Fatalf("problem = %+v; want the OOM diagnosis on the health step", p)
	}
	if !strings.Contains(p.Hint, "Naikkan memori") {
		t.Errorf("hint = %q; want advice to raise memory", p.Hint)
	}
	if !strings.Contains(p.Detail, "dinyalakan ulang") || !strings.Contains(p.Detail, "OOMKilled") {
		t.Errorf("detail = %q; want the restart and the OOM kill named", p.Detail)
	}
	i := slices.IndexFunc(failed.Steps, func(s store.DeployStep) bool { return s.ID == "health" })
	if i < 0 || failed.Steps[i].Status != store.StepFailed || failed.Steps[i].Detail != "Memori habis (OOMKilled)" {
		t.Fatalf("steps = %+v; want the health step failed with the diagnosis", failed.Steps)
	}
	if _, ok := e.engine.Container(containerOf(failed, "budi", "starboom")); ok {
		t.Error("restarting container was not removed")
	}
	if c, ok := e.engine.Container(containerOf(first, "budi", "starboom")); !ok || !c.State.Running {
		t.Fatal("old version stopped; it must keep serving")
	}
	if cur, _ := e.store.App(ctx, app.ID); cur.Status != store.AppRunning || *cur.CurrentDeploymentID != d1.ID {
		t.Fatalf("app = %s current %v; want running on #1", cur.Status, cur.CurrentDeploymentID)
	}
}

// A restart that is not an OOM kill names the exit code.
func TestDeployRestartNamesExitCode(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	e.svc.HealthTimeout = 3 * time.Second
	app, d := e.createStatic(u, "billing")
	restartAfter(e, &app, 2, 1, false)
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err == nil {
		t.Fatal("deploy succeeded")
	}
	p := reload(t, e, d.ID).Problem
	if p == nil || p.Title != "Aplikasi berhenti dan dinyalakan ulang (exit code 1)" || p.ExitCode == nil || *p.ExitCode != 1 {
		t.Fatalf("problem = %+v", p)
	}
}

// Without any event (the stream was not available) a changed StartedAt alone
// still ends the wait.
func TestDeployRestartSeenWithoutEvents(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	e.svc.HealthTimeout = 3 * time.Second
	app, d := e.createStatic(u, "billing")
	var cid string
	e.engine.OnStart = func(c dockertest.Container) { cid = c.ID }
	probes := 0
	e.setHealthy(func(string) bool {
		if probes++; probes == 2 {
			e.engine.Update(cid, func(c *dockertest.Container) { c.State.StartedAt = c.State.StartedAt.Add(time.Minute) })
		}
		return false
	})
	start := time.Now()
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("Deploy = %v after %s; want a quick failure", err, time.Since(start))
	}
	if p := reload(t, e, d.ID).Problem; p == nil || p.Title != "Aplikasi berhenti dan dinyalakan ulang" {
		t.Fatalf("problem = %+v", p)
	}
}

// Caught in Docker's "restarting" moment, the state itself carries the OOM kill.
func TestDeployRestartingStateIsOOM(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	e.svc.HealthTimeout = 3 * time.Second
	e.setHealthy(func(string) bool { return false })
	e.engine.OnStart = func(c dockertest.Container) {
		e.engine.Update(c.ID, func(c *dockertest.Container) {
			c.State.Running, c.State.Status, c.State.ExitCode, c.State.OOMKilled = true, "restarting", 137, true
		})
	}
	app, d := e.createStatic(u, "billing")
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err == nil {
		t.Fatal("deploy succeeded")
	}
	if p := reload(t, e, d.ID).Problem; p == nil || p.Title != "Memori habis (OOMKilled)" {
		t.Fatalf("problem = %+v", p)
	}
}

// Restart (and Start) wait with the same check: an app that is OOM-killed and
// restarted ends failed with the diagnosis, and its container is stopped so
// the restart policy does not keep it looping unseen (the crash monitor
// ignores a failed app).
func TestRestartThatLoopsFailsAndStops(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := deployFirst(t, e, u, "billing")
	e.svc.HealthTimeout = 3 * time.Second
	restartAfter(e, &app, 2, 137, true)
	if err := e.svc.Restart(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.store.App(ctx, app.ID)
	p := reload(t, e, d.ID).Problem
	if cur.Status != store.AppFailed || p == nil || p.Title != "Memori habis (OOMKilled)" {
		t.Fatalf("app = %s, problem = %+v; want failed with the OOM diagnosis", cur.Status, p)
	}
	if c, _ := e.engine.Container(reload(t, e, d.ID).ContainerID); c.State.Running {
		t.Fatal("container still running; the restart loop would go on unseen")
	}
}

// Once the deploy is active, OOM kills that Docker restarts are dies the crash
// monitor counts; the sixth stops the app with the OOM diagnosis.
func TestCrashLoopAfterDeployNamesOOM(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := deployFirst(t, e, u, "billing")
	cid := activeContainer(t, e, app)
	startMonitor(t, e)
	for range 6 {
		restartLike(e, app, cid, 137, true)
		e.clock.Advance(30 * time.Second)
		settle(t, e)
	}
	waitFor(t, "crash-loop notification", func() bool {
		n, _ := e.store.UnreadCount(ctx, u.ID)
		return n > 0
	})
	p := reload(t, e, d.ID).Problem
	if cur, _ := e.store.App(ctx, app.ID); cur.Status != store.AppFailed || p == nil || p.Kind != "crash_loop" || p.Title != "Memori habis (OOMKilled)" {
		t.Fatalf("app = %s, problem = %+v", cur.Status, p)
	}
}
