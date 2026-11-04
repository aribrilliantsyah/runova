package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"runova/internal/config"
	"runova/internal/store"
)

const (
	serviceName     = "runova"
	installedBin    = "/usr/local/bin/runova"
	rollbackDirName = "rollback"
)

// highestSchemaLine matches the last line of `runova db version`. Binaries up to
// v0.3.0, which rollback/ may hold, print it in Indonesian.
var (
	highestSchemaLine = regexp.MustCompile(`(?m)^(?:Highest schema of this binary|Skema tertinggi binary ini): (\d+)$`)
	backupSuffix      = regexp.MustCompile(`^\.bak-v(\d+)-(\d+)$`)
)

// rollbackDeps are the things `runova rollback` takes from the machine it runs on.
type rollbackDeps struct {
	IsRoot  func() bool
	Console console
	Exec    execFunc
	// BinPath is the installed binary that is swapped with rollback/runova.
	BinPath string
	// Chown gives a restored database file to the service user.
	Chown   func(path string) error
	Now     func() time.Time
	Version string
}

func prodRollbackDeps() rollbackDeps {
	return rollbackDeps{
		IsRoot: func() bool { return os.Geteuid() == 0 }, Console: stdConsole(), Exec: osExec,
		BinPath: installedBin, Chown: chownToService, Now: time.Now, Version: version,
	}
}

func chownToService(path string) error {
	u, err := user.Lookup("runova")
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return os.Chown(path, uid, gid)
}

// dbBackup is a runova.db.bak-v<schema>-<unix> file written by a migration.
type dbBackup struct {
	path string
	at   time.Time
}

// newestBackup finds the newest backup of a database at schema version.
func newestBackup(dbPath string, schema int) (dbBackup, bool, error) {
	entries, err := os.ReadDir(filepath.Dir(dbPath))
	if err != nil {
		return dbBackup{}, false, err
	}
	var best dbBackup
	var found bool
	for _, e := range entries {
		suffix, ok := strings.CutPrefix(e.Name(), filepath.Base(dbPath))
		if !ok {
			continue
		}
		m := backupSuffix.FindStringSubmatch(suffix)
		if m == nil || m[1] != strconv.Itoa(schema) {
			continue
		}
		unix, _ := strconv.ParseInt(m[2], 10, 64)
		if at := time.Unix(unix, 0); !found || at.After(best.at) {
			best, found = dbBackup{filepath.Join(filepath.Dir(dbPath), e.Name()), at}, true
		}
	}
	return best, found, nil
}

