package api_test

import (
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"runova/internal/docker"
	"runova/internal/store"
)

// holdHealth makes the health probe of every deploy block until release is
// called; started is closed when the first probe arrives.
func (e *appEnv) holdHealth() (release func(), started <-chan struct{}) {
	hold, begun := make(chan struct{}), make(chan struct{})
	var startOnce, releaseOnce sync.Once
	e.svc.Dial = func(string, string, time.Duration) (net.Conn, error) {
		startOnce.Do(func() { close(begun) })
		<-hold
		return silentConn(), nil
	}
	return func() { releaseOnce.Do(func() { close(hold) }) }, begun
}

// While a deploy runs, a second deploy request must be refused with 409 and
// leave no pending deployment behind (the check used to come before Prepare*
// without holding the lock, so the client got 202 for a deploy that never ran).
func TestRedeployWhileDeployingIsRefusedWithoutSideEffects(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id, _ := c.createApp("billing", nil)
	c.waitStatus(id, "running")

	release, started := e.holdHealth()
	defer release()
	if r := c.do("POST", appPath(id, "/deploy"), nil); r.status != 202 {
		t.Fatalf("first deploy = %d %s", r.status, r.body)
	}
	<-started

	before, _ := e.store.Deployments(t.Context(), id)
	for name, path := range map[string]string{
		"deploy":   appPath(id, "/deploy"),
		"rollback": appPath(id, "/deployments/"+strconv.FormatInt(before[len(before)-1].ID, 10)+"/rollback"),
	} {
		if r := c.do("POST", path, nil); r.status != 409 {
			t.Errorf("%s while deploying = %d %s, want 409", name, r.status, r.body)
		}
	}
	after, _ := e.store.Deployments(t.Context(), id)
	if len(after) != len(before) {
		t.Errorf("refused requests created deployments: %d -> %d", len(before), len(after))
	}
	release()
	e.deploys.Wait()
	c.waitStatus(id, "running")
}

// The lock the request takes must be released when preparation fails.
func TestRedeployFailureReleasesLock(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id, _ := c.createApp("billing", nil)
	c.waitStatus(id, "running")

	if r := c.do("POST", appPath(id, "/deployments/9999/rollback"), nil); r.status != 400 {
		t.Fatalf("rollback of unknown deployment = %d %s", r.status, r.body)
	}
	if r := c.do("POST", appPath(id, "/deploy"), nil); r.status != 202 {
		t.Fatalf("deploy after failed rollback = %d %s; the lock leaked", r.status, r.body)
	}
	e.deploys.Wait()
}

// The server's WaitGroup covers background deploys, so shutdown can wait for them.
func TestBackgroundDeployIsTracked(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	release, started := e.holdHealth()
	defer release()
	c.createApp("billing", nil)
	<-started

	done := make(chan struct{})
	go func() { e.deploys.Wait(); close(done) }()
	select {
	case <-done:
		t.Fatal("Wait returned while a deploy was still running")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after the deploy finished")
	}
}

func TestAppStatsCachedForThreeSeconds(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	e.engine.StatsResult = docker.Stats{MemoryBytes: 1 << 20, MemoryLimit: 256 << 20, CPUPercent: 1.5}
	c := e.login("budi", "budi-password-1")
	id, _ := c.createApp("billing", nil)
	c.waitStatus(id, "running")

	read := func() (statsBody obj) {
		t.Helper()
		var b obj
		c.do("GET", appPath(id, ""), nil).into(t, &b)
		s, _ := b["stats"].(obj)
		return s
	}
	calls := func() int { return len(e.engine.CallsWithPrefix("stats ")) }

	e.clock.advance(time.Minute) // waitStatus above already filled the cache
	base := calls()
	for range 5 {
		if s := read(); s["memory_bytes"] != float64(1<<20) {
			t.Fatalf("stats = %v", s)
		}
	}
	if got := calls() - base; got != 1 {
		t.Fatalf("5 reads made %d Docker stats calls, want 1", got)
	}

	e.clock.advance(2900 * time.Millisecond)
	read()
	if got := calls() - base; got != 1 {
		t.Errorf("read inside the TTL made a new call (%d)", got)
	}
	e.clock.advance(200 * time.Millisecond)
	e.engine.StatsResult = docker.Stats{MemoryBytes: 2 << 20}
	if s := read(); s["memory_bytes"] != float64(2<<20) {
		t.Errorf("stats after TTL = %v, want fresh values", s)
	}
	if got := calls() - base; got != 2 {
		t.Errorf("calls after TTL = %d, want 2", got)
	}

	if r := c.do("POST", appPath(id, "/stop"), nil); r.status != 202 {
		t.Fatalf("stop = %d", r.status)
	}
	if s := read(); s != nil {
		t.Errorf("stopped app shows stats %v (cache must not outlive the running state)", s)
	}
}

func TestAppStatsNotSharedAcrossOwnership(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("andi", "andi-password-1", store.RoleUser)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	a := e.login("andi", "andi-password-1")
	b := e.login("budi", "budi-password-1")
	id, _ := a.createApp("billing", nil)
	a.waitStatus(id, "running")
	a.do("GET", appPath(id, ""), nil)
	if r := b.do("GET", appPath(id, ""), nil); r.status != 404 {
		t.Fatalf("other user reads app: %d %s", r.status, r.body)
	}
}
