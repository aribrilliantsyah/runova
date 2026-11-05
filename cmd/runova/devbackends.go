package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"runova/internal/api"
	"runova/internal/apps"
	"runova/internal/caddy"
	"runova/internal/config"
	"runova/internal/dbs"
	"runova/internal/docker"
	"runova/internal/docker/dockertest"
	"runova/internal/hostdns"
	"runova/internal/pg/pgfake"
	"runova/internal/secret"
	"runova/internal/store"
)

// productionData is the data directory of a real installation; fake backends never run on it.
// A variable only so a test can point it at a temporary directory.
var productionData = "/var/lib/runova"

// errFakeOnProduction is returned when RUNOVA_FAKE_BACKENDS=1 meets the production data directory.
var errFakeOnProduction = errors.New("RUNOVA_FAKE_BACKENDS=1 refused for RUNOVA_DATA=" + productionData)

// onProductionData reports whether dir is the production data directory. It compares the
// directories themselves (os.SameFile), so a relative path, a symlink or a bind of the
// production directory is caught as well as the plain spelling.
func onProductionData(dir string) bool {
	if filepath.Clean(dir) == filepath.Clean(productionData) {
		return true
	}
	prod, err := os.Stat(productionData)
	if err != nil {
		return false // no production directory on this machine, nothing to protect
	}
	d, err := os.Stat(dir)
	return err == nil && os.SameFile(prod, d)
}

// checkFakeBackends refuses fake backends on the production data directory.
func checkFakeBackends(cfg config.Config) error {
	if cfg.FakeBackends && onProductionData(cfg.DataDir) {
		return errFakeOnProduction
	}
	return nil
}

// errDevOnProduction is returned when RUNOVA_DEV_CHMOD=1 meets the production data directory.
var errDevOnProduction = errors.New("RUNOVA_DEV_CHMOD=1 refused for RUNOVA_DATA=" + productionData)

// checkDevMode refuses RUNOVA_DEV_CHMOD on the production data directory.
func checkDevMode(cfg config.Config) error {
	if cfg.DevChmod && onProductionData(cfg.DataDir) {
		return errDevOnProduction
	}
	return nil
}

// devChmod makes app data folders world-writable instead of handing them to the
// container UID, for a development process that is not root and so lacks CAP_CHOWN.
func devChmod(svc *apps.Service) {
	svc.Chown = func(path string, _ int) error { return os.Chmod(path, 0o777) }
}

func ok(detail string) api.Check {
	return func(context.Context) (string, error) { return detail, nil }
}

// newEngine returns the Docker engine and its health probe.
func newEngine(cfg config.Config) (docker.Engine, api.Check) {
	if cfg.FakeBackends {
		return newFakeEngine(), ok("fake")
	}
	c := docker.New(cfg.DockerHost)
	return c, func(ctx context.Context) (string, error) { return c.Version(ctx) }
}

// newCaddyLoader returns Caddy's admin client and its health probe.
func newCaddyLoader(cfg config.Config) (caddy.Loader, api.Check) {
	if cfg.FakeBackends {
		return &fakeLoader{}, ok("")
	}
	c := caddy.NewClient(cfg.CaddyAdmin)
	return c, func(ctx context.Context) (string, error) {
		_, err := c.Config(ctx)
		return "", err
	}
}

// postgresCheck probes PostgreSQL through the service's admin connection.
func postgresCheck(svc *dbs.Service) api.Check {
	return func(ctx context.Context) (string, error) {
		if v, ok := svc.PG.(interface {
			Version(context.Context) (string, error)
		}); ok {
			return v.Version(ctx)
		}
		return "fake", svc.PG.Ping(ctx)
	}
}

// fakeLoader keeps the last configuration it was given, like a Caddy that never restarts.
type fakeLoader struct {
	mu  sync.Mutex
	cfg []byte
}

