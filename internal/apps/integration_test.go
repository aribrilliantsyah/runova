package apps_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"runova/internal/apps"
	"runova/internal/artifact"
	"runova/internal/docker"
	"runova/internal/secret"
	"runova/internal/store"
)

const integrationSrc = `package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	if err := os.WriteFile("/data/x", []byte("written by the app"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write /data/x:", err)
		os.Exit(1)
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	fmt.Println("listening")
	if err := http.ListenAndServe(":"+os.Getenv("PORT"), nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
`

func buildStaticBinary(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module hello\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "hello")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	return out
}

func httpGet(t *testing.T, url string) string {
	t.Helper()
	var last error
	for range 30 {
		resp, err := http.Get(url)
		if err == nil {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != 200 {
				t.Fatalf("GET %s = %d", url, resp.StatusCode)
			}
			return string(body)
		}
		last = err
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("GET %s: %v", url, last)
	return ""
}

func TestIntegrationDeployRollbackDelete(t *testing.T) {
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
	user, err := st.CreateUser(ctx, store.NewUser{Username: "itest", PasswordHash: "x", Role: store.RoleUser,
		Quota: store.Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 20}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		list, _ := engine.ListContainers(bg, map[string]string{"runova.user": "itest"})
		for _, c := range list {
			engine.RemoveContainer(bg, c.ID)
		}
		exec.Command("docker", "network", "rm", fmt.Sprintf("runova-u%d", user.ID)).Run()
	})

	// The dev process is not root, so it cannot chown to the container UID.
	// Open the folder instead; production uses OSChown with CAP_CHOWN.
	svc := &apps.Service{
		Store: st, Engine: engine, Box: box, DataDir: filepath.Join(dir, "root"),
		Chown: func(path string, _ int) error { return os.Chmod(path, 0o777) },
	}

	bin := buildStaticBinary(t, integrationSrc)
	upload := func() string {
		id := strconv.FormatInt(time.Now().UnixNano(), 36)
		udir := filepath.Join(dir, "tmp", id)
		if err := os.MkdirAll(udir, 0o700); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(bin)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(udir, "hello")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		rep := artifact.Report{Kind: artifact.KindStatic, FileName: "hello", Size: int64(len(raw)), SHA256: "ab", Port: 8080,
			PortSource: "bawaan", Env: []artifact.EnvVar{}, Problems: []string{}}
		if err := st.CreateUpload(ctx, store.Upload{ID: id, UserID: user.ID, Dir: udir, ArtifactPath: path, Report: rep, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		return id
	}

	app, d1, err := svc.Create(ctx, user, apps.CreateInput{UploadID: upload(), Name: "hello", MemoryMB: 128, CPUMilli: 100,
		Env: []store.EnvEntry{{Name: "GREETING", Value: "v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Deploy(ctx, user, app.ID, d1.ID); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	got1, _ := st.Deployment(ctx, d1.ID)
	if got1.Status != store.DeployActive || got1.HostPort < 30000 {
		t.Fatalf("deployment = %+v", got1)
	}
	url := "http://127.0.0.1:" + strconv.Itoa(got1.HostPort) + "/"
	if body := httpGet(t, url); body != "ok\n" {
		t.Fatalf("GET = %q", body)
	}

	dataFile := filepath.Join(svc.DataDir, "data", "itest", "hello", "x")
	fi, err := os.Stat(dataFile)
	if err != nil {
		t.Fatalf("app did not write /data/x: %v", err)
	}
	if uid := fi.Sys().(*syscall.Stat_t).Uid; int(uid) != 100000+int(user.ID) {
		t.Fatalf("x is owned by uid %d, want %d", uid, 100000+int(user.ID))
	}

	d2, err := svc.NewVersion(ctx, user, app.ID, upload())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Deploy(ctx, user, app.ID, d2.ID); err != nil {
		t.Fatalf("second Deploy: %v", err)
	}
	if err := svc.Rollback(ctx, user, app.ID, d1.ID); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	ds, _ := st.Deployments(ctx, app.ID)
	if len(ds) != 3 || ds[0].Status != store.DeployActive || ds[1].Status != store.DeploySuperseded {
		t.Fatalf("deployments after rollback = %+v", ds)
	}
	httpGet(t, "http://127.0.0.1:"+strconv.Itoa(ds[0].HostPort)+"/")
	running, _ := engine.ListContainers(ctx, map[string]string{"runova.app": strconv.FormatInt(app.ID, 10)})
	if len(running) != 1 {
		t.Fatalf("%d containers for the app after deploys, want exactly 1", len(running))
	}

	if err := svc.Delete(ctx, user, app.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	left, err := engine.ListContainers(ctx, map[string]string{"runova.app": strconv.FormatInt(app.ID, 10)})
	if err != nil || len(left) != 0 {
		t.Fatalf("containers left after Delete: %+v, %v", left, err)
	}
	if _, err := os.Stat(filepath.Join(svc.DataDir, "data", "itest", "hello")); !os.IsNotExist(err) {
		t.Errorf("data dir survived Delete: %v", err)
	}
}
