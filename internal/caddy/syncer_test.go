package caddy_test

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"runova/internal/caddy"
)

// fakeLoader is a Caddy that remembers the last loaded configuration.
type fakeLoader struct {
	mu       sync.Mutex
	config   []byte
	loads    int
	active   int
	overlap  bool
	loadErr  error
	loadWork time.Duration
}

func (f *fakeLoader) Load(_ context.Context, cfg []byte) error {
	f.mu.Lock()
	f.active++
	if f.active > 1 {
		f.overlap = true
	}
	work, err := f.loadWork, f.loadErr
	f.mu.Unlock()

	time.Sleep(work)

	f.mu.Lock()
	defer f.mu.Unlock()
	f.active--
	if err != nil {
		return err
	}
	f.loads++
	f.config = cfg
	return nil
}

func (f *fakeLoader) Config(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.config == nil {
		return []byte("null\n"), nil
	}
	return f.config, nil
}

func (f *fakeLoader) restart() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.config = nil
}

func (f *fakeLoader) loadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loads
}

func staticState(s caddy.State) func(context.Context) (caddy.State, error) {
	return func(context.Context) (caddy.State, error) { return s, nil }
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSyncerReloadsAfterCaddyRestart(t *testing.T) {
	loader := &fakeLoader{}
	s := &caddy.Syncer{
		Client:   loader,
		State:    staticState(caddy.State{Sites: []caddy.Site{{Host: "a.co", Upstream: "127.0.0.1:30001"}}}),
		Interval: 20 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, "initial load", func() bool { return loader.loadCount() == 1 })
	time.Sleep(80 * time.Millisecond)
	if n := loader.loadCount(); n != 1 {
		t.Fatalf("loads = %d while Caddy is unchanged, want 1", n)
	}

	loader.restart()
	start := time.Now()
	waitFor(t, "reload after restart", func() bool { return loader.loadCount() == 2 })
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("reload took %s, want within a few intervals", d)
	}
	if cfg, _ := loader.Config(ctx); !strings.Contains(string(cfg), "site:a.co") {
		t.Errorf("reloaded config lacks the site: %s", cfg)
	}
}

func TestSyncerReloadsWhenRunovaServerMissing(t *testing.T) {
	loader := &fakeLoader{config: []byte(`{"apps":{"http":{"servers":{"other":{}}}}}`)}
	s := &caddy.Syncer{Client: loader, State: staticState(caddy.State{}), Interval: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, "load over foreign config", func() bool { return loader.loadCount() >= 1 })
}

func TestApplySerialized(t *testing.T) {
	loader := &fakeLoader{loadWork: 2 * time.Millisecond}
	s := &caddy.Syncer{Client: loader, State: staticState(caddy.State{})}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Apply(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if loader.overlap {
		t.Error("two Load calls ran at the same time")
	}
	if n := loader.loadCount(); n != 10 {
		t.Errorf("loads = %d, want 10", n)
	}
}

func TestApplyReturnsLoadError(t *testing.T) {
	loader := &fakeLoader{loadErr: errors.New("bad upstream")}
	s := &caddy.Syncer{Client: loader, State: staticState(caddy.State{})}
	if err := s.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "bad upstream") {
		t.Fatalf("Apply = %v", err)
	}
}

func TestApplyBuildsFromCurrentState(t *testing.T) {
	loader := &fakeLoader{}
	var host atomic.Value
	host.Store("a.co")
	s := &caddy.Syncer{Client: loader, State: func(context.Context) (caddy.State, error) {
		return caddy.State{Sites: []caddy.Site{{Host: host.Load().(string), Upstream: "127.0.0.1:30001"}}}, nil
	}}
	for _, h := range []string{"a.co", "b.co"} {
		host.Store(h)
		if err := s.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		cfg, _ := loader.Config(context.Background())
		if !strings.Contains(string(cfg), "site:"+h) {
			t.Errorf("config after switching to %s: %s", h, cfg)
		}
	}
}

func TestApplyStateError(t *testing.T) {
	loader := &fakeLoader{}
	s := &caddy.Syncer{Client: loader, State: func(context.Context) (caddy.State, error) { return caddy.State{}, errors.New("db gone") }}
	if err := s.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "db gone") {
		t.Fatalf("Apply = %v", err)
	}
	if loader.loadCount() != 0 {
		t.Error("loaded a configuration although the state could not be read")
	}
}

func TestSyncerReportsErrors(t *testing.T) {
	loader := &fakeLoader{loadErr: errors.New("refused")}
	errs := make(chan error, 8)
	s := &caddy.Syncer{
		Client: loader, State: staticState(caddy.State{}), Interval: 5 * time.Millisecond,
		OnError: func(err error) {
			select {
			case errs <- err:
			default:
			}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case err := <-errs:
		if !strings.Contains(err.Error(), "refused") {
			t.Errorf("OnError got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("no error reported")
	}
}

func TestLoadErrorSurfaced(t *testing.T) {
	var got struct {
		path, ctype, body string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.path, got.ctype, got.body = r.URL.Path, r.Header.Get("Content-Type"), string(b)
		if r.Header.Get("Origin") != "" {
			t.Errorf("Origin header sent: %q", r.Header.Get("Origin"))
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"loading config: bad"}`))
	}))
	defer srv.Close()

	err := caddy.NewClient(srv.URL).Load(context.Background(), []byte(`{"x":1}`))
	if err == nil || !strings.Contains(err.Error(), "loading config: bad") {
		t.Fatalf("Load = %v, want the error text of Caddy's response", err)
	}
	if got.path != "/load" || got.ctype != "application/json" || got.body != `{"x":1}` {
		t.Errorf("request = %+v", got)
	}
}

func TestLoadErrorWithoutJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	err := caddy.NewClient(srv.URL).Load(context.Background(), []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Load = %v", err)
	}
}

func TestClientConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config/" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("null\n"))
	}))
	defer srv.Close()
	cfg, err := caddy.NewClient(srv.URL).Config(context.Background())
	if err != nil || strings.TrimSpace(string(cfg)) != "null" {
		t.Fatalf("Config = %q, %v", cfg, err)
	}
}

func TestProbeTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	addr := srv.Listener.Addr().String()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	if ok, detail := caddy.ProbeTLSAt(context.Background(), addr, "example.com", pool); !ok {
		t.Errorf("trusted certificate rejected: %s", detail)
	}
	ok, detail := caddy.ProbeTLSAt(context.Background(), addr, "example.com", x509.NewCertPool())
	if ok || detail == "" {
		t.Errorf("untrusted certificate accepted: ok=%v detail=%q", ok, detail)
	}
	ok, detail = caddy.ProbeTLSAt(context.Background(), addr, "other.example.org", pool)
	if ok || detail == "" {
		t.Errorf("certificate for another name accepted: ok=%v detail=%q", ok, detail)
	}
	ok, detail = caddy.ProbeTLSAt(context.Background(), "127.0.0.1:1", "example.com", pool)
	if ok || detail == "" {
		t.Errorf("closed port reported ok=%v detail=%q", ok, detail)
	}
}
