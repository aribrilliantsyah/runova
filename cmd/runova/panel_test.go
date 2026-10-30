package main

import (
	"context"
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
