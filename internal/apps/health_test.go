package apps_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"runova/internal/docker/dockertest"
	"runova/internal/store"
)

// Docker's port proxy accepts the connection and hangs up when nothing listens
// in the container: that must not count as a healthy app.
func TestDeployProxyHangUpIsNotHealthy(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	e.svc.HealthTimeout = 100 * time.Millisecond
	e.svc.Dial = func(string, string, time.Duration) (net.Conn, error) {
		c1, c2 := net.Pipe()
		c2.Close()
		return c1, nil
	}
	app, d := e.createStatic(u, "billing")

	err := e.svc.Deploy(ctx, u, app.ID, d.ID)
	if err == nil || !strings.Contains(err.Error(), "tidak menerima koneksi") {
		t.Fatalf("Deploy = %v, want a failed health check", err)
	}
	got, _ := e.store.Deployment(ctx, d.ID)
	cur, _ := e.store.App(ctx, app.ID)
	if got.Status != store.DeployFailed || cur.Status != store.AppFailed {
		t.Fatalf("deployment %s, app %s; want both failed", got.Status, cur.Status)
	}
}

func TestDeployFailsAtOnceWhenContainerExits(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	e.svc.HealthTimeout = time.Minute
	e.setHealthy(func(string) bool { return false })
	e.engine.OnStart = func(c dockertest.Container) {
		e.engine.Update(c.ID, func(c *dockertest.Container) {
			c.State.Running, c.State.Status, c.State.ExitCode = false, "exited", 2
		})
	}
	app, d := e.createStatic(u, "billing")

	start := time.Now()
	err := e.svc.Deploy(ctx, u, app.ID, d.ID)
	if err == nil || !strings.Contains(err.Error(), "berhenti") || !strings.Contains(err.Error(), "exit code 2") {
		t.Fatalf("Deploy = %v, want the exited container reported", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("deploy took %s; an exited container must fail without waiting for the health timeout", took)
	}
	if got, _ := e.store.Deployment(ctx, d.ID); got.Status != store.DeployFailed {
		t.Fatalf("deployment = %s", got.Status)
	}
}
