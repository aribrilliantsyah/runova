package config_test

import (
	"testing"

	"runova/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	c := config.Load(func(string) string { return "" })
	if c.Listen != "127.0.0.1:7070" {
		t.Errorf("Listen = %q", c.Listen)
	}
	if c.DataDir != "/var/lib/runova" {
		t.Errorf("DataDir = %q", c.DataDir)
	}
	if c.SecretKeyPath != "/etc/runova/secret.key" {
		t.Errorf("SecretKeyPath = %q", c.SecretKeyPath)
	}
	if got := c.DBPath(); got != "/var/lib/runova/runova.db" {
		t.Errorf("DBPath = %q", got)
	}
}

func TestLoadOverrides(t *testing.T) {
	env := map[string]string{"RUNOVA_LISTEN": ":9000", "RUNOVA_DATA": "/tmp/x", "RUNOVA_SECRET_KEY": "/tmp/k"}
	c := config.Load(func(k string) string { return env[k] })
	if c.Listen != ":9000" || c.DataDir != "/tmp/x" || c.SecretKeyPath != "/tmp/k" {
		t.Errorf("config = %+v", c)
	}
	if got := c.DBPath(); got != "/tmp/x/runova.db" {
		t.Errorf("DBPath = %q", got)
	}
}

func TestCaddyAdmin(t *testing.T) {
	c := config.Load(func(string) string { return "" })
	if c.CaddyAdmin != "http://127.0.0.1:2019" || c.CaddyAdminListen() != "127.0.0.1:2019" {
		t.Errorf("defaults: %q, %q", c.CaddyAdmin, c.CaddyAdminListen())
	}
	c = config.Load(func(k string) string {
		if k == "RUNOVA_CADDY_ADMIN" {
			return "http://127.0.0.1:2999/"
		}
		return ""
	})
	if c.CaddyAdmin != "http://127.0.0.1:2999/" || c.CaddyAdminListen() != "127.0.0.1:2999" {
		t.Errorf("override: %q, %q", c.CaddyAdmin, c.CaddyAdminListen())
	}
}
