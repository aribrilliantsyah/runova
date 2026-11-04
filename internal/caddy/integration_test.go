package caddy_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"runova/internal/caddy"
)

const caddyImage = "caddy:2.6.2"

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// testCaddy is a Caddy to run integration tests against.
type testCaddy struct {
	admin string
	// restart restarts Caddy so that it forgets the loaded configuration; nil for an external Caddy.
	restart func()
}

// startCaddy returns the Caddy named by RUNOVA_CADDY_TEST_ADMIN (an admin URL),
// or starts the pinned caddyImage on the host network with its admin API on a
// random port. The container is removed when the test ends.
func startCaddy(t *testing.T) testCaddy {
	t.Helper()
	if os.Getenv("RUNOVA_CADDY_TEST") != "1" {
		t.Skip("set RUNOVA_CADDY_TEST=1 to run against a real Caddy")
	}
	if admin := os.Getenv("RUNOVA_CADDY_TEST_ADMIN"); admin != "" {
		return testCaddy{admin: admin}
	}

	port := freePort(t)
	dir := t.TempDir()
	initial := filepath.Join(dir, "caddy.json")
	if err := os.WriteFile(initial, []byte(fmt.Sprintf(`{"admin":{"listen":"localhost:%d"}}`, port)), 0o644); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("caddytest-%d", port)
	docker(t, "run", "-d", "--name", name, "--network", "host", "-v", dir+":/etc/caddytest:ro",
		caddyImage, "caddy", "run", "--config", "/etc/caddytest/caddy.json")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })

	c := testCaddy{
		admin:   fmt.Sprintf("http://localhost:%d", port),
		restart: func() { docker(t, "restart", "-t", "1", name) },
	}
	waitAdmin(t, c.admin)
	return c
}

