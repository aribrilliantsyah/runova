package apps_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"runova/internal/apps"
	"runova/internal/docker"
	"runova/internal/docker/dockertest"
	"runova/internal/store"
)

// healthClock is a clock for the health wait that moves one second per reading,
// so a wait of minutes takes milliseconds.
type healthClock struct {
	mu    sync.Mutex
	start time.Time
	t     time.Time
}

func newHealthClock() *healthClock {
	t0 := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	return &healthClock{start: t0, t: t0}
}

func (c *healthClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

func (c *healthClock) elapsed() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t.Sub(c.start)
}

// healthyAfter makes the app open its port once d has passed on the health clock.
func (e *testEnv) healthyAfter(d time.Duration) *healthClock {
	hc := newHealthClock()
	e.svc.HealthTimeout = 0
	e.svc.HealthNow = hc.Now
	e.setHealthy(func(string) bool { return hc.elapsed() >= d })
	return hc
}

// A Spring Boot app on half a core takes about a minute to open its port, and
// longer while the old version still runs (zero-downtime redeploy): measured
// 57 s alone and 70 s next to the old JVM. A fixed 60 s wait failed the
// redeploy of an app that was fine.
func TestJarRedeployWaitsForSlowJVM(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d1 := e.create(u, "starboom", jarReport("starboom.jar", 21), apps.CreateInput{JavaVersion: 21, MemoryMB: 512, CPUMilli: 500})
	e.healthyAfter(57 * time.Second)
	if err := e.svc.Deploy(ctx, u, app.ID, d1.ID); err != nil {
		t.Fatalf("first deploy: %v", err)
	}

	e.healthyAfter(70 * time.Second)
	if err := e.svc.DeployLatest(ctx, u, app.ID); err != nil {
		t.Fatalf("redeploy of a JVM that needs 70 s on 0.5 core: %v", err)
	}
	cur, _ := e.store.App(ctx, app.ID)
	if cur.Status != store.AppRunning {
		t.Fatalf("app = %s", cur.Status)
	}
}

// The wait grows when the app gets less CPU: a JVM on a quarter core needs
// twice as long as on half a core.
func TestJarHealthWaitScalesWithCPU(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := e.create(u, "starboom", jarReport("starboom.jar", 21), apps.CreateInput{JavaVersion: 21, MemoryMB: 512, CPUMilli: 250})
	e.healthyAfter(300 * time.Second)
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err != nil {
		t.Fatalf("deploy on 0.25 core that opens its port after 300 s: %v", err)
	}
}

// Binaries start in seconds; they keep the one-minute wait.
func TestBinaryHealthWaitStaysOneMinute(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := e.createStatic(u, "billing")
	e.healthyAfter(90 * time.Second)
	err := e.svc.Deploy(ctx, u, app.ID, d.ID)
	if err == nil || !strings.Contains(err.Error(), "dalam 1m0s") {
		t.Fatalf("Deploy = %v, want a failure after 60 s", err)
	}
}

// While the JVM keeps logging its start-up and has not logged "Started", the
// wait goes on past the timeout (up to twice as long) instead of failing a
// start that is still making progress.
func TestJarStillStartingIsNotFailedEarly(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := e.create(u, "starboom", jarReport("starboom.jar", 21), apps.CreateInput{JavaVersion: 21})
	hc := e.healthyAfter(15 * time.Second)
	e.svc.HealthTimeout = 10 * time.Second
	var cid string
	e.engine.OnStart = func(c dockertest.Container) { cid = c.ID }
	n := 0
	e.setHealthy(func(string) bool {
		n++
		e.engine.Update(cid, func(c *dockertest.Container) {
			c.Logs = append(c.Logs, docker.LogLine{Stream: "stdout", Time: time.Now(), Text: "INFO 1 --- [main] o.h.e.t.j.p.i.JtaPlatformInitiator : starting step " + strings.Repeat("x", n%3)})
		})
		return hc.elapsed() >= 15*time.Second
	})
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err != nil {
		t.Fatalf("deploy of a JVM still logging its start-up: %v", err)
	}
}

// A JVM whose log stands still gets no extra time.
func TestJarSilentPastTimeoutFails(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := e.create(u, "starboom", jarReport("starboom.jar", 21), apps.CreateInput{JavaVersion: 21})
	e.healthyAfter(15 * time.Second)
	e.svc.HealthTimeout = 10 * time.Second
	e.engine.OnStart = func(c dockertest.Container) {
		e.engine.Update(c.ID, func(c *dockertest.Container) {
			c.Logs = []docker.LogLine{{Stream: "stdout", Time: time.Now(), Text: "INFO 1 --- [main] demo.StarboomApplication : Starting StarboomApplication"}}
		})
	}
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err == nil {
		t.Fatal("deploy succeeded although the JVM logged nothing new past the timeout")
	}
}

