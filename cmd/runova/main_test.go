package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"runova/internal/auth"
	"runova/internal/config"
	"runova/internal/store"
)

type result struct {
	code           int
	stdout, stderr string
}

func runIn(t *testing.T, env map[string]string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, func(k string) string { return env[k] }, &out, &errb)
	return result{code, out.String(), errb.String()}
}

var passwordRE = regexp.MustCompile(`(?m)^Password: (\S+)$`)

func passwordOf(t *testing.T, r result) string {
	t.Helper()
	m := passwordRE.FindStringSubmatch(r.stdout)
	if m == nil {
		t.Fatalf("no password line in %q", r.stdout)
	}
	return m[1]
}

func hashOf(t *testing.T, env map[string]string, username string) (store.User, string) {
	t.Helper()
	s, err := store.Open(config.Load(func(k string) string { return env[k] }).DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, hash, err := s.UserByUsername(context.Background(), username)
	if err != nil {
		t.Fatal(err)
	}
	return u, hash
}

func TestAdminCreateAndReset(t *testing.T) {
	env := map[string]string{"RUNOVA_DATA": t.TempDir()}

	r := runIn(t, env, "admin", "create", "budi")
	if r.code != 0 {
		t.Fatalf("create: code %d, stderr %q", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "Username: budi\n") {
		t.Fatalf("stdout = %q", r.stdout)
	}
	first := passwordOf(t, r)
	if len(first) != 20 || strings.ContainsAny(first, "0OIl1") {
		t.Fatalf("password %q: want 20 chars without 0OIl1", first)
	}
	u, hash := hashOf(t, env, "budi")
	if !u.IsAdmin() || !auth.CheckPassword(hash, first) {
		t.Fatalf("stored user %+v does not match printed password", u)
	}
	if u.Quota != (store.Quota{Apps: 3, MemoryMB: 1024, Databases: 3, PGConnections: 20, DiskMB: 1024}) {
		t.Fatalf("quota = %+v", u.Quota)
	}

	r = runIn(t, env, "admin", "create", "budi")
	if r.code != 1 || !strings.Contains(r.stderr, "user already exists") || strings.Contains(r.stdout, "Password") {
		t.Fatalf("duplicate: %+v", r)
	}

	r = runIn(t, env, "admin", "reset-password", "budi")
	if r.code != 0 {
		t.Fatalf("reset: %+v", r)
	}
	second := passwordOf(t, r)
	if second == first || len(second) != 20 {
		t.Fatalf("reset password %q", second)
	}
	_, hash = hashOf(t, env, "budi")
	if auth.CheckPassword(hash, first) || !auth.CheckPassword(hash, second) {
		t.Fatal("reset-password did not replace the hash")
	}

	r = runIn(t, env, "admin", "reset-password", "hantu")
	if r.code != 1 || !strings.Contains(r.stderr, "not found") {
		t.Fatalf("reset unknown: %+v", r)
	}
}

func TestAdminCreateRejectsInvalidUsername(t *testing.T) {
	env := map[string]string{"RUNOVA_DATA": t.TempDir()}
	for _, name := range []string{"Budi", "postgres", "b"} {
		if r := runIn(t, env, "admin", "create", name); r.code != 1 || r.stdout != "" {
			t.Errorf("create %q: %+v", name, r)
		}
	}
}

func TestAdminUsage(t *testing.T) {
	env := map[string]string{"RUNOVA_DATA": t.TempDir()}
	for _, args := range [][]string{{"admin"}, {"admin", "create"}, {"admin", "create", "a", "b"}, {"admin", "nope", "x"}, {"admin", "reset-password"}} {
		if r := runIn(t, env, args...); r.code != 2 || r.stderr == "" {
			t.Errorf("run(%v): %+v", args, r)
		}
	}
}

func TestKeygen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k")
	env := map[string]string{"RUNOVA_SECRET_KEY": path}
	if r := runIn(t, env, "keygen"); r.code != 0 {
		t.Fatalf("keygen: %+v", r)
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() != 32 || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", st, err)
	}
	before, _ := os.ReadFile(path)
	if r := runIn(t, env, "keygen"); r.code != 1 || r.stderr == "" {
		t.Fatalf("second keygen: %+v", r)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("second keygen overwrote the key")
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"x"}} {
		if r := runIn(t, nil, args...); r.code != 2 || !strings.Contains(r.stderr, "runova") {
			t.Errorf("run(%v) = %+v", args, r)
		}
	}
}

func TestVersion(t *testing.T) {
	if r := runIn(t, nil, "version"); r.code != 0 || r.stdout != "runova dev\n" {
		t.Fatalf("version = %+v, want %q", r, "runova dev\n")
	}
}

