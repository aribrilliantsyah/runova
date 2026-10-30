// Package apps holds Runova's application logic: quota, ports, environment,
// container plans and the deploy lifecycle. It reaches Docker only through
// docker.Engine.
package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"runova/internal/artifact"
	"runova/internal/docker"
	"runova/internal/secret"
	"runova/internal/store"
)

// ErrAppNotFound covers a missing app and an app the actor may not touch.
var ErrAppNotFound = errors.New("app not found")

const (
	defaultHealthTimeout  = 60 * time.Second
	defaultHealthInterval = 500 * time.Millisecond
	dialTimeout           = time.Second
	stopGrace             = 10 * time.Second
	keepDeployments       = 10
	failureLogLines       = 200
)

// Service is the application domain logic. Store, Engine, Box and DataDir are
// required; the remaining exported fields have working defaults and exist so
// tests can replace time, ports, ownership changes and health probing.
type Service struct {
	Store   *store.Store
	Engine  docker.Engine
	Box     *secret.Box
	DataDir string

	Now      func() time.Time
	PortFree func(port int) bool
	// Chown hands path to the user's container UID; nil means OSChown.
	Chown func(path string, uid int) error
	// Dial probes the deployed app; nil means net.DialTimeout.
	Dial func(network, addr string, timeout time.Duration) (net.Conn, error)

	HealthTimeout  time.Duration // 0 means 60 s
	HealthInterval time.Duration // 0 means 500 ms
	// ReconnectDelay is the pause before Monitor resubscribes to Docker events; 0 means 2 s.
	ReconnectDelay time.Duration

	locks     sync.Map   // app ID → *sync.Mutex held while the app is being changed
	createMu  sync.Mutex // serializes quota checks with the inserts they guard
	portMu    sync.Mutex
	randStart func(n int) int // test hook for host port selection

	monMu   sync.Mutex
	crashes map[int64][]time.Time // app ID → recent die events
	stopped map[int64]time.Time   // app ID → last stop requested by Runova
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) chown() func(string, int) error {
	if s.Chown != nil {
		return s.Chown
	}
	return OSChown
}

func (s *Service) healthTimeout() time.Duration {
	if s.HealthTimeout > 0 {
		return s.HealthTimeout
	}
	return defaultHealthTimeout
}

func (s *Service) healthInterval() time.Duration {
	if s.HealthInterval > 0 {
		return s.HealthInterval
	}
	return defaultHealthInterval
}

// lockApp takes the app's change lock without waiting; ok is false when a
// deploy or another change is in progress.
func (s *Service) lockApp(appID int64) (unlock func(), ok bool) {
	v, _ := s.locks.LoadOrStore(appID, new(sync.Mutex))
	mu := v.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
}

// load returns the app and its owner. Only the owner and admins may use an
// app; everyone else gets ErrAppNotFound so IDs reveal nothing.
func (s *Service) load(ctx context.Context, actor store.User, appID int64) (store.App, store.User, error) {
	app, err := s.Store.App(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		return store.App{}, store.User{}, ErrAppNotFound
	}
	if err != nil {
		return store.App{}, store.User{}, err
	}
	switch {
	case app.UserID == actor.ID:
		return app, actor, nil
	case actor.IsAdmin():
		owner, err := s.Store.User(ctx, app.UserID)
		return app, owner, err
	default:
		return store.App{}, store.User{}, ErrAppNotFound
	}
}

// Get returns an app the actor may access, with its owner.
func (s *Service) Get(ctx context.Context, actor store.User, appID int64) (store.App, store.User, error) {
	return s.load(ctx, actor, appID)
}

// UsageOf returns what the user's apps have allocated.
func (s *Service) UsageOf(ctx context.Context, userID int64) (Usage, error) {
	apps, mem, cpu, err := s.Store.UserUsage(ctx, userID)
	return Usage{Apps: apps, MemoryMB: mem, CPUMilli: cpu}, err
}

func (s *Service) notify(ctx context.Context, app store.App, level store.NotificationLevel, title, body string) {
	id := app.ID
	n := store.Notification{UserID: app.UserID, At: s.now().UnixMilli(), Level: level, Title: title, Body: body, AppID: &id}
	if err := s.Store.Notify(ctx, n); err != nil {
		slog.Error("notify", "app", app.Name, "err", err)
	}
}

