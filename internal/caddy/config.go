package caddy

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// DefaultAdminListen is where Runova expects Caddy's admin API.
const DefaultAdminListen = "localhost:2019"

// Server names in Caddy's configuration. The Syncer looks for HTTPSServer to
// tell whether Caddy still runs Runova's configuration.
const (
	HTTPSServer    = "runova-https"
	PanelIPServer  = "runova-panel-ip"
	ipServerPrefix = "runova-ip-"
)

// Site routes a domain to an app's host port.
type Site struct {
	Host     string
	Upstream string // 127.0.0.1:port
}

// IPListener serves one app on a plain HTTP port of its own.
type IPListener struct {
	Port     int
	Upstream string
}

// State is everything Caddy has to serve. Build turns it into the complete
// configuration, so Caddy never holds anything the database does not.
type State struct {
	Sites       []Site
	IPListeners []IPListener
	// PanelDomain, when set, adds a route to PanelUpstream on the HTTPS server.
	PanelDomain string
	// PanelIPPort, when set, serves the panel over TLS on that port with an internal certificate.
	PanelIPPort   int
	PanelUpstream string
	PublicIP      string
	// TrustedProxies are the IPs and CIDRs of reverse proxies in front of
	// Caddy. Caddy keeps X-Forwarded-For from them and appends their address;
	// from anyone else it discards the header and sets the peer's address.
	// A non-empty list is front-proxy mode: the proxy holds the public
	// certificates, so Caddy issues every certificate from its internal CA.
	TrustedProxies []string
	// AdminListen is Caddy's admin address; empty means DefaultAdminListen.
	AdminListen string
}

type obj = map[string]any

func (s State) proxyRoute(id, upstream string, hosts ...string) obj {
	handler := obj{"handler": "reverse_proxy", "upstreams": []any{obj{"dial": upstream}}}
	if len(s.TrustedProxies) > 0 {
		handler["trusted_proxies"] = slices.Sorted(slices.Values(s.TrustedProxies))
	}
	route := obj{"@id": id, "handle": []any{handler}}
	if len(hosts) > 0 {
		route["match"] = []any{obj{"host": hosts}}
	}
	return route
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

func (s State) validate() error {
	hosts := map[string]bool{}
	add := func(host string) error {
		if hosts[host] {
			return fmt.Errorf("host %q appears twice", host)
		}
		hosts[host] = true
		return nil
	}
	for _, site := range s.Sites {
		if site.Host == "" || site.Upstream == "" {
			return errors.New("site needs a host and an upstream")
		}
		if err := add(site.Host); err != nil {
			return err
		}
	}
	if s.PanelDomain != "" {
		if s.PanelUpstream == "" {
			return errors.New("panel domain needs an upstream")
		}
		if err := add(s.PanelDomain); err != nil {
			return err
		}
	}
	ports := map[int]bool{80: true, 443: true}
	for _, l := range s.IPListeners {
		if !validPort(l.Port) || l.Upstream == "" {
			return fmt.Errorf("invalid IP listener %d → %q", l.Port, l.Upstream)
		}
		if ports[l.Port] {
			return fmt.Errorf("port %d is used twice", l.Port)
		}
		ports[l.Port] = true
	}
	if s.PanelIPPort != 0 {
		if !validPort(s.PanelIPPort) || s.PanelUpstream == "" {
			return fmt.Errorf("invalid panel port %d → %q", s.PanelIPPort, s.PanelUpstream)
		}
		if ports[s.PanelIPPort] {
			return fmt.Errorf("port %d is used twice", s.PanelIPPort)
		}
	}
	return nil
}

// Build returns the complete Caddy configuration for s. The output depends
// only on the contents of s, not on the order of its slices.
func Build(s State) ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}

	type hostRoute struct{ host, id, upstream string }
	var hostRoutes []hostRoute
	for _, site := range s.Sites {
		hostRoutes = append(hostRoutes, hostRoute{site.Host, "site:" + site.Host, site.Upstream})
	}
	if s.PanelDomain != "" {
		hostRoutes = append(hostRoutes, hostRoute{s.PanelDomain, "panel:" + s.PanelDomain, s.PanelUpstream})
	}
	slices.SortFunc(hostRoutes, func(a, b hostRoute) int { return strings.Compare(a.host, b.host) })
	httpsRoutes := make([]any, len(hostRoutes))
	for i, r := range hostRoutes {
		httpsRoutes[i] = s.proxyRoute(r.id, r.upstream, r.host)
	}
	// Listening on :443 only is deliberate: Caddy adds the :80 server that
	// redirects HTTP to HTTPS (and answers ACME challenges) by itself. With
	// :80 listed here the server serves plain HTTP and no redirect happens.
	servers := obj{
		HTTPSServer: obj{"listen": []string{":443"}, "routes": httpsRoutes},
	}

	listeners := slices.Clone(s.IPListeners)
	slices.SortFunc(listeners, func(a, b IPListener) int { return a.Port - b.Port })
	for _, l := range listeners {
		port := strconv.Itoa(l.Port)
		servers[ipServerPrefix+port] = obj{
			"listen":          []string{":" + port},
			"automatic_https": obj{"disable": true},
			"routes":          []any{s.proxyRoute("ip:"+port, l.Upstream)},
		}
	}

	apps := obj{"http": obj{"servers": servers}}
	if s.PanelIPPort != 0 {
		policy := obj{"default_sni": cmp.Or(s.PublicIP, panelCertName)}
		servers[PanelIPServer] = obj{
			"listen":                  []string{":" + strconv.Itoa(s.PanelIPPort)},
			"automatic_https":         obj{"disable": true},
			"tls_connection_policies": []any{policy},
			"routes":                  []any{s.proxyRoute("panel-ip", s.PanelUpstream)},
		}
	}
	if t := s.tlsApp(); t != nil {
		apps["tls"] = t
	}

	admin := s.AdminListen
	if admin == "" {
		admin = DefaultAdminListen
	}
	out, err := json.Marshal(obj{"admin": obj{"listen": admin}, "apps": apps})
	if err != nil {
		return nil, fmt.Errorf("encode caddy config: %w", err)
	}
	return out, nil
}

