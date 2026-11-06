package apps_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"runova/internal/apps"
	"runova/internal/artifact"
	"runova/internal/docker"
	"runova/internal/secret"
	"runova/internal/store"
)

// hungrySrc starts like a server, then touches more memory than the container
// may use: the kernel OOM-kills it without a word in the log, and Docker's
// restart policy (on-failure) starts it again, over and over, as the JVM in
// the report did after Hibernate's start.
const hungrySrc = `package main

import (
	"fmt"
	"time"
)

func main() {
	fmt.Println("Starting HungryApplication")
	time.Sleep(time.Second)
	var keep [][]byte
	for {
		b := make([]byte, 8<<20)
		for i := range b {
			b[i] = 1
		}
		keep = append(keep, b)
		time.Sleep(20 * time.Millisecond)
	}
}
`

func TestIntegrationOOMRestartLoopFailsDeploy(t *testing.T) {
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
	user, err := st.CreateUser(ctx, store.NewUser{Username: "itestoom", PasswordHash: "x", Role: store.RoleUser,
		Quota: store.Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 20, DiskMB: 4096}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		list, _ := engine.ListContainers(bg, map[string]string{"runova.user": "itestoom"})
		for _, c := range list {
			engine.RemoveContainer(bg, c.ID)
		}
		exec.Command("docker", "network", "rm", fmt.Sprintf("runova-u%d", user.ID)).Run()
	})
	svc := &apps.Service{
		Store: st, Engine: engine, Box: box, DataDir: filepath.Join(dir, "root"),
		Chown: func(path string, _ int) error { return os.Chmod(path, 0o777) },
	}
	bin := buildStaticBinary(t, hungrySrc)
	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	udir := filepath.Join(dir, "tmp", id)
	if err := os.MkdirAll(udir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(bin)
	path := filepath.Join(udir, "hungry")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	rep := artifact.Report{Kind: artifact.KindStatic, FileName: "hungry", Size: int64(len(raw)), SHA256: "ab", Port: 8080,
		PortSource: "bawaan", Env: []artifact.EnvVar{}, Problems: []string{}}
	if err := st.CreateUpload(ctx, store.Upload{ID: id, UserID: user.ID, Dir: udir, ArtifactPath: path, Report: rep, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	app, d, err := svc.Create(ctx, user, apps.CreateInput{UploadID: id, Name: "hungry", MemoryMB: 128, CPUMilli: 250})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err = svc.Deploy(ctx, user, app.ID, d.ID)
	took := time.Since(start)
	if err == nil {
		t.Fatal("deploy of an app that is OOM-killed in a loop succeeded")
	}
	if took > 30*time.Second {
		t.Fatalf("deploy took %s; the restart must end the wait at once, not after the 60 s health timeout", took)
	}
	got, _ := st.Deployment(ctx, d.ID)
	if got.Problem == nil || got.Problem.Title != "Memori habis (OOMKilled)" || got.Problem.ExitCode == nil || *got.Problem.ExitCode != 137 {
		t.Fatalf("problem = %+v (deploy error %v)", got.Problem, err)
	}
	if !strings.Contains(got.ContainerLog, "Starting HungryApplication") {
		t.Errorf("saved log = %.200s; want the app's output kept", got.ContainerLog)
	}
	if list, _ := engine.ListContainers(ctx, map[string]string{"runova.user": "itestoom"}); len(list) != 0 {
		t.Errorf("containers left: %+v", list)
	}
	t.Logf("deploy failed after %s: %s / %s", took.Round(time.Millisecond), got.Problem.Title, got.Problem.Detail)
}