// logActivity records an event the service itself performed.
func (s *Service) logActivity(ctx context.Context, actor store.User, action, target, detail string) {
	a := store.Activity{At: s.now().UnixMilli(), ActorName: actor.Username, Action: action, Target: target, Detail: detail}
	if actor.ID != 0 {
		a.ActorID = &actor.ID
	}
	if err := s.Store.Log(context.WithoutCancel(ctx), a); err != nil {
		slog.Error("record activity", "action", action, "err", err)
	}
}

// CreateInput is what a user provides to create an app from an upload.
type CreateInput struct {
	UploadID     string           `json:"upload_id"`
	Name         string           `json:"name"`
	JavaVersion  int              `json:"java_version"`
	JVMOpts      string           `json:"jvm_opts"`
	InternalPort int              `json:"internal_port"`
	MemoryMB     int              `json:"memory_mb"`
	CPUMilli     int              `json:"cpu_milli"`
	IPAccess     bool             `json:"ip_access"`
	Env          []store.EnvEntry `json:"env"`
}

// takeUpload loads the user's upload and requires a clean report.
func (s *Service) takeUpload(ctx context.Context, user store.User, id string) (store.Upload, error) {
	up, err := s.Store.Upload(ctx, id, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Upload{}, invalid("Unggahan tidak ditemukan atau sudah kedaluwarsa")
	}
	if err != nil {
		return store.Upload{}, err
	}
	if !up.Report.OK() {
		return store.Upload{}, invalid("Artefak tidak bisa dideploy: %s", strings.Join(up.Report.Problems, "; "))
	}
	if err := validateFileName(up.Report.FileName); err != nil {
		return store.Upload{}, err
	}
	return up, nil
}

// sealEnv encrypts secret values for storage.
func (s *Service) sealEnv(env []store.EnvEntry) ([]store.EnvEntry, error) {
	out := slices.Clone(env)
	for i, e := range out {
		if !e.Secret {
			continue
		}
		sealed, err := s.Box.Seal([]byte(e.Value))
		if err != nil {
			return nil, err
		}
		out[i].Value = sealed
	}
	return out, nil
}

// openEnv decrypts the secret values of stored entries.
func (s *Service) openEnv(env []store.EnvEntry) ([]store.EnvEntry, error) {
	out := slices.Clone(env)
	for i, e := range out {
		if !e.Secret {
			continue
		}
		plain, err := s.Box.Open(e.Value)
		if err != nil {
			return nil, fmt.Errorf("decrypt %s: %w", e.Name, err)
		}
		out[i].Value = string(plain)
	}
	return out, nil
}

// snapshotEnv seals the app's current environment, secrets included in
// plain text inside the sealed JSON, so a rollback restores it exactly.
func (s *Service) snapshotEnv(ctx context.Context, appID int64) (string, error) {
	stored, err := s.Store.Env(ctx, appID)
	if err != nil {
		return "", err
	}
	plain, err := s.openEnv(stored)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(plain)
	if err != nil {
		return "", fmt.Errorf("encode env snapshot: %w", err)
	}
	return s.Box.Seal(raw)
}

func (s *Service) openSnapshot(snapshot string) ([]store.EnvEntry, error) {
	if snapshot == "" {
		return nil, nil
	}
	raw, err := s.Box.Open(snapshot)
	if err != nil {
		return nil, fmt.Errorf("decrypt env snapshot: %w", err)
	}
	var env []store.EnvEntry
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode env snapshot: %w", err)
	}
	return env, nil
}

