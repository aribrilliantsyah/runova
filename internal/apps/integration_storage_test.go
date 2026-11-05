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
	"runova/internal/folders"
	"runova/internal/secret"
	"runova/internal/store"
)

// storageSrc writes one file into ./storage (relative, so in the workspace)
// and one into /app/storage (absolute, a mounted folder) at every start, then
// serves how many files each folder holds.
const storageSrc = `package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"
)

func count(dir string) int {
	list, _ := os.ReadDir(dir)
	return len(list)
}

func main() {
	name := strconv.FormatInt(time.Now().UnixNano(), 10)
	for _, dir := range []string{"./storage", "/app/storage"} {
		if err := os.WriteFile(dir+"/"+name, []byte("x"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%d %d", count("./storage"), count("/app/storage"))
	})
	if err := http.ListenAndServe(":"+os.Getenv("PORT"), nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
`

func TestIntegrationSavedFoldersSurviveRedeployAndRestart(t *testing.T) {
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
	user, err := st.CreateUser(ctx, store.NewUser{Username: "itestfs", PasswordHash: "x", Role: store.RoleUser,
		Quota: store.Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 20, DiskMB: 4096}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		list, _ := engine.ListContainers(bg, map[string]string{"runova.user": "itestfs"})
		for _, c := range list {
			engine.RemoveContainer(bg, c.ID)
		}
		exec.Command("docker", "network", "rm", fmt.Sprintf("runova-u%d", user.ID)).Run()
		// Files the container wrote belong to its UID; clear them with a container so TempDir can go.
		exec.Command("docker", "run", "--rm", "-v", dir+":/x", "busybox", "sh", "-c", "chmod -R 777 /x").Run()
	})
	svc := &apps.Service{
		Store: st, Engine: engine, Box: box, DataDir: filepath.Join(dir, "root"),
		Chown: func(path string, _ int) error { return os.Chmod(path, 0o777) },
	}
	raw, err := os.ReadFile(buildStaticBinary(t, storageSrc))
	if err != nil {
		t.Fatal(err)
	}
	upload := func() string {
		id := strconv.FormatInt(time.Now().UnixNano(), 36)
		udir := filepath.Join(dir, "tmp", id)
		if err := os.MkdirAll(udir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(udir, "hello")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		rep := artifact.Report{Kind: artifact.KindStatic, FileName: "hello", Size: int64(len(raw)), SHA256: "ab", Port: 8080,
			PortSource: "bawaan", Env: []artifact.EnvVar{}, Problems: []string{}, Folders: []artifact.DetectedFolder{
				{Folder: folders.Folder{Path: "storage", Dir: "storage"}, Source: "env.example", Key: "STORAGE_PATH"},
				{Folder: folders.Folder{Path: "/app/storage", Dir: "uploads"}, Source: "env.example", Key: "UPLOAD_DIR"},
			}}
		if err := st.CreateUpload(ctx, store.Upload{ID: id, UserID: user.ID, Dir: udir, ArtifactPath: path, Report: rep, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		return id
	}

	app, d, err := svc.Create(ctx, user, apps.CreateInput{UploadID: upload(), Name: "starboom", MemoryMB: 128, CPUMilli: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Deploy(ctx, user, app.ID, d.ID); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	get := func() string {
		cur, _ := st.App(ctx, app.ID)
		active, _ := st.Deployment(ctx, *cur.CurrentDeploymentID)
		return httpGet(t, "http://127.0.0.1:"+strconv.Itoa(active.HostPort)+"/")
	}
	hostFiles := func() (int, int) {
		a, _ := os.ReadDir(filepath.Join(svc.DataDir, "data", "itestfs", "starboom", "storage"))
		b, _ := os.ReadDir(filepath.Join(svc.DataDir, "mounts", "itestfs", "starboom", "uploads"))
		return len(a), len(b)
	}
	if got := get(); got != "1 1" {
		t.Fatalf("after first deploy the app sees %q, want 1 1", got)
	}
	if a, b := hostFiles(); a != 1 || b != 1 {
		t.Fatalf("host workspace holds %d and %d files, want 1 and 1", a, b)
	}

	if err := svc.DeployLatest(ctx, user, app.ID); err != nil {
		t.Fatalf("redeploy: %v", err)
	}
	if got := get(); got != "2 2" {
		t.Fatalf("after redeploy the app sees %q, want 2 2 (files kept)", got)
	}
	if err := svc.Restart(ctx, user, app.ID); err != nil {
		t.Fatalf("restart: %v", err)
	}
	var got string
	for range 50 {
		if got = get(); got == "3 3" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if got != "3 3" {
		t.Fatalf("after restart the app sees %q, want 3 3", got)
	}
	if a, b := hostFiles(); a != 3 || b != 3 {
		t.Fatalf("host workspace holds %d and %d files, want 3 and 3", a, b)
	}
	// The mounted folder also shows in the workspace as uploads/ (the File tab's view).
	entries, err := svc.ListFiles(ctx, user, app.ID, "uploads")
	if err != nil || len(entries) != 3 {
		t.Fatalf("File tab view of uploads = %d entries, %v", len(entries), err)
	}
	if err := svc.ScanDisk(ctx); err != nil {
		t.Fatal(err)
	}
	if cur, _ := st.App(ctx, app.ID); cur.DiskUsedBytes != 6 {
		t.Errorf("disk used = %d bytes, want 6 (six one-byte files; the uploads mount is not counted twice)", cur.DiskUsedBytes)
	}
}