// panelCertName is the certificate name of the panel's IP listener when the
// public IP is unknown. Browsers connect to an IP without SNI, so Caddy
// serves its default_sni certificate; the internal CA is untrusted either way.
const panelCertName = "localhost"

// FrontProxy reports front-proxy mode: a reverse proxy such as nginx holds
// the public certificates and forwards to Caddy over HTTPS.
func (s State) FrontProxy() bool { return len(s.TrustedProxies) > 0 }

// tlsApp returns Caddy's tls app, nil when Caddy's defaults do.
//
// The panel's IP listener gets a certificate from Caddy's internal CA, for
// the public IP when it is known.
//
// In front-proxy mode every other host does too, through a catch-all policy
// without subjects (what the Caddyfile's local_certs writes). A public CA
// cannot validate a host behind the proxy: TLS-ALPN-01 ends at the proxy's
// TLS and HTTP-01 only works while the proxy forwards port 80. Without a
// certificate Caddy answers the handshake with an internal error alert and
// the proxy shows 502. The internal certificate exists at once; the proxy
// keeps TLS to Caddy encrypted. Outside that mode domains keep Caddy's
// default public issuers. on_demand stays off in both.
func (s State) tlsApp() obj {
	var policies []any
	out := obj{}
	if s.PanelIPPort != 0 {
		name := cmp.Or(s.PublicIP, panelCertName)
		out["certificates"] = obj{"automate": []string{name}}
		policies = append(policies, obj{"subjects": []string{name}, "issuers": []any{obj{"module": "internal"}}})
	}
	if s.FrontProxy() {
		policies = append(policies, obj{"issuers": []any{obj{"module": "internal"}}})
	}
	if len(policies) == 0 {
		return nil
	}
	out["automation"] = obj{"policies": policies}
	return out
}
