package caddy_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"runova/internal/caddy"
)

// fakeLoader is a Caddy that remembers the last loaded configuration.
type fakeLoader struct {
	mu       sync.Mutex
	config   []byte
	loads    int
	active   int
	overlap  bool
	loadErr  error
	loadWork time.Duration
}

func (f *fakeLoader) Load(_ context.Context, cfg []byte) error {
	f.mu.Lock()
	f.active++
	if f.active > 1 {
		f.overlap = true
	}
	work, err := f.loadWork, f.loadErr
	f.mu.Unlock()

	time.Sleep(work)

	f.mu.Lock()
	defer f.mu.Unlock()
	f.active--
	if err != nil {
		return err
	}
	f.loads++
	f.config = cfg
	return nil
}

func (f *fakeLoader) Config(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.config == nil {
		return []byte("null\n"), nil
	}
	return f.config, nil
}

func (f *fakeLoader) restart() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.config = nil
}

func (f *fakeLoader) loadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loads
}

func staticState(s caddy.State) func(context.Context) (caddy.State, error) {
	return func(context.Context) (caddy.State, error) { return s, nil }
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSyncerReloadsAfterCaddyRestart(t *testing.T) {
	loader := &fakeLoader{}
	s := &caddy.Syncer{
		Client:   loader,
		State:    staticState(caddy.State{Sites: []caddy.Site{{Host: "a.co", Upstream: "127.0.0.1:30001"}}}),
		Interval: 20 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, "initial load", func() bool { return loader.loadCount() == 1 })
	time.Sleep(80 * time.Millisecond)
	if n := loader.loadCount(); n != 1 {
		t.Fatalf("loads = %d while Caddy is unchanged, want 1", n)
	}

	loader.restart()
	start := time.Now()
	waitFor(t, "reload after restart", func() bool { return loader.loadCount() == 2 })
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("reload took %s, want within a few intervals", d)
	}
	if cfg, _ := loader.Config(ctx); !strings.Contains(string(cfg), "site:a.co") {
		t.Errorf("reloaded config lacks the site: %s", cfg)
	}
}

func TestSyncerReloadsWhenRunovaServerMissing(t *testing.T) {
	loader := &fakeLoader{config: []byte(`{"apps":{"http":{"servers":{"other":{}}}}}`)}
	s := &caddy.Syncer{Client: loader, State: staticState(caddy.State{}), Interval: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, "load over foreign config", func() bool { return loader.loadCount() >= 1 })
}

func listenConfig(ports ...string) []byte {
	servers := ""
	for i, p := range ports {
		if i > 0 {
			servers += ","
		}
		servers += `"s` + p + `":{"listen":[":` + p + `"]}`
	}
	return []byte(`{"apps":{"http":{"servers":{` + servers + `}}}}`)
}

func TestApplyRefusesPortInUseWithoutLoading(t *testing.T) {
	loader := &fakeLoader{config: listenConfig("20001")}
	var checked []int
	s := &caddy.Syncer{
		Client: loader,
		State: staticState(caddy.State{
			IPListeners: []caddy.IPListener{{Port: 20001, Upstream: "127.0.0.1:1"}, {Port: 20002, Upstream: "127.0.0.1:2"}},
			PanelIPPort: 8443, PanelUpstream: "127.0.0.1:7070",
		}),
		PortFree: func(port int) error {
			checked = append(checked, port)
			if port == 20002 {
				return errors.New("taken")
			}
			return nil
		},
	}
	err := s.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "20002") {
		t.Fatalf("Apply = %v, want an error naming the busy port", err)
	}
	if loader.loadCount() != 0 {
		t.Error("Caddy was asked to load a configuration whose port is taken")
	}
	if !slices.Equal(checked, []int{8443, 20002}) {
		t.Errorf("checked ports %v, want only the ones Caddy does not hold yet: [8443 20002]", checked)
	}
}

func TestApplyLoadsWhenPortsAreFree(t *testing.T) {
	loader := &fakeLoader{}
	s := &caddy.Syncer{
		Client:   loader,
		State:    staticState(caddy.State{IPListeners: []caddy.IPListener{{Port: 20001, Upstream: "127.0.0.1:1"}}}),
		PortFree: func(int) error { return nil },
	}
	if err := s.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loader.loadCount() != 1 {
		t.Errorf("loads = %d, want 1", loader.loadCount())
	}
}

func TestPortFree(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := caddy.PortFree(port); err == nil {
		t.Errorf("port %d is held but reported free", port)
	}
	ln.Close()
	if err := caddy.PortFree(port); err != nil {
		t.Errorf("port %d was released but reported busy: %v", port, err)
	}
}

