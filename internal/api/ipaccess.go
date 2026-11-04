package api

import (
	"net"
	"net/netip"
)

// runovaNets are the Docker networks on a Runova host: the default bridge,
// the address pool the installer gives Docker, and Runova's user networks.
// Their gateways are interfaces of the host, but nobody outside reaches them.
var runovaNets = []netip.Prefix{
	netip.MustParsePrefix("172.17.0.0/16"),
	netip.MustParsePrefix("172.31.0.0/16"),
	netip.MustParsePrefix("172.30.0.0/16"),
}

// IPAccess says where an app's IP port is reached. Host is what the panel
// links to; Local is true when that is a LAN address because the configured
// public IP is not one of this machine's (the server sits behind NAT or a
// proxy that forwards only 80/443). LocalIP is the detected LAN address, for
// the Server page, whether or not it is used.
type IPAccess struct {
	Host    string `json:"host"`
	Local   bool   `json:"local"`
	LocalIP string `json:"local_ip"`
}

// ipAccess picks the host for IP access from the configured public IP and the
// machine's interface addresses. A public IP the machine owns is used as is;
// otherwise the first usable private address (not loopback, link-local or a
// Docker network), then any other usable address. Empty Host means unknown:
// the panel falls back to the address it was opened on.
func ipAccess(publicIP string, ifaces []netip.Addr) IPAccess {
	var private, other netip.Addr
	own := false
	pub, pubErr := netip.ParseAddr(publicIP)
	for _, a := range ifaces {
		a = a.Unmap()
		if pubErr == nil && a == pub.Unmap() {
			own = true
		}
		if !usable(a) {
			continue
		}
		switch {
		case a.IsPrivate() && a.Is4() && !private.IsValid():
			private = a
		case !a.IsPrivate() && !other.IsValid():
			other = a
		}
	}
	local := private
	if !local.IsValid() {
		local = other
	}
	out := IPAccess{}
	if local.IsValid() {
		out.LocalIP = local.String()
	}
	switch {
	case pubErr == nil && own:
		out.Host = pub.Unmap().String()
	case pubErr == nil && local.IsValid():
		out.Host, out.Local = out.LocalIP, local.IsPrivate()
	case pubErr == nil:
		out.Host = pub.Unmap().String()
	default:
		out.Host, out.Local = out.LocalIP, local.IsValid() && local.IsPrivate()
	}
	return out
}

func usable(a netip.Addr) bool {
	if !a.IsValid() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	for _, p := range runovaNets {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// interfaceAddrs lists the machine's addresses.
func interfaceAddrs() []netip.Addr {
	addrs, _ := net.InterfaceAddrs()
	var out []netip.Addr
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			out = append(out, p.Addr())
		}
	}
	return out
}
