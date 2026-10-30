package apps_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"runova/internal/apps"
	"runova/internal/docker"
	"runova/internal/docker/dockertest"
	"runova/internal/store"
)

func deployFirst(t *testing.T, e *testEnv, u store.User, name string) (store.App, store.Deployment) {
	t.Helper()
	app, d := e.createStatic(u, name)
	if err := e.svc.Deploy(context.Background(), u, app.ID, d.ID); err != nil {
		t.Fatalf("Deploy %s: %v", name, err)
	}
	return app, d
}

func newVersion(t *testing.T, e *testEnv, u store.User, app store.App) store.Deployment {
	t.Helper()
	d, err := e.svc.NewVersion(context.Background(), u, app.ID, e.upload(u, staticReport(app.Name)))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDeployHappyPath(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d1 := deployFirst(t, e, u, "billing")

	calls := e.engine.Calls()
	want := []string{"pull gcr.io/distroless/static-debian12", "network runova-u1 172.30.1.0/24", "create runova-budi-billing-" + strconv.FormatInt(d1.ID, 10), "start runova-budi-billing-" + strconv.FormatInt(d1.ID, 10)}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %v\nwant    %v", calls, want)
	}
	if got := e.chowned[e.svc.DataDir+"/data/budi/billing"]; got != 100000+int(u.ID) {
		t.Errorf("data dir chowned to %d", got)
	}
	if fi, err := os.Stat(e.svc.DataDir + "/data/budi/billing"); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("data dir = %v, %v; want 0700", fi, err)
	}

	d2 := newVersion(t, e, u, app)
	if err := e.svc.Deploy(ctx, u, app.ID, d2.ID); err != nil {
		t.Fatal(err)
	}
	calls = e.engine.Calls()[len(want):]
	n1, n2 := "runova-budi-billing-"+strconv.FormatInt(d1.ID, 10), "runova-budi-billing-"+strconv.FormatInt(d2.ID, 10)
	if want2 := []string{"network runova-u1 172.30.1.0/24", "create " + n2, "start " + n2, "stop " + n1, "remove " + n1}; !slices.Equal(calls, want2) {
		t.Fatalf("second deploy calls = %v\nwant %v (zero-downtime: old container goes after the new one runs; image pulled only once)", calls, want2)
	}

	got1, _ := e.store.Deployment(ctx, d1.ID)
	got2, _ := e.store.Deployment(ctx, d2.ID)
	cur, _ := e.store.App(ctx, app.ID)
	if got1.Status != store.DeploySuperseded || got2.Status != store.DeployActive || got2.FinishedAt == 0 ||
		cur.Status != store.AppRunning || cur.CurrentDeploymentID == nil || *cur.CurrentDeploymentID != d2.ID {
		t.Fatalf("d1=%s d2=%s app=%s current=%v", got1.Status, got2.Status, cur.Status, cur.CurrentDeploymentID)
	}
	if got1.HostPort == 0 || got2.HostPort < 30000 || got2.HostPort > 39999 {
		t.Errorf("host ports = %d, %d", got1.HostPort, got2.HostPort)
	}
	acts, _ := e.store.ActivityList(ctx, store.ActivityFilter{})
	if len(acts) != 2 || acts[0].Action != store.ActDeploy || acts[0].Target != "billing" || acts[0].Detail != "#2" {
		t.Errorf("activity = %+v", acts)
	}
}

func TestDeployHostPortsAvoidBusyAndUsed(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	for p := 30000; p <= 39999; p++ {
		e.portBusy[p] = p != 30042
	}
	app, d := e.createStatic(u, "one")
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.store.Deployment(ctx, d.ID); got.HostPort != 30042 {
		t.Fatalf("host port = %d, want the only free one (30042)", got.HostPort)
	}

	app2, d2 := e.createStatic(u, "two")
	err := e.svc.Deploy(ctx, u, app2.ID, d2.ID)
	if err == nil || !strings.Contains(err.Error(), "no free host port") {
		t.Fatalf("second deploy = %v, want no free port (30042 is taken by an active deployment)", err)
	}
}