func TestApplySerialized(t *testing.T) {
	loader := &fakeLoader{loadWork: 2 * time.Millisecond}
	s := &caddy.Syncer{Client: loader, State: staticState(caddy.State{})}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Apply(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if loader.overlap {
		t.Error("two Load calls ran at the same time")
	}
	if n := loader.loadCount(); n != 10 {
		t.Errorf("loads = %d, want 10", n)
	}
}

func TestApplyReturnsLoadError(t *testing.T) {
	loader := &fakeLoader{loadErr: errors.New("bad upstream")}
	s := &caddy.Syncer{Client: loader, State: staticState(caddy.State{})}
	if err := s.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "bad upstream") {
		t.Fatalf("Apply = %v", err)
	}
}

func TestApplyBuildsFromCurrentState(t *testing.T) {
	loader := &fakeLoader{}
	var host atomic.Value
	host.Store("a.co")
	s := &caddy.Syncer{Client: loader, State: func(context.Context) (caddy.State, error) {
		return caddy.State{Sites: []caddy.Site{{Host: host.Load().(string), Upstream: "127.0.0.1:30001"}}}, nil
	}}
	for _, h := range []string{"a.co", "b.co"} {
		host.Store(h)
		if err := s.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		cfg, _ := loader.Config(context.Background())
		if !strings.Contains(string(cfg), "site:"+h) {
			t.Errorf("config after switching to %s: %s", h, cfg)
		}
	}
}

func TestApplyStateError(t *testing.T) {
	loader := &fakeLoader{}
	s := &caddy.Syncer{Client: loader, State: func(context.Context) (caddy.State, error) { return caddy.State{}, errors.New("db gone") }}
	if err := s.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "db gone") {
		t.Fatalf("Apply = %v", err)
	}
	if loader.loadCount() != 0 {
		t.Error("loaded a configuration although the state could not be read")
	}
}

