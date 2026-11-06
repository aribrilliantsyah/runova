package apps_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"runova/internal/apps"
	"runova/internal/artifact"
	"runova/internal/docker"
	"runova/internal/secret"
	"runova/internal/store"
)

// A server like Spring Boot or a Go server with signal handling: SIGTERM ends
// it with exit code 0, which the on-failure restart policy never restarts.
const gracefulSrc = `package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	srv := &http.Server{Addr: ":" + os.Getenv("PORT"), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "up") })}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	go func() { <-ctx.Done(); srv.Shutdown(context.Background()) }()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
`

// The server rebooted: Docker stopped the container the way its shutdown does
// (stop = SIGTERM), the app exited 0 and on-failure left it down. A new
// Runova process reconciles and must bring the app back; an app the user
// stopped must stay down.
func TestIntegrationRebootStartsAppAgain(t *testing.T) {
	if os.Getenv("RUNOVA_DOCKER_TEST") != "1" {
		t.Skip("set RUNOVA_DOCKER_TEST=1 to run against a real Docker Engine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	engine := docker.New(os.Getenv("RUNOVA_DOCKER_HOST"))
	if err := engine.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	keyPath := filepath.Join(dir, "key")
	if err := secret.GenerateKey(keyPath); err != nil {
		t.Fatal(err)
	}
	box, err := secret.LoadKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	user, err := st.CreateUser(ctx, store.NewUser{Username: "itestboot", PasswordHash: "x", Role: store.RoleUser,
		Quota: store.Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 20, DiskMB: 4096}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		list, _ := engine.ListContainers(bg, map[string]string{"runova.user": "itestboot"})
		for _, c := range list {
			engine.RemoveContainer(bg, c.ID)
		}
		exec.Command("docker", "network", "rm", fmt.Sprintf("runova-u%d", user.ID)).Run()
	})
	newSvc := func() *apps.Service {
		return &apps.Service{
			Store: st, Engine: engine, Box: box, DataDir: filepath.Join(dir, "root"),
			Chown: func(path string, _ int) error { return os.Chmod(path, 0o777) },
		}
	}
	svc := newSvc()
	bin := buildStaticBinary(t, gracefulSrc)
	deploy := func(name string) store.App {
		id := strconv.FormatInt(time.Now().UnixNano(), 36)
		udir := filepath.Join(dir, "tmp", id)
		if err := os.MkdirAll(udir, 0o700); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(bin)
		path := filepath.Join(udir, name)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		rep := artifact.Report{Kind: artifact.KindStatic, FileName: name, Size: int64(len(raw)), SHA256: "ab", Port: 8080,
			PortSource: "bawaan", Env: []artifact.EnvVar{}, Problems: []string{}}
		if err := st.CreateUpload(ctx, store.Upload{ID: id, UserID: user.ID, Dir: udir, ArtifactPath: path, Report: rep, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		app, d, err := svc.Create(ctx, user, apps.CreateInput{UploadID: id, Name: name, MemoryMB: 128, CPUMilli: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.Deploy(ctx, user, app.ID, d.ID); err != nil {
			t.Fatalf("Deploy %s: %v", name, err)
		}
		app, _ = st.App(ctx, app.ID)
		return app
	}
	container := func(app store.App) docker.ContainerState {
		d, _ := st.Deployment(ctx, *app.CurrentDeploymentID)
		cs, err := engine.InspectContainer(ctx, d.ContainerID)
		if err != nil {
			t.Fatal(err)
		}
		return cs
	}
	keep := deploy("boot")
	userStopped := deploy("userstop")
	if err := svc.Stop(ctx, user, userStopped.ID); err != nil {
		t.Fatal(err)
	}
	// What Docker's shutdown does to the rest.
	dk, _ := st.Deployment(ctx, *keep.CurrentDeploymentID)
	if err := engine.StopContainer(ctx, dk.ContainerID, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if cs := container(keep); cs.Running || cs.ExitCode != 0 {
		t.Fatalf("after docker stop: running=%v exit=%d, want exited 0", cs.Running, cs.ExitCode)
	}

	// Runova starts again (a new Service, as after the reboot).
	svc2 := newSvc()
	svc2.ReviveGap = time.Second
	if err := svc2.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		got, _ := st.App(ctx, keep.ID)
		if got.Status == store.AppRunning {
			break
		}
		if got.Status == store.AppFailed || time.Now().After(deadline) {
			t.Fatalf("app status = %s, want running", got.Status)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !container(keep).Running {
		t.Error("container of the revived app is not running")
	}
	d, _ := st.Deployment(ctx, *keep.CurrentDeploymentID)
	if body := httpGet(t, "http://127.0.0.1:"+strconv.Itoa(d.HostPort)+"/"); body != "up\n" {
		t.Errorf("GET = %q", body)
	}
	if cs := container(userStopped); cs.Running {
		t.Error("the app the user stopped was started")
	}
	if got, _ := st.App(ctx, userStopped.ID); got.Status != store.AppStopped {
		t.Errorf("user-stopped app status = %s", got.Status)
	}
}
