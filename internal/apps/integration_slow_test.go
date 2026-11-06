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

// slowJVMSrc starts like a Spring Boot app on a small CPU: it logs its
// start-up for a while (SLOW_SECONDS, default 70) and only then listens on
// SERVER_PORT, logging Tomcat's and Spring's start lines. Measured: a Spring
// Boot 4.1.2 app with JPA opened its port after 57 s on 0.5 core, and 70 s
// while the old version still ran (zero-downtime redeploy).
const slowJVMSrc = `import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;

public class Slow {
	public static void main(String[] a) throws Exception {
		long t0 = System.currentTimeMillis();
		int port = Integer.parseInt(System.getenv().getOrDefault("SERVER_PORT", "8080"));
		int secs = Integer.parseInt(System.getenv().getOrDefault("SLOW_SECONDS", "70"));
		System.out.println("INFO 1 --- [main] demo.Slow : Starting Slow using Java " + System.getProperty("java.version"));
		for (int i = 0; i < secs; i += 2) {
			Thread.sleep(2000);
			System.out.println("INFO 1 --- [main] o.h.e.t.j.p.i.JtaPlatformInitiator : initializing step " + i);
		}
		HttpServer s = HttpServer.create(new InetSocketAddress(port), 0);
		s.createContext("/", x -> { byte[] b = "ok\n".getBytes(); x.sendResponseHeaders(200, b.length); x.getResponseBody().write(b); x.close(); });
		s.start();
		System.out.println("INFO 1 --- [main] o.s.boot.tomcat.TomcatWebServer : Tomcat started on port " + port + " (http) with context path '/'");
		System.out.printf("INFO 1 --- [main] demo.Slow : Started Slow in %.3f seconds (process running for %.3f)%n", (System.currentTimeMillis() - t0) / 1000.0, (System.currentTimeMillis() - t0) / 1000.0);
	}
}
`

func buildSlowJar(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("javac not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Slow.java"), []byte(slowJVMSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command("javac", "--release", "21", "-d", dir, filepath.Join(dir, "Slow.java")).CombinedOutput(); err != nil {
		t.Fatalf("javac: %v\n%s", err, b)
	}
	out := filepath.Join(dir, "slow.jar")
	if b, err := exec.Command("jar", "--create", "--file", out, "--main-class", "Slow", "-C", dir, "Slow.class").CombinedOutput(); err != nil {
		t.Fatalf("jar: %v\n%s", err, b)
	}
	return out
}

// A JAR that needs 70 s to open its port (half a core) deploys, redeploys in
// the default zero-downtime mode next to the old JVM, and restarts, ending
// running every time; Restart reports "starting" until the port is open.
func TestIntegrationSlowJVMDeployRedeployRestart(t *testing.T) {
	if os.Getenv("RUNOVA_DOCKER_TEST") != "1" {
		t.Skip("set RUNOVA_DOCKER_TEST=1 to run against a real Docker Engine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	engine := docker.New(os.Getenv("RUNOVA_DOCKER_HOST"))
	if err := engine.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	jar := buildSlowJar(t)
	raw, err := os.ReadFile(jar)
	if err != nil {
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
	user, err := st.CreateUser(ctx, store.NewUser{Username: "itestslow", PasswordHash: "x", Role: store.RoleUser,
		Quota: store.Quota{Apps: 3, MemoryMB: 2048, Databases: 3, PGConnections: 20, DiskMB: 1024}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		list, _ := engine.ListContainers(bg, map[string]string{"runova.user": "itestslow"})
		for _, c := range list {
			engine.RemoveContainer(bg, c.ID)
		}
		engine.RemoveNetwork(bg, fmt.Sprintf("runova-u%d", user.ID))
	})
	svc := &apps.Service{
		Store: st, Engine: engine, Box: box, DataDir: filepath.Join(dir, "root"),
		Chown: func(path string, _ int) error { return os.Chmod(path, 0o777) },
	}

	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	udir := filepath.Join(dir, "tmp", id)
	if err := os.MkdirAll(udir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(udir, "slow.jar")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	rep := artifact.Report{Kind: artifact.KindJar, FileName: "slow.jar", Size: int64(len(raw)), SHA256: "ab", MinJava: 21,
		Port: 8080, PortSource: "bawaan", Env: []artifact.EnvVar{}, Problems: []string{}}
	if err := st.CreateUpload(ctx, store.Upload{ID: id, UserID: user.ID, Dir: udir, ArtifactPath: path, Report: rep, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	app, d1, err := svc.Create(ctx, user, apps.CreateInput{UploadID: id, Name: "starboom", JavaVersion: 21, MemoryMB: 512})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if err := svc.Deploy(ctx, user, app.ID, d1.ID); err != nil {
		t.Fatalf("first deploy: %v", err)
	}
	t.Logf("first deploy healthy after %s", time.Since(start).Round(time.Second))

	start = time.Now()
	if err := svc.DeployLatest(ctx, user, app.ID); err != nil {
		t.Fatalf("Deploy ulang (zero-downtime): %v", err)
	}
	t.Logf("redeploy healthy after %s", time.Since(start).Round(time.Second))
	ds, _ := st.Deployments(ctx, app.ID)
	if len(ds) != 2 || ds[0].Status != store.DeployActive || ds[0].InternalPort != 8080 {
		t.Fatalf("deployments after redeploy = %+v", ds)
	}
	httpGet(t, "http://127.0.0.1:"+strconv.Itoa(ds[0].HostPort)+"/")

	start = time.Now()
	if err := svc.Restart(ctx, user, app.ID); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if cur, _ := st.App(ctx, app.ID); string(cur.Status) == "running" {
		t.Fatal("app reported running right after Restart, before its port opened")
	}
	for {
		cur, _ := st.App(ctx, app.ID)
		if string(cur.Status) == "running" {
			break
		}
		if string(cur.Status) == "failed" || time.Since(start) > 5*time.Minute {
			d, _ := st.Deployment(ctx, ds[0].ID)
			t.Fatalf("after Restart: app %s, problem %+v", cur.Status, d.Problem)
		}
		time.Sleep(time.Second)
	}
	t.Logf("restart running after %s", time.Since(start).Round(time.Second))
	httpGet(t, "http://127.0.0.1:"+strconv.Itoa(ds[0].HostPort)+"/")
}
