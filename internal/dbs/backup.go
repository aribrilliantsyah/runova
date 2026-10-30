package dbs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"runova/internal/store"
)

const (
	backupLayout    = "20060102-150405"
	manualSuffix    = "-m"
	backupExt       = ".dump"
	backupRetention = 7 * 24 * time.Hour
	stderrLimit     = 4 << 10
)

var backupIDRE = regexp.MustCompile(`^(\d{8}-\d{6})(-m)?\.dump$`)

// ErrBackupNotFound is ErrNotFound for a backup the database does not have.
var ErrBackupNotFound = &userError{ErrNotFound, "Backup tidak ditemukan"}

// ToolError is a failed pg_dump or pg_restore run, with the start of its stderr.
type ToolError struct {
	Tool   string
	Stderr string
	Err    error
}

func (e *ToolError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("%s gagal: %v", e.Tool, e.Err)
	}
	return fmt.Sprintf("%s gagal: %s", e.Tool, e.Stderr)
}

func (e *ToolError) Unwrap() error { return e.Err }

// Backup is one dump file of a database.
type Backup struct {
	ID        string `json:"id"` // file name inside the database's backup folder
	At        int64  `json:"at"` // Unix milliseconds, from the file name
	SizeBytes int64  `json:"size_bytes"`
	Manual    bool   `json:"manual"`
}

func (s *Service) dumpBin() string {
	if s.PGDump != "" {
		return s.PGDump
	}
	return "pg_dump"
}

func (s *Service) restoreBin() string {
	if s.PGRestore != "" {
		return s.PGRestore
	}
	return "pg_restore"
}

func (s *Service) backupFolder(db string) string { return filepath.Join(s.BackupDir, db) }

// dbLock serializes backups and restores of one database.
func (s *Service) dbLock(name string) (unlock func()) {
	v, _ := s.dbLocks.LoadOrStore(name, new(sync.Mutex))
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// limitedBuffer keeps the first stderrLimit bytes written to it.
type limitedBuffer struct{ buf bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := stderrLimit - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return strings.TrimSpace(b.buf.String()) }

func (s *Service) command(ctx context.Context, bin string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), s.PGEnv...)
	return cmd
}

// BackupNow dumps the database into its backup folder. The dump goes to a
// temporary file that is renamed on success and removed on failure.
func (s *Service) BackupNow(ctx context.Context, db store.Database, manual bool) (Backup, error) {
	defer s.dbLock(db.Name)()
	folder := s.backupFolder(db.Name)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return Backup{}, fmt.Errorf("create backup folder: %w", err)
	}
	tmp, err := os.CreateTemp(folder, ".tmp-*")
	if err != nil {
		return Backup{}, fmt.Errorf("create backup file: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(err error) (Backup, error) {
		tmp.Close()
		os.Remove(tmpName)
		return Backup{}, err
	}

	var stderr limitedBuffer
	cmd := s.command(ctx, s.dumpBin(), "-Fc", "--no-owner", "-d", db.Name)
	cmd.Stdout, cmd.Stderr = tmp, &stderr
	if err := cmd.Run(); err != nil {
		return fail(&ToolError{Tool: "pg_dump", Stderr: stderr.String(), Err: err})
	}
	if err := tmp.Close(); err != nil {
		return fail(fmt.Errorf("write backup: %w", err))
	}

	at := s.now()
	id := at.Format(backupLayout)
	if manual {
		id += manualSuffix
	}
	id += backupExt
	if err := os.Rename(tmpName, filepath.Join(folder, id)); err != nil {
		return fail(fmt.Errorf("store backup: %w", err))
	}
	info, err := os.Stat(filepath.Join(folder, id))
	if err != nil {
		return Backup{}, fmt.Errorf("stat backup: %w", err)
	}
	return Backup{ID: id, At: backupTime(id, at.Location()).UnixMilli(), SizeBytes: info.Size(), Manual: manual}, nil
}

