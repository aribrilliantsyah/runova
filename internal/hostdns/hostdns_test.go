package hostdns

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type fakeResolver struct {
	addrs []string
	err   error
	block bool
	asked string
}

func (f *fakeResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	f.asked = host
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.addrs, f.err
}

func checker(r Resolver) Checker {
	return Checker{
		Resolver: r,
		LocalIPs: func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("10.0.0.5")} },
		Token:    func() string { return "abc123" },
		Timeout:  50 * time.Millisecond,
	}
}

func TestCheck(t *testing.T) {
	notFound := &net.DNSError{Err: "no such host", IsNotFound: true}
	cases := []struct {
		name     string
		addrs    []string
		err      error
		publicIP string
		ok       bool
		detail   string
	}{
		{"matches public ip", []string{"203.0.113.15"}, nil, "203.0.113.15", true, ""},
		{"one of several matches", []string{"198.51.100.1", "203.0.113.15"}, nil, "203.0.113.15", true, ""},
		{"empty public ip uses local address", []string{"10.0.0.5"}, nil, "", true, ""},
		{"ipv6 matches", []string{"2001:db8::1"}, nil, "2001:db8::1", true, ""},
		{"no record", nil, notFound, "203.0.113.15", false, "Belum ada record DNS untuk *.porto.ex.id"},
		{"other ip", []string{"198.51.100.9"}, nil, "203.0.113.15", false, "198.51.100.9"},
		{"cloudflare v4", []string{"104.16.1.1"}, nil, "203.0.113.15", false, "matikan proxy"},
		{"cloudflare v6", []string{"2606:4700::1111"}, nil, "203.0.113.15", false, "matikan proxy"},
		{"lookup failure", nil, &net.DNSError{Err: "server misbehaving"}, "203.0.113.15", false, "server misbehaving"},
		{"unknown server ip", []string{"198.51.100.9"}, nil, "", false, "10.0.0.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeResolver{addrs: tc.addrs, err: tc.err}
			got := checker(r).Check(context.Background(), "porto.ex.id", tc.publicIP)
			if got.OK != tc.ok || !strings.Contains(got.Detail, tc.detail) || (tc.ok && got.Detail != "") {
				t.Fatalf("Check = %+v, want ok=%v detail containing %q", got, tc.ok, tc.detail)
			}
			if r.asked != "runova-check-abc123.porto.ex.id" {
				t.Fatalf("looked up %q", r.asked)
			}
		})
	}
}

func TestCheckStopsAtTimeout(t *testing.T) {
	start := time.Now()
	got := checker(&fakeResolver{block: true}).Check(context.Background(), "porto.ex.id", "203.0.113.15")
	if got.OK || got.Detail == "" {
		t.Fatalf("Check = %+v, want a failure with a reason", got)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v with a 50 ms timeout", d)
	}
}

func TestDefaultToken(t *testing.T) {
	a, b := randomToken(), randomToken()
	if len(a) != 6 || a == b || strings.Trim(a, "0123456789abcdef") != "" {
		t.Fatalf("tokens %q %q: want 6 distinct hex characters", a, b)
	}
}

func TestIsCloudflare(t *testing.T) {
	for addr, want := range map[string]bool{
		"173.245.48.0": true, "173.245.63.255": true, "131.0.72.1": true, "131.0.76.1": false,
		"104.16.0.1": true, "104.27.255.255": true, "1.1.1.1": false, "203.0.113.15": false,
		"2606:4700::1": true, "2400:cb00:1::1": true, "2001:db8::1": false,
	} {
		if got := IsCloudflare(netip.MustParseAddr(addr)); got != want {
			t.Errorf("IsCloudflare(%s) = %v, want %v", addr, got, want)
		}
	}
}