func (f *fakeLoader) Load(_ context.Context, cfg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg = append([]byte(nil), cfg...)
	return nil
}

func (f *fakeLoader) Config(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.cfg...), nil
}

// fakeFail is the failure a fake container simulates, chosen with the app's
// environment variable RUNOVA_FAKE_FAIL (see docs/superpowers/notes/ui-polish-logs-ip.md):
// health (never opens its port), slow (opens it after 8 s), exit (stops with
// a Spring start-up error), oom (killed for memory) and crash (crash-loops
// after it started).
func fakeFail(spec docker.ContainerSpec) string {
	for _, e := range spec.Env {
		if v, ok := strings.CutPrefix(e, "RUNOVA_FAKE_FAIL="); ok {
			return v
		}
	}
	return ""
}

// springLines is what a Spring Boot start looks like in a container log.
func springLines(now time.Time, port int) []docker.LogLine {
	at := func(i int) time.Time { return now.Add(time.Duration(i) * 37 * time.Millisecond) }
	text := []string{
		"",
		"  .   ____          _            __ _ _",
		" /\\\\ / ___'_ __ _ _(_)_ __  __ _ \\ \\ \\ \\",
		"( ( )\\___ | '_ | '_| | '_ \\/ _` | \\ \\ \\ \\",
		" \\\\/  ___)| |_)| | | | | || (_| |  ) ) ) )",
		"  '  |____| .__|_| |_|_| |_\\__, | / / / /",
		" =========|_|==============|___/=/_/_/_/",
		" :: Spring Boot ::                (v4.1.2)",
		"",
		now.UTC().Format("2006-01-02T15:04:05.000Z") + "  INFO 7 --- [starboom] [           main] c.d.starboom.StarboomApplication         : Starting StarboomApplication v0.0.1 using Java 21.0.5 with PID 7 (/app/starboom.jar started by 100001 in /data)",
		now.UTC().Format("2006-01-02T15:04:05.000Z") + "  INFO 7 --- [starboom] [           main] c.d.starboom.StarboomApplication         : No active profile set, falling back to 1 default profile: \"default\"",
		now.UTC().Format("2006-01-02T15:04:05.000Z") + "  INFO 7 --- [starboom] [           main] o.s.b.w.embedded.tomcat.TomcatWebServer  : Tomcat initialized with port " + strconv.Itoa(port) + " (http)",
		now.UTC().Format("2006-01-02T15:04:05.000Z") + "  WARN 7 --- [starboom] [           main] JpaBaseConfiguration$JpaWebConfiguration : spring.jpa.open-in-view is enabled by default.",
	}
	out := make([]docker.LogLine, len(text))
	for i, t := range text {
		out[i] = docker.LogLine{Stream: "stdout", Time: at(i), Text: t}
	}
	return out
}

