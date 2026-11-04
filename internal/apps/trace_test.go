package apps_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"runova/internal/apps"
	"runova/internal/docker"
	"runova/internal/docker/dockertest"
	"runova/internal/store"
)

// stepsOf renders a deployment's timeline as "id:status" for comparison.
func stepsOf(d store.Deployment) string {
	parts := make([]string, len(d.Steps))
	for i, s := range d.Steps {
		parts[i] = s.ID + ":" + string(s.Status)
	}
	return strings.Join(parts, " ")
}

func TestTraceSuccess(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d1 := deployFirst(t, e, u, "billing")
	got := reload(t, e, d1.ID)
	if want := "artifact:done image:done prepare:done create:done start:done health:done route:done"; stepsOf(got) != want {
		t.Fatalf("first deploy steps = %s\nwant %s", stepsOf(got), want)
	}
	for _, s := range got.Steps {
		if s.StartedAt == 0 || s.FinishedAt < s.StartedAt {
			t.Errorf("step %s times = %d..%d", s.ID, s.StartedAt, s.FinishedAt)
		}
	}
	if got.Problem != nil {
		t.Errorf("problem on a good deploy: %+v", got.Problem)
	}

	// A second (zero-downtime) deploy retires the old container at the end and keeps its output.
	e.engine.Update(got.ContainerID, func(c *dockertest.Container) {
		c.Logs = []docker.LogLine{{Stream: "stdout", Time: time.Now(), Text: "old version served"}}
	})
	d2 := newVersion(t, e, u, app)
	if err := e.svc.Deploy(ctx, u, app.ID, d2.ID); err != nil {
		t.Fatal(err)
	}
	if want := "artifact:done image:done prepare:done create:done start:done health:done route:done retire:done"; stepsOf(reload(t, e, d2.ID)) != want {
		t.Fatalf("second deploy steps = %s\nwant %s", stepsOf(reload(t, e, d2.ID)), want)
	}
	log, err := e.svc.DeploymentLog(ctx, u, app.ID, d1.ID, 100)
	if err != nil || log.Source != "saved" || len(log.Lines) != 1 || log.Lines[0].Text != "old version served" {
		t.Fatalf("log of the retired deployment = %+v, %v; want its saved output", log, err)
	}
}

func TestTraceCreateFails(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := e.createStatic(u, "one")
	e.engine.CreateErr = errors.New("no space left on device")
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err == nil {
		t.Fatal("deploy succeeded")
	}
	got := reload(t, e, d.ID)
	if want := "artifact:done image:done prepare:done create:failed start:skipped health:skipped route:skipped"; stepsOf(got) != want {
		t.Fatalf("steps = %s\nwant %s", stepsOf(got), want)
	}
	if got.Problem == nil || got.Problem.Step != "create" || got.Problem.Title != "Container tidak bisa dibuat" || !strings.Contains(got.Problem.Detail, "no space left") {
		t.Fatalf("problem = %+v", got.Problem)
	}
	if cur, _ := e.store.App(ctx, app.ID); cur.Status != store.AppFailed {
		t.Errorf("app status = %s, want failed (nothing else serves)", cur.Status)
	}
}

func TestTraceStartFails(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := e.createStatic(u, "one")
	e.engine.StartErr = errors.New("OCI runtime create failed")
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err == nil {
		t.Fatal("deploy succeeded")
	}
	got := reload(t, e, d.ID)
	if want := "artifact:done image:done prepare:done create:done start:failed health:skipped route:skipped"; stepsOf(got) != want {
		t.Fatalf("steps = %s\nwant %s", stepsOf(got), want)
	}
	if got.Problem == nil || got.Problem.Step != "start" || !strings.Contains(got.Problem.Detail, "OCI runtime") {
		t.Fatalf("problem = %+v", got.Problem)
	}
}

func TestTraceHealthTimeoutNamesPorts(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	e.setHealthy(func(string) bool { return false })
	e.svc.HealthTimeout = 30 * time.Millisecond
	e.engine.OnStart = func(c dockertest.Container) {
		e.engine.Update(c.ID, func(c *dockertest.Container) {
			c.Logs = []docker.LogLine{
				{Stream: "stdout", Time: time.Now(), Text: "  .   ____          _            __ _ _"},
				{Stream: "stdout", Time: time.Now(), Text: "INFO 7 --- [main] o.s.b.w.embedded.tomcat.TomcatWebServer : Tomcat started on port 9090 (http) with context path '/'"},
			}
		})
	}
	app, d := e.create(u, "starboom", jarReport("starboom.jar", 21), apps.CreateInput{JavaVersion: 21})
	err := e.svc.Deploy(ctx, u, app.ID, d.ID)
	if err == nil {
		t.Fatal("deploy succeeded")
	}
	got := reload(t, e, d.ID)
	if !strings.Contains(err.Error(), "port 8080") || strings.Contains(err.Error(), "port "+strconv.Itoa(got.HostPort)) {
		t.Errorf("error = %q; it must name the app port 8080, not the host port %d", err, got.HostPort)
	}
	if want := "artifact:done image:done prepare:done create:done start:done health:failed route:skipped"; stepsOf(got) != want {
		t.Fatalf("steps = %s\nwant %s", stepsOf(got), want)
	}
	p := got.Problem
	if p == nil || p.Kind != "health_timeout" || !strings.Contains(p.Title, "port 8080") || !strings.Contains(p.Hint, "port 9090") {
		t.Fatalf("problem = %+v; want the awaited port 8080 and the logged port 9090", p)
	}
	log, err := e.svc.DeploymentLog(ctx, u, app.ID, d.ID, 100)
	if err != nil || log.Source != "saved" || len(log.Lines) != 2 {
		t.Fatalf("saved log = %+v, %v", log, err)
	}
}

