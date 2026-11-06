package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"runova/internal/artifact"
	"runova/internal/store"
)

func newApp(t *testing.T, s *store.Store, user store.User, name string, ipPort, mem int) store.App {
	t.Helper()
	a, err := s.CreateApp(context.Background(), store.App{
		UserID: user.ID, Name: name, Kind: artifact.KindStatic, InternalPort: 8080,
		IPPort: ipPort, MemoryMB: mem, Status: store.AppStopped, DeployMode: "stopstart", CreatedAt: 1000,
	})
	if err != nil {
		t.Fatalf("CreateApp(%s): %v", name, err)
	}
	return a
}

func newDeployment(t *testing.T, s *store.Store, app store.App) store.Deployment {
	t.Helper()
	d, err := s.CreateDeployment(context.Background(), store.Deployment{
		AppID: app.ID, ArtifactPath: "/a/" + app.Name, FileName: app.Name, SHA256: "ab", Report: artifact.Report{Kind: artifact.KindStatic},
		Status: store.DeployPending, CreatedAt: 2000,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return d
}

func TestAppNameUniquePerUser(t *testing.T) {
	s := newStore(t)
	a := mustUser(t, s, "andi", store.RoleUser)
	b := mustUser(t, s, "budi", store.RoleUser)
	newApp(t, s, a, "billing", 20000, 128)
	_, err := s.CreateApp(context.Background(), store.App{UserID: a.ID, Name: "billing", Kind: artifact.KindJar, IPPort: 20001, Status: store.AppStopped, DeployMode: "stopstart"})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same user, same name: %v, want ErrConflict", err)
	}
	newApp(t, s, b, "billing", 20001, 128)
	_, err = s.CreateApp(context.Background(), store.App{UserID: b.ID, Name: "other", Kind: artifact.KindJar, IPPort: 20000, Status: store.AppStopped, DeployMode: "stopstart"})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate ip port: %v, want ErrConflict", err)
	}
}

func TestDeploymentNumbering(t *testing.T) {
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	a1 := newApp(t, s, u, "one", 20000, 128)
	a2 := newApp(t, s, u, "two", 20001, 128)
	var got []int
	for range 3 {
		got = append(got, newDeployment(t, s, a1).Number)
	}
	if !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("numbers = %v", got)
	}
	if n := newDeployment(t, s, a2).Number; n != 1 {
		t.Fatalf("other app starts at %d, want 1", n)
	}
	list, err := s.Deployments(context.Background(), a1.ID)
	if err != nil || len(list) != 3 || list[0].Number != 3 || list[2].Number != 1 {
		t.Fatalf("Deployments = %+v, %v (newest first)", list, err)
	}
	if list[0].Report.Kind != artifact.KindStatic {
		t.Errorf("report not round-tripped: %+v", list[0].Report)
	}
}

