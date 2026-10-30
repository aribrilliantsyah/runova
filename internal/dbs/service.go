// Package dbs is Runova's PostgreSQL domain logic: per-user roles, databases
// within the user's quota, the environment handed to apps, pg_hba.conf and
// backups. It reaches PostgreSQL only through pg.Admin and the pg_dump and
// pg_restore binaries.
package dbs

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"runova/internal/pg"
	"runova/internal/secret"
	"runova/internal/store"
)

var (
	// ErrNotFound covers a missing database and one the actor does not own.
	ErrNotFound = &userError{store.ErrNotFound, "Database tidak ditemukan"}
	// ErrQuota marks a request refused because the database quota is used up.
	ErrQuota = errors.New("quota exceeded")
	// ErrInUse marks a database that apps still use.
	ErrInUse = errors.New("database in use")
	// ErrValidation marks a request the user has to correct.
	ErrValidation = errors.New("validation failed")
	// ErrPGDown marks PostgreSQL as unreachable.
	ErrPGDown = &userError{errPGDown, "PostgreSQL tidak bisa diakses"}
	// ErrPG marks PostgreSQL refusing a command while reachable.
	ErrPG = errors.New("postgres error")

	errPGDown = errors.New("postgres unreachable")
)

// userError carries a message meant for the user and the sentinel it matches.
type userError struct {
	kind error
	msg  string
}

func (e *userError) Error() string { return e.msg }
func (e *userError) Unwrap() error { return e.kind }

func invalid(format string, a ...any) error {
	return &userError{ErrValidation, fmt.Sprintf(format, a...)}
}

const (
	defaultPGPort    = 5432
	dockerHost       = "host.docker.internal"
	rolePasswordLen  = 32
	rolePasswordAlph = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	dockerSubnet     = "172.30.0.0/16"
)

// Service is the database domain logic. Store, PG, Box and BackupDir are
// required; the rest have working defaults.
type Service struct {
	Store     *store.Store
	PG        pg.Admin
	Box       *secret.Box
	BackupDir string
	Now       func() time.Time
	// HBAPath is the pg_hba.d file Runova owns; WriteHBA fails when it is empty.
	HBAPath string
	// PGDump and PGRestore are the binaries; empty means "pg_dump" and "pg_restore" from PATH.
	PGDump, PGRestore string
	// PGEnv is the libpq environment (PGHOST, PGUSER, …) the binaries run with.
	PGEnv []string
	// PGPort is the port apps and external tools connect to; 0 means 5432.
	PGPort int

	roleLocks sync.Map   // username → *sync.Mutex
	createMu  sync.Mutex // serializes quota checks with the creates they guard
	dbLocks   sync.Map   // database name → *sync.Mutex held during a backup or restore
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) port() int {
	if s.PGPort > 0 {
		return s.PGPort
	}
	return defaultPGPort
}