// The failure says how long the JVM ran and quotes its last log line.
func TestHealthFailureQuotesJVMProgress(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := e.create(u, "starboom", jarReport("starboom.jar", 21), apps.CreateInput{JavaVersion: 21})
	e.healthyAfter(time.Hour)
	e.svc.HealthTimeout = 10 * time.Second
	e.engine.OnStart = func(c dockertest.Container) {
		e.engine.Update(c.ID, func(c *dockertest.Container) {
			c.Logs = []docker.LogLine{
				{Stream: "stdout", Time: time.Now(), Text: "INFO 1 --- [main] demo.StarboomApplication : Starting StarboomApplication"},
				{Stream: "stdout", Time: time.Now(), Text: "INFO 1 --- [main] o.s.b.w.c.ServletWebServerApplicationContext : Root WebApplicationContext: initialization completed"},
				{Stream: "stdout", Time: time.Now(), Text: ""},
			}
		})
	}
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err == nil {
		t.Fatal("deploy succeeded")
	}
	p := reload(t, e, d.ID).Problem
	if p == nil || p.Kind != "health_timeout" {
		t.Fatalf("problem = %+v", p)
	}
	if !strings.Contains(p.Hint, "JVM sudah berjalan") || !strings.Contains(p.Hint, "belum selesai start") ||
		!strings.Contains(p.Hint, "Root WebApplicationContext: initialization completed") {
		t.Fatalf("hint = %q; want the JVM's running time, that it has not finished starting, and its last log line", p.Hint)
	}
}

// When Spring logged "Started … in N seconds" and Tomcat's port, the failure
// says the app is up on that port.
func TestHealthFailureNamesStartedPort(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := e.create(u, "starboom", jarReport("starboom.jar", 21), apps.CreateInput{JavaVersion: 21})
	e.healthyAfter(time.Hour)
	e.svc.HealthTimeout = 10 * time.Second
	e.engine.OnStart = func(c dockertest.Container) {
		e.engine.Update(c.ID, func(c *dockertest.Container) {
			c.Logs = []docker.LogLine{
				{Stream: "stdout", Time: time.Now(), Text: "INFO 1 --- [main] o.s.boot.tomcat.TomcatWebServer : Tomcat started on port 9090 (http) with context path '/'"},
				{Stream: "stdout", Time: time.Now(), Text: "INFO 1 --- [main] demo.StarboomApplication : Started StarboomApplication in 48.2 seconds (process running for 55.7)"},
			}
		})
	}
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err == nil {
		t.Fatal("deploy succeeded")
	}
	p := reload(t, e, d.ID).Problem
	if p == nil || !strings.Contains(p.Hint, "sudah start dalam 48.2 detik di port 9090") || !strings.Contains(p.Hint, "Port aplikasi di tab Lanjutan menjadi 9090") {
		t.Fatalf("hint = %q", p.Hint)
	}
}

// An app created with the port left empty runs on the detected port, and
// "Deploy ulang" keeps exactly that port.
func TestRedeployKeepsStoredAppPort(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	rep := jarReport("starboom.jar", 21)
	rep.Port, rep.PortSource = 9090, "application.properties"
	app, d1 := e.create(u, "starboom", rep, apps.CreateInput{JavaVersion: 21})
	if app.InternalPort != 9090 {
		t.Fatalf("stored port = %d, want the detected 9090", app.InternalPort)
	}
	if err := e.svc.Deploy(ctx, u, app.ID, d1.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeployLatest(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	ds, _ := e.store.Deployments(ctx, app.ID)
	for _, d := range ds {
		c, ok := e.engine.Container(containerOf(d, "budi", "starboom"))
		if d.InternalPort != 9090 || (ok && (c.Spec.InternalPort != 9090 || !hasEnv(c.Spec.Env, "SERVER_PORT=9090"))) {
			t.Fatalf("v%d: port %d, container %+v", d.Number, d.InternalPort, c.Spec)
		}
	}
}

func hasEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

// Restart and Start report "starting" until the port opens, then "running".
func TestRestartWaitsUntilHealthy(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")
	var jobs []func()
	e.svc.Go = func(f func()) { jobs = append(jobs, f) }

	for _, op := range []struct {
		name string
		do   func() error
	}{
		{"restart", func() error { return e.svc.Restart(ctx, u, app.ID) }},
		{"start", func() error {
			if err := e.svc.Stop(ctx, u, app.ID); err != nil {
				return err
			}
			return e.svc.Start(ctx, u, app.ID)
		}},
	} {
		jobs = nil
		if err := op.do(); err != nil {
			t.Fatalf("%s: %v", op.name, err)
		}
		if cur, _ := e.store.App(ctx, app.ID); cur.Status != store.AppStarting || len(jobs) != 1 {
			t.Fatalf("%s: status = %s with %d waits; want starting until the port opens", op.name, cur.Status, len(jobs))
		}
		jobs[0]()
		if cur, _ := e.store.App(ctx, app.ID); cur.Status != store.AppRunning {
			t.Fatalf("%s: status after the port opened = %s", op.name, cur.Status)
		}
	}
}

// A restarted app that never opens its port ends failed with the reason on
// its active deployment, not "running".
func TestRestartThatNeverOpensPortFails(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := deployFirst(t, e, u, "billing")
	e.setHealthy(func(string) bool { return false })
	e.svc.HealthTimeout = 20 * time.Millisecond
	if err := e.svc.Restart(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.store.App(ctx, app.ID)
	p := reload(t, e, d.ID).Problem
	if cur.Status != store.AppFailed || p == nil || p.Kind != "health_timeout" || !strings.Contains(p.Title, "port 8080") {
		t.Fatalf("app = %s, problem = %+v; want failed with the health timeout", cur.Status, p)
	}
}