// newFakeEngine is an in-memory Docker whose containers log a few lines when
// they start and answer the terminal with a shell prompt.
func newFakeEngine() *dockertest.Fake {
	f := dockertest.New()
	f.StatsResult = docker.Stats{MemoryBytes: 120 << 20, MemoryLimit: 512 << 20, CPUPercent: 18}
	f.OnStart = func(c dockertest.Container) {
		now := time.Now()
		var lines []docker.LogLine
		jar := strings.HasPrefix(c.Spec.Image, "eclipse-temurin")
		if jar {
			lines = springLines(now, c.Spec.InternalPort)
		} else {
			lines = []docker.LogLine{{Stream: "stdout", Time: now, Text: "runova  menjalankan " + c.Spec.Image}}
		}
		fail := fakeFail(c.Spec)
		switch fail {
		case "exit":
			ts := now.UTC().Format("2006-01-02T15:04:05.000Z")
			lines = append(lines,
				docker.LogLine{Stream: "stdout", Time: now, Text: ts + " ERROR 7 --- [starboom] [           main] o.s.boot.SpringApplication               : Application run failed"},
				docker.LogLine{Stream: "stdout", Time: now, Text: "org.springframework.beans.factory.BeanCreationException: Error creating bean with name 'jwtFilter': Could not resolve placeholder 'JWT_ISSUER' in value \"${JWT_ISSUER}\""},
				docker.LogLine{Stream: "stdout", Time: now, Text: "\tat org.springframework.beans.factory.support.AbstractAutowireCapableBeanFactory.doCreateBean(AbstractAutowireCapableBeanFactory.java:579)"},
				docker.LogLine{Stream: "stdout", Time: now, Text: "\tat org.springframework.boot.SpringApplication.run(SpringApplication.java:318)"},
				docker.LogLine{Stream: "stdout", Time: now, Text: "Caused by: java.lang.IllegalArgumentException: Could not resolve placeholder 'JWT_ISSUER' in value \"${JWT_ISSUER}\""},
				docker.LogLine{Stream: "stdout", Time: now, Text: "\t... 42 common frames omitted"},
			)
		case "oom":
			lines = append(lines, docker.LogLine{Stream: "stderr", Time: now, Text: "Terminating due to java.lang.OutOfMemoryError: Java heap space"})
		case "health":
			if jar {
				// The app listens on a port of its own: what a wrong server.port looks like.
				lines = append(lines, docker.LogLine{Stream: "stdout", Time: now, Text: now.UTC().Format("2006-01-02T15:04:05.000Z") + "  INFO 7 --- [starboom] [           main] o.s.b.w.embedded.tomcat.TomcatWebServer  : Tomcat started on port 9090 (http) with context path '/'"})
			}
		default:
			lines = append(lines,
				docker.LogLine{Stream: "stdout", Time: now, Text: fmt.Sprintf("Listening on port %d", c.Spec.InternalPort)},
				docker.LogLine{Stream: "stderr", Time: now, Text: "WARN  slow connection acquire 812ms"},
				docker.LogLine{Stream: "stderr", Time: now, Text: "ERROR mailer: SMTP timed out"},
			)
		}
		for _, e := range c.Spec.Env {
			if v, ok := strings.CutPrefix(e, "LOG_ECHO="); ok {
				lines = append(lines, docker.LogLine{Stream: "stdout", Time: now, Text: v})
			}
		}
		f.Update(c.ID, func(ct *dockertest.Container) {
			ct.Logs = append(ct.Logs, lines...)
			switch fail {
			case "exit":
				ct.State.Running, ct.State.Status, ct.State.ExitCode = false, "exited", 1
			case "oom":
				ct.State.Running, ct.State.Status, ct.State.ExitCode, ct.State.OOMKilled = false, "exited", 137, true
			}
		})
		if fail == "crash" {
			go fakeCrashLoop(f, c)
		}
	}
	f.ExecHandler = func(_ string, _ []string, conn io.ReadWriteCloser) {
		defer conn.Close()
		conn.Write([]byte("$ "))
		buf := make([]byte, 1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				conn.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}
	return f
}

// fakeCrashLoop makes a started container die six times, as Docker's restart
// policy would report an app that exits right after starting.
func fakeCrashLoop(f *dockertest.Fake, c dockertest.Container) {
	time.Sleep(3 * time.Second) // after the deploy saw it healthy
	for i := range 6 {
		now := time.Now()
		f.Update(c.ID, func(ct *dockertest.Container) {
			ct.Logs = append(ct.Logs, docker.LogLine{Stream: "stderr", Time: now, Text: fmt.Sprintf("panic: runtime error: invalid memory address or nil pointer dereference (restart %d)", i+1)})
			ct.State.ExitCode = 2
		})
		labels := maps.Clone(c.Spec.Labels)
		labels["exitCode"] = "2"
		f.Emit(docker.Event{ContainerID: c.ID, Action: "die", Labels: labels, Time: now})
		time.Sleep(200 * time.Millisecond)
	}
}

// fakeApps adjusts the app service so deploys against the fake engine finish:
// health probes succeed (except for containers simulating a failure, see
// fakeFail), ownership changes are skipped and domains starting with
// "pending." never get a certificate.
func fakeApps(svc *apps.Service, engine docker.Engine) {
	fake, _ := engine.(*dockertest.Fake)
	svc.Chown = func(string, int) error { return nil }
	svc.HealthTimeout = 15 * time.Second
	svc.Dial = func(_, addr string, _ time.Duration) (net.Conn, error) {
		if fake != nil {
			_, port, _ := net.SplitHostPort(addr)
			for _, c := range fake.Containers() {
				if strconv.Itoa(c.Spec.HostPort) != port {
					continue
				}
				switch fakeFail(c.Spec) {
				case "health":
					return nil, errors.New("connection refused")
				case "slow":
					if time.Since(c.State.StartedAt) < 8*time.Second {
						return nil, errors.New("connection refused")
					}
				}
			}
		}
		// The far end stays open and silent, which the health probe reads as a
		// listening app (an immediately closed peer means "nothing listens").
		a, b := net.Pipe()
		go func() {
			io.Copy(io.Discard, b)
			b.Close()
		}()
		return a, nil
	}
	svc.DNS = fakeDNS{}
	svc.Probe = func(_ context.Context, host string, _ bool) (bool, string) {
		if strings.HasPrefix(host, "pending.") {
			return false, "Caddy belum punya sertifikat untuk domain ini"
		}
		return true, ""
	}
}

// fakeDNS answers hosting-domain DNS checks without a network: domains starting
// with "bad." have no record, "proxy." ones sit behind Cloudflare and "nat."
// ones point at 117.54.12.173 until that is the public IP.
type fakeDNS struct{}

func (fakeDNS) Check(_ context.Context, domain, publicIP string) hostdns.Result {
	switch {
	case strings.HasPrefix(domain, "bad."):
		return hostdns.Result{Detail: "Belum ada record DNS untuk *." + domain}
	case strings.HasPrefix(domain, "proxy."):
		return hostdns.Result{Detail: "Alamat itu milik Cloudflare (proxy menyala); matikan proxy: pilih DNS only"}
	case strings.HasPrefix(domain, "nat.") && publicIP != "117.54.12.173":
		// a server behind nginx at 117.54.12.173 (docs/nginx-front-proxy.md)
		return hostdns.Result{Detail: "Record menunjuk ke 117.54.12.173, bukan ke server ini (10.81.20.111, 172.31.0.1)"}
	}
	return hostdns.Result{OK: true}
}

// fakeDatabases builds the database service over an in-memory PostgreSQL.
// pg_dump and pg_restore become scripts that write and accept a stub dump.
func fakeDatabases(ctx context.Context, cfg config.Config, st *store.Store, box *secret.Box) (*dbs.Service, func(), error) {
	bin := filepath.Join(cfg.DataDir, "fakebin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return nil, nil, err
	}
	dump := filepath.Join(bin, "pg_dump")
	restore := filepath.Join(bin, "pg_restore")
	// A real custom-format header, so a downloaded export restores through the upload path too.
	if err := os.WriteFile(dump, []byte("#!/bin/sh\nprintf 'PGDMP fake dump\\n'\n"), 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(restore, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		return nil, nil, err
	}
	fake := pgfake.New()
	users := fake.Users()
	users.Demo = true
	svc := &dbs.Service{
		Store: st, PG: fake, Users: users, Box: box, BackupDir: cfg.BackupDir(),
		HBAPath: filepath.Join(cfg.DataDir, "pg_hba.conf"), PGDump: dump, PGRestore: restore,
	}
	// Like the real startup: users that exist before this process (the installer's admin) get their roles.
	if err := svc.ReconcileRoles(ctx); err != nil {
		return nil, nil, err
	}
	return svc, func() {}, nil
}