// lockRole serializes DDL on one role: PostgreSQL refuses concurrent changes
// to the same role ("tuple concurrently updated").
func (s *Service) lockRole(username string) (unlock func()) {
	v, _ := s.roleLocks.LoadOrStore(username, new(sync.Mutex))
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func randomPassword() (string, error) {
	size := big.NewInt(int64(len(rolePasswordAlph)))
	out := make([]byte, rolePasswordLen)
	for i := range out {
		n, err := rand.Int(rand.Reader, size)
		if err != nil {
			return "", fmt.Errorf("generate password: %w", err)
		}
		out[i] = rolePasswordAlph[n.Int64()]
	}
	return string(out), nil
}

// pgFail classifies a failed PostgreSQL call: unreachable server → ErrPGDown,
// rejected argument → ErrValidation, anything else → ErrPG.
func (s *Service) pgFail(ctx context.Context, what string, err error) error {
	if errors.Is(err, pg.ErrInvalid) {
		return invalid("%v", err)
	}
	if perr := s.PG.Ping(context.WithoutCancel(ctx)); perr != nil {
		slog.Warn("postgres unreachable", "op", what, "err", err)
		return ErrPGDown
	}
	slog.Error("postgres command failed", "op", what, "err", err)
	return fmt.Errorf("%w: %s: %w", ErrPG, what, err)
}

func (s *Service) notifyUser(ctx context.Context, userID int64, level store.NotificationLevel, title, body string, appID *int64) {
	n := store.Notification{UserID: userID, At: s.now().UnixMilli(), Level: level, Title: title, Body: body, AppID: appID}
	if err := s.Store.Notify(context.WithoutCancel(ctx), n); err != nil {
		slog.Error("notify", "user", userID, "err", err)
	}
}

// NotifyAdmins tells every admin about a problem only they can fix.
func (s *Service) NotifyAdmins(ctx context.Context, level store.NotificationLevel, title, body string) {
	users, err := s.Store.Users(ctx)
	if err != nil {
		slog.Error("list users for notification", "err", err)
		return
	}
	for _, u := range users {
		if u.IsAdmin() {
			s.notifyUser(ctx, u.ID, level, title, body, nil)
		}
	}
}

// ensureRole creates the user's role or resets it to the stored password and
// connection limit, generating the password on first use. Callers hold the
// role lock.
func (s *Service) ensureRole(ctx context.Context, u store.User) error {
	sealed, err := s.Store.PGPassword(ctx, u.ID)
	if err != nil {
		return err
	}
	var password string
	if sealed == "" {
		if password, err = randomPassword(); err != nil {
			return err
		}
	} else {
		plain, err := s.Box.Open(sealed)
		if err != nil {
			return fmt.Errorf("open pg password of %s: %w", u.Username, err)
		}
		password = string(plain)
	}
	if err := s.PG.EnsureRole(ctx, u.Username, password, u.Quota.PGConnections); err != nil {
		return s.pgFail(ctx, "ensure role "+u.Username, err)
	}
	if sealed != "" {
		return nil
	}
	if sealed, err = s.Box.Seal([]byte(password)); err != nil {
		return err
	}
	return s.Store.SetPGPassword(ctx, u.ID, sealed)
}

// OnUserCreated creates the user's PostgreSQL role with a random password
// and stores the password sealed.
func (s *Service) OnUserCreated(ctx context.Context, u store.User) error {
	defer s.lockRole(u.Username)()
	return s.ensureRole(ctx, u)
}

// OnQuotaChanged applies the user's connection quota to the role.
func (s *Service) OnQuotaChanged(ctx context.Context, u store.User) error {
	defer s.lockRole(u.Username)()
	if err := s.PG.SetConnLimit(ctx, u.Username, u.Quota.PGConnections); err != nil {
		return s.pgFail(ctx, "set connection limit of "+u.Username, err)
	}
	return nil
}

// OnUserDeleted drops the role with all its databases and removes the
// backups of those databases.
func (s *Service) OnUserDeleted(ctx context.Context, username string) error {
	defer s.lockRole(username)()
	if err := s.PG.DropRole(ctx, username); err != nil {
		return s.pgFail(ctx, "drop role "+username, err)
	}
	dirs, err := filepath.Glob(filepath.Join(s.BackupDir, username+"_*"))
	if err != nil {
		return fmt.Errorf("find backups of %s: %w", username, err)
	}
	for _, d := range dirs {
		if err := os.RemoveAll(d); err != nil {
			return fmt.Errorf("remove backups %s: %w", d, err)
		}
	}
	return nil
}

// ReconcileRoles makes sure every user has a role with the stored password
// and the quota's connection limit. A failure for one user does not stop the
// others; the errors are joined.
func (s *Service) ReconcileRoles(ctx context.Context) error {
	users, err := s.Store.Users(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, u := range users {
		if err := s.OnUserCreated(ctx, u); err != nil {
			errs = append(errs, fmt.Errorf("role %s: %w", u.Username, err))
		}
	}
	return errors.Join(errs...)
}

// PGDown reports whether PostgreSQL is unreachable.
func (s *Service) PGDown(ctx context.Context) bool { return s.PG.Ping(ctx) != nil }

// Create makes a database named "<username>_<suffix>" for the user.
func (s *Service) Create(ctx context.Context, u store.User, suffix string) (store.Database, error) {
	name, err := pg.DBName(u.Username, suffix)
	if err != nil {
		return store.Database{}, invalid("%v", err)
	}
	s.createMu.Lock()
	defer s.createMu.Unlock()
	existing, err := s.Store.DatabasesByUser(ctx, u.ID)
	if err != nil {
		return store.Database{}, err
	}
	if len(existing) >= u.Quota.Databases {
		return store.Database{}, &userError{ErrQuota, fmt.Sprintf("Batas database tercapai (%d dari %d)", len(existing), u.Quota.Databases)}
	}
	for _, d := range existing {
		if d.Name == name {
			return store.Database{}, &userError{store.ErrConflict, "Nama database sudah dipakai"}
		}
	}

	unlock := s.lockRole(u.Username)
	err = s.PG.CreateDatabase(ctx, name, u.Username)
	if err != nil {
		// The role may be missing when PostgreSQL was down at user creation.
		if rerr := s.ensureRole(ctx, u); rerr == nil {
			err = s.PG.CreateDatabase(ctx, name, u.Username)
		}
	}
	unlock()
	if err != nil {
		return store.Database{}, s.pgFail(ctx, "create database "+name, err)
	}

	d, err := s.Store.CreateDatabase(ctx, store.Database{UserID: u.ID, Name: name, CreatedAt: s.now().UnixMilli()})
	if err != nil {
		if derr := s.PG.DropDatabase(context.WithoutCancel(ctx), name); derr != nil {
			slog.Error("roll back database creation", "database", name, "err", derr)
		}
		if errors.Is(err, store.ErrConflict) {
			return store.Database{}, &userError{store.ErrConflict, "Nama database sudah dipakai"}
		}
		return store.Database{}, err
	}
	return d, nil
}

// DBView is a database as the owner sees it.
type DBView struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	SizeBytes   int64    `json:"size_bytes"`
	Connections int      `json:"connections"`
	Apps        []string `json:"apps"`
	CreatedAt   int64    `json:"created_at"`
}

// List returns the user's databases. Size and connections are 0 when
// PostgreSQL cannot be asked; PGDown tells the caller why.
func (s *Service) List(ctx context.Context, u store.User) ([]DBView, error) {
	dbs, err := s.Store.DatabasesByUser(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	links, err := s.Store.DatabaseApps(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	apps := map[int64][]string{}
	for _, l := range links {
		apps[l.DatabaseID] = append(apps[l.DatabaseID], l.AppName)
	}
	live := map[string]pg.DBInfo{}
	if infos, err := s.PG.Databases(ctx, u.Username); err == nil {
		for _, i := range infos {
			live[i.Name] = i
		}
	} else {
		slog.Warn("list postgres databases", "user", u.Username, "err", err)
	}
	out := make([]DBView, 0, len(dbs))
	for _, d := range dbs {
		v := DBView{ID: d.ID, Name: d.Name, Apps: apps[d.ID], CreatedAt: d.CreatedAt}
		if v.Apps == nil {
			v.Apps = []string{}
		}
		if i, ok := live[d.Name]; ok {
			v.SizeBytes, v.Connections = i.SizeBytes, i.Connections
		}
		out = append(out, v)
	}
	return out, nil
}

// owned returns the database when u owns it; everything else is ErrNotFound.
func (s *Service) owned(ctx context.Context, u store.User, id int64) (store.Database, error) {
	d, err := s.Store.Database(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.UserID != u.ID) {
		return store.Database{}, ErrNotFound
	}
	return d, err
}

// Delete drops the database after the owner typed its name.
func (s *Service) Delete(ctx context.Context, u store.User, dbID int64, confirm string) error {
	d, err := s.owned(ctx, u, dbID)
	if err != nil {
		return err
	}
	if confirm != d.Name {
		return invalid("Ketik nama database untuk konfirmasi")
	}
	if err := s.checkUnused(ctx, d); err != nil {
		return err
	}
	unlock := s.lockRole(u.Username)
	err = s.PG.DropDatabase(ctx, d.Name)
	unlock()
	if err != nil {
		return s.pgFail(ctx, "drop database "+d.Name, err)
	}
	if err := s.Store.DeleteDatabase(ctx, d.ID); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return s.checkUnused(ctx, d)
		}
		return err
	}
	if err := os.RemoveAll(filepath.Join(s.BackupDir, d.Name)); err != nil {
		slog.Warn("remove backups", "database", d.Name, "err", err)
	}
	return nil
}

func (s *Service) checkUnused(ctx context.Context, d store.Database) error {
	links, err := s.Store.DatabaseApps(ctx, d.UserID)
	if err != nil {
		return err
	}
	var names []string
	for _, l := range links {
		if l.DatabaseID == d.ID {
			names = append(names, l.AppName)
		}
	}
	if len(names) > 0 {
		return &userError{ErrInUse, "Masih dipakai oleh: " + strings.Join(names, ", ")}
	}
	return nil
}

// ConnInfo is what an outside tool needs to connect.
type ConnInfo struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
}