// backupTime parses the time out of a valid backup ID.
func backupTime(id string, loc *time.Location) time.Time {
	m := backupIDRE.FindStringSubmatch(id)
	if m == nil {
		return time.Time{}
	}
	t, err := time.ParseInLocation(backupLayout, m[1], loc)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (s *Service) location() *time.Location { return s.now().Location() }

// Backups lists the database's backups, newest first.
func (s *Service) Backups(ctx context.Context, u store.User, dbID int64) ([]Backup, error) {
	d, err := s.owned(ctx, u, dbID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.backupFolder(d.Name))
	if errors.Is(err, fs.ErrNotExist) {
		return []Backup{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	loc := s.location()
	out := []Backup{}
	for _, e := range entries {
		m := backupIDRE.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{ID: e.Name(), At: backupTime(e.Name(), loc).UnixMilli(), SizeBytes: info.Size(), Manual: m[2] != ""})
	}
	slices.SortFunc(out, func(a, b Backup) int {
		if a.At != b.At {
			return int(b.At - a.At)
		}
		return strings.Compare(b.ID, a.ID)
	})
	return out, nil
}

// BackupPath returns the file of one backup of a database the user owns. The
// ID must be a plain backup file name that exists in that database's folder.
func (s *Service) BackupPath(ctx context.Context, u store.User, dbID int64, backupID string) (string, error) {
	d, err := s.owned(ctx, u, dbID)
	if err != nil {
		return "", err
	}
	return s.backupFile(d, backupID)
}

func (s *Service) backupFile(d store.Database, backupID string) (string, error) {
	if !backupIDRE.MatchString(backupID) {
		return "", ErrBackupNotFound
	}
	path := filepath.Join(s.backupFolder(d.Name), backupID)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrBackupNotFound
	}
	return path, nil
}

// Backup makes a manual backup of one of the user's databases.
func (s *Service) Backup(ctx context.Context, u store.User, dbID int64) (Backup, error) {
	d, err := s.owned(ctx, u, dbID)
	if err != nil {
		return Backup{}, err
	}
	return s.BackupNow(ctx, d, true)
}

// Restore loads a backup into its database, replacing what is there. Other
// connections are ended first. On failure the error is a *ToolError with the
// start of pg_restore's stderr.
func (s *Service) Restore(ctx context.Context, u store.User, dbID int64, backupID string) error {
	d, err := s.owned(ctx, u, dbID)
	if err != nil {
		return err
	}
	path, err := s.backupFile(d, backupID)
	if err != nil {
		return err
	}
	defer s.dbLock(d.Name)()
	if err := s.PG.TerminateConnections(ctx, d.Name); err != nil {
		return s.pgFail(ctx, "terminate connections of "+d.Name, err)
	}
	var stderr limitedBuffer
	cmd := s.command(ctx, s.restoreBin(), "--clean", "--if-exists", "--no-owner", "--role="+u.Username, "-d", d.Name, path)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return &ToolError{Tool: "pg_restore", Stderr: stderr.String(), Err: err}
	}
	return nil
}

// Prune removes backups older than seven days, manual ones included.
func (s *Service) Prune(_ context.Context, now time.Time) error {
	folders, err := os.ReadDir(s.BackupDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list backup folders: %w", err)
	}
	var errs []error
	for _, f := range folders {
		if !f.IsDir() {
			continue
		}
		dir := filepath.Join(s.BackupDir, f.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("list %s: %w", dir, err))
			continue
		}
		for _, file := range files {
			at := backupTime(file.Name(), now.Location())
			if at.IsZero() || now.Sub(at) <= backupRetention {
				continue
			}
			if err := os.Remove(filepath.Join(dir, file.Name())); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// BackupAll backs up every database one after the other and then prunes. A
// failed database is reported to its owner and does not stop the rest.
func (s *Service) BackupAll(ctx context.Context) {
	all, err := s.Store.AllDatabases(ctx)
	if err != nil {
		slog.Error("list databases for backup", "err", err)
		return
	}
	for _, d := range all {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.BackupNow(ctx, d, false); err != nil {
			slog.Error("scheduled backup", "database", d.Name, "err", err)
			s.notifyUser(ctx, d.UserID, store.LevelError, "Backup "+d.Name+" gagal", err.Error(), nil)
		}
	}
	if err := s.Prune(ctx, s.now()); err != nil {
		slog.Error("prune backups", "err", err)
	}
}