// rollbackCmd handles `runova rollback [--yes]`: it puts back the binary that
// install.sh kept in rollback/ and, when the database has been migrated past
// what that binary knows, the backup the migration wrote. The binaries are
// swapped, so a second rollback goes forward again.
func rollbackCmd(ctx context.Context, cfg config.Config, args []string, d rollbackDeps, stdout, stderr io.Writer) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--yes" && args[0] != "-y") {
		fmt.Fprint(stderr, usage)
		return 2
	}
	yes := len(args) == 1
	if !d.IsRoot() {
		fmt.Fprintln(stderr, "Run with sudo: sudo runova rollback")
		return 1
	}

	dir := filepath.Join(cfg.DataDir, rollbackDirName)
	oldBin := filepath.Join(dir, "runova")
	if st, err := os.Stat(oldBin); err != nil || !st.Mode().IsRegular() {
		fmt.Fprintln(stderr, "No previous version to roll back to")
		return 1
	}
	oldVersion := "unknown"
	if b, err := os.ReadFile(filepath.Join(dir, "VERSION")); err == nil && strings.TrimSpace(string(b)) != "" {
		oldVersion = strings.TrimSpace(string(b))
	}

	var probe bytes.Buffer
	if err := d.Exec(ctx, nil, &probe, io.Discard, oldBin, "db", "version"); err != nil {
		fmt.Fprintf(stderr, "The previous binary (%s) cannot run: %v\n", oldVersion, err)
		return 1
	}
	m := highestSchemaLine.FindSubmatch(probe.Bytes())
	if m == nil {
		fmt.Fprintf(stderr, "The previous binary (%s) does not report its schema version; rollback canceled\n", oldVersion)
		return 1
	}
	oldSchema, _ := strconv.Atoi(string(m[1]))
	current, _, err := store.SchemaVersion(cfg.DBPath())
	if err != nil {
		fmt.Fprintf(stderr, "Could not read the database schema version: %v\n", err)
		return 1
	}

	var restore dbBackup
	needRestore := current > oldSchema
	if needRestore {
		var found bool
		if restore, found, err = newestBackup(cfg.DBPath(), oldSchema); err != nil {
			fmt.Fprintf(stderr, "Could not read the data folder: %v\n", err)
			return 1
		}
		if !found {
			fmt.Fprintf(stderr, "The database was migrated to schema %d, but Runova %s only knows schema %d, and there is no %s.bak-v%d-* backup to restore. Nothing was changed.\n",
				current, oldVersion, oldSchema, filepath.Base(cfg.DBPath()), oldSchema)
			return 1
		}
	}

	fmt.Fprintf(stdout, "Rollback Runova %s -> %s\n", d.Version, oldVersion)
	if needRestore {
		fmt.Fprintf(stdout, "The database will be restored from backup %s (%s).\n", filepath.Base(restore.path), restore.at.Local().Format("2006-01-02 15:04:05"))
		fmt.Fprintln(stdout, "WARNING: panel data changed since the update (users, apps, domains, settings) will be lost.")
		fmt.Fprintln(stdout, "App containers and users' PostgreSQL databases are not touched.")
	} else {
		fmt.Fprintf(stdout, "Database schema (%d) is still known to Runova %s; the database is not changed.\n", current, oldVersion)
	}
	ok, err := d.Console.confirm(stdout, yes)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintln(stderr, "Canceled")
		return 1
	}

	systemctl := func(action string) error {
		return d.Exec(ctx, nil, stdout, stderr, "systemctl", action, serviceName)
	}
	if err := systemctl("stop"); err != nil {
		fmt.Fprintf(stderr, "Could not stop service %s; nothing was changed: %v\n", serviceName, err)
		return 1
	}
	safety := fmt.Sprintf("%s.before-rollback-%d", cfg.DBPath(), d.Now().Unix())
	if err := applyRollback(cfg, d, dir, oldBin, safety, restore, needRestore); err != nil {
		fmt.Fprintf(stderr, "Rollback failed: %v\n", err)
		if startErr := systemctl("start"); startErr != nil {
			fmt.Fprintf(stderr, "The service could not be started again either: %v\n", startErr)
		}
		return 1
	}
	if err := systemctl("start"); err != nil {
		fmt.Fprintf(stderr, "Service %s could not be started: %v\nSee: journalctl -u %s -n 50\n", serviceName, err, serviceName)
		return 1
	}
	// As the service user, like the installer: root would be refused by PostgreSQL peer auth.
	if err := d.Exec(ctx, nil, stdout, stderr, "runuser", "-u", serviceName, "--", "env",
		"RUNOVA_DATA="+cfg.DataDir, "RUNOVA_SECRET_KEY="+cfg.SecretKeyPath, d.BinPath, "check"); err != nil {
		fmt.Fprintf(stderr, "Rollback finished, but runova check failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Runova %s is back.", oldVersion)
	if needRestore {
		fmt.Fprintf(stdout, " The database from before the rollback is kept at %s", safety)
	}
	fmt.Fprintln(stdout)
	return 0
}

// applyRollback does the file work while the service is stopped. Every file
// is written beside its destination and renamed into place, so a failure
// leaves each one either old or new, never half-written. The binaries are
// staged first so that nothing is restored when they cannot be.
func applyRollback(cfg config.Config, d rollbackDeps, dir, oldBin, safety string, restore dbBackup, needRestore bool) error {
	oldCopy, err := stageCopy(oldBin, filepath.Dir(d.BinPath), 0o755)
	if err != nil {
		return fmt.Errorf("preparing the previous binary: %w", err)
	}
	defer os.Remove(oldCopy)
	curCopy, err := stageCopy(d.BinPath, dir, 0o755)
	if err != nil {
		return fmt.Errorf("saving the current binary: %w", err)
	}
	defer os.Remove(curCopy)
	dbPath := cfg.DBPath()
	if needRestore {
		if err := copyFile(dbPath, safety, 0o600, d.Chown); err != nil {
			return fmt.Errorf("copying the current database: %w", err)
		}
		for _, side := range []string{"-wal", "-shm"} {
			if err := os.Remove(dbPath + side); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := copyFile(restore.path, dbPath, 0o600, d.Chown); err != nil {
			return fmt.Errorf("restoring the backup (the current database is safe at %s): %w", safety, err)
		}
	}

	if err := os.Rename(oldCopy, d.BinPath); err != nil {
		return fmt.Errorf("installing the previous binary: %w", err)
	}
	if err := os.Rename(curCopy, oldBin); err != nil {
		return fmt.Errorf("saving the current binary for the next rollback: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, "VERSION"), []byte(d.Version+"\n"), 0o644)
}

// copyFile copies src to dst through a temporary file in dst's folder.
func copyFile(src, dst string, mode os.FileMode, chown func(string) error) error {
	tmp, err := stageCopy(src, filepath.Dir(dst), mode)
	if err != nil {
		return err
	}
	if chown != nil {
		if err := chown(tmp); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// stageCopy copies src into a new temporary file in dir and returns its path.
func stageCopy(src, dir string, mode os.FileMode) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, ".runova-rollback-*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Chmod(mode)
	}
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".runova-rollback-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
