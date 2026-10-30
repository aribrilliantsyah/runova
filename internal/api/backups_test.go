package api_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"runova/internal/dbs"
	"runova/internal/store"
)

func fakeTool(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tool.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBackupFlow(t *testing.T) {
	e := newDBEnv(t)
	_, c := e.userWithRole("budi")
	id := c.createDB("billing")
	e.dbs.PGDump = fakeTool(t, "printf 'DUMP-BYTES'")
	argsFile := filepath.Join(t.TempDir(), "restore-args")
	e.dbs.PGRestore = fakeTool(t, `echo "$@" > `+argsFile)

	r := c.do("POST", dbPath(id, "/backups"), nil)
	var b dbs.Backup
	r.into(t, &b)
	if r.status != 201 || !b.Manual || b.SizeBytes != 10 || !strings.HasSuffix(b.ID, "-m.dump") {
		t.Fatalf("create backup = %d %s", r.status, r.body)
	}

	var list []dbs.Backup
	c.do("GET", dbPath(id, "/backups"), nil).into(t, &list)
	if len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("list = %+v", list)
	}

	r = c.do("GET", dbPath(id, "/backups/"+b.ID), nil)
	if r.status != 200 || string(r.body) != "DUMP-BYTES" ||
		!strings.HasPrefix(r.header.Get("Content-Disposition"), "attachment;") || !strings.Contains(r.header.Get("Content-Disposition"), b.ID) {
		t.Fatalf("download = %d %q %v", r.status, r.body, r.header)
	}

	if r := c.do("POST", dbPath(id, "/backups/"+b.ID+"/restore"), obj{"confirm": "wrong"}); r.status != 400 {
		t.Fatalf("restore with wrong confirm = %d", r.status)
	}
	if got := e.pg.CallsWithPrefix("terminate"); len(got) != 0 {
		t.Fatalf("terminated before confirmation: %v", got)
	}
	if r := c.do("POST", dbPath(id, "/backups/"+b.ID+"/restore"), obj{"confirm": "budi_billing"}); r.status != 200 {
		t.Fatalf("restore = %d %s", r.status, r.body)
	}
	if args, _ := os.ReadFile(argsFile); !strings.Contains(string(args), "--role=budi -d budi_billing") {
		t.Fatalf("pg_restore args = %q", args)
	}

	acts := activityActions(t, c, "")
	for _, want := range []string{store.ActDBBackup, store.ActDBRestore} {
		if !slices.Contains(acts, want) {
			t.Errorf("activity %v lacks %s", acts, want)
		}
	}
}

func TestBackupErrors(t *testing.T) {
	e := newDBEnv(t)
	_, c := e.userWithRole("budi")
	id := c.createDB("billing")
	other := c.createDB("other")

	for _, bid := range []string{"x.dump", "..%2Fbudi_other%2F20261030-020000.dump", "20261030-020000.dump"} {
		r := c.do("GET", dbPath(id, "/backups/"+bid), nil)
		if r.status != 404 {
			t.Errorf("download %q = %d %s", bid, r.status, r.body)
		}
	}

	e.dbs.PGDump = fakeTool(t, "echo 'pg_dump: server closed the connection' >&2; exit 1")
	r := c.do("POST", dbPath(id, "/backups"), nil)
	if r.status != 502 || !strings.Contains(r.errMsg(t), "server closed the connection") {
		t.Fatalf("failed dump = %d %s", r.status, r.body)
	}

	// A backup of one database cannot be restored into another of the same user.
	e.dbs.PGDump = fakeTool(t, "printf x")
	var b dbs.Backup
	c.do("POST", dbPath(other, "/backups"), nil).into(t, &b)
	r = c.do("POST", dbPath(id, "/backups/"+b.ID+"/restore"), obj{"confirm": "budi_billing"})
	if r.status != 404 || r.errMsg(t) != "Backup tidak ditemukan" {
		t.Fatalf("cross-database restore = %d %s", r.status, r.body)
	}
}
