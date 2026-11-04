package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"runova/internal/config"
	"runova/internal/store"
)

func panelSettings(t *testing.T, env map[string]string) store.Settings {
	t.Helper()
	s, err := store.Open(config.Load(func(k string) string { return env[k] }).DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, err := s.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPanelSetCommand(t *testing.T) {
	env := map[string]string{"RUNOVA_DATA": t.TempDir()}

	if r := runIn(t, env, "panel", "set", "--domain", "Panel.A.co"); r.code != 0 {
		t.Fatalf("set domain: %+v", r)
	}
	if v := panelSettings(t, env); v.PanelDomain != "panel.a.co" || v.PanelPort != 0 {
		t.Fatalf("settings = domain %q port %d, want panel.a.co and 0", v.PanelDomain, v.PanelPort)
	}

	if r := runIn(t, env, "panel", "set", "--port", "8443"); r.code != 0 {
		t.Fatalf("set port: %+v", r)
	}
	if v := panelSettings(t, env); v.PanelDomain != "" || v.PanelPort != 8443 {
		t.Fatalf("settings = domain %q port %d; the panel is reached by domain or by IP:port, so each replaces the other", v.PanelDomain, v.PanelPort)
	}
	if v := panelSettings(t, env); v.UploadLimitMB != 500 {
		t.Errorf("other settings changed: %+v", v)
	}
}

func TestPanelSetRejectsBadInput(t *testing.T) {
	env := map[string]string{"RUNOVA_DATA": t.TempDir()}
	for _, args := range [][]string{
		{"panel"},
		{"panel", "set"},
		{"panel", "set", "--domain", "a.co", "--port", "8443"},
		{"panel", "get"},
		{"panel", "set", "--domain"},
		{"panel", "set", "--bogus", "x"},
	} {
		if r := runIn(t, env, append([]string(nil), args...)...); r.code != 2 || r.stderr == "" {
			t.Errorf("run(%v) = %+v, want usage (2)", args, r)
		}
	}
	for _, args := range [][]string{
		{"panel", "set", "--domain", "203.0.113.5"},
		{"panel", "set", "--domain", "panel"},
		{"panel", "set", "--port", "443"},
		{"panel", "set", "--port", "abc"},
		{"panel", "set", "--port", "20500"},
	} {
		if r := runIn(t, env, args...); r.code == 0 || r.stderr == "" {
			t.Errorf("run(%v) = %+v, want a failure with a message", args, r)
		}
	}
	if v := panelSettings(t, env); v.PanelDomain != "" || v.PanelPort != 0 {
		t.Errorf("a rejected command changed the settings: %+v", v)
	}
}

func TestPanelSetRefusesDomainOfAnApp(t *testing.T) {
	env := map[string]string{"RUNOVA_DATA": t.TempDir()}
	cfg := config.Load(func(k string) string { return env[k] })
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u, err := st.CreateUser(ctx, store.NewUser{Username: "budi", PasswordHash: "x", Role: store.RoleUser, Quota: store.Quota{Apps: 1}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, store.App{UserID: u.ID, Name: "one", Kind: "static", IPPort: 20000, Status: store.AppStopped, DeployMode: "zero"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDomain(ctx, app.ID, "taken.co", 1); err != nil {
		t.Fatal(err)
	}
	st.Close()

	if r := runIn(t, env, "panel", "set", "--domain", "taken.co"); r.code != 1 || r.stderr == "" {
		t.Fatalf("set an app's domain: %+v", r)
	}
}

func TestPanelSetPublicIP(t *testing.T) {
	env := map[string]string{"RUNOVA_DATA": t.TempDir()}
	if r := runIn(t, env, "panel", "set", "--port", "8443"); r.code != 0 {
		t.Fatalf("set port: %+v", r)
	}
	if r := runIn(t, env, "panel", "set", "--public-ip", "203.0.113.5"); r.code != 0 {
		t.Fatalf("set public ip alone: %+v", r)
	}
	if v := panelSettings(t, env); v.PublicIP != "203.0.113.5" || v.PanelPort != 8443 {
		t.Fatalf("public ip %q port %d; the address option must survive a public-ip-only change", v.PublicIP, v.PanelPort)
	}
	if r := runIn(t, env, "panel", "set", "--domain", "panel.a.co", "--public-ip", "2001:db8::1"); r.code != 0 {
		t.Fatalf("set domain with public ip: %+v", r)
	}
	if v := panelSettings(t, env); v.PublicIP != "2001:db8::1" || v.PanelDomain != "panel.a.co" || v.PanelPort != 0 {
		t.Fatalf("settings = %+v", v)
	}
	if r := runIn(t, env, "panel", "set", "--public-ip", "abc"); r.code != 1 || r.stderr == "" {
		t.Fatalf("invalid ip: %+v", r)
	}
	if r := runIn(t, env, "panel", "set", "--port", "8443", "--public-ip", "abc"); r.code != 1 {
		t.Fatalf("invalid ip with port: %+v", r)
	}
	if v := panelSettings(t, env); v.PublicIP != "2001:db8::1" || v.PanelDomain != "panel.a.co" {
		t.Fatalf("a rejected command changed the settings: %+v", v)
	}
}

func TestPanelSetTrustedProxy(t *testing.T) {
	env := map[string]string{"RUNOVA_DATA": t.TempDir()}
	if r := runIn(t, env, "panel", "set", "--port", "8443", "--public-ip", "203.0.113.5"); r.code != 0 {
		t.Fatalf("set port: %+v", r)
	}
	r := runIn(t, env, "panel", "set", "--trusted-proxy", "10.0.0.5", "--trusted-proxy", "192.168.0.0/24")
	if r.code != 0 || !strings.Contains(r.stdout, "Trusted proxies: 10.0.0.5, 192.168.0.0/24") {
		t.Fatalf("set proxies: %+v", r)
	}
	v := panelSettings(t, env)
	if !slices.Equal(v.TrustedProxies, []string{"10.0.0.5", "192.168.0.0/24"}) || v.PanelPort != 8443 || v.PublicIP != "203.0.113.5" {
		t.Fatalf("settings = %+v; the proxies must not touch the panel address or the public IP", v)
	}
	if r := runIn(t, env, "panel", "set", "--domain", "panel.a.co", "--trusted-proxy", "10.0.0.9"); r.code != 0 {
		t.Fatalf("set domain with proxy: %+v", r)
	}
	if v := panelSettings(t, env); !slices.Equal(v.TrustedProxies, []string{"10.0.0.9"}) || v.PanelDomain != "panel.a.co" {
		t.Fatalf("settings = %+v; a new list replaces the old one", v)
	}
	if r := runIn(t, env, "panel", "set", "--public-ip", "203.0.113.6"); r.code != 0 {
		t.Fatalf("set public ip: %+v", r)
	}
	if v := panelSettings(t, env); !slices.Equal(v.TrustedProxies, []string{"10.0.0.9"}) {
		t.Fatalf("proxies = %v; an unrelated option must keep them", v.TrustedProxies)
	}
	for _, bad := range []string{"abc", "10.0.0.0/33", "0.0.0.0/0", "::/0"} {
		if r := runIn(t, env, "panel", "set", "--trusted-proxy", bad); r.code != 1 || r.stderr == "" {
			t.Fatalf("%q accepted: %+v", bad, r)
		}
	}
	if v := panelSettings(t, env); !slices.Equal(v.TrustedProxies, []string{"10.0.0.9"}) {
		t.Fatalf("a rejected command changed the proxies: %v", v.TrustedProxies)
	}
	r = runIn(t, env, "panel", "set", "--trusted-proxy", "")
	if r.code != 0 || !strings.Contains(r.stdout, "Trusted proxies: none") {
		t.Fatalf("clear: %+v", r)
	}
	if v := panelSettings(t, env); len(v.TrustedProxies) != 0 {
		t.Fatalf("proxies after clear = %v", v.TrustedProxies)
	}
}
