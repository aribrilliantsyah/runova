package caddy

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// DefaultAdminURL is Caddy's admin API on the same host.
const DefaultAdminURL = "http://127.0.0.1:2019"

const (
	requestTimeout = 30 * time.Second
	maxReply       = 1 << 20
	probeTimeout   = 5 * time.Second
	httpsAddr      = "127.0.0.1:443"
)

// Client talks to Caddy's admin API. It never sends an Origin header, which
// Caddy would check against its allowed origins.
type Client struct {
	base string
	http *http.Client
}

func NewClient(adminURL string) *Client {
	return &Client{base: strings.TrimRight(adminURL, "/"), http: &http.Client{Timeout: requestTimeout}}
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("caddy %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("caddy %s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	reply, err := io.ReadAll(io.LimitReader(res.Body, maxReply))
	if err != nil {
		return nil, fmt.Errorf("caddy %s %s: %w", method, path, err)
	}
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("caddy %s %s: %d %s", method, path, res.StatusCode, replyMessage(reply))
	}
	return reply, nil
}

// replyMessage returns the "error" field of Caddy's JSON error reply, or the raw text.
func replyMessage(reply []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(reply, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(reply))
}

// Load replaces Caddy's whole configuration with cfg.
func (c *Client) Load(ctx context.Context, cfg []byte) error {
	_, err := c.do(ctx, http.MethodPost, "/load", cfg)
	return err
}

// Config returns Caddy's running configuration; "null" after a restart without one.
func (c *Client) Config(ctx context.Context) ([]byte, error) {
	return c.do(ctx, http.MethodGet, "/config/", nil)
}

