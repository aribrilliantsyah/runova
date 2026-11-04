package update

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeSource struct {
	mu     sync.Mutex
	latest string
	err    error
	calls  int
}

func (f *fakeSource) Latest(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.latest, f.err
}

func (f *fakeSource) ReleaseURL(v string) string { return "https://example.test/tag/v" + v }

func (f *fakeSource) set(latest string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latest, f.err = latest, err
}

func (f *fakeSource) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var epoch = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

func newChecker(current string, src *fakeSource) *Checker {
	return &Checker{Current: current, Source: src, Now: func() time.Time { return epoch }}
}

func TestCheckerStatus(t *testing.T) {
	for _, tc := range []struct {
		name            string
		current, latest string
		available       bool
	}{
		{"newer", "0.2.0", "0.3.0", true},
		{"same", "0.3.0", "0.3.0", false},
		{"older", "0.4.0", "0.3.0", false},
		{"pre-release installed", "0.3.0-rc.1", "0.3.0", true},
		{"unparsable installed", "weird", "0.3.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newChecker(tc.current, &fakeSource{latest: tc.latest})
			c.Check(context.Background())
			st := c.Status(context.Background())
			if st.UpdateAvailable != tc.available || st.Latest != tc.latest || st.Current != tc.current || !st.Enabled {
				t.Fatalf("status = %+v", st)
			}
			if st.CheckedAt != epoch.UnixMilli() || st.ReleaseURL != "https://example.test/tag/v"+tc.latest {
				t.Fatalf("status = %+v", st)
			}
		})
	}
}

func TestCheckerBeforeFirstCheck(t *testing.T) {
	st := newChecker("0.2.0", &fakeSource{}).Status(context.Background())
	if st.UpdateAvailable || st.Latest != "" || st.CheckedAt != 0 || st.ReleaseURL != "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestCheckerDevNeverChecks(t *testing.T) {
	src := &fakeSource{latest: "9.9.9"}
	c := newChecker("dev", src)
	c.Check(context.Background())
	if src.count() != 0 || c.Status(context.Background()).UpdateAvailable {
		t.Fatal("dev build looked for updates")
	}
}

func TestCheckerDisabled(t *testing.T) {
	src := &fakeSource{latest: "0.3.0"}
	on := true
	c := newChecker("0.2.0", src)
	c.Enabled = func(context.Context) bool { return on }
	c.Check(context.Background())
	on = false
	c.Check(context.Background())
	if src.count() != 1 {
		t.Fatalf("source called %d times with checking off", src.count())
	}
	if st := c.Status(context.Background()); st.Enabled || st.UpdateAvailable {
		t.Fatalf("status with checking off = %+v", st)
	}
}

func TestCheckerFailureKeepsLastResult(t *testing.T) {
	src := &fakeSource{latest: "0.3.0"}
	c := newChecker("0.2.0", src)
	c.Check(context.Background())
	src.set("", errors.New("offline"))
	c.Check(context.Background())
	if st := c.Status(context.Background()); st.Latest != "0.3.0" || !st.UpdateAvailable {
		t.Fatalf("status = %+v", st)
	}

	failing := newChecker("0.2.0", &fakeSource{err: errors.New("offline")})
	failing.Check(context.Background())
	if st := failing.Status(context.Background()); st.UpdateAvailable || st.Latest != "" {
		t.Fatalf("status after failed first check = %+v", st)
	}
}

func TestCheckerRunSchedule(t *testing.T) {
	src := &fakeSource{latest: "0.3.0"}
	delays := make(chan time.Duration, 8)
	tick := make(chan time.Time)
	c := newChecker("0.2.0", src)
	c.After = func(d time.Duration) <-chan time.Time {
		delays <- d
		return tick
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()

	if d := <-delays; d != time.Minute {
		t.Fatalf("first delay = %v, want 1m", d)
	}
	tick <- epoch
	if d := <-delays; d != 6*time.Hour {
		t.Fatalf("interval = %v, want 6h", d)
	}
	if src.count() != 1 || !c.Status(ctx).UpdateAvailable {
		t.Fatalf("after first tick: calls %d, status %+v", src.count(), c.Status(ctx))
	}

	c.Poke()
	deadline := time.Now().Add(2 * time.Second)
	for src.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if src.count() != 2 {
		t.Fatalf("Poke did not trigger a check: %d calls", src.count())
	}
	cancel()
	<-done
}
