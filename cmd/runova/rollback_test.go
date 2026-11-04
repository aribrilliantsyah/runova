package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"runova/internal/config"
	"runova/internal/store"
)

const rollbackNow = 1793800000

type rollbackRig struct {
	t       *testing.T
	cfg     config.Config
	dir     string
	bin     string
	schema  int  // schema the old binary reports
	current int  // schema of the database on disk
	legacy  bool // the old binary prints `db version` in Indonesian, like v0.3.0 and older
	calls   []string
	failOn  string
	tty     bool
	input   string
	root    bool
	chowned []string
}

// newRollbackRig lays out a data folder with a migrated database, an
// installed binary "new" and a kept binary "old" (version 0.1.0).
func newRollbackRig(t *testing.T) *rollbackRig {
	t.Helper()
	data := t.TempDir()
	cfg := config.Load(func(k string) string { return map[string]string{"RUNOVA_DATA": data}[k] })
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	current, _, err := store.SchemaVersion(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	r := &rollbackRig{t: t, cfg: cfg, dir: filepath.Join(data, "rollback"), bin: filepath.Join(t.TempDir(), "runova"), schema: current, current: current, root: true}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.write(filepath.Join(r.dir, "runova"), "old-binary")
	r.write(filepath.Join(r.dir, "VERSION"), "0.1.0\n")
	r.write(r.bin, "new-binary")
	return r
}

func (r *rollbackRig) write(path, content string) {
	r.t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rollbackRig) read(path string) string {
	r.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

func (r *rollbackRig) run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	d := rollbackDeps{
		IsRoot:  func() bool { return r.root },
		Console: console{In: strings.NewReader(r.input), Interactive: r.tty},
		BinPath: r.bin,
		Chown:   func(p string) error { r.chowned = append(r.chowned, p); return nil },
		Now:     func() time.Time { return time.Unix(rollbackNow, 0) },
		Version: "0.2.0",
		Exec: func(_ context.Context, _ io.Reader, stdout, _ io.Writer, name string, a ...string) error {
			call := name + " " + strings.Join(a, " ")
			if name == filepath.Join(r.dir, "runova") {
				call = "old-binary " + strings.Join(a, " ")
				if r.legacy {
					fmt.Fprintf(stdout, "Skema database: %d\nSkema tertinggi binary ini: %d\n", r.current, r.schema)
				} else {
					fmt.Fprintf(stdout, "Database schema: %d\nHighest schema of this binary: %d\n", r.current, r.schema)
				}
			}
			if name == "runuser" {
				if len(a) < 2 || a[0] != "-u" || a[1] != "runova" || a[len(a)-2] != r.bin {
					r.t.Errorf("check must run as the service user: %v", a)
				}
				call = "installed " + a[len(a)-1]
			}
			r.calls = append(r.calls, strings.TrimSpace(call))
			if r.failOn != "" && strings.TrimSpace(call) == r.failOn {
				return errors.New("boom")
			}
			return nil
		},
	}
	code := rollbackCmd(context.Background(), r.cfg, args, d, &out, &errb)
	return code, out.String(), errb.String()
}

func (r *rollbackRig) addBackup(schema int, at int64, content string) string {
	p := fmt.Sprintf("%s.bak-v%d-%d", r.cfg.DBPath(), schema, at)
	r.write(p, content)
	return p
}

func (r *rollbackRig) systemctlCalls() []string {
	var out []string
	for _, c := range r.calls {
		if strings.HasPrefix(c, "systemctl") {
			out = append(out, c)
		}
	}
	return out
}

func TestRollbackNeedsRoot(t *testing.T) {
	r := newRollbackRig(t)
	r.root = false
	if code, _, errOut := r.run("--yes"); code != 1 || !strings.Contains(errOut, "Run with sudo: sudo runova rollback") || len(r.calls) != 0 {
		t.Fatalf("code %d, err %q, calls %v", code, errOut, r.calls)
	}
}

func TestRollbackWithoutPreviousVersion(t *testing.T) {
	r := newRollbackRig(t)
	os.RemoveAll(r.dir)
	code, _, errOut := r.run("--yes")
	if code != 1 || !strings.Contains(errOut, "No previous version to roll back to") || len(r.calls) != 0 {
		t.Fatalf("code %d, err %q, calls %v", code, errOut, r.calls)
	}
}

func TestRollbackSameSchemaKeepsDatabase(t *testing.T) {
	r := newRollbackRig(t)
	before := r.read(r.cfg.DBPath())
	code, out, errOut := r.run("--yes")
	if code != 0 {
		t.Fatalf("code %d, out %q, err %q", code, out, errOut)
	}
	if r.read(r.cfg.DBPath()) != before {
		t.Error("database changed although the schema is the same")
	}
	if matches, _ := filepath.Glob(r.cfg.DBPath() + ".before-rollback-*"); len(matches) != 0 {
		t.Errorf("unexpected safety copy %v", matches)
	}
	if r.read(r.bin) != "old-binary" || r.read(filepath.Join(r.dir, "runova")) != "new-binary" || r.read(filepath.Join(r.dir, "VERSION")) != "0.2.0\n" {
		t.Error("binaries were not swapped")
	}
	want := []string{"old-binary db version", "systemctl stop runova", "systemctl start runova", "installed check"}
	if strings.Join(r.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %v, want %v", r.calls, want)
	}
	if !strings.Contains(out, "0.2.0 -> 0.1.0") {
		t.Errorf("summary = %q", out)
	}
}

func TestRollbackRestoresDatabaseBackup(t *testing.T) {
	r := newRollbackRig(t)
	r.schema = r.current - 1
	r.addBackup(r.schema, rollbackNow-5000, "older backup")
	newest := r.addBackup(r.schema, rollbackNow-100, "newest backup")
	r.addBackup(r.schema-1, rollbackNow-50, "other schema backup")
	current := r.read(r.cfg.DBPath())
	r.write(r.cfg.DBPath()+"-wal", "wal")
	r.write(r.cfg.DBPath()+"-shm", "shm")

	code, out, errOut := r.run("--yes")
	if code != 0 {
		t.Fatalf("code %d, out %q, err %q", code, out, errOut)
	}
	if got := r.read(r.cfg.DBPath()); got != "newest backup" {
		t.Fatalf("database = %q, want the newest backup", got)
	}
	safety := fmt.Sprintf("%s.before-rollback-%d", r.cfg.DBPath(), rollbackNow)
	if r.read(safety) != current {
		t.Error("safety copy does not hold the database from before the rollback")
	}
	for _, f := range []string{r.cfg.DBPath() + "-wal", r.cfg.DBPath() + "-shm"} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s still exists", f)
		}
	}
	if st, _ := os.Stat(r.cfg.DBPath()); st.Mode().Perm() != 0o600 {
		t.Errorf("database mode = %v", st.Mode().Perm())
	}
	if _, err := os.Stat(newest); err != nil {
		t.Errorf("the backup itself must stay: %v", err)
	}
	if len(r.chowned) == 0 {
		t.Error("restored database was not handed to the service user")
	}
	if r.read(r.bin) != "old-binary" {
		t.Error("binary not swapped")
	}
	for _, want := range []string{"WARNING", "users, apps, domains, settings", filepath.Base(newest), "users' PostgreSQL databases are not touched"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
}

