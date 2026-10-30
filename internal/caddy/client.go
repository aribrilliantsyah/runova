package caddy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
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

// ProbeTLS connects to this machine's port 443 with host as SNI and reports
// whether Caddy presents a certificate that the system trusts for host. On
// failure detail says why, briefly.
func ProbeTLS(ctx context.Context, host string) (ok bool, detail string) {
	return ProbeTLSAt(ctx, httpsAddr, host, nil)
}

// ProbeTLSAt is ProbeTLS against addr, verifying with roots (nil: system roots).
func ProbeTLSAt(ctx context.Context, addr, host string, roots *x509.CertPool) (ok bool, detail string) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	d := tls.Dialer{
		NetDialer: &net.Dialer{},
		Config:    &tls.Config{ServerName: host, RootCAs: roots, MinVersion: tls.VersionTLS12},
	}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false, probeDetail(err)
	}
	conn.Close()
	return true, ""
}

func probeDetail(err error) string {
	var (
		unknown x509.UnknownAuthorityError
		host    x509.HostnameError
		invalid x509.CertificateInvalidError
		alert   tls.AlertError
		op      *net.OpError
	)
	switch {
	case errors.As(err, &unknown):
		return "Sertifikat belum dipercaya"
	case errors.As(err, &host):
		return "Sertifikat bukan untuk domain ini"
	case errors.As(err, &invalid):
		return "Sertifikat tidak valid"
	case errors.Is(err, context.DeadlineExceeded):
		return "Koneksi TLS melewati batas waktu"
	case errors.As(err, &alert):
		return "Caddy belum punya sertifikat untuk domain ini"
	case errors.As(err, &op) && op.Op == "dial":
		return "Caddy tidak bisa dihubungi di port 443"
	default:
		return "Koneksi TLS gagal: " + err.Error()
	}
}
