package apps_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"runova/internal/apps"
	"runova/internal/artifact"
	"runova/internal/store"
)

func TestCreateValidations(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, store.Quota{Apps: 1, MemoryMB: 1024, Databases: 3, PGConnections: 20, DiskMB: 4096})

	try := func(name string, rep artifact.Report, in apps.CreateInput) error {
		in.UploadID = e.upload(u, rep)
		in.Name = name
		if in.MemoryMB == 0 {
			in.MemoryMB = 256
		}
		_, _, err := e.svc.Create(ctx, u, in)
		return err
	}
	expect := func(label string, err error, kind error, contains string) {
		t.Helper()
		if !errors.Is(err, kind) || !strings.Contains(err.Error(), contains) {
			t.Errorf("%s: err = %v, want %v containing %q", label, err, kind, contains)
		}
	}

	expect("name uppercase", try("Billing", staticReport("x"), apps.CreateInput{}), apps.ErrValidation, "Nama aplikasi")
	expect("name too short", try("ab", staticReport("x"), apps.CreateInput{}), apps.ErrValidation, "Nama aplikasi")
	expect("name trailing hyphen", try("abc-", staticReport("x"), apps.CreateInput{}), apps.ErrValidation, "Nama aplikasi")
	expect("java below min", try("one", jarReport("a.jar", 17), apps.CreateInput{JavaVersion: 8}), apps.ErrValidation, "Java 17")
	e.setSettings(func(s *store.Settings) { s.Runtimes["java11"] = false })
	expect("java disabled", try("one", jarReport("a.jar", 8), apps.CreateInput{JavaVersion: 11}), apps.ErrValidation, "dinonaktifkan")
	bad := staticReport("x")
	bad.Problems = []string{"Bukan binary ELF"}
	expect("upload with problems", try("one", bad, apps.CreateInput{}), apps.ErrValidation, "Bukan binary ELF")
	expect("lowercase env", try("one", staticReport("x"), apps.CreateInput{Env: []store.EnvEntry{{Name: "lower", Value: "1"}}}), apps.ErrValidation, "lower")
	expect("duplicate env", try("one", staticReport("x"), apps.CreateInput{Env: []store.EnvEntry{{Name: "AA", Value: "1"}, {Name: "AA", Value: "2"}}}), apps.ErrValidation, "AA")
	expect("env value too big", try("one", staticReport("x"), apps.CreateInput{Env: []store.EnvEntry{{Name: "AA", Value: strings.Repeat("x", 32<<10+1)}}}), apps.ErrValidation, "32 KB")
	expect("jvm opts without dash", try("one", jarReport("a.jar", 8), apps.CreateInput{JavaVersion: 8, JVMOpts: "Xmx64m"}), apps.ErrValidation, "Xmx64m")
	expect("memory step", try("one", staticReport("x"), apps.CreateInput{MemoryMB: 100}), apps.ErrValidation, "kelipatan")
	expect("java opts on static", try("one", staticReport("x"), apps.CreateInput{JVMOpts: "-Xss1m"}), apps.ErrValidation, "JAR")
	if _, _, err := e.svc.Create(ctx, u, apps.CreateInput{UploadID: "nope", Name: "one", MemoryMB: 128}); !errors.Is(err, apps.ErrValidation) {
		t.Errorf("unknown upload: %v", err)
	}

	if err := try("one", staticReport("x"), apps.CreateInput{}); err != nil {
		t.Fatalf("valid create: %v", err)
	}
	expect("app count", try("two", staticReport("x"), apps.CreateInput{}), apps.ErrQuota, "Kuota aplikasi habis (1 dari 1)")
}

func TestCreateConflictAndOtherUsersUpload(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	a := e.addUser("andi", store.RoleUser, roomyQuota)
	b := e.addUser("budi", store.RoleUser, roomyQuota)
	e.createStatic(a, "billing")
	_, _, err := e.svc.Create(ctx, a, apps.CreateInput{UploadID: e.upload(a, staticReport("x")), Name: "billing", MemoryMB: 128})
	if !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "Nama aplikasi sudah dipakai") {
		t.Fatalf("duplicate name: %v", err)
	}
	e.createStatic(b, "billing")
	_, _, err = e.svc.Create(ctx, b, apps.CreateInput{UploadID: e.upload(a, staticReport("x")), Name: "other", MemoryMB: 128})
	if !errors.Is(err, apps.ErrValidation) {
		t.Fatalf("someone else's upload: %v, want ErrValidation", err)
	}
	if left, _ := e.store.AppsByUser(ctx, b.ID); len(left) != 1 {
		t.Errorf("failed create left apps behind: %+v", left)
	}
}