func TestTraceExitDuringHealthIsOOM(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	e.setHealthy(func(string) bool { return false })
	e.engine.OnStart = func(c dockertest.Container) {
		e.engine.Update(c.ID, func(c *dockertest.Container) {
			c.State.Running, c.State.Status, c.State.ExitCode, c.State.OOMKilled = false, "exited", 137, true
		})
	}
	app, d := e.createStatic(u, "hungry")
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err == nil {
		t.Fatal("deploy succeeded")
	}
	p := reload(t, e, d.ID).Problem
	if p == nil || p.Kind != "exited" || p.Title != "Memori habis (OOMKilled)" || p.ExitCode == nil || *p.ExitCode != 137 {
		t.Fatalf("problem = %+v", p)
	}
}

func TestTraceCaddyRefusedKeepsOld(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")
	rc := &snapCaddy{e: e}
	e.svc.Caddy = rc
	rc.refuse(errors.New("caddy POST /load: 400 nope"))
	d2 := newVersion(t, e, u, app)
	if err := e.svc.Deploy(ctx, u, app.ID, d2.ID); !errors.Is(err, apps.ErrCaddy) {
		t.Fatalf("Deploy = %v", err)
	}
	got := reload(t, e, d2.ID)
	if want := "artifact:done image:done prepare:done create:done start:done health:done route:failed retire:skipped"; stepsOf(got) != want {
		t.Fatalf("steps = %s\nwant %s", stepsOf(got), want)
	}
	if p := got.Problem; p == nil || p.Kind != "deploy_kept_old" || p.Step != "route" || p.Title != "Caddy menolak alamat aplikasi" || !strings.Contains(p.Detail, "400 nope") {
		t.Fatalf("problem = %+v", got.Problem)
	}
}

func TestTraceCrashLoopKeepsReason(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := deployFirst(t, e, u, "billing")
	cid := activeContainer(t, e, app)
	e.engine.Update(cid, func(c *dockertest.Container) {
		c.Logs = append(c.Logs, docker.LogLine{Stream: "stderr", Time: time.Now(), Text: "panic: config missing"})
	})
	startMonitor(t, e)
	for range 6 {
		die(t, e, app, cid)
	}
	waitFor(t, "crash-loop problem", func() bool { return reload(t, e, d.ID).Problem != nil })
	got := reload(t, e, d.ID)
	p := got.Problem
	if p.Kind != "crash_loop" || p.ExitCode == nil || *p.ExitCode != 1 || !strings.Contains(p.Detail, "6 kali") {
		t.Fatalf("problem = %+v", p)
	}
	waitFor(t, "saved log", func() bool {
		l, _ := e.svc.DeploymentLog(ctx, u, app.ID, d.ID, 10)
		return len(l.Lines) > 0
	})

	// Starting it again clears the reason; stopping it says who did.
	if err := e.svc.Start(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	if reload(t, e, d.ID).Problem != nil {
		t.Fatal("problem kept after Start")
	}
	if err := e.svc.Stop(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	if p := reload(t, e, d.ID).Problem; p == nil || p.Kind != "stopped_user" || !strings.Contains(p.Title, "budi") {
		t.Fatalf("after stop: %+v", p)
	}
}

func TestDeploymentLogIsolation(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	other := e.addUser("andi", store.RoleUser, roomyQuota)
	admin := e.addUser("root", store.RoleAdmin, roomyQuota)
	app, d := deployFirst(t, e, u, "billing")
	if _, err := e.svc.DeploymentLog(ctx, other, app.ID, d.ID, 10); !errors.Is(err, apps.ErrAppNotFound) {
		t.Fatalf("another user's log: %v, want not found", err)
	}
	if l, err := e.svc.DeploymentLog(ctx, admin, app.ID, d.ID, 10); err != nil || l.Source != "container" {
		t.Fatalf("admin: %+v %v", l, err)
	}
	app2, _ := deployFirst(t, e, other, "two")
	if _, err := e.svc.DeploymentLog(ctx, other, app2.ID, d.ID, 10); !errors.Is(err, apps.ErrValidation) {
		t.Fatalf("a deployment of another app: %v, want validation error", err)
	}
}