func TestDeployUnhealthy(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d1 := deployFirst(t, e, u, "billing")

	e.setHealthy(func(string) bool { return false })
	e.svc.HealthTimeout = 50 * time.Millisecond
	e.engine.OnStart = func(c dockertest.Container) {
		e.engine.Update(c.ID, func(c *dockertest.Container) {
			c.Logs = []docker.LogLine{{Stream: "stderr", Time: time.Now(), Text: "panic: listen tcp :9999: bind: address already in use"}}
		})
	}
	d2 := newVersion(t, e, u, app)
	err := e.svc.Deploy(ctx, u, app.ID, d2.ID)
	if err == nil || !strings.Contains(err.Error(), "tidak menerima koneksi") {
		t.Fatalf("Deploy = %v", err)
	}

	got2, _ := e.store.Deployment(ctx, d2.ID)
	if got2.Status != store.DeployFailed || !strings.Contains(got2.FailureLog, "address already in use") || got2.FinishedAt == 0 {
		t.Fatalf("failed deployment = %+v", got2)
	}
	cur, _ := e.store.App(ctx, app.ID)
	if cur.Status != store.AppRunning || cur.CurrentDeploymentID == nil || *cur.CurrentDeploymentID != d1.ID {
		t.Errorf("app = %s current %v, want running on #1 (zero-downtime: the old version kept serving)", cur.Status, cur.CurrentDeploymentID)
	}
	got1, _ := e.store.Deployment(ctx, d1.ID)
	if got1.Status != store.DeploySuperseded && got1.Status != store.DeployActive {
		t.Errorf("old deployment lost: %s", got1.Status)
	}
	if _, ok := e.engine.Container("runova-budi-billing-" + strconv.FormatInt(d2.ID, 10)); ok {
		t.Error("failed container was not removed")
	}
	if n, _ := e.store.UnreadCount(ctx, u.ID); n != 1 {
		t.Fatalf("notifications = %d, want 1", n)
	}
	list, _ := e.store.Notifications(ctx, u.ID, 10)
	if list[0].Title != "Deploy billing #2 gagal" || list[0].Level != store.LevelError || list[0].AppID == nil || *list[0].AppID != app.ID {
		t.Errorf("notification = %+v", list[0])
	}
	if used, _ := e.store.UsedHostPorts(ctx); used[got2.HostPort] {
		t.Errorf("failed deployment still holds host port %d", got2.HostPort)
	}
}

func TestDeployFailureOnPullAndCreate(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := e.createStatic(u, "one")
	e.engine.PullErr = errors.New("manifest unknown")
	err := e.svc.Deploy(ctx, u, app.ID, d.ID)
	if err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("pull failure: %v", err)
	}
	got, _ := e.store.Deployment(ctx, d.ID)
	cur, _ := e.store.App(ctx, app.ID)
	if got.Status != store.DeployFailed || !strings.Contains(got.FailureLog, "manifest unknown") || cur.Status != store.AppFailed {
		t.Fatalf("deployment=%+v app=%s", got, cur.Status)
	}
	if _, _, err := e.svc.Get(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Deploy(ctx, u, app.ID, d.ID); !errors.Is(err, apps.ErrValidation) {
		t.Fatalf("redeploying a failed deployment: %v, want ErrValidation", err)
	}
}

