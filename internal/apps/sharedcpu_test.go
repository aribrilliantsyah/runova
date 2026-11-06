package apps_test

import (
	"context"
	"slices"
	"testing"

	"runova/internal/docker"
	"runova/internal/docker/dockertest"
)

// Containers created by a Runova that capped CPU per app lose the cap when
// Runova starts, running or stopped, without a redeploy; containers that are
// not Runova's are left alone.
func TestLiftCPULimitsClearsEveryAppContainer(t *testing.T) {
	e := newTestEnv(t)
	e.engine.Add(dockertest.Container{
		Name: "runova-budi-billing-3", State: docker.ContainerState{Running: true, Status: "running"},
		Spec: docker.ContainerSpec{Labels: map[string]string{"runova.app": "1", "runova.user": "budi"}},
	})
	e.engine.Add(dockertest.Container{
		Name: "runova-budi-worker-4", State: docker.ContainerState{Status: "exited"},
		Spec: docker.ContainerSpec{Labels: map[string]string{"runova.app": "2", "runova.user": "budi"}},
	})
	e.engine.Add(dockertest.Container{Name: "unrelated", Spec: docker.ContainerSpec{Labels: map[string]string{"x": "y"}}})

	e.svc.LiftCPULimits(context.Background())

	got := e.engine.CallsWithPrefix("cpu-unlimit ")
	slices.Sort(got)
	want := []string{"cpu-unlimit runova-budi-billing-3", "cpu-unlimit runova-budi-worker-4"}
	if !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}