func TestCheckReportsFailures(t *testing.T) {
	env := map[string]string{
		"RUNOVA_DATA":        t.TempDir(),
		"RUNOVA_SECRET_KEY":  filepath.Join(t.TempDir(), "missing.key"),
		"RUNOVA_DOCKER_HOST": "unix:///nonexistent/docker.sock",
		"RUNOVA_CADDY_ADMIN": "http://127.0.0.1:1",
		"RUNOVA_PG_DSN":      "host=127.0.0.1 port=1 user=runova dbname=postgres",
	}
	r := runIn(t, env, "check")
	if r.code != 1 {
		t.Fatalf("exit code = %d, want 1; stdout %q", r.code, r.stdout)
	}
	for _, name := range []string{"docker", "caddy", "postgres", "secret"} {
		if !regexp.MustCompile(`(?m)^fail ` + name + `: \S`).MatchString(r.stdout) {
			t.Errorf("no failure line for %s in %q", name, r.stdout)
		}
	}
	if !strings.Contains(r.stdout, "ok data ") {
		t.Errorf("a usable data directory should be reported ok: %q", r.stdout)
	}
}

func TestCheckPassesWithFakeBackends(t *testing.T) {
	key := filepath.Join(t.TempDir(), "secret.key")
	env := map[string]string{"RUNOVA_DATA": t.TempDir(), "RUNOVA_SECRET_KEY": key, "RUNOVA_FAKE_BACKENDS": "1"}
	if r := runIn(t, env, "keygen"); r.code != 0 {
		t.Fatalf("keygen: %+v", r)
	}
	r := runIn(t, env, "check")
	if r.code != 0 || strings.Contains(r.stdout, "fail ") || !strings.Contains(r.stdout, "ok docker") {
		t.Fatalf("check = %+v", r)
	}
}

func TestAdminExists(t *testing.T) {
	env := map[string]string{"RUNOVA_DATA": t.TempDir()}
	if r := runIn(t, env, "admin", "exists"); r.code != 1 || r.stdout != "" {
		t.Fatalf("before any admin: %+v", r)
	}
	if r := runIn(t, env, "admin", "create", "admin"); r.code != 0 {
		t.Fatalf("create: %+v", r)
	}
	if r := runIn(t, env, "admin", "exists"); r.code != 0 {
		t.Fatalf("after create: %+v", r)
	}
}

func TestAdminExistsIgnoresPlainUsers(t *testing.T) {
	env := map[string]string{"RUNOVA_DATA": t.TempDir()}
	cfg := config.Load(func(k string) string { return env[k] })
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateUser(context.Background(), store.NewUser{Username: "budi", PasswordHash: "x", Role: store.RoleUser, Quota: store.Quota{Apps: 1}}, 1)
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	if r := runIn(t, env, "admin", "exists"); r.code != 1 {
		t.Fatalf("a plain user is not an admin: %+v", r)
	}
}

func TestServiceUnit(t *testing.T) {
	raw, err := os.ReadFile("../../packaging/runova.service")
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]bool{}
	for _, l := range strings.Split(string(raw), "\n") {
		lines[strings.TrimSpace(l)] = true
	}
	caps := "CAP_CHOWN CAP_FOWNER CAP_DAC_OVERRIDE"
	for _, want := range []string{
		"User=runova", "Group=runova", "SupplementaryGroups=docker",
		"EnvironmentFile=/etc/runova/runova.env", "ExecStart=/usr/local/bin/runova serve",
		"AmbientCapabilities=" + caps, "CapabilityBoundingSet=" + caps,
		"NoNewPrivileges=yes", "ProtectSystem=strict", "ProtectHome=yes", "PrivateTmp=yes",
		"ReadWritePaths=/var/lib/runova /etc/postgresql/18/main/pg_hba.d",
		"Restart=on-failure",
		"After=docker.service caddy.service postgresql.service",
		"Wants=docker.service caddy.service postgresql.service",
		"WantedBy=multi-user.target",
	} {
		if !lines[want] {
			t.Errorf("runova.service lacks %q", want)
		}
	}
}

func TestDevChmodRefusedOnProductionData(t *testing.T) {
	if err := checkDevMode(config.Config{DataDir: "/var/lib/runova", DevChmod: true}); !errors.Is(err, errDevOnProduction) {
		t.Errorf("err = %v, want errDevOnProduction", err)
	}
	if err := checkDevMode(config.Config{DataDir: t.TempDir(), DevChmod: true}); err != nil {
		t.Errorf("a temporary directory must be allowed: %v", err)
	}
}

func TestDevChmodFlagFromEnv(t *testing.T) {
	get := func(v string) func(string) string {
		return func(k string) string {
			if k == "RUNOVA_DEV_CHMOD" {
				return v
			}
			return ""
		}
	}
	if !config.Load(get("1")).DevChmod {
		t.Error("RUNOVA_DEV_CHMOD=1 should enable the flag")
	}
	for _, v := range []string{"", "0", "true"} {
		if config.Load(get(v)).DevChmod {
			t.Errorf("RUNOVA_DEV_CHMOD=%q must not enable the flag", v)
		}
	}
}