// Connection returns the connection details of one of the user's databases;
// the password only when reveal is set.
func (s *Service) Connection(ctx context.Context, u store.User, dbID int64, reveal bool) (ConnInfo, error) {
	d, err := s.owned(ctx, u, dbID)
	if err != nil {
		return ConnInfo{}, err
	}
	settings, err := s.Store.Settings(ctx)
	if err != nil {
		return ConnInfo{}, err
	}
	info := ConnInfo{Host: settings.PublicIP, Port: s.port(), Database: d.Name, User: u.Username}
	if reveal {
		if info.Password, err = s.rolePassword(ctx, u.ID); err != nil {
			return ConnInfo{}, err
		}
	}
	return info, nil
}

func (s *Service) rolePassword(ctx context.Context, userID int64) (string, error) {
	sealed, err := s.Store.PGPassword(ctx, userID)
	if err != nil {
		return "", err
	}
	if sealed == "" {
		return "", fmt.Errorf("%w: role belum dibuat", ErrPG)
	}
	plain, err := s.Box.Open(sealed)
	if err != nil {
		return "", fmt.Errorf("open pg password: %w", err)
	}
	return string(plain), nil
}

// RotatePassword gives the user's role a new random password. PostgreSQL is
// changed first and the stored password only after that succeeded, so a
// failure leaves both unchanged. Owners of apps that use the user's
// databases are told to redeploy.
func (s *Service) RotatePassword(ctx context.Context, u store.User) error {
	password, err := randomPassword()
	if err != nil {
		return err
	}
	sealed, err := s.Box.Seal([]byte(password))
	if err != nil {
		return err
	}
	unlock := s.lockRole(u.Username)
	defer unlock()
	if err := s.PG.SetPassword(ctx, u.Username, password); err != nil {
		return s.pgFail(ctx, "set password of "+u.Username, err)
	}
	if err := s.Store.SetPGPassword(ctx, u.ID, sealed); err != nil {
		// PostgreSQL now holds a password nobody knows; the next reconcile resets it.
		return fmt.Errorf("store new pg password: %w", err)
	}
	links, err := s.Store.DatabaseApps(ctx, u.ID)
	if err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, l := range links {
		if seen[l.AppID] {
			continue
		}
		seen[l.AppID] = true
		id := l.AppID
		s.notifyUser(ctx, u.ID, store.LevelWarning, "Password database berubah", "Deploy ulang "+l.AppName+" agar memakai password baru", &id)
	}
	return nil
}

