// Package hostdns checks that a wildcard hosting domain points at this server.
package hostdns

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

const defaultTimeout = 5 * time.Second

// Resolver is the part of net.Resolver the check needs.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// Result is the outcome of one check; Detail explains a failure.
type Result struct {
	OK     bool
	Detail string
}

// Checker resolves a random name under the domain: if the wildcard record
// exists, that name answers with the server's address. The zero value uses the
// system resolver, the machine's own addresses, random 6-hex tokens and a
// 5 second timeout.
type Checker struct {
	Resolver Resolver
	// LocalIPs lists this machine's addresses, used when no public IP is configured.
	LocalIPs func() []netip.Addr
	Token    func() string
	Timeout  time.Duration
}

// Check looks up runova-check-<token>.<domain>. publicIP is the configured
// server address; empty means the machine's own non-loopback addresses.
func (c Checker) Check(ctx context.Context, domain, publicIP string) Result {
	want, err := c.expected(publicIP)
	if err != nil {
		return Result{Detail: err.Error()}
	}
	resolver := c.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	token := randomToken
	if c.Token != nil {
		token = c.Token
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	found, err := resolver.LookupHost(ctx, "runova-check-"+token()+"."+domain)
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return Result{Detail: "Belum ada record DNS untuk *." + domain}
	case err != nil:
		return Result{Detail: "Pencarian DNS gagal: " + err.Error()}
	}

	var got []netip.Addr
	proxied := false
	for _, s := range found {
		a, err := netip.ParseAddr(s)
		if err != nil {
			continue
		}
		a = a.Unmap()
		got = append(got, a)
		for _, w := range want {
			if a == w {
				return Result{OK: true}
			}
		}
		proxied = proxied || IsCloudflare(a)
	}
	if proxied {
		return Result{Detail: "Alamat itu milik Cloudflare (proxy menyala); matikan proxy: pilih DNS only"}
	}
	return Result{Detail: fmt.Sprintf("Record menunjuk ke %s, bukan ke server ini (%s)", joinAddrs(got), joinAddrs(want))}
}

func (c Checker) expected(publicIP string) ([]netip.Addr, error) {
	if publicIP != "" {
		a, err := netip.ParseAddr(publicIP)
		if err != nil {
			return nil, errors.New("IP publik server tidak valid")
		}
		return []netip.Addr{a.Unmap()}, nil
	}
	local := localAddrs
	if c.LocalIPs != nil {
		local = c.LocalIPs
	}
	var out []netip.Addr
	for _, a := range local() {
		if !a.IsLoopback() && !a.IsLinkLocalUnicast() {
			out = append(out, a.Unmap())
		}
	}
	if len(out) == 0 {
		return nil, errors.New("IP publik server belum diketahui; isi di Setelan")
	}
	return out, nil
}

func localAddrs() []netip.Addr {
	addrs, _ := net.InterfaceAddrs()
	var out []netip.Addr
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			out = append(out, p.Addr())
		}
	}
	return out
}

func joinAddrs(a []netip.Addr) string {
	s := make([]string, len(a))
	for i, x := range a {
		s[i] = x.String()
	}
	return strings.Join(s, ", ")
}

func randomToken() string {
	var b [3]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
