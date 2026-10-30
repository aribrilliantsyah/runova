// Package config reads Runova's environment configuration.
package config

import "path/filepath"

type Config struct {
	Listen        string
	DataDir       string
	SecretKeyPath string
	// DockerHost is the Engine address, "unix:///path"; empty means the default socket.
	DockerHost string
	// PGDSN is the admin connection to PostgreSQL.
	PGDSN string
	// PGHBAPath is the pg_hba.d file Runova writes.
	PGHBAPath string
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
		PGDSN:         get("RUNOVA_PG_DSN", "host=/var/run/postgresql user=runova dbname=postgres"),
		PGHBAPath:     get("RUNOVA_PG_HBA", "/etc/postgresql/18/main/pg_hba.d/runova.conf"),
	}
}

func (c Config) DBPath() string { return filepath.Join(c.DataDir, "runova.db") }

// BackupDir is where PostgreSQL backups are kept.
func (c Config) BackupDir() string { return filepath.Join(c.DataDir, "backup") }