// AppEnv returns the database variables for the app ("NAME=value"), nil when
// it uses no database. It reads the stored password and never connects to
// PostgreSQL.
func (s *Service) AppEnv(ctx context.Context, app store.App) ([]string, error) {
	if app.DatabaseID == nil {
		return nil, nil
	}
	d, err := s.Store.Database(ctx, *app.DatabaseID)
	if err != nil {
		return nil, fmt.Errorf("database of app %s: %w", app.Name, err)
	}
	owner, err := s.Store.User(ctx, d.UserID)
	if err != nil {
		return nil, err
	}
	password, err := s.rolePassword(ctx, owner.ID)
	if err != nil {
		return nil, err
	}
	port := strconv.Itoa(s.port())
	hostport := dockerHost + ":" + port
	u := url.URL{Scheme: "postgres", User: url.UserPassword(owner.Username, password), Host: hostport, Path: "/" + d.Name}
	return []string{
		"DATABASE_URL=" + u.String(),
		"SPRING_DATASOURCE_URL=jdbc:postgresql://" + hostport + "/" + d.Name,
		"SPRING_DATASOURCE_USERNAME=" + owner.Username,
		"SPRING_DATASOURCE_PASSWORD=" + password,
		"PGHOST=" + dockerHost,
		"PGPORT=" + port,
		"PGUSER=" + owner.Username,
		"PGPASSWORD=" + password,
		"PGDATABASE=" + d.Name,
	}, nil
}

// WriteHBA writes the pg_hba.d file Runova owns and has PostgreSQL reload it.
func (s *Service) WriteHBA(ctx context.Context) error {
	if s.HBAPath == "" {
		return errors.New("pg_hba path is not configured")
	}
	settings, err := s.Store.Settings(ctx)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Managed by Runova; changes are overwritten.\n")
	for _, n := range append([]string{dockerSubnet}, settings.PGAllowedNets...) {
		fmt.Fprintf(&b, "host all all %s scram-sha-256\n", n)
	}
	if err := writeFileAtomic(s.HBAPath, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", s.HBAPath, err)
	}
	if err := s.PG.ReloadConfig(ctx); err != nil {
		return s.pgFail(ctx, "reload config", err)
	}
	return nil
}

// writeFileAtomic writes to a temporary file next to path and renames it over path.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(mode)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