// Create makes an app and its first (pending) deployment from an upload. It
// never touches Docker: the artifact moves into the artifact tree and the
// upload disappears; call Deploy to run it.
func (s *Service) Create(ctx context.Context, user store.User, in CreateInput) (store.App, store.Deployment, error) {
	if err := ValidateAppName(in.Name); err != nil {
		return store.App{}, store.Deployment{}, err
	}
	if err := ValidateEnv(in.Env); err != nil {
		return store.App{}, store.Deployment{}, err
	}
	up, err := s.takeUpload(ctx, user, in.UploadID)
	if err != nil {
		return store.App{}, store.Deployment{}, err
	}
	settings, err := s.Store.Settings(ctx)
	if err != nil {
		return store.App{}, store.Deployment{}, err
	}
	rep := up.Report

	app := store.App{
		UserID: user.ID, Name: in.Name, Kind: rep.Kind, JVMOpts: in.JVMOpts,
		InternalPort: in.InternalPort, IPAccess: in.IPAccess, MemoryMB: in.MemoryMB, CPUMilli: in.CPUMilli,
		Status: store.AppStopped, DeployMode: store.DeployModeStopStart, CreatedAt: s.now().UnixMilli(),
	}
	if app.InternalPort == 0 {
		app.InternalPort = rep.Port
	}
	if err := validateInternalPort(app.InternalPort); err != nil {
		return store.App{}, store.Deployment{}, err
	}
	if rep.Kind == artifact.KindJar {
		if err := ValidateJVMOpts(in.JVMOpts); err != nil {
			return store.App{}, store.Deployment{}, err
		}
		v := in.JavaVersion
		if v == 0 {
			allowed := allowedJava(settings.Runtimes, rep.MinJava)
			if len(allowed) == 0 {
				return store.App{}, store.Deployment{}, invalid("Tidak ada runtime Java yang diizinkan untuk aplikasi ini")
			}
			v = allowed[0]
		}
		if err := validateJava(settings.Runtimes, rep.MinJava, v); err != nil {
			return store.App{}, store.Deployment{}, err
		}
		app.JavaVersion = &v
	} else {
		if in.JavaVersion != 0 || in.JVMOpts != "" {
			return store.App{}, store.Deployment{}, invalid("Versi Java dan opsi JVM hanya untuk JAR")
		}
		app.JVMOpts = ""
		if !settings.Runtimes[string(rep.Kind)] {
			return store.App{}, store.Deployment{}, invalid("Admin menonaktifkan runtime %s di server ini", rep.Kind)
		}
	}
	if err := validateRange(app.MemoryMB, app.CPUMilli); err != nil {
		return store.App{}, store.Deployment{}, err
	}
	sealed, err := s.sealEnv(in.Env)
	if err != nil {
		return store.App{}, store.Deployment{}, err
	}
	plainSnapshot, err := json.Marshal(in.Env)
	if err != nil {
		return store.App{}, store.Deployment{}, fmt.Errorf("encode env snapshot: %w", err)
	}
	snapshot, err := s.Box.Seal(plainSnapshot)
	if err != nil {
		return store.App{}, store.Deployment{}, err
	}

	s.createMu.Lock()
	defer s.createMu.Unlock()
	used, err := s.UsageOf(ctx, user.ID)
	if err != nil {
		return store.App{}, store.Deployment{}, err
	}
	if used.Apps >= user.Quota.Apps {
		return store.App{}, store.Deployment{}, overQuota("Kuota aplikasi habis (%d dari %d)", used.Apps, user.Quota.Apps)
	}
	if err := checkMemory(user.Quota, used, app.MemoryMB); err != nil {
		return store.App{}, store.Deployment{}, err
	}
	if err := checkCPU(user.Quota, used, app.CPUMilli); err != nil {
		return store.App{}, store.Deployment{}, err
	}
	if app.IPPort, err = s.allocIPPort(ctx, settings); err != nil {
		return store.App{}, store.Deployment{}, err
	}

	app, err = s.Store.CreateApp(ctx, app)
	if errors.Is(err, store.ErrConflict) {
		return store.App{}, store.Deployment{}, conflict("Nama aplikasi sudah dipakai")
	}
	if err != nil {
		return store.App{}, store.Deployment{}, err
	}
	fail := func(err error) (store.App, store.Deployment, error) {
		if derr := s.Store.DeleteApp(context.WithoutCancel(ctx), app.ID); derr != nil {
			slog.Error("roll back app creation", "app", app.Name, "err", derr)
		}
		return store.App{}, store.Deployment{}, err
	}
	if err := s.Store.ReplaceEnv(ctx, app.ID, sealed); err != nil {
		return fail(err)
	}
	d, err := s.adoptUpload(ctx, user, app, up, snapshot)
	if err != nil {
		return fail(err)
	}
	return app, d, nil
}

