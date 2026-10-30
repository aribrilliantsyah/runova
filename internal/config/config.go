// Package config reads Runova's environment configuration.
package config

import (
	"net/url"
	"path/filepath"
	"strings"
)

type Config struct {
	Listen        string
	DataDir       string
	SecretKeyPath string
	// DockerHost is the Engine address, "unix:///path"; empty means the default socket.
	DockerHost string
	// CaddyAdmin is the URL of Caddy's admin API.
	CaddyAdmin string
}

// Load reads configuration through getenv, falling back to the defaults.
func Load(getenv func(string) string) Config {
	get := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	return Config{
		Listen:        get("RUNOVA_LISTEN", "127.0.0.1:7070"),
		DataDir:       get("RUNOVA_DATA", "/var/lib/runova"),
		SecretKeyPath: get("RUNOVA_SECRET_KEY", "/etc/runova/secret.key"),
		DockerHost:    get("RUNOVA_DOCKER_HOST", ""),
		CaddyAdmin:    get("RUNOVA_CADDY_ADMIN", "http://127.0.0.1:2019"),
	}
}

func (c Config) DBPath() string { return filepath.Join(c.DataDir, "runova.db") }

// CaddyAdminListen is CaddyAdmin as the host:port Caddy listens on, which
// Runova writes back into the configuration it loads.
func (c Config) CaddyAdminListen() string {
	u, err := url.Parse(c.CaddyAdmin)
	if err != nil || u.Host == "" {
		return strings.TrimPrefix(strings.TrimPrefix(c.CaddyAdmin, "http://"), "https://")
	}
	return u.Host
}