func TestSyncerReportsErrors(t *testing.T) {
	loader := &fakeLoader{loadErr: errors.New("refused")}
	errs := make(chan error, 8)
	s := &caddy.Syncer{
		Client: loader, State: staticState(caddy.State{}), Interval: 5 * time.Millisecond,
		OnError: func(err error) {
			select {
			case errs <- err:
			default:
			}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case err := <-errs:
		if !strings.Contains(err.Error(), "refused") {
			t.Errorf("OnError got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("no error reported")
	}
}

func TestLoadErrorSurfaced(t *testing.T) {
	var got struct {
		path, ctype, body string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.path, got.ctype, got.body = r.URL.Path, r.Header.Get("Content-Type"), string(b)
		if r.Header.Get("Origin") != "" {
			t.Errorf("Origin header sent: %q", r.Header.Get("Origin"))
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"loading config: bad"}`))
	}))
	defer srv.Close()

	err := caddy.NewClient(srv.URL).Load(context.Background(), []byte(`{"x":1}`))
	if err == nil || !strings.Contains(err.Error(), "loading config: bad") {
		t.Fatalf("Load = %v, want the error text of Caddy's response", err)
	}
	if got.path != "/load" || got.ctype != "application/json" || got.body != `{"x":1}` {
		t.Errorf("request = %+v", got)
	}
}

func TestLoadErrorWithoutJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	err := caddy.NewClient(srv.URL).Load(context.Background(), []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Load = %v", err)
	}
}

func TestClientConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config/" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("null\n"))
	}))
	defer srv.Close()
	cfg, err := caddy.NewClient(srv.URL).Config(context.Background())
	if err != nil || strings.TrimSpace(string(cfg)) != "null" {
		t.Fatalf("Config = %q, %v", cfg, err)
	}
}

// tlsServer serves TLS with a certificate for names signed by a fresh CA, or
// with no certificate at all (like Caddy before it has one) when names is empty.
func tlsServer(t *testing.T, names ...string) (addr string, ca *x509.Certificate) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	ca, _ = x509.ParseCertificate(caDER)
	cfg := &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return nil, errors.New("no certificate")
	}}
	if len(names) > 0 {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		leaf := &x509.Certificate{
			SerialNumber: big.NewInt(2), DNSNames: names,
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		der, _ := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
		cfg = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = cfg
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String(), ca
}

func pool(certs ...*x509.Certificate) *x509.CertPool {
	p := x509.NewCertPool()
	for _, c := range certs {
		p.AddCert(c)
	}
	return p
}

func TestProbeDirect(t *testing.T) {
	ctx := context.Background()
	addr, ca := tlsServer(t, "x.co")
	if ok, detail := (caddy.Prober{Addr: addr, Roots: pool(ca)}).Probe(ctx, "x.co", false); !ok {
		t.Errorf("trusted certificate rejected: %s", detail)
	}
	for name, p := range map[string]struct {
		prober caddy.Prober
		host   string
		want   string
	}{
		"untrusted":    {caddy.Prober{Addr: addr, Roots: x509.NewCertPool()}, "x.co", "Sertifikat belum dipercaya"},
		"other name":   {caddy.Prober{Addr: addr, Roots: pool(ca)}, "y.co", "Sertifikat bukan untuk domain ini"},
		"closed port":  {caddy.Prober{Addr: "127.0.0.1:1", Roots: pool(ca)}, "x.co", "Caddy tidak bisa dihubungi di port 443"},
		"internal CA":  {caddy.Prober{Addr: addr, Roots: x509.NewCertPool(), LocalCA: func(context.Context) (*x509.Certificate, error) { return ca, nil }}, "x.co", "Sertifikat belum dipercaya"},
		"without cert": {caddy.Prober{Addr: func() string { a, _ := tlsServer(t); return a }(), Roots: pool(ca)}, "startboom.app.co", "Caddy belum mendapat sertifikat untuk startboom.app.co. Pastikan port 80 dan 443 terbuka dan DNS menunjuk ke server ini. Bila ada nginx di depan, isi Proxy di depan Runova. (remote error: tls: internal error)"},
	} {
		ok, detail := p.prober.Probe(ctx, p.host, false)
		if ok || !strings.HasPrefix(detail, p.want) {
			t.Errorf("%s: ok=%v detail=%q, want prefix %q", name, ok, detail, p.want)
		}
	}
}

func TestProbeFrontProxy(t *testing.T) {
	ctx := context.Background()
	const host = "startboom.app.co"
	local, internal := tlsServer(t, host)
	localCA := func(context.Context) (*x509.Certificate, error) { return internal, nil }
	public, publicCA := tlsServer(t, "*.app.co")
	_, port, _ := net.SplitHostPort(public)
	resolve := func(addrs ...string) func(context.Context, string) ([]string, error) {
		return func(_ context.Context, h string) ([]string, error) {
			if h != host {
				t.Errorf("resolved %q", h)
			}
			if len(addrs) == 0 {
				return nil, &net.DNSError{Err: "no such host", Name: h, IsNotFound: true}
			}
			return addrs, nil
		}
	}
	visitorRoots := pool(publicCA) // the system roots of a visitor; the internal CA is not among them
	base := caddy.Prober{Addr: local, LocalCA: localCA, PublicPort: port, Roots: visitorRoots}

	p := base
	p.LookupHost = resolve("127.0.0.1")
	if ok, detail := p.Probe(ctx, host, true); !ok {
		t.Errorf("internal certificate at Caddy and a trusted one at the proxy: %s", detail)
	}
	p.LookupHost = resolve()
	if ok, detail := p.Probe(ctx, host, true); !ok {
		t.Errorf("no DNS yet must not fail the check of Caddy's certificate: %s", detail)
	}
	p.LookupHost, p.PublicPort = resolve("127.0.0.1"), "1"
	if ok, detail := p.Probe(ctx, host, true); !ok {
		t.Errorf("a public address unreachable from the server proves nothing: %s", detail)
	}
	if ok, _ := (caddy.Prober{Addr: local, Roots: visitorRoots}).Probe(ctx, host, false); ok {
		t.Error("without front-proxy mode the internal CA must not be trusted")
	}

	wrong, wrongCA := tlsServer(t, "runova.prodak.id")
	_, wrongPort, _ := net.SplitHostPort(wrong)
	p = base
	p.LookupHost, p.PublicPort, p.Roots = resolve("127.0.0.1"), wrongPort, pool(publicCA, wrongCA)
	ok, detail := p.Probe(ctx, host, true)
	if ok || !strings.HasPrefix(detail, "Proxy di depan (nginx) di 127.0.0.1 belum menyajikan sertifikat tepercaya untuk "+host+": sertifikat bukan untuk domain ini.") {
		t.Errorf("proxy with a certificate for another name: ok=%v %q", ok, detail)
	}

	none, _ := tlsServer(t)
	p = base
	p.Addr, p.LookupHost = none, resolve("127.0.0.1")
	ok, detail = p.Probe(ctx, host, true)
	if ok || !strings.HasPrefix(detail, "Caddy belum punya sertifikat untuk "+host+" dari CA internalnya") {
		t.Errorf("Caddy without a certificate: ok=%v %q", ok, detail)
	}

	p = base
	p.LocalCA = func(context.Context) (*x509.Certificate, error) { return nil, errors.New("connection refused") }
	if ok, detail := p.Probe(ctx, host, true); ok || !strings.Contains(detail, "CA internal Caddy") {
		t.Errorf("unreadable internal CA: ok=%v %q", ok, detail)
	}
}

func TestClientLocalCA(t *testing.T) {
	_, ca := tlsServer(t, "x.co")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pki/ca/local" {
			t.Errorf("path = %s", r.URL.Path)
		}
		root := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "local", "root_certificate": string(root)})
	}))
	defer srv.Close()
	got, err := caddy.NewClient(srv.URL).LocalCA(context.Background())
	if err != nil || !got.Equal(ca) {
		t.Fatalf("LocalCA = %v, %v", got, err)
	}
}
