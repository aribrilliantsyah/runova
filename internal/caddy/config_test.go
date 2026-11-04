package caddy_test

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"slices"
	"testing"

	"runova/internal/caddy"
)

type obj = map[string]any

func decodeConfig(t *testing.T, s caddy.State) obj {
	t.Helper()
	raw, err := caddy.Build(s)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var cfg obj
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("Build output is not JSON: %v\n%s", err, raw)
	}
	return cfg
}

// get walks keys (map keys or slice indexes) and fails the test if a step is missing.
func get(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(obj)
			if !ok {
				t.Fatalf("at %q: %T is not an object", k, v)
			}
			if v, ok = m[k]; !ok {
				t.Fatalf("key %q missing in %v", k, m)
			}
		case int:
			s, ok := v.([]any)
			if !ok || k >= len(s) {
				t.Fatalf("index %d: %T of length %d", k, v, len(s))
			}
			v = s[k]
		}
	}
	return v
}

func servers(t *testing.T, cfg obj) obj {
	t.Helper()
	return get(t, cfg, "apps", "http", "servers").(obj)
}

func strs(t *testing.T, v any) []string {
	t.Helper()
	var out []string
	for _, e := range v.([]any) {
		out = append(out, e.(string))
	}
	return out
}

func TestBuildSitesAndIP(t *testing.T) {
	cfg := decodeConfig(t, caddy.State{
		Sites: []caddy.Site{
			{Host: "c.co", Upstream: "127.0.0.1:30003"},
			{Host: "b.co", Upstream: "127.0.0.1:30001"},
		},
		IPListeners: []caddy.IPListener{{Port: 20001, Upstream: "127.0.0.1:30002"}},
	})
	if got := get(t, cfg, "admin", "listen"); got != "localhost:2019" {
		t.Errorf("admin.listen = %v", got)
	}
	srv := servers(t, cfg)

	https := srv["runova-https"].(obj)
	if got := strs(t, https["listen"]); !slices.Equal(got, []string{":443"}) {
		t.Errorf("runova-https listen = %v", got)
	}
	if _, set := https["automatic_https"]; set {
		t.Error("runova-https must keep Caddy's automatic HTTPS (and the HTTP redirect)")
	}
	routes := https["routes"].([]any)
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(routes))
	}
	for i, want := range []struct{ host, id, dial string }{
		{"b.co", "site:b.co", "127.0.0.1:30001"},
		{"c.co", "site:c.co", "127.0.0.1:30003"},
	} {
		if got := get(t, routes, i, "match", 0, "host", 0); got != want.host {
			t.Errorf("route %d host = %v, want %s (sorted by host)", i, got, want.host)
		}
		if got := get(t, routes, i, "@id"); got != want.id {
			t.Errorf("route %d @id = %v, want %s", i, got, want.id)
		}
		if got := get(t, routes, i, "handle", 0, "handler"); got != "reverse_proxy" {
			t.Errorf("route %d handler = %v", i, got)
		}
		if got := get(t, routes, i, "handle", 0, "upstreams", 0, "dial"); got != want.dial {
			t.Errorf("route %d dial = %v, want %s", i, got, want.dial)
		}
	}
	if _, set := get(t, routes, 0, "handle", 0).(obj)["trusted_proxies"]; set {
		t.Error("trusted_proxies must stay unset: Caddy has to overwrite X-Forwarded-For with the real client address")
	}

	ip := srv["runova-ip-20001"].(obj)
	if got := strs(t, ip["listen"]); !slices.Equal(got, []string{":20001"}) {
		t.Errorf("runova-ip-20001 listen = %v", got)
	}
	if got := get(t, ip, "automatic_https", "disable"); got != true {
		t.Errorf("automatic_https.disable = %v", got)
	}
	ipRoutes := ip["routes"].([]any)
	if len(ipRoutes) != 1 {
		t.Fatalf("ip routes = %d", len(ipRoutes))
	}
	if _, set := ipRoutes[0].(obj)["match"]; set {
		t.Error("the IP route must not have a host matcher")
	}
	if got := get(t, ipRoutes, 0, "@id"); got != "ip:20001" {
		t.Errorf("ip route @id = %v", got)
	}
	if got := get(t, ipRoutes, 0, "handle", 0, "upstreams", 0, "dial"); got != "127.0.0.1:30002" {
		t.Errorf("ip route dial = %v", got)
	}
}