// adoptUpload creates a pending deployment for app from the upload, moves the
// artifact into place and removes the upload.
func (s *Service) adoptUpload(ctx context.Context, owner store.User, app store.App, up store.Upload, snapshot string) (store.Deployment, error) {
	rep := up.Report
	d, err := s.Store.CreateDeployment(ctx, store.Deployment{
		AppID: app.ID, FileName: rep.FileName, SHA256: rep.SHA256, Report: rep, EnvSnapshot: snapshot,
		JavaVersion: app.JavaVersion, JVMOpts: app.JVMOpts, InternalPort: app.InternalPort,
		Status: store.DeployPending, CreatedBy: &owner.ID, CreatedAt: s.now().UnixMilli(),
	})
	if err != nil {
		return store.Deployment{}, err
	}
	dst := s.artifactPath(owner.Username, app.Name, d.Number, rep.FileName)
	undo := func(err error) (store.Deployment, error) {
		if derr := s.Store.DeleteDeployment(context.WithoutCancel(ctx), d.ID); derr != nil {
			slog.Error("roll back deployment", "deployment", d.ID, "err", derr)
		}
		return store.Deployment{}, err
	}
	if err := placeArtifact(up.ArtifactPath, dst, rep.Kind); err != nil {
		return undo(err)
	}
	if err := s.Store.UpdateDeployment(ctx, d.ID, store.DeploymentPatch{ArtifactPath: &dst}); err != nil {
		if rerr := os.Rename(dst, up.ArtifactPath); rerr != nil {
			slog.Error("restore artifact", "path", dst, "err", rerr)
		}
		return undo(err)
	}
	d.ArtifactPath = dst
	if err := os.RemoveAll(up.Dir); err != nil {
		slog.Warn("remove upload dir", "dir", up.Dir, "err", err)
	}
	if err := s.Store.DeleteUpload(ctx, up.ID); err != nil {
		slog.Warn("delete upload row", "upload", up.ID, "err", err)
	}
	return d, nil
}

// NewVersion adds a pending deployment built from a new upload of the same
// kind, with the app's current settings and environment.
func (s *Service) NewVersion(ctx context.Context, user store.User, appID int64, uploadID string) (store.Deployment, error) {
	app, owner, err := s.load(ctx, user, appID)
	if err != nil {
		return store.Deployment{}, err
	}
	up, err := s.takeUpload(ctx, owner, uploadID)
	if err != nil {
		return store.Deployment{}, err
	}
	if up.Report.Kind != app.Kind {
		return store.Deployment{}, invalid("Jenis artefak harus sama dengan aplikasi (%s)", app.Kind)
	}
	if app.Kind == artifact.KindJar && app.JavaVersion != nil && *app.JavaVersion < up.Report.MinJava {
		return store.Deployment{}, invalid("Versi ini butuh Java %d atau lebih baru; ubah versi Java aplikasi dulu", up.Report.MinJava)
	}
	snapshot, err := s.snapshotEnv(ctx, app.ID)
	if err != nil {
		return store.Deployment{}, err
	}
	return s.adoptUpload(ctx, owner, app, up, snapshot)
}

// SettingsInput changes app settings; nil fields stay as they are.
type SettingsInput struct {
	JavaVersion  *int    `json:"java_version"`
	JVMOpts      *string `json:"jvm_opts"`
	InternalPort *int    `json:"internal_port"`
	MemoryMB     *int    `json:"memory_mb"`
	CPUMilli     *int    `json:"cpu_milli"`
	IPAccess     *bool   `json:"ip_access"`
	DeployMode   *string `json:"deploy_mode"`
}

// deployModes lists the modes this build can run.
var deployModes = []string{store.DeployModeStopStart}