func TestQuotaBelowUsage(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, store.Quota{Apps: 5, MemoryMB: 1024, Databases: 3, PGConnections: 20, DiskMB: 4096})
	a1, _ := e.create(u, "one", staticReport("one"), apps.CreateInput{MemoryMB: 256})
	e.create(u, "two", staticReport("two"), apps.CreateInput{MemoryMB: 512})

	lowered := store.Quota{Apps: 5, MemoryMB: 512, Databases: 3, PGConnections: 20, DiskMB: 4096}
	u, err := e.store.UpdateUser(ctx, u.ID, store.UserPatch{Quota: &lowered})
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []int64{a1.ID} {
		got, err := e.store.App(ctx, id)
		if err != nil || got.MemoryMB != 256 {
			t.Fatalf("existing app touched: %+v, %v", got, err)
		}
	}
	mem := 320
	_, _, err = e.svc.UpdateSettings(ctx, u, a1.ID, apps.SettingsInput{MemoryMB: &mem})
	if !errors.Is(err, apps.ErrQuota) || !strings.Contains(err.Error(), "Memori melebihi sisa kuota (sisa 0 MB)") {
		t.Fatalf("raise memory: %v", err)
	}
	_, _, err = e.svc.Create(ctx, u, apps.CreateInput{UploadID: e.upload(u, staticReport("x")), Name: "three", MemoryMB: 128})
	if !errors.Is(err, apps.ErrQuota) || !strings.Contains(err.Error(), "sisa 0 MB") {
		t.Fatalf("create above quota: %v", err)
	}
	mem = 192
	app, redeploy, err := e.svc.UpdateSettings(ctx, u, a1.ID, apps.SettingsInput{MemoryMB: &mem})
	if err != nil || app.MemoryMB != 192 || !redeploy {
		t.Fatalf("decrease memory: %+v, redeploy=%v, %v", app, redeploy, err)
	}
}

func TestValidateResourcesMessages(t *testing.T) {
	q := store.Quota{MemoryMB: 1024}
	err := apps.ValidateResources(q, apps.Usage{MemoryMB: 768}, 320)
	if !errors.Is(err, apps.ErrQuota) || err.Error() != "Memori melebihi sisa kuota (sisa 256 MB)" {
		t.Errorf("memory: %v", err)
	}
	if err := apps.ValidateResources(q, apps.Usage{MemoryMB: 768}, 256); err != nil {
		t.Errorf("exact fit: %v", err)
	}
}