func TestDeployConcurrent(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d1 := e.createStatic(u, "billing")
	d2 := newVersion(t, e, u, app)

	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.setHealthy(func(string) bool {
		once.Do(func() { close(entered) })
		<-release
		return true
	})
	done := make(chan error, 1)
	go func() { done <- e.svc.Deploy(ctx, u, app.ID, d1.ID) }()
	<-entered

	err := e.svc.Deploy(ctx, u, app.ID, d2.ID)
	if !errors.Is(err, apps.ErrBusy) || err.Error() != "Deploy sedang berjalan" {
		t.Fatalf("second Deploy = %v, want ErrBusy", err)
	}
	if err := e.svc.Restart(ctx, u, app.ID); !errors.Is(err, apps.ErrBusy) {
		t.Fatalf("Restart during deploy = %v, want ErrBusy", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Deploy(ctx, u, app.ID, d2.ID); err != nil {
		t.Fatalf("Deploy after the first finished: %v", err)
	}
}

func TestRollbackUsesOldSnapshot(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d1 := e.create(u, "billing", staticReport("billing"), apps.CreateInput{
		Env: []store.EnvEntry{{Name: "MODE", Value: "one"}, {Name: "API_KEY", Value: "k1", Secret: true}},
	})
	if err := e.svc.Deploy(ctx, u, app.ID, d1.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetEnv(ctx, u, app.ID, []store.EnvEntry{{Name: "MODE", Value: "two"}, {Name: "API_KEY", Value: "k2", Secret: true}}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeployLatest(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	ds, _ := e.store.Deployments(ctx, app.ID)
	if len(ds) != 2 || ds[0].Number != 2 {
		t.Fatalf("deployments = %+v", ds)
	}
	c2, _ := e.engine.Container("runova-budi-billing-" + strconv.FormatInt(ds[0].ID, 10))
	if !slices.Contains(c2.Spec.Env, "MODE=two") || !slices.Contains(c2.Spec.Env, "API_KEY=k2") {
		t.Fatalf("v2 env = %v", c2.Spec.Env)
	}

	if err := e.svc.Rollback(ctx, u, app.ID, d1.ID); err != nil {
		t.Fatal(err)
	}
	ds, _ = e.store.Deployments(ctx, app.ID)
	if len(ds) != 3 || ds[0].Number != 3 || ds[0].Status != store.DeployActive {
		t.Fatalf("after rollback = %+v", ds)
	}
	if ds[0].ArtifactPath != d1.ArtifactPath {
		t.Errorf("rollback artifact = %s, want #1's %s", ds[0].ArtifactPath, d1.ArtifactPath)
	}
	c3, ok := e.engine.Container("runova-budi-billing-" + strconv.FormatInt(ds[0].ID, 10))
	if !ok || !slices.Contains(c3.Spec.Env, "MODE=one") || !slices.Contains(c3.Spec.Env, "API_KEY=k1") {
		t.Fatalf("rolled-back env = %v", c3.Spec.Env)
	}
	if got, _ := e.svc.RevealEnv(ctx, u, app.ID, "MODE"); got != "two" {
		t.Errorf("rollback changed the app's current env: MODE=%s", got)
	}
	acts, _ := e.store.ActivityList(ctx, store.ActivityFilter{Limit: 1})
	if acts[0].Action != store.ActDeploy {
		t.Errorf("last activity = %+v", acts[0])
	}
	if rb, _ := e.store.ActivityList(ctx, store.ActivityFilter{}); !slices.ContainsFunc(rb, func(a store.Activity) bool { return a.Action == store.ActRollback && a.Detail == "#1 → #3" }) {
		t.Errorf("no rollback activity in %+v", rb)
	}

	failed, _ := deployFailedOne(t, e, u)
	if err := e.svc.Rollback(ctx, u, failed.AppID, failed.ID); !errors.Is(err, apps.ErrValidation) {
		t.Fatalf("rollback to a failed deployment: %v", err)
	}
}

// deployFailedOne creates an app whose only deployment failed.
func deployFailedOne(t *testing.T, e *testEnv, u store.User) (store.Deployment, store.App) {
	t.Helper()
	app, d := e.createStatic(u, "broken")
	e.engine.CreateErr = errors.New("nope")
	if err := e.svc.Deploy(context.Background(), u, app.ID, d.ID); err == nil {
		t.Fatal("setup: deploy was expected to fail")
	}
	e.engine.CreateErr = nil
	got, _ := e.store.Deployment(context.Background(), d.ID)
	return got, app
}

func TestDeleteRemovesFiles(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := deployFirst(t, e, u, "billing")
	dataDir := e.svc.DataDir + "/data/budi/billing"
	if err := os.WriteFile(dataDir+"/x", []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := e.svc.Delete(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dataDir, e.svc.DataDir + "/artifacts/budi/billing", d.ArtifactPath} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists: %v", p, err)
		}
	}
	if len(e.engine.Containers()) != 0 {
		t.Errorf("containers left: %+v", e.engine.Containers())
	}
	if _, err := e.store.App(ctx, app.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("app row: %v", err)
	}
	if err := e.svc.Delete(ctx, u, app.ID); !errors.Is(err, apps.ErrAppNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestPruneDeletesArtifactFiles(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, first := deployFirst(t, e, u, "billing")
	var paths []string
	paths = append(paths, first.ArtifactPath)
	for range 10 {
		d := newVersion(t, e, u, app)
		paths = append(paths, d.ArtifactPath)
		if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Errorf("#1 artifact still on disk after the 11th deploy: %v", err)
	}
	if _, err := os.Stat(paths[1]); err != nil {
		t.Errorf("#2 artifact must be kept: %v", err)
	}
	ds, _ := e.store.Deployments(ctx, app.ID)
	if len(ds) != 10 || ds[len(ds)-1].Number != 2 {
		t.Fatalf("kept %d deployments, oldest #%d", len(ds), ds[len(ds)-1].Number)
	}
}

func TestPruneKeepsFileSharedWithRollback(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, first := deployFirst(t, e, u, "billing")
	for range 3 {
		d := newVersion(t, e, u, app)
		if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.Rollback(ctx, u, app.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		d := newVersion(t, e, u, app)
		if err := e.svc.Deploy(ctx, u, app.ID, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	ds, _ := e.store.Deployments(ctx, app.ID)
	if slices.ContainsFunc(ds, func(d store.Deployment) bool { return d.Number == first.Number }) {
		t.Fatalf("#1 should be pruned by now: %d deployments", len(ds))
	}
	if !slices.ContainsFunc(ds, func(d store.Deployment) bool { return d.ArtifactPath == first.ArtifactPath }) {
		t.Fatalf("setup: the rollback deployment sharing #1's file was pruned too: %+v", ds)
	}
	if _, err := os.Stat(first.ArtifactPath); err != nil {
		t.Errorf("file still used by the rollback deployment was deleted: %v", err)
	}
}

func TestRestartStopStart(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := deployFirst(t, e, u, "billing")
	name := "runova-budi-billing-" + strconv.FormatInt(d.ID, 10)
	before := len(e.engine.Calls())

	if err := e.svc.Restart(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.engine.Calls()[before:]; !slices.Equal(got, []string{"stop " + name, "start " + name}) {
		t.Fatalf("restart calls = %v", got)
	}
	if err := e.svc.Stop(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	if cur, _ := e.store.App(ctx, app.ID); cur.Status != store.AppStopped {
		t.Fatalf("status after stop = %s", cur.Status)
	}
	if c, _ := e.engine.Container(name); c.State.Running {
		t.Error("container still running after Stop")
	}
	if err := e.svc.Start(ctx, u, app.ID); err != nil {
		t.Fatal(err)
	}
	if cur, _ := e.store.App(ctx, app.ID); cur.Status != store.AppRunning {
		t.Fatalf("status after start = %s", cur.Status)
	}
	acts, _ := e.store.ActivityList(ctx, store.ActivityFilter{})
	var seen []string
	for _, a := range acts {
		seen = append(seen, a.Action)
	}
	for _, want := range []string{store.ActRestart, store.ActStop, store.ActStart, store.ActDeploy} {
		if !slices.Contains(seen, want) {
			t.Errorf("activity %q missing from %v", want, seen)
		}
	}

	fresh, _ := e.createStatic(u, "never")
	if err := e.svc.Restart(ctx, u, fresh.ID); !errors.Is(err, apps.ErrValidation) {
		t.Fatalf("Restart before first deploy: %v", err)
	}
	e.engine.RemoveContainer(ctx, name)
	if err := e.svc.Start(ctx, u, app.ID); !errors.Is(err, apps.ErrValidation) {
		t.Fatalf("Start with missing container: %v", err)
	}
}

func TestAccessIsOwnerOrAdmin(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	a := e.addUser("andi", store.RoleUser, roomyQuota)
	b := e.addUser("budi", store.RoleUser, roomyQuota)
	adm := e.addUser("root1", store.RoleAdmin, roomyQuota)
	app, d := e.createStatic(a, "one")
	if err := e.svc.Deploy(ctx, b, app.ID, d.ID); !errors.Is(err, apps.ErrAppNotFound) {
		t.Fatalf("other user deploy: %v", err)
	}
	if err := e.svc.Delete(ctx, b, app.ID); !errors.Is(err, apps.ErrAppNotFound) {
		t.Fatalf("other user delete: %v", err)
	}
	if err := e.svc.Deploy(ctx, adm, app.ID, d.ID); err != nil {
		t.Fatalf("admin deploy: %v", err)
	}
	uid := strconv.Itoa(100000 + int(a.ID))
	if c, ok := e.engine.Container("runova-andi-one-" + strconv.FormatInt(d.ID, 10)); !ok || c.Spec.User != uid+":"+uid {
		t.Fatalf("admin-deployed container must run as the owner (%s): %+v", uid, c.Spec.User)
	}
	if err := e.svc.Delete(ctx, adm, app.ID); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
}

func TestNetworkIndexPerUser(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	a := e.addUser("andi", store.RoleUser, roomyQuota)
	b := e.addUser("budi", store.RoleUser, roomyQuota)
	deployFirst(t, e, b, "one")
	deployFirst(t, e, a, "two")
	nets := e.engine.CallsWithPrefix("network ")
	if len(nets) != 2 || nets[0] != "network runova-u2 172.30.1.0/24" || nets[1] != "network runova-u1 172.30.2.0/24" {
		t.Fatalf("networks = %v", nets)
	}
	_ = ctx
}