func TestBuildEmptyStateKeepsHTTPSServer(t *testing.T) {
	cfg := decodeConfig(t, caddy.State{})
	https := servers(t, cfg)["runova-https"].(obj)
	if routes, ok := https["routes"].([]any); !ok || len(routes) != 0 {
		t.Errorf("routes = %#v, want an empty array (the Syncer looks for this server to detect a Caddy restart)", https["routes"])
	}
	if _, set := cfg["apps"].(obj)["tls"]; set {
		t.Error("tls app must be absent without a panel IP listener")
	}
}

func TestBuildAdminListen(t *testing.T) {
	cfg := decodeConfig(t, caddy.State{AdminListen: "127.0.0.1:2999"})
	if got := get(t, cfg, "admin", "listen"); got != "127.0.0.1:2999" {
		t.Errorf("admin.listen = %v", got)
	}
}

func TestBuildPanelDomain(t *testing.T) {
	cfg := decodeConfig(t, caddy.State{
		Sites:         []caddy.Site{{Host: "z.co", Upstream: "127.0.0.1:30001"}},
		PanelDomain:   "panel.a.co",
		PanelUpstream: "127.0.0.1:7070",
	})
	routes := servers(t, cfg)["runova-https"].(obj)["routes"].([]any)
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want the app and the panel", len(routes))
	}
	if got := get(t, routes, 0, "match", 0, "host", 0); got != "panel.a.co" {
		t.Errorf("first route host = %v, want panel.a.co (hosts are sorted)", got)
	}
	if got := get(t, routes, 0, "handle", 0, "upstreams", 0, "dial"); got != "127.0.0.1:7070" {
		t.Errorf("panel dial = %v", got)
	}
	if _, set := servers(t, cfg)["runova-panel-ip"]; set {
		t.Error("panel IP server present without PanelIPPort")
	}
}

func TestBuildPanelIP(t *testing.T) {
	cfg := decodeConfig(t, caddy.State{PanelIPPort: 8443, PanelUpstream: "127.0.0.1:7070", PublicIP: "203.0.113.5"})
	panel := servers(t, cfg)["runova-panel-ip"].(obj)
	if got := strs(t, panel["listen"]); !slices.Equal(got, []string{":8443"}) {
		t.Errorf("listen = %v", got)
	}
	if got := get(t, panel, "automatic_https", "disable"); got != true {
		t.Errorf("automatic_https.disable = %v", got)
	}
	if got := get(t, panel, "tls_connection_policies", 0, "default_sni"); got != "203.0.113.5" {
		t.Errorf("default_sni = %v, want the public IP so clients without SNI get its certificate", got)
	}
	if got := get(t, panel, "routes", 0, "handle", 0, "upstreams", 0, "dial"); got != "127.0.0.1:7070" {
		t.Errorf("dial = %v", got)
	}
	policy := get(t, cfg, "apps", "tls", "automation", "policies", 0)
	if got := get(t, policy, "issuers", 0, "module"); got != "internal" {
		t.Errorf("issuer = %v, want internal", got)
	}
	if got := strs(t, get(t, policy, "subjects")); !slices.Equal(got, []string{"203.0.113.5"}) {
		t.Errorf("subjects = %v", got)
	}
	if got := strs(t, get(t, cfg, "apps", "tls", "certificates", "automate")); !slices.Equal(got, []string{"203.0.113.5"}) {
		t.Errorf("automate = %v, want the public IP so the certificate exists before the first request", got)
	}
}

func TestBuildPanelIPWithoutPublicIP(t *testing.T) {
	cfg := decodeConfig(t, caddy.State{PanelIPPort: 8443, PanelUpstream: "127.0.0.1:7070"})
	panel := servers(t, cfg)["runova-panel-ip"].(obj)
	if got := get(t, panel, "tls_connection_policies", 0, "default_sni"); got != "localhost" {
		t.Errorf("default_sni = %v, want a fixed name: browsers connect to an IP without SNI", got)
	}
	policy := get(t, cfg, "apps", "tls", "automation", "policies", 0)
	if got := get(t, policy, "issuers", 0, "module"); got != "internal" {
		t.Errorf("issuer = %v, want internal", got)
	}
	if _, set := policy.(obj)["on_demand"]; set {
		t.Error("on-demand TLS would let any client make Caddy issue certificates")
	}
	if got := strs(t, get(t, cfg, "apps", "tls", "certificates", "automate")); !slices.Equal(got, []string{"localhost"}) {
		t.Errorf("automate = %v", got)
	}
}