func TestRollbackWithoutMatchingBackupChangesNothing(t *testing.T) {
	r := newRollbackRig(t)
	r.schema = r.current - 1
	r.addBackup(r.schema-1, rollbackNow-100, "wrong schema")
	db := r.read(r.cfg.DBPath())
	code, _, errOut := r.run("--yes")
	if code != 1 || !strings.Contains(errOut, "no ") || !strings.Contains(errOut, "backup to restore") {
		t.Fatalf("code %d, err %q", code, errOut)
	}
	if got := r.systemctlCalls(); len(got) != 0 {
		t.Errorf("touched the service: %v", got)
	}
	if r.read(r.cfg.DBPath()) != db || r.read(r.bin) != "new-binary" || r.read(filepath.Join(r.dir, "runova")) != "old-binary" {
		t.Error("files changed")
	}
}

func TestRollbackConfirmation(t *testing.T) {
	r := newRollbackRig(t)
	if code, _, errOut := r.run(); code != 1 || !strings.Contains(errOut, "--yes") || len(r.systemctlCalls()) != 0 {
		t.Fatalf("no terminal: code %d, err %q", code, errOut)
	}
	r.tty, r.input = true, "n\n"
	if code, out, _ := r.run(); code != 1 || !strings.Contains(out, "Continue? [y/N]") || len(r.systemctlCalls()) != 0 || r.read(r.bin) != "new-binary" {
		t.Fatalf("answer n: code %d, out %q", code, out)
	}
	r.input = "y\n"
	if code, _, errOut := r.run(); code != 0 {
		t.Fatalf("answer y: code %d, err %q", code, errOut)
	}
}