func waitAdmin(t *testing.T, admin string) {
	t.Helper()
	client := caddy.NewClient(admin)
	deadline := time.Now().Add(20 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := client.Config(ctx)
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Caddy admin %s did not come up: %v", admin, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func fetch(t *testing.T, url string) (int, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		return 0, err.Error()
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func waitGet(t *testing.T, url, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, body := fetch(t, url)
		if status == 200 && body == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s = %d %q, want 200 %q", url, status, body, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestCaddyIntegration(t *testing.T) {
	c := startCaddy(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "app xff=%q", r.Header.Get("X-Forwarded-For"))
	}))
	defer upstream.Close()
	ipPort := freePort(t)
	state := caddy.State{
		AdminListen: strings.TrimPrefix(c.admin, "http://"),
		IPListeners: []caddy.IPListener{{Port: ipPort, Upstream: upstream.Listener.Addr().String()}},
	}
	s := &caddy.Syncer{
		Client:   caddy.NewClient(c.admin),
		State:    func(context.Context) (caddy.State, error) { return state, nil },
		Interval: 200 * time.Millisecond,
	}
	if err := s.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", ipPort)
	waitGet(t, url, `app xff="127.0.0.1"`)

	t.Run("port held by another process is refused before Caddy loads and the old configuration keeps serving", func(t *testing.T) {
		taken, err := net.Listen("tcp", ":0")
		if err != nil {
			t.Fatal(err)
		}
		defer taken.Close()
		bad := state
		bad.IPListeners = append(slices.Clone(state.IPListeners), caddy.IPListener{Port: taken.Addr().(*net.TCPAddr).Port, Upstream: "127.0.0.1:1"})
		err = (&caddy.Syncer{
			Client: s.Client,
			State:  func(context.Context) (caddy.State, error) { return bad, nil },
		}).Apply(context.Background())
		if err == nil || !strings.Contains(err.Error(), "sudah dipakai") {
			t.Fatalf("Apply = %v, want a refusal naming the busy port", err)
		}
		t.Logf("refusal: %v", err)
		waitGet(t, url, `app xff="127.0.0.1"`)
	})

	t.Run("after a refused change, removing the listener still closes the port", func(t *testing.T) {
		off := state
		off.IPListeners = nil
		if err := (&caddy.Syncer{Client: s.Client, State: func(context.Context) (caddy.State, error) { return off, nil }}).Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", ipPort), time.Second)
			if err != nil {
				break
			}
			conn.Close()
			if time.Now().After(deadline) {
				t.Fatal("port still accepts connections")
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err := s.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		waitGet(t, url, `app xff="127.0.0.1"`)
	})

	t.Run("syncer reloads after Caddy restarts", func(t *testing.T) {
		if c.restart == nil {
			t.Skip("external Caddy cannot be restarted by the test")
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		defer func() { cancel(); <-done }()

		c.restart()
		waitAdmin(t, c.admin)
		waitGet(t, url, `app xff="127.0.0.1"`)
	})
}

func TestCaddyTrustedProxyIntegration(t *testing.T) {
	c := startCaddy(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "xff=%q", r.Header.Get("X-Forwarded-For"))
	}))
	defer upstream.Close()
	port := freePort(t)
	apply := func(trusted []string) {
		t.Helper()
		state := caddy.State{
			AdminListen:    strings.TrimPrefix(c.admin, "http://"),
			IPListeners:    []caddy.IPListener{{Port: port, Upstream: upstream.Listener.Addr().String()}},
			TrustedProxies: trusted,
		}
		s := &caddy.Syncer{Client: caddy.NewClient(c.admin), State: func(context.Context) (caddy.State, error) { return state, nil }}
		if err := s.Apply(context.Background()); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	// The test client plays the proxy: it connects from 127.0.0.1 and sends the
	// header a proxy would have built.
	get := func(want string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
			req.Header.Set("X-Forwarded-For", "198.51.100.9")
			res, err := http.DefaultClient.Do(req)
			got := ""
			if err == nil {
				b, _ := io.ReadAll(res.Body)
				res.Body.Close()
				got = string(b)
			}
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("upstream saw %q (err %v), want %q", got, err, want)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	apply(nil)
	get(`xff="127.0.0.1"`)
	apply([]string{"127.0.0.1"})
	get(`xff="198.51.100.9, 127.0.0.1"`)
	apply([]string{"10.99.0.0/16"})
	get(`xff="127.0.0.1"`)
}

// Front-proxy mode on a real Caddy: a public-looking name that no public CA
// will ever issue gets an internal certificate at once, the probe accepts it
// through the admin API's root, and clearing the proxies sends the name back
// to the public issuers (Caddy then has no certificate and the probe says so).
func TestCaddyFrontProxyIntegration(t *testing.T) {
	c := startCaddy(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "app") }))
	defer upstream.Close()
	const host = "startboom.app.runova-lab.test"
	state := caddy.State{
		AdminListen:    strings.TrimPrefix(c.admin, "http://"),
		Sites:          []caddy.Site{{Host: host, Upstream: upstream.Listener.Addr().String()}},
		TrustedProxies: []string{"10.99.0.1"},
	}
	client := caddy.NewClient(c.admin)
	s := &caddy.Syncer{Client: client, State: func(context.Context) (caddy.State, error) { return state, nil }}
	if err := s.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	noDNS := func(context.Context, string) ([]string, error) { return nil, &net.DNSError{IsNotFound: true} }
	p := caddy.Prober{LocalCA: client.LocalCA, LookupHost: noDNS}
	deadline := time.Now().Add(15 * time.Second)
	for {
		ok, detail := p.Probe(context.Background(), host, true)
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("front-proxy probe: %s", detail)
		}
		time.Sleep(200 * time.Millisecond)
	}

	state.TrustedProxies = nil
	if err := s.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ok, detail := p.Probe(context.Background(), host, false)
	if ok || !strings.HasPrefix(detail, "Caddy belum mendapat sertifikat untuk "+host) {
		t.Fatalf("without a proxy: ok=%v %q, want Caddy without a certificate (no public CA issues .test)", ok, detail)
	}
	t.Logf("without a proxy: %s", detail)
}