func TestBuildDeterministic(t *testing.T) {
	base := caddy.State{
		PanelDomain: "panel.a.co", PanelIPPort: 8443, PanelUpstream: "127.0.0.1:7070", PublicIP: "203.0.113.5",
	}
	for i := range 8 {
		base.Sites = append(base.Sites, caddy.Site{Host: string(rune('a'+i)) + ".co", Upstream: "127.0.0.1:" + string(rune('1'+i)) + "0000"})
		base.IPListeners = append(base.IPListeners, caddy.IPListener{Port: 20000 + i, Upstream: "127.0.0.1:3000" + string(rune('0'+i))})
	}
	want, err := caddy.Build(base)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		s := base
		s.Sites = slices.Clone(base.Sites)
		s.IPListeners = slices.Clone(base.IPListeners)
		rand.Shuffle(len(s.Sites), func(i, j int) { s.Sites[i], s.Sites[j] = s.Sites[j], s.Sites[i] })
		rand.Shuffle(len(s.IPListeners), func(i, j int) { s.IPListeners[i], s.IPListeners[j] = s.IPListeners[j], s.IPListeners[i] })
		got, err := caddy.Build(s)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("shuffled input changed the output:\n%s\n%s", got, want)
		}
	}
}

func TestBuildRejectsInconsistentState(t *testing.T) {
	site := caddy.Site{Host: "a.co", Upstream: "127.0.0.1:30001"}
	cases := map[string]caddy.State{
		"duplicate host":        {Sites: []caddy.Site{site, site}},
		"panel host is a site":  {Sites: []caddy.Site{site}, PanelDomain: "a.co", PanelUpstream: "127.0.0.1:7070"},
		"site without upstream": {Sites: []caddy.Site{{Host: "a.co"}}},
		"duplicate ip port":     {IPListeners: []caddy.IPListener{{Port: 20000, Upstream: "127.0.0.1:1"}, {Port: 20000, Upstream: "127.0.0.1:2"}}},
		"bad ip port":           {IPListeners: []caddy.IPListener{{Port: 0, Upstream: "127.0.0.1:1"}}},
		"panel port is app port": {
			IPListeners: []caddy.IPListener{{Port: 8443, Upstream: "127.0.0.1:1"}}, PanelIPPort: 8443, PanelUpstream: "127.0.0.1:7070",
		},
		"panel port 443":          {PanelIPPort: 443, PanelUpstream: "127.0.0.1:7070"},
		"panel domain no target":  {PanelDomain: "panel.a.co"},
		"panel ip port no target": {PanelIPPort: 8443},
	}
	for name, s := range cases {
		if _, err := caddy.Build(s); err == nil {
			t.Errorf("%s: Build succeeded, want an error", name)
		}
	}
}

func TestBuildTrustedProxies(t *testing.T) {
	state := caddy.State{
		Sites:          []caddy.Site{{Host: "b.co", Upstream: "127.0.0.1:30001"}},
		IPListeners:    []caddy.IPListener{{Port: 20001, Upstream: "127.0.0.1:30002"}},
		PanelDomain:    "panel.co",
		PanelIPPort:    8443,
		PanelUpstream:  "127.0.0.1:7070",
		TrustedProxies: []string{"10.0.0.0/8", "10.0.0.1"},
	}
	handlers := func(cfg obj) map[string]obj {
		out := map[string]obj{}
		for name, srv := range servers(t, cfg) {
			for _, r := range srv.(obj)["routes"].([]any) {
				out[name+" "+r.(obj)["@id"].(string)] = get(t, r, "handle", 0).(obj)
			}
		}
		return out
	}

	with := handlers(decodeConfig(t, state))
	if len(with) != 4 {
		t.Fatalf("handlers = %d, want panel, site, IP listener and panel IP", len(with))
	}
	for id, h := range with {
		if got := strs(t, h["trusted_proxies"]); !slices.Equal(got, []string{"10.0.0.0/8", "10.0.0.1"}) {
			t.Errorf("%s trusted_proxies = %v", id, got)
		}
	}

	state.TrustedProxies = nil
	for id, h := range handlers(decodeConfig(t, state)) {
		if _, set := h["trusted_proxies"]; set {
			t.Errorf("%s has trusted_proxies without any trusted proxy", id)
		}
	}
}
