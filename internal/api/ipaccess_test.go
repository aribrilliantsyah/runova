package api

import (
	"net/netip"
	"testing"
)

func addrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, len(s))
	for i, a := range s {
		out[i] = netip.MustParseAddr(a)
	}
	return out
}

func TestIPAccess(t *testing.T) {
	// The production case: nginx at 117.54.12.173 forwards only 80/443 to a
	// LAN server that also has Docker's gateway 172.31.0.1.
	lan := addrs("127.0.0.1", "::1", "fe80::1", "172.31.0.1", "172.17.0.1", "172.30.1.1", "10.81.20.111")
	cases := []struct {
		name   string
		public string
		ifaces []netip.Addr
		want   IPAccess
	}{
		{"behind nat: lan address", "117.54.12.173", lan, IPAccess{Host: "10.81.20.111", Local: true, LocalIP: "10.81.20.111"}},
		{"public ip owned", "203.0.113.5", addrs("127.0.0.1", "172.31.0.1", "203.0.113.5"), IPAccess{Host: "203.0.113.5", LocalIP: "203.0.113.5"}},
		{"public ip owned next to a lan", "203.0.113.5", addrs("192.168.1.4", "203.0.113.5"), IPAccess{Host: "203.0.113.5", LocalIP: "192.168.1.4"}},
		{"no public ip: lan", "", lan, IPAccess{Host: "10.81.20.111", Local: true, LocalIP: "10.81.20.111"}},
		{"no public ip, only a public interface", "", addrs("127.0.0.1", "198.51.100.7"), IPAccess{Host: "198.51.100.7", LocalIP: "198.51.100.7"}},
		{"only docker networks: keep the public ip", "117.54.12.173", addrs("127.0.0.1", "172.31.0.1"), IPAccess{Host: "117.54.12.173"}},
		{"nothing known", "", addrs("127.0.0.1"), IPAccess{}},
		{"ipv4-mapped public ip", "::ffff:203.0.113.5", addrs("203.0.113.5"), IPAccess{Host: "203.0.113.5", LocalIP: "203.0.113.5"}},
	}
	for _, c := range cases {
		if got := ipAccess(c.public, c.ifaces); got != c.want {
			t.Errorf("%s: ipAccess(%q) = %+v, want %+v", c.name, c.public, got, c.want)
		}
	}
}