func TestPortAllocation(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, store.Quota{Apps: 10, MemoryMB: 4096, Databases: 3, PGConnections: 20, DiskMB: 4096})
	e.setSettings(func(s *store.Settings) { s.PortMin, s.PortMax = 20000, 20002 })

	var ports []int
	for _, n := range []string{"one", "two", "three"} {
		app, _ := e.createStatic(u, n)
		ports = append(ports, app.IPPort)
	}
	if !slices.Equal(ports, []int{20000, 20001, 20002}) {
		t.Fatalf("ip ports = %v", ports)
	}
	_, _, err := e.svc.Create(ctx, u, apps.CreateInput{UploadID: e.upload(u, staticReport("x")), Name: "four", MemoryMB: 128})
	if !errors.Is(err, apps.ErrQuota) || !strings.Contains(err.Error(), "Port akses IP habis") {
		t.Fatalf("exhausted: %v", err)
	}

	// Freeing a lower port makes it the next one: lowest free wins.
	apps2, _ := e.store.AppsByUser(ctx, u.ID)
	for _, a := range apps2 {
		if a.IPPort == 20001 {
			if err := e.store.DeleteApp(ctx, a.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	again, _ := e.createStatic(u, "again")
	if again.IPPort != 20001 {
		t.Fatalf("reused ip port = %d, want 20001", again.IPPort)
	}
}

func TestContainerSpec(t *testing.T) {
	e := newTestEnv(t)
	owner := store.User{ID: 2, Username: "budi"}
	v := 21
	app := store.App{ID: 7, UserID: 2, Name: "billing", Kind: artifact.KindJar, JavaVersion: &v, MemoryMB: 512}
	d := store.Deployment{
		ID: 9, AppID: 7, FileName: "billing.jar", ArtifactPath: "/var/lib/runova/artifacts/budi/billing/1/billing.jar",
		JavaVersion: &v, JVMOpts: "-Xss512k  -Dx=1", InternalPort: 8080, HostPort: 30123,
	}
	spec := e.svc.ContainerSpec(app, owner, d,
		[]store.EnvEntry{{Name: "PORT", Value: "1"}, {Name: "MODE", Value: "prod"}, {Name: "DB", Value: "user"}, {Name: "DB_PASSWORD", Value: ""}},
		[]string{"DB=fromdb", "PGHOST=host.docker.internal", "DB_PASSWORD=fromdb"})

	if spec.Image != "eclipse-temurin:21-jre" {
		t.Errorf("image = %s", spec.Image)
	}
	if want := []string{"java", "-Xss512k", "-Dx=1", "-jar", "/app/billing.jar"}; !slices.Equal(spec.Cmd, want) {
		t.Errorf("cmd = %v", spec.Cmd)
	}
	if spec.User != "100002:100002" {
		t.Errorf("user = %s", spec.User)
	}
	// A value the user typed beats the database's; an empty one leaves it to the database.
	wantEnv := []string{"PORT=8080", "MODE=prod", "DB=user", "PGHOST=host.docker.internal", "DB_PASSWORD=fromdb", "SERVER_PORT=8080", "RUNOVA_DATA_DIR=/data", "HOME=/tmp"}
	if !slices.Equal(spec.Env, wantEnv) {
		t.Errorf("env = %v, want %v", spec.Env, wantEnv)
	}
	if want := []string{d.ArtifactPath + ":/app/billing.jar:ro", e.svc.DataDir + "/data/budi/billing:/data"}; !slices.Equal(spec.Binds, want) {
		t.Errorf("binds = %v", spec.Binds)
	}
	if spec.MemoryBytes != 512<<20 || spec.Network != "runova-u2" || !spec.ReadOnly {
		t.Errorf("limits: %+v", spec)
	}
	if opts := spec.Tmpfs["/tmp"]; !strings.Contains(","+opts+",", ",exec,") || !strings.Contains(","+opts+",", ",mode=1777,") {
		t.Errorf("/tmp tmpfs options = %q, want exec (native libs unpacked by JVM apps) and mode=1777 (user UID can write)", opts)
	}
	if spec.InternalPort != 8080 || spec.HostPort != 30123 || spec.Tmpfs["/tmp"] == "" {
		t.Errorf("ports/tmpfs: %+v", spec)
	}
	if spec.Labels["runova.user"] != "budi" || spec.Labels["runova.app"] != "7" || spec.Labels["runova.deployment"] != "9" {
		t.Errorf("labels = %v", spec.Labels)
	}
	if !slices.Contains(spec.ExtraHosts, "host.docker.internal:host-gateway") {
		t.Errorf("extra hosts = %v", spec.ExtraHosts)
	}

	for kind, image := range map[artifact.Kind]string{artifact.KindStatic: "gcr.io/distroless/static-debian12", artifact.KindGlibc: "ubuntu:26.04"} {
		s := e.svc.ContainerSpec(store.App{Kind: kind}, owner, store.Deployment{FileName: "svc", InternalPort: 80}, nil, nil)
		if s.Image != image || !slices.Equal(s.Cmd, []string{"/app/svc"}) {
			t.Errorf("%s: image=%s cmd=%v", kind, s.Image, s.Cmd)
		}
	}
}

func TestSecretEnvKeepsValue(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.create(u, "one", staticReport("one"), apps.CreateInput{
		Env: []store.EnvEntry{{Name: "API_KEY", Value: "s3cret-value", Secret: true}, {Name: "MODE", Value: "prod"}},
	})
	stored, _ := e.store.Env(ctx, app.ID)
	for _, en := range stored {
		if en.Secret && strings.Contains(en.Value, "s3cret-value") {
			t.Fatalf("secret stored in plaintext: %+v", en)
		}
	}
	err := e.svc.SetEnv(ctx, u, app.ID, []store.EnvEntry{{Name: "API_KEY", Value: "", Secret: true}, {Name: "MODE", Value: "dev"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.RevealEnv(ctx, u, app.ID, "API_KEY")
	if err != nil || got != "s3cret-value" {
		t.Fatalf("revealed %q, %v; want the old value kept", got, err)
	}
	if err := e.svc.SetEnv(ctx, u, app.ID, []store.EnvEntry{{Name: "API_KEY", Value: "new", Secret: true}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.RevealEnv(ctx, u, app.ID, "API_KEY"); got != "new" {
		t.Fatalf("after replace = %q", got)
	}
	if _, err := e.svc.RevealEnv(ctx, u, app.ID, "MODE"); !errors.Is(err, apps.ErrValidation) {
		t.Fatalf("reveal removed var: %v", err)
	}
}

func TestArtifactMovedAndUploadRemoved(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	rep := staticReport("hello")
	id := e.upload(u, rep)
	up, err := e.store.Upload(ctx, id, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	app, d, err := e.svc.Create(ctx, u, apps.CreateInput{UploadID: id, Name: "hello", MemoryMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(e.svc.DataDir, "artifacts", "budi", "hello", "1", "hello")
	if d.ArtifactPath != want || d.Number != 1 || d.Status != store.DeployPending || app.Status != store.AppStopped {
		t.Fatalf("deployment = %+v app=%+v", d, app)
	}
	fi, err := os.Stat(want)
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("artifact stat = %v, %v; want mode 0755 for binaries", fi, err)
	}
	if _, err := os.Stat(up.Dir); !os.IsNotExist(err) {
		t.Errorf("upload dir still there: %v", err)
	}
	if _, err := e.store.Upload(ctx, id, u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("upload row still there: %v", err)
	}
	stored, _ := e.store.Deployment(ctx, d.ID)
	if stored.ArtifactPath != want || stored.EnvSnapshot == "" {
		t.Errorf("stored deployment = %+v", stored)
	}

	jar, jd := e.create(u, "jarapp", jarReport("a.jar", 17), apps.CreateInput{})
	if fi, _ := os.Stat(jd.ArtifactPath); fi.Mode().Perm() != 0o644 {
		t.Errorf("jar mode = %v, want 0644", fi.Mode().Perm())
	}
	if jar.JavaVersion == nil || *jar.JavaVersion != 17 {
		t.Errorf("default java = %v, want the lowest allowed (17)", jar.JavaVersion)
	}
}

func TestNewVersion(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	a := e.addUser("andi", store.RoleUser, roomyQuota)
	b := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.createStatic(a, "one")
	if err := e.svc.SetEnv(ctx, a, app.ID, []store.EnvEntry{{Name: "MODE", Value: "v2"}}); err != nil {
		t.Fatal(err)
	}
	d2, err := e.svc.NewVersion(ctx, a, app.ID, e.upload(a, staticReport("one")))
	if err != nil || d2.Number != 2 || !strings.HasSuffix(d2.ArtifactPath, "/artifacts/andi/one/2/one") {
		t.Fatalf("NewVersion = %+v, %v", d2, err)
	}
	if _, err := e.svc.NewVersion(ctx, b, app.ID, e.upload(b, staticReport("x"))); !errors.Is(err, apps.ErrAppNotFound) {
		t.Fatalf("other user: %v", err)
	}
	if _, err := e.svc.NewVersion(ctx, a, app.ID, e.upload(a, jarReport("x.jar", 8))); !errors.Is(err, apps.ErrValidation) {
		t.Fatalf("kind change: %v", err)
	}
}

// A Spring Boot app reads SERVER_PORT, not PORT: without it a JAR whose port
// is set anywhere Runova cannot see (a profile file, a default in code) listens
// elsewhere and the health check waits on a port nobody opens.
func TestContainerSpecJarGetsServerPort(t *testing.T) {
	e := newTestEnv(t)
	v := 21
	app := store.App{ID: 7, UserID: 2, Name: "starboom", Kind: artifact.KindJar, JavaVersion: &v, MemoryMB: 512}
	d := store.Deployment{ID: 9, FileName: "starboom.jar", JavaVersion: &v, InternalPort: 9090, HostPort: 38398}
	spec := e.svc.ContainerSpec(app, store.User{ID: 2, Username: "budi"}, d, []store.EnvEntry{{Name: "SERVER_PORT", Value: "1234"}}, nil)
	if !slices.Contains(spec.Env, "SERVER_PORT=9090") || !slices.Contains(spec.Env, "PORT=9090") || slices.Contains(spec.Env, "SERVER_PORT=1234") {
		t.Fatalf("env = %v, want SERVER_PORT and PORT set to the app port 9090", spec.Env)
	}
	static := e.svc.ContainerSpec(store.App{Kind: artifact.KindStatic}, store.User{ID: 2}, store.Deployment{FileName: "svc", InternalPort: 80}, nil, nil)
	for _, kv := range static.Env {
		if strings.HasPrefix(kv, "SERVER_PORT=") {
			t.Fatalf("static binary env = %v; SERVER_PORT is a Spring setting", static.Env)
		}
	}
}
