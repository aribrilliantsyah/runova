package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const helloSrc = `package main

import (
	"fmt"
	"net"
	"os"
)

func main() {
	ln, err := net.Listen("tcp", ":"+os.Getenv("PORT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("hello from runova")
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		c.Close()
	}
}
`

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// buildStatic compiles helloSrc with CGO disabled into a world-executable file.
func buildStatic(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(helloSrc), 0o644); err != nil {
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
	if err := os.Chmod(out, 0o755); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIntegrationEngine(t *testing.T) {
	if os.Getenv("RUNOVA_DOCKER_TEST") != "1" {
		t.Skip("set RUNOVA_DOCKER_TEST=1 to run against a real Docker Engine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := New(os.Getenv("RUNOVA_DOCKER_HOST"))
	if err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	const (
		static = "gcr.io/distroless/static-debian12"
		glibc  = "ubuntu:26.04"
		netw   = "runova-test"
		uid    = "100001:100001"
	)
	for _, img := range []string{static, glibc} {
		if err := c.PullImage(ctx, img); err != nil {
			t.Fatalf("pull %s: %v", img, err)
		}
		if ok, err := c.ImageExists(ctx, img); err != nil || !ok {
			t.Fatalf("ImageExists(%s) = %v, %v", img, ok, err)
		}
	}
	if err := c.EnsureNetwork(ctx, netw, "172.30.250.0/24"); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureNetwork(ctx, netw, "172.30.250.0/24"); err != nil {
		t.Fatalf("second EnsureNetwork: %v", err)
	}
	labels := map[string]string{"runova.app": "dkitest", "runova.user": "dkitest"}

	var ids []string
	t.Cleanup(func() {
		bg := context.Background()
		for _, id := range ids {
			c.RemoveContainer(bg, id)
		}
		exec.Command("docker", "network", "rm", netw).Run()
	})

	bin := buildStatic(t)
	hostPort := freePort(t)
	id, err := c.CreateContainer(ctx, "runova-dkitest-static", ContainerSpec{
		Image: static, Cmd: []string{"/app/hello"}, Env: []string{"PORT=8080"}, User: uid,
		Labels: labels, Network: netw, InternalPort: 8080, HostPort: hostPort,
		Binds: []string{bin + ":/app/hello:ro"}, Tmpfs: map[string]string{"/tmp": "rw,size=64m"},
		MemoryBytes: 64 << 20, NanoCPUs: 250_000_000, ReadOnly: true,
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, id)
	if err := c.StartContainer(ctx, id); err != nil {
		t.Fatal(err)
	}

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(hostPort))
	var dialErr error
	for range 50 {
		var conn net.Conn
		if conn, dialErr = net.DialTimeout("tcp", addr, time.Second); dialErr == nil {
			conn.Close()
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if dialErr != nil {
		t.Fatalf("connect %s: %v", addr, dialErr)
	}

	st, err := c.InspectContainer(ctx, id)
	if err != nil || !st.Running || st.Labels["runova.app"] != "dkitest" {
		t.Fatalf("inspect = %+v, %v", st, err)
	}
	list, err := c.ListContainers(ctx, map[string]string{"runova.app": "dkitest"})
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("list = %+v, %v", list, err)
	}
	stats, err := c.Stats(ctx, id)
	if err != nil || stats.MemoryLimit != 64<<20 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}

	rc, err := c.Logs(ctx, id, 50, false)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	err = ReadLogs(rc, func(l LogLine) error { lines = append(lines, l.Text); return nil })
	rc.Close()
	if err != nil || !slices.Contains(lines, "hello from runova") {
		t.Fatalf("logs = %q, %v", lines, err)
	}

	evCtx, evCancel := context.WithCancel(ctx)
	events, evErr := c.Events(evCtx, map[string]string{"runova.app": "dkitest"})
	if err := c.StopContainer(ctx, id, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	gotDie := false
	for !gotDie {
		select {
		case ev := <-events:
			gotDie = ev.Action == "die" && ev.ContainerID == id
		case err := <-evErr:
			t.Fatalf("events ended: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("no die event")
		}
	}
	evCancel()
	if err := c.RemoveContainer(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InspectContainer(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inspect after remove = %v, want ErrNotFound", err)
	}

	shell, err := c.CreateContainer(ctx, "runova-dkitest-glibc", ContainerSpec{
		Image: glibc, Cmd: []string{"sleep", "300"}, User: uid, Labels: labels, Network: netw,
		Tmpfs: map[string]string{"/tmp": "rw,size=64m"}, MemoryBytes: 64 << 20, NanoCPUs: 250_000_000, ReadOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, shell)
	if err := c.StartContainer(ctx, shell); err != nil {
		t.Fatal(err)
	}
	sess, err := c.Exec(ctx, shell, uid, []string{"/bin/sh", "-c", "id -u; sleep 1; stty size"})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.Resize(ctx, 24, 80); err != nil {
		t.Fatalf("resize: %v", err)
	}
	var out bytes.Buffer
	io.Copy(&out, sess)
	fields := strings.Fields(out.String())
	if len(fields) != 3 || fields[0] != "100001" {
		t.Fatalf("exec output = %q, want the uid 100001 first", out.String())
	}
	if fields[1] != "24" || fields[2] != "80" {
		t.Fatalf("stty size = %q %q, want the resized 24 80", fields[1], fields[2])
	}

	// The hardening the client applies must hold inside a real container,
	// and a session closed by the caller must not leave its shell behind.
	sh, err := c.Exec(ctx, shell, uid, []string{"/bin/sh", "-c", "grep -E 'NoNewPrivs|CapEff' /proc/self/status; touch /rootfs-probe 2>&1; ulimit -u"})
	if err != nil {
		t.Fatal(err)
	}
	var hard bytes.Buffer
	io.Copy(&hard, sh)
	sh.Close()
	for _, want := range []string{"NoNewPrivs:\t1", "CapEff:\t0000000000000000", "Read-only file system"} {
		if !strings.Contains(hard.String(), want) {
			t.Errorf("hardening probe output lacks %q:\n%s", want, hard.String())
		}
	}

	idle, err := c.Exec(ctx, shell, uid, []string{"/bin/sh"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	idle.Close()
	time.Sleep(time.Second)
	ps, err := c.Exec(ctx, shell, uid, []string{"/bin/sh", "-c", "ps -eo args 2>/dev/null || ls /proc | grep -c '^[0-9]'"})
	if err != nil {
		t.Fatal(err)
	}
	var procs bytes.Buffer
	io.Copy(&procs, ps)
	ps.Close()
	if n := strings.Count(procs.String(), "/bin/sh"); n > 1 { // the probe's own shell
		t.Errorf("closed exec sessions left %d shells running:\n%s", n-1, procs.String())
	}

	// Init: PID 1 is docker-init, which reaps children and forwards signals.
	// `sleep` has no SIGTERM handler, and the kernel ignores such signals for
	// PID 1, so without an init Stop would wait out its whole grace period.
	p1, err := c.Exec(ctx, shell, uid, []string{"/bin/sh", "-c", "tr '\\0' ' ' </proc/1/cmdline"})
	if err != nil {
		t.Fatal(err)
	}
	var cmdline bytes.Buffer
	io.Copy(&cmdline, p1)
	p1.Close()
	if !strings.Contains(cmdline.String(), "docker-init") && !strings.Contains(cmdline.String(), "tini") {
		t.Errorf("PID 1 = %q, want docker-init or tini", cmdline.String())
	}
	begin := time.Now()
	if err := c.StopContainer(ctx, shell, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(begin); took > 4*time.Second {
		t.Errorf("stopping a program without a SIGTERM handler took %s; PID 1 swallowed the signal", took.Round(time.Millisecond))
	}
	t.Logf("PID 1 = %q, stop took %s", strings.TrimSpace(cmdline.String()), time.Since(begin).Round(time.Millisecond))
}
