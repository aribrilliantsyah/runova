package apps_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"runova/internal/apps"
	"runova/internal/caddy"
	"runova/internal/store"
)

// applySnapshot is what the world looked like when Caddy was told to reload.
type applySnapshot struct {
	calls []string    // Docker calls so far
	state caddy.State // what Caddy would have been given
}

// snapCaddy records a snapshot on every Apply and can refuse it.
type snapCaddy struct {
	e *testEnv

	mu    sync.Mutex
	snaps []applySnapshot
	err   error
}

func (c *snapCaddy) Apply(ctx context.Context) error {
	state, err := apps.CaddyState(ctx, c.e.store, "127.0.0.1:7070")
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snaps = append(c.snaps, applySnapshot{calls: c.e.engine.Calls(), state: state})
	return c.err
}

func (c *snapCaddy) snapshots() []applySnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.snaps)
}

func (c *snapCaddy) refuse(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

func containerOf(d store.Deployment, owner, app string) string {
	return "runova-" + owner + "-" + app + "-" + strconv.FormatInt(d.ID, 10)
}

func upstreamOf(d store.Deployment) string { return "127.0.0.1:" + strconv.Itoa(d.HostPort) }

func reload(t *testing.T, e *testEnv, id int64) store.Deployment {
	t.Helper()
	d, err := e.store.Deployment(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestNewAppDefaultsZero(t *testing.T) {
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(u, "billing")
	if app.DeployMode != store.DeployModeZero {
		t.Fatalf("DeployMode = %q, want zero", app.DeployMode)
	}
	got, _ := e.store.App(context.Background(), app.ID)
	if got.DeployMode != store.DeployModeZero {
		t.Fatalf("stored DeployMode = %q", got.DeployMode)
	}
}

func TestZeroDowntimeSwitchesUpstream(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d1 := deployFirst(t, e, u, "billing")
	if _, err := e.store.AddDomain(ctx, app.ID, "b.co", 1); err != nil {
		t.Fatal(err)
	}
	rc := &snapCaddy{e: e}
	e.svc.Caddy = rc
	first := reload(t, e, d1.ID)
	before := len(e.engine.Calls())

	d2 := newVersion(t, e, u, app)
	if err := e.svc.Deploy(ctx, u, app.ID, d2.ID); err != nil {
		t.Fatal(err)
	}
	second := reload(t, e, d2.ID)
	n1, n2 := containerOf(first, "budi", "billing"), containerOf(second, "budi", "billing")

	if want := []string{"network runova-u1 172.30.1.0/24", "create " + n2, "start " + n2, "stop " + n1, "remove " + n1}; !slices.Equal(e.engine.Calls()[before:], want) {
		t.Fatalf("calls = %v\nwant   %v (new container first, old one only after the switch)", e.engine.Calls()[before:], want)
	}
	if second.HostPort == first.HostPort {
		t.Fatalf("both deployments on host port %d", second.HostPort)
	}

	snaps := rc.snapshots()
	if len(snaps) != 1 {
		t.Fatalf("Apply calls = %d, want 1", len(snaps))
	}
	s := snaps[0]
	if !slices.Contains(s.calls, "start "+n2) || slices.Contains(s.calls, "stop "+n1) {
		t.Errorf("Apply ran with calls %v; it must come after the new container is up and before the old one stops", s.calls)
	}
	if len(s.state.Sites) != 1 || s.state.Sites[0] != (caddy.Site{Host: "b.co", Upstream: upstreamOf(second)}) {
		t.Errorf("state at Apply = %+v, want b.co → %s", s.state.Sites, upstreamOf(second))
	}

	used, _ := e.store.UsedHostPorts(ctx)
	if used[first.HostPort] || !used[second.HostPort] {
		t.Errorf("used host ports = %v; the old port %d must be free and the new %d taken", used, first.HostPort, second.HostPort)
	}
	if got := reload(t, e, d1.ID); got.Status != store.DeploySuperseded {
		t.Errorf("old deployment = %s", got.Status)
	}
	if cur, _ := e.store.App(ctx, app.ID); cur.Status != store.AppRunning || *cur.CurrentDeploymentID != d2.ID {
		t.Errorf("app = %s current %v", cur.Status, cur.CurrentDeploymentID)
	}
	if _, ok := e.engine.Container(n1); ok {
		t.Error("old container still exists")
	}
}

func TestZeroDowntimeKeepsRouteWhileDeploying(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d1 := deployFirst(t, e, u, "billing")
	if _, err := e.store.AddDomain(ctx, app.ID, "b.co", 1); err != nil {
		t.Fatal(err)
	}
	first := reload(t, e, d1.ID)

	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.setHealthy(func(string) bool {
		once.Do(func() { close(entered) })
		<-release
		return true
	})
	d2 := newVersion(t, e, u, app)
	done := make(chan error, 1)
	go func() { done <- e.svc.Deploy(ctx, u, app.ID, d2.ID) }()
	<-entered

	state, err := apps.CaddyState(ctx, e.store, "127.0.0.1:7070")
	if err != nil {
		t.Fatal(err)
	}
	if cur, _ := e.store.App(ctx, app.ID); cur.Status != store.AppDeploying {
		t.Fatalf("setup: status = %s", cur.Status)
	}
	if len(state.Sites) != 1 || state.Sites[0].Upstream != upstreamOf(first) {
		t.Errorf("while the new version starts, the route is %+v, want it to stay on %s", state.Sites, upstreamOf(first))
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestZeroDowntimeFailureKeepsOld(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d1 := deployFirst(t, e, u, "billing")
	rc := &snapCaddy{e: e}
	e.svc.Caddy = rc
	first := reload(t, e, d1.ID)
	n1 := containerOf(first, "budi", "billing")

	e.setHealthy(func(addr string) bool { return addr == "127.0.0.1:"+strconv.Itoa(first.HostPort) })
	e.svc.HealthTimeout = 50 * time.Millisecond
	d2 := newVersion(t, e, u, app)
	err := e.svc.Deploy(ctx, u, app.ID, d2.ID)
	if err == nil || !strings.Contains(err.Error(), "tidak menerima koneksi") {
		t.Fatalf("Deploy = %v", err)
	}

	if slices.Contains(e.engine.Calls(), "stop "+n1) || slices.Contains(e.engine.Calls(), "remove "+n1) {
		t.Fatalf("old container touched: %v", e.engine.Calls())
	}
	if c, ok := e.engine.Container(n1); !ok || !c.State.Running {
		t.Fatalf("old container = %+v, %v; want it still running", c.State, ok)
	}
	failed := reload(t, e, d2.ID)
	if failed.Status != store.DeployFailed || failed.FinishedAt == 0 {
		t.Fatalf("new deployment = %+v", failed)
	}
	if _, ok := e.engine.Container(containerOf(failed, "budi", "billing")); ok {
		t.Error("failed container was not removed")
	}
	cur, _ := e.store.App(ctx, app.ID)
	if cur.Status != store.AppRunning || cur.CurrentDeploymentID == nil || *cur.CurrentDeploymentID != d1.ID {
		t.Fatalf("app = %s current %v, want running on #1", cur.Status, cur.CurrentDeploymentID)
	}
	if got := reload(t, e, d1.ID); got.Status != store.DeployActive {
		t.Errorf("old deployment = %s, want active", got.Status)
	}
	if len(rc.snapshots()) != 0 {
		t.Error("Caddy was reloaded for a deployment that never became healthy")
	}
	list, _ := e.store.Notifications(ctx, u.ID, 10)
	if len(list) != 1 || list[0].Title != "Deploy billing #2 gagal" || list[0].Level != store.LevelError {
		t.Fatalf("notifications = %+v", list)
	}
	if used, _ := e.store.UsedHostPorts(ctx); used[failed.HostPort] {
		t.Errorf("failed deployment still holds host port %d", failed.HostPort)
	}
}

func TestZeroDowntimeCaddyRefusesKeepsOld(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d1 := deployFirst(t, e, u, "billing")
	rc := &snapCaddy{e: e}
	e.svc.Caddy = rc
	first := reload(t, e, d1.ID)
	n1 := containerOf(first, "budi", "billing")

	rc.refuse(errors.New("caddy POST /load: 400 nope"))
	d2 := newVersion(t, e, u, app)
	err := e.svc.Deploy(ctx, u, app.ID, d2.ID)
	if !errors.Is(err, apps.ErrCaddy) {
		t.Fatalf("Deploy = %v, want ErrCaddy", err)
	}
	if c, ok := e.engine.Container(n1); !ok || !c.State.Running {
		t.Fatal("old container stopped although Caddy still points at it")
	}
	cur, _ := e.store.App(ctx, app.ID)
	if cur.Status != store.AppRunning || *cur.CurrentDeploymentID != d1.ID {
		t.Fatalf("app = %s current %v, want running on #1", cur.Status, cur.CurrentDeploymentID)
	}
	if got := reload(t, e, d1.ID); got.Status != store.DeployActive {
		t.Errorf("#1 = %s, want active again", got.Status)
	}
	if got := reload(t, e, d2.ID); got.Status != store.DeployFailed || !strings.Contains(got.FailureLog, "Caddy menolak") {
		t.Errorf("#2 = %s %q", got.Status, got.FailureLog)
	}
	if _, ok := e.engine.Container(containerOf(reload(t, e, d2.ID), "budi", "billing")); ok {
		t.Error("new container left behind")
	}
}

func TestStopStartStillWorks(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d1 := deployFirst(t, e, u, "billing")
	mode := store.DeployModeStopStart
	if _, _, err := e.svc.UpdateSettings(ctx, u, app.ID, apps.SettingsInput{DeployMode: &mode}); err != nil {
		t.Fatal(err)
	}
	rc := &snapCaddy{e: e}
	e.svc.Caddy = rc
	n1 := containerOf(reload(t, e, d1.ID), "budi", "billing")
	before := len(e.engine.Calls())

	d2 := newVersion(t, e, u, app)
	if err := e.svc.Deploy(ctx, u, app.ID, d2.ID); err != nil {
		t.Fatal(err)
	}
	n2 := containerOf(reload(t, e, d2.ID), "budi", "billing")
	if want := []string{"network runova-u1 172.30.1.0/24", "stop " + n1, "remove " + n1, "create " + n2, "start " + n2}; !slices.Equal(e.engine.Calls()[before:], want) {
		t.Fatalf("calls = %v\nwant   %v", e.engine.Calls()[before:], want)
	}
	snaps := rc.snapshots()
	if len(snaps) != 1 || !slices.Contains(snaps[0].calls, "start "+n2) {
		t.Fatalf("Apply snapshots = %+v, want one after the new container started", snaps)
	}
}

func TestZeroModeFirstDeployHasNothingToReplace(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	rc := &snapCaddy{e: e}
	e.svc.Caddy = rc
	app, d := e.createStatic(u, "billing")

	e.engine.CreateErr = errors.New("nope")
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err == nil {
		t.Fatal("deploy was expected to fail")
	}
	if cur, _ := e.store.App(ctx, app.ID); cur.Status != store.AppFailed {
		t.Fatalf("first deploy failed → status %s, want failed (there is no earlier version to fall back on)", cur.Status)
	}
	e.engine.CreateErr = nil
	d2, err := e.svc.PrepareRedeploy(ctx, u, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Deploy(ctx, u, app.ID, d2.ID); err != nil {
		t.Fatal(err)
	}
	if got := len(rc.snapshots()); got != 1 {
		t.Errorf("Apply calls = %d, want 1", got)
	}
}

func TestStopStartDeleteReloadCaddy(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d1 := deployFirst(t, e, u, "billing")
	if _, err := e.store.AddDomain(ctx, app.ID, "b.co", 1); err != nil {
		t.Fatal(err)
	}
	rc := &snapCaddy{e: e}
	e.svc.Caddy = rc
	first := reload(t, e, d1.ID)

	if err := e.svc.Stop(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	snaps := rc.snapshots()
	if len(snaps) != 1 || len(snaps[0].state.Sites) != 0 {
		t.Fatalf("after Stop: %+v, want one Apply without the site", snaps)
	}
	if err := e.svc.Start(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	snaps = rc.snapshots()
	if len(snaps) != 2 || len(snaps[1].state.Sites) != 1 || snaps[1].state.Sites[0].Upstream != upstreamOf(first) {
		t.Fatalf("after Start: %+v, want the site back", snaps)
	}
	if err := e.svc.Delete(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	snaps = rc.snapshots()
	if len(snaps) != 3 || len(snaps[2].state.Sites) != 0 {
		t.Fatalf("after Delete: %+v, want one more Apply without the site", snaps)
	}
	if n := len(snaps[2].calls); n == 0 {
		t.Fatal("setup")
	}
}

func TestStopStillWorksWhenCaddyRefuses(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "billing")
	rc := &snapCaddy{e: e}
	rc.refuse(errors.New("down"))
	e.svc.Caddy = rc
	if err := e.svc.Stop(ctx, u, app.ID); err != nil {
		t.Fatalf("Stop = %v; the container is stopped either way, Caddy catches up on the next change", err)
	}
	if cur, _ := e.store.App(ctx, app.ID); cur.Status != store.AppStopped {
		t.Fatalf("status = %s", cur.Status)
	}
}
