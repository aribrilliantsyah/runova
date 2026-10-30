package main

import (
	"bytes"
	"context"
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
	if u.Quota != (store.Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 20}) {
		t.Fatalf("quota = %+v", u.Quota)
	}

	r = runIn(t, env, "admin", "create", "budi")
	if r.code != 1 || !strings.Contains(r.stderr, "pengguna sudah ada") || strings.Contains(r.stdout, "Password") {
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
	if r.code != 1 || !strings.Contains(r.stderr, "tidak ditemukan") {
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
