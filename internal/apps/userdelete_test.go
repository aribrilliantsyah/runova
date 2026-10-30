package apps_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"runova/internal/apps"
	"runova/internal/store"
)

func TestDeleteAllForUser(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	a := e.addUser("andi", store.RoleUser, roomyQuota)
	b := e.addUser("budi", store.RoleUser, roomyQuota)
	deployFirst(t, e, a, "billing")
	deployFirst(t, e, a, "reports")
	deployFirst(t, e, b, "other")

	if err := e.svc.DeleteAllForUser(ctx, a, a); err != nil {
		t.Fatal(err)
	}

	if left := e.engine.Containers(); len(left) != 1 || left[0].Spec.Labels["runova.user"] != "budi" {
		t.Errorf("containers = %+v, want only budi's", left)
	}
	for _, sub := range []string{"artifacts", "data"} {
		if _, err := os.Stat(filepath.Join(e.svc.DataDir, sub, "andi")); !os.IsNotExist(err) {
			t.Errorf("%s/andi survived: %v", sub, err)
		}
		if _, err := os.Stat(filepath.Join(e.svc.DataDir, sub, "budi")); err != nil {
			t.Errorf("%s/budi was removed: %v", sub, err)
		}
	}
	if rm := e.engine.CallsWithPrefix("network rm "); !slices.Equal(rm, []string{"network rm runova-u1"}) {
		t.Errorf("network removals = %v, want only the deleted user's network", rm)
	}
	if list, _ := e.store.AppsByUser(ctx, a.ID); len(list) != 0 {
		t.Errorf("apps left: %+v", list)
	}
}

// A deploy in progress holds the app's lock: the cleanup must stop there and
// leave the network alone, so the caller keeps the user row and can retry.
func TestDeleteAllForUserRefusesWhileDeploying(t *testing.T) {
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

	err := e.svc.DeleteAllForUser(ctx, u, u)
	close(hold)
	if !errors.Is(err, apps.ErrBusy) {
		t.Fatalf("DeleteAllForUser = %v, want ErrBusy", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if rm := e.engine.CallsWithPrefix("network rm "); len(rm) != 0 {
		t.Errorf("network removed although cleanup failed: %v", rm)
	}
}