// UpdateSettings validates and stores the change. redeploy reports that a
// running app only picks the change up with a new deployment. Raising memory
// or CPU needs the extra amount to fit in the remaining quota; lowering is
// always allowed, even when the owner is over quota.
func (s *Service) UpdateSettings(ctx context.Context, user store.User, appID int64, in SettingsInput) (app store.App, redeploy bool, err error) {
	app, owner, err := s.load(ctx, user, appID)
	if err != nil {
		return store.App{}, false, err
	}
	s.createMu.Lock()
	defer s.createMu.Unlock()

	patch := store.AppPatch{IPAccess: in.IPAccess}
	if in.JavaVersion != nil || in.JVMOpts != nil {
		if app.Kind != artifact.KindJar {
			return store.App{}, false, invalid("Versi Java dan opsi JVM hanya untuk JAR")
		}
	}
	if in.JavaVersion != nil {
		settings, err := s.Store.Settings(ctx)
		if err != nil {
			return store.App{}, false, err
		}
		minJava, err := s.minJava(ctx, app)
		if err != nil {
			return store.App{}, false, err
		}
		if err := validateJava(settings.Runtimes, minJava, *in.JavaVersion); err != nil {
			return store.App{}, false, err
		}
		patch.JavaVersion = in.JavaVersion
		redeploy = redeploy || app.JavaVersion == nil || *app.JavaVersion != *in.JavaVersion
	}
	if in.JVMOpts != nil {
		if err := ValidateJVMOpts(*in.JVMOpts); err != nil {
			return store.App{}, false, err
		}
		patch.JVMOpts = in.JVMOpts
		redeploy = redeploy || app.JVMOpts != *in.JVMOpts
	}
	if in.InternalPort != nil {
		if err := validateInternalPort(*in.InternalPort); err != nil {
			return store.App{}, false, err
		}
		patch.InternalPort = in.InternalPort
		redeploy = redeploy || app.InternalPort != *in.InternalPort
	}
	if in.DeployMode != nil {
		if !slices.Contains(deployModes, *in.DeployMode) {
			return store.App{}, false, invalid("Mode deploy tidak dikenal: %s", *in.DeployMode)
		}
		patch.DeployMode = in.DeployMode
	}
	mem, cpu := app.MemoryMB, app.CPUMilli
	if in.MemoryMB != nil {
		mem = *in.MemoryMB
	}
	if in.CPUMilli != nil {
		cpu = *in.CPUMilli
	}
	if in.MemoryMB != nil || in.CPUMilli != nil {
		if err := validateRange(mem, cpu); err != nil {
			return store.App{}, false, err
		}
		used, err := s.UsageOf(ctx, owner.ID)
		if err != nil {
			return store.App{}, false, err
		}
		if mem > app.MemoryMB {
			if err := checkMemory(owner.Quota, used, mem-app.MemoryMB); err != nil {
				return store.App{}, false, err
			}
		}
		if cpu > app.CPUMilli {
			if err := checkCPU(owner.Quota, used, cpu-app.CPUMilli); err != nil {
				return store.App{}, false, err
			}
		}
		patch.MemoryMB, patch.CPUMilli = &mem, &cpu
		redeploy = redeploy || mem != app.MemoryMB || cpu != app.CPUMilli
	}
	app, err = s.Store.UpdateApp(ctx, appID, patch)
	return app, redeploy, err
}

// minJava is the minimum Java version of the app's newest artifact.
func (s *Service) minJava(ctx context.Context, app store.App) (int, error) {
	ds, err := s.Store.Deployments(ctx, app.ID)
	if err != nil || len(ds) == 0 {
		return 0, err
	}
	return ds[0].Report.MinJava, nil
}

// SetEnv replaces the app's environment. A secret entry with an empty value
// keeps the stored value of the secret of the same name.
func (s *Service) SetEnv(ctx context.Context, user store.User, appID int64, env []store.EnvEntry) error {
	if _, _, err := s.load(ctx, user, appID); err != nil {
		return err
	}
	if err := ValidateEnv(env); err != nil {
		return err
	}
	old, err := s.Store.Env(ctx, appID)
	if err != nil {
		return err
	}
	next := slices.Clone(env)
	for i, e := range next {
		if !e.Secret || e.Value != "" {
			continue
		}
		if j := slices.IndexFunc(old, func(o store.EnvEntry) bool { return o.Name == e.Name && o.Secret }); j >= 0 {
			next[i].Value = ""
			if plain, err := s.Box.Open(old[j].Value); err == nil {
				next[i].Value = string(plain)
			} else {
				return fmt.Errorf("decrypt %s: %w", e.Name, err)
			}
		}
	}
	sealed, err := s.sealEnv(next)
	if err != nil {
		return err
	}
	return s.Store.ReplaceEnv(ctx, appID, sealed)
}

// RevealEnv returns the plain value of one stored variable.
func (s *Service) RevealEnv(ctx context.Context, user store.User, appID int64, name string) (string, error) {
	if _, _, err := s.load(ctx, user, appID); err != nil {
		return "", err
	}
	env, err := s.Store.Env(ctx, appID)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(env, func(e store.EnvEntry) bool { return e.Name == name })
	if i < 0 {
		return "", invalid("Variabel %s tidak ditemukan", name)
	}
	plain, err := s.openEnv(env[i : i+1])
	if err != nil {
		return "", err
	}
	return plain[0].Value, nil
}
