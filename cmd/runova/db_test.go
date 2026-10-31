package main

import (
	"database/sql"
	"os"
	"strconv"
	"strings"
	"testing"

	"runova/internal/config"
	"runova/internal/store"
)

func TestDBVersion(t *testing.T) {
	env := map[string]string{"RUNOVA_DATA": t.TempDir()}
	dbPath := config.Load(func(k string) string { return env[k] }).DBPath()
	_, latest, err := store.SchemaVersion(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	r := runIn(t, env, "db", "version")
	if r.code != 0 || !strings.Contains(r.stdout, "Skema database: 0 (belum dibuat)\n") || !strings.Contains(r.stdout, "Skema tertinggi binary ini: ") {
		t.Fatalf("missing database: %+v", r)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("db version created the database: %v", err)
	}

	if r := runIn(t, env, "admin", "create", "budi"); r.code != 0 {
		t.Fatalf("create: %+v", r)
	}
	r = runIn(t, env, "db", "version")
	want := "Skema database: " + strconv.Itoa(latest) + "\nSkema tertinggi binary ini: " + strconv.Itoa(latest) + "\n"
	if r.code != 0 || r.stdout != want {
		t.Fatalf("stdout = %q, want %q (code %d, stderr %q)", r.stdout, want, r.code, r.stderr)
	}
}

func TestDBVersionOfPreMigrationDatabase(t *testing.T) {
	env := map[string]string{"RUNOVA_DATA": t.TempDir()}
	legacy, err := sql.Open("sqlite", config.Load(func(k string) string { return env[k] }).DBPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE users(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	r := runIn(t, env, "db", "version")
	if r.code != 0 || !strings.Contains(r.stdout, "Skema database: 0 (belum dimigrasi; dimigrasi saat Runova dibuka)\n") {
		t.Fatalf("stdout = %q, code %d", r.stdout, r.code)
	}
}

func TestDBUsage(t *testing.T) {
	env := map[string]string{"RUNOVA_DATA": t.TempDir()}
	for _, args := range [][]string{{"db"}, {"db", "nope"}, {"db", "version", "extra"}} {
		if r := runIn(t, env, args...); r.code != 2 || !strings.Contains(r.stderr, "Penggunaan:") {
			t.Errorf("%v: %+v", args, r)
		}
	}
}