// LocalCA returns the root of Caddy's internal CA ("local"), which signs the
// certificates of front-proxy mode.
func (c *Client) LocalCA(ctx context.Context) (*x509.Certificate, error) {
	reply, err := c.do(ctx, http.MethodGet, "/pki/ca/local", nil)
	if err != nil {
		return nil, err
	}
	var ca struct {
		Root string `json:"root_certificate"`
	}
	if err := json.Unmarshal(reply, &ca); err != nil {
		return nil, fmt.Errorf("caddy internal CA: %w", err)
	}
	block, _ := pem.Decode([]byte(ca.Root))
	if block == nil {
		return nil, errors.New("caddy internal CA: no root certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

// Prober checks whether a domain's HTTPS works. The zero value probes Caddy at
// 127.0.0.1:443, reads the internal CA from DefaultAdminURL, resolves with the
// system resolver and trusts the system roots.
type Prober struct {
	// Addr is Caddy's HTTPS address; empty means 127.0.0.1:443.
	Addr string
	// LocalCA returns the root of Caddy's internal CA; nil means NewClient(DefaultAdminURL).LocalCA.
	LocalCA func(ctx context.Context) (*x509.Certificate, error)
	// LookupHost resolves a host's public addresses; nil means net.DefaultResolver.LookupHost.
	LookupHost func(ctx context.Context, host string) ([]string, error)
	// PublicPort is the HTTPS port of a host's public address; empty means 443.
	PublicPort string
	// Roots are the roots a visitor trusts; nil means the system roots.
	Roots *x509.CertPool
}

// ProbeTLS probes host with the zero Prober.
func ProbeTLS(ctx context.Context, host string, frontProxy bool) (ok bool, detail string) {
	return Prober{}.Probe(ctx, host, frontProxy)
}

// Probe reports whether HTTPS works for host; on failure detail says why and
// what to do, in Indonesian, with the technical error last.
//
// Without a front proxy visitors reach Caddy, so Caddy must present a
// certificate the system trusts for host.
//
// With a front proxy (nginx) Caddy holds a certificate of its internal CA,
// which the probe trusts. The proxy holds the public one: when host resolves
// and its public address presents a certificate that fails verification, the
// check fails and names the proxy. A public address that cannot be reached
// from this server (no DNS yet, NAT without hairpin, firewall) proves nothing
// either way, so it does not fail the check.
func (p Prober) Probe(ctx context.Context, host string, frontProxy bool) (ok bool, detail string) {
	addr := cmp.Or(p.Addr, httpsAddr)
	if !frontProxy {
		if err := handshake(ctx, addr, host, p.Roots); err != nil {
			return false, localDetail(err, host, false)
		}
		return true, ""
	}
	localCA := p.LocalCA
	if localCA == nil {
		localCA = NewClient(DefaultAdminURL).LocalCA
	}
	root, err := localCA(ctx)
	if err != nil {
		return false, "Runova tidak bisa membaca CA internal Caddy (" + err.Error() + ")"
	}
	roots := p.Roots
	if roots == nil {
		if roots, err = x509.SystemCertPool(); err != nil {
			roots = x509.NewCertPool()
		}
	} else {
		roots = roots.Clone()
	}
	roots.AddCert(root)
	if err := handshake(ctx, addr, host, roots); err != nil {
		return false, localDetail(err, host, true)
	}
	return p.probePublic(ctx, host)
}

// probePublic checks the certificate visitors get from host's first public address.
func (p Prober) probePublic(ctx context.Context, host string) (bool, string) {
	lookup := p.LookupHost
	if lookup == nil {
		lookup = net.DefaultResolver.LookupHost
	}
	lctx, cancel := context.WithTimeout(ctx, probeTimeout)
	addrs, err := lookup(lctx, host)
	cancel()
	if err != nil || len(addrs) == 0 {
		return true, ""
	}
	public := net.JoinHostPort(addrs[0], cmp.Or(p.PublicPort, "443"))
	err = handshake(ctx, public, host, p.Roots)
	if err == nil || !certRefused(err) {
		return true, ""
	}
	return false, fmt.Sprintf("Proxy di depan (nginx) di %s belum menyajikan sertifikat tepercaya untuk %s: %s. Pastikan sertifikat wildcard nginx mencakup nama ini dan server_name-nya cocok. (%v)",
		addrs[0], host, certReason(err), err)
}

// handshake completes a TLS handshake with addr for host, verifying with roots (nil: system roots).
func handshake(ctx context.Context, addr, host string, roots *x509.CertPool) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	d := tls.Dialer{
		NetDialer: &net.Dialer{},
		Config:    &tls.Config{ServerName: host, RootCAs: roots, MinVersion: tls.VersionTLS12},
	}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// remoteAlert reports a TLS alert from the server. Caddy answers a handshake
// for a host it has no certificate for with "internal error". crypto/tls
// wraps the (unexported) alert in a net.OpError with Op "remote error".
func remoteAlert(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "remote error"
}

// certRefused reports a handshake that failed on the certificate the server presented or lacks.
func certRefused(err error) bool {
	return certReason(err) != "" || remoteAlert(err)
}

func certReason(err error) string {
	var (
		unknown x509.UnknownAuthorityError
		host    x509.HostnameError
		invalid x509.CertificateInvalidError
	)
	switch {
	case errors.As(err, &unknown):
		return "sertifikat belum dipercaya"
	case errors.As(err, &host):
		return "sertifikat bukan untuk domain ini"
	case errors.As(err, &invalid):
		return "sertifikat tidak valid"
	case remoteAlert(err):
		return "tidak ada sertifikat untuk nama ini"
	}
	return ""
}

// localDetail explains a failed handshake with this server's Caddy.
func localDetail(err error, host string, frontProxy bool) string {
	var op *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "Koneksi TLS ke Caddy melewati batas waktu"
	case remoteAlert(err) && frontProxy:
		return fmt.Sprintf("Caddy belum punya sertifikat untuk %s dari CA internalnya; biasanya terbit dalam beberapa detik. Cek ulang sebentar lagi, atau lihat journalctl -u caddy. (%v)", host, err)
	case remoteAlert(err):
		return fmt.Sprintf("Caddy belum mendapat sertifikat untuk %s. Pastikan port 80 dan 443 terbuka dan DNS menunjuk ke server ini. Bila ada nginx di depan, isi Proxy di depan Runova. (%v)", host, err)
	case certReason(err) != "":
		r := certReason(err)
		return strings.ToUpper(r[:1]) + r[1:] + " (" + err.Error() + ")"
	case errors.As(err, &op) && op.Op == "dial":
		return "Caddy tidak bisa dihubungi di port 443 (" + err.Error() + ")"
	default:
		return "Koneksi TLS gagal (" + err.Error() + ")"
	}
}
