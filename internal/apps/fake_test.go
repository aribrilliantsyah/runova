package apps_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"runova/internal/apps"
	"runova/internal/artifact"
	"runova/internal/docker/dockertest"
	"runova/internal/secret"
	"runova/internal/store"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// testEnv wires a Service to a real store in a temp dir and a fake Docker.
type testEnv struct {
	t      *testing.T
	svc    *apps.Service
	store  *store.Store
	engine *dockertest.Fake
	clock  *clock
	dir    string

	mu       sync.Mutex
	chowned  map[string]int
	portBusy map[int]bool
	healthy  func(addr string) bool
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	keyPath := filepath.Join(dir, "key")
	if err := secret.GenerateKey(keyPath); err != nil {
		t.Fatal(err)
	}
	box, err := secret.LoadKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	e := &testEnv{
		t: t, store: st, engine: dockertest.New(), dir: dir,
		clock:    &clock{t: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)},
		chowned:  map[string]int{},
		portBusy: map[int]bool{},
		healthy:  func(string) bool { return true },
	}
	e.svc = &apps.Service{
		Store: st, Engine: e.engine, Box: box, DataDir: filepath.Join(dir, "data-root"),
		Now: e.clock.Now,
		PortFree: func(p int) bool {
			e.mu.Lock()
			defer e.mu.Unlock()
			return !e.portBusy[p]
		},
		Chown: func(path string, uid int) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.chowned[path] = uid
			return nil
		},
		Dial: func(_, addr string, _ time.Duration) (net.Conn, error) {
			e.mu.Lock()
			ok := e.healthy(addr)
			e.mu.Unlock()
			if !ok {
				return nil, &net.OpError{Op: "dial", Err: os.ErrDeadlineExceeded}
			}
			c1, c2 := net.Pipe()
			c2.Close()
			return c1, nil
		},
		HealthTimeout:  time.Second,
		HealthInterval: time.Millisecond,
		ReconnectDelay: time.Millisecond,
	}
	return e
}

func (e *testEnv) setHealthy(fn func(addr string) bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.healthy = fn
}

func (e *testEnv) addUser(username string, role store.Role, quota store.Quota) store.User {
	e.t.Helper()
	u, err := e.store.CreateUser(context.Background(), store.NewUser{Username: username, PasswordHash: "x", Role: role, Quota: quota}, 1)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

var roomyQuota = store.Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 20}

// upload stores a fake upload for user with the given report and returns its ID.
func (e *testEnv) upload(user store.User, rep artifact.Report) string {
	e.t.Helper()
	dir := filepath.Join(e.dir, "tmp", rep.FileName+"-"+time.Now().Format("150405.000000000"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		e.t.Fatal(err)
	}
	path := filepath.Join(dir, rep.FileName)
	if err := os.WriteFile(path, []byte("artifact "+rep.FileName), 0o600); err != nil {
		e.t.Fatal(err)
	}
	id := filepath.Base(dir)
	if err := e.store.CreateUpload(context.Background(), store.Upload{
		ID: id, UserID: user.ID, Dir: dir, ArtifactPath: path, Report: rep, CreatedAt: 1,
	}); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func staticReport(file string) artifact.Report {
	return artifact.Report{Kind: artifact.KindStatic, FileName: file, Size: 10, SHA256: "ab", Port: 8080, PortSource: "bawaan",
		Env: []artifact.EnvVar{}, Problems: []string{}}
}

func jarReport(file string, minJava int) artifact.Report {
	return artifact.Report{Kind: artifact.KindJar, FileName: file, Size: 10, SHA256: "cd", MinJava: minJava, Port: 8080, PortSource: "bawaan",
		Env: []artifact.EnvVar{}, Problems: []string{}}
}

func (e *testEnv) createStatic(user store.User, name string) (store.App, store.Deployment) {
	e.t.Helper()
	return e.create(user, name, staticReport(name), apps.CreateInput{})
}

func (e *testEnv) create(user store.User, name string, rep artifact.Report, in apps.CreateInput) (store.App, store.Deployment) {
	e.t.Helper()
	in.UploadID = e.upload(user, rep)
	in.Name = name
	if in.MemoryMB == 0 {
		in.MemoryMB = 256
	}
	if in.CPUMilli == 0 {
		in.CPUMilli = 250
	}
	app, d, err := e.svc.Create(context.Background(), user, in)
	if err != nil {
		e.t.Fatalf("Create(%s): %v", name, err)
	}
	return app, d
}

func (e *testEnv) setSettings(fn func(s *store.Settings)) {
	e.t.Helper()
	s, err := e.store.Settings(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	fn(&s)
	if err := e.store.SetSettings(context.Background(), s); err != nil {
		e.t.Fatal(err)
	}
}