func TestRollbackTwiceGoesForwardAgain(t *testing.T) {
	r := newRollbackRig(t)
	for i, want := range []string{"old-binary", "new-binary"} {
		if code, _, errOut := r.run("--yes"); code != 0 {
			t.Fatalf("rollback %d: code %d, err %q", i+1, code, errOut)
		}
		if got := r.read(r.bin); got != want {
			t.Fatalf("after rollback %d the installed binary is %q, want %q", i+1, got, want)
		}
	}
	if v := r.read(filepath.Join(r.dir, "VERSION")); v != "0.2.0\n" {
		t.Errorf("VERSION = %q", v)
	}
}

func TestRollbackStopFailureChangesNothing(t *testing.T) {
	r := newRollbackRig(t)
	r.failOn = "systemctl stop runova"
	code, _, errOut := r.run("--yes")
	if code != 1 || !strings.Contains(errOut, "nothing was changed") || r.read(r.bin) != "new-binary" {
		t.Fatalf("code %d, err %q", code, errOut)
	}
	if got := r.systemctlCalls(); len(got) != 1 {
		t.Fatalf("calls %v", got)
	}
}

func TestRollbackRestartsServiceWhenFilesFail(t *testing.T) {
	r := newRollbackRig(t)
	r.schema = r.current - 1
	r.addBackup(r.schema, rollbackNow-100, "backup")
	if err := os.Chmod(r.dir, 0o500); err != nil { // the swap cannot stage the current binary
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(r.dir, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	db := r.read(r.cfg.DBPath())
	code, _, errOut := r.run("--yes")
	if code != 1 || !strings.Contains(errOut, "Rollback gagal") {
		t.Fatalf("code %d, err %q", code, errOut)
	}
	if r.read(r.cfg.DBPath()) != db {
		t.Error("database was restored although the binaries could not be staged")
	}
	if got := r.systemctlCalls(); len(got) != 2 || got[1] != "systemctl start runova" {
		t.Fatalf("service not restarted: %v", got)
	}
}

func TestRollbackUsage(t *testing.T) {
	r := newRollbackRig(t)
	if code, _, errOut := r.run("--nope"); code != 2 || !strings.Contains(errOut, "Usage:") {
		t.Fatalf("code %d, err %q", code, errOut)
	}
}

// An old binary (v0.3.0 and older) prints `db version` in Indonesian; rollback
// must read both that and the current English format.
func TestRollbackReadsOldAndNewDBVersionOutput(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		r := newRollbackRig(t)
		r.legacy = legacy
		r.schema = r.current - 1
		r.addBackup(r.schema, rollbackNow-100, "backup")
		if code, out, errOut := r.run("--yes"); code != 0 || r.read(r.cfg.DBPath()) != "backup" {
			t.Errorf("legacy=%v: code %d, out %q, err %q", legacy, code, out, errOut)
		}
	}
}

func TestHighestSchemaLine(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Database schema: 3\nHighest schema of this binary: 7\n", "7"},
		{"Skema database: 3\nSkema tertinggi binary ini: 7\n", "7"},
		{"Highest schema of this binary: 7 extra\n", ""},
		{"Highest schema: 7\n", ""},
	} {
		got := ""
		if m := highestSchemaLine.FindStringSubmatch(tc.in); m != nil {
			got = m[1]
		}
		if got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}