func TestPruneKeepsActive(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	app := newApp(t, s, u, "one", 20000, 128)
	var ds []store.Deployment
	for range 12 {
		ds = append(ds, newDeployment(t, s, app))
	}
	if err := s.ActivateDeployment(ctx, app.ID, ds[0].ID); err != nil {
		t.Fatal(err)
	}
	removed, err := s.PruneDeployments(ctx, app.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var nums []int
	for _, d := range removed {
		nums = append(nums, d.Number)
	}
	slices.Sort(nums)
	if !slices.Equal(nums, []int{2, 3}) {
		t.Fatalf("removed %v, want [2 3]", nums)
	}
	left, _ := s.Deployments(ctx, app.ID)
	if len(left) != 10 || left[len(left)-1].Number != 1 {
		t.Fatalf("left = %d deployments, last #%d", len(left), left[len(left)-1].Number)
	}
}

func TestActivateDeploymentSupersedes(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	app := newApp(t, s, u, "one", 20000, 128)
	d1, d2 := newDeployment(t, s, app), newDeployment(t, s, app)
	for _, d := range []store.Deployment{d1, d2} {
		if err := s.ActivateDeployment(ctx, app.ID, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	got1, _ := s.Deployment(ctx, d1.ID)
	got2, _ := s.Deployment(ctx, d2.ID)
	cur, _ := s.App(ctx, app.ID)
	if got1.Status != store.DeploySuperseded || got2.Status != store.DeployActive ||
		cur.CurrentDeploymentID == nil || *cur.CurrentDeploymentID != d2.ID || cur.Status != store.AppRunning {
		t.Fatalf("d1=%s d2=%s app=%s current=%v", got1.Status, got2.Status, cur.Status, cur.CurrentDeploymentID)
	}
}

func TestUsedHostPorts(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	app := newApp(t, s, u, "one", 20000, 128)
	for i, st := range []store.DeployStatus{store.DeployPending, store.DeployRunning, store.DeployActive, store.DeploySuperseded, store.DeployFailed} {
		d := newDeployment(t, s, app)
		port := 30000 + i
		if err := s.UpdateDeployment(ctx, d.ID, store.DeploymentPatch{HostPort: &port, Status: &st}); err != nil {
			t.Fatal(err)
		}
	}
	used, err := s.UsedHostPorts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(used) != 3 || !used[30000] || !used[30001] || !used[30002] || used[30003] || used[30004] {
		t.Fatalf("used = %v", used)
	}
	ips, _ := s.UsedIPPorts(ctx)
	if !ips[20000] || len(ips) != 1 {
		t.Fatalf("ip ports = %v", ips)
	}
}

func TestUserUsage(t *testing.T) {
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	other := mustUser(t, s, "budi", store.RoleUser)
	newApp(t, s, u, "one", 20000, 256)
	newApp(t, s, u, "two", 20001, 512)
	newApp(t, s, other, "three", 20002, 1024)
	got, err := s.UserUsage(context.Background(), u.ID)
	if err != nil || got.Apps != 2 || got.MemoryMB != 768 {
		t.Fatalf("usage = %+v, %v; want 2, 768", got, err)
	}
	got, err = s.UserUsage(context.Background(), 999)
	if err != nil || got != (store.AppUsage{}) {
		t.Fatalf("empty usage = %+v, %v", got, err)
	}
}

func TestCascadeOnUserDelete(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	mustUser(t, s, "admin", store.RoleAdmin)
	u := mustUser(t, s, "andi", store.RoleUser)
	app := newApp(t, s, u, "one", 20000, 128)
	d := newDeployment(t, s, app)
	if err := s.ReplaceEnv(ctx, app.ID, []store.EnvEntry{{Name: "A", Value: "1"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Notify(ctx, store.Notification{UserID: u.ID, Level: store.LevelInfo, Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.App(ctx, app.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("app after cascade: %v", err)
	}
	if _, err := s.Deployment(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deployment after cascade: %v", err)
	}
	if env, _ := s.Env(ctx, app.ID); len(env) != 0 {
		t.Errorf("env after cascade: %v", env)
	}
	if n, _ := s.UnreadCount(ctx, u.ID); n != 0 {
		t.Errorf("notifications after cascade: %d", n)
	}
}

func TestNotificationsUnread(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	v := mustUser(t, s, "budi", store.RoleUser)
	for _, title := range []string{"a", "b"} {
		if err := s.Notify(ctx, store.Notification{UserID: u.ID, At: int64(len(title)), Level: store.LevelError, Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	s.Notify(ctx, store.Notification{UserID: v.ID, Level: store.LevelInfo, Title: "other"})
	if n, _ := s.UnreadCount(ctx, u.ID); n != 2 {
		t.Fatalf("unread = %d, want 2", n)
	}
	list, err := s.Notifications(ctx, u.ID, 10)
	if err != nil || len(list) != 2 || list[0].Read {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := s.MarkNotificationsRead(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.UnreadCount(ctx, u.ID); n != 0 {
		t.Fatalf("unread after read = %d", n)
	}
	if n, _ := s.UnreadCount(ctx, v.ID); n != 1 {
		t.Fatalf("other user's unread = %d, want 1", n)
	}
}

func TestEnvReplace(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	app := newApp(t, s, u, "one", 20000, 128)
	s.ReplaceEnv(ctx, app.ID, []store.EnvEntry{{Name: "B", Value: "2"}, {Name: "A", Value: "sealed", Secret: true}})
	s.ReplaceEnv(ctx, app.ID, []store.EnvEntry{{Name: "C", Value: "3"}, {Name: "A", Value: "sealed2", Secret: true}})
	env, err := s.Env(ctx, app.ID)
	if err != nil || len(env) != 2 || env[0].Name != "A" || env[0].Value != "sealed2" || !env[0].Secret || env[1].Name != "C" {
		t.Fatalf("env = %+v, %v", env, err)
	}
}

func TestNetIndexStable(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := mustUser(t, s, "andi", store.RoleUser)
	b := mustUser(t, s, "budi", store.RoleUser)
	na, err := s.NetIndex(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	nb, _ := s.NetIndex(ctx, b.ID)
	again, _ := s.NetIndex(ctx, a.ID)
	if na != 1 || nb != 2 || again != na {
		t.Fatalf("indices = %d, %d, %d; want 1, 2, 1", na, nb, again)
	}
}
