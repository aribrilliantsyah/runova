package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"runova/internal/artifact"
	"runova/internal/folders"
)

type AppStatus string

const (
	AppDeploying AppStatus = "deploying"
	AppRunning   AppStatus = "running"
	AppStopped   AppStatus = "stopped"
	AppFailed    AppStatus = "failed"
)

type DeployStatus string

const (
	DeployPending    DeployStatus = "pending"
	DeployRunning    DeployStatus = "running"
	DeployActive     DeployStatus = "active"
	DeploySuperseded DeployStatus = "superseded"
	DeployFailed     DeployStatus = "failed"
)

const (
	DeployModeStopStart = "stopstart"
	DeployModeZero      = "zero"

	maxNetIndex = 254
)

// ErrNoNetIndex means all 254 per-user network slots are taken.
var ErrNoNetIndex = errors.New("no free network index")

type App struct {
	ID                  int64         `json:"id"`
	UserID              int64         `json:"user_id"`
	Name                string        `json:"name"`
	Kind                artifact.Kind `json:"kind"`
	JavaVersion         *int          `json:"java_version"`
	JVMOpts             string        `json:"jvm_opts"`
	InternalPort        int           `json:"internal_port"`
	IPPort              int           `json:"ip_port"`
	IPAccess            bool          `json:"ip_access"`
	MemoryMB            int           `json:"memory_mb"`
	CPUMilli            int           `json:"cpu_milli"`
	Status              AppStatus     `json:"status"`
	CurrentDeploymentID *int64        `json:"current_deployment_id"`
	DeployMode          string        `json:"deploy_mode"`
	CreatedAt           int64         `json:"created_at"`
	DatabaseID          *int64        `json:"database_id"`
	// Folders are the app's saved folders (paths kept in its workspace).
	Folders []folders.Folder `json:"folders"`
	// DiskMB is the disk allocated to the app's workspace; 0 means not limited
	// (apps created before disk quotas until a size is chosen).
	DiskMB int `json:"disk_mb"`
	// DiskUsedBytes is what the last scan of the workspace found, at DiskScannedAt (Unix ms).
	DiskUsedBytes int64     `json:"disk_used_bytes"`
	DiskScannedAt int64     `json:"disk_scanned_at"`
	DiskLevel     DiskLevel `json:"disk_level"`
}

// DiskLevel is how full an app's workspace was at the last scan.
type DiskLevel string

const (
	DiskOK   DiskLevel = ""
	DiskWarn DiskLevel = "warn" // 80% of DiskMB or more
	DiskFull DiskLevel = "full" // DiskMB or more
)

// EnvEntry is one environment variable. Secret values are sealed in the
// store; the API blanks them before sending.
type EnvEntry struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

type Deployment struct {
	ID           int64           `json:"id"`
	AppID        int64           `json:"app_id"`
	Number       int             `json:"number"`
	ArtifactPath string          `json:"-"`
	FileName     string          `json:"file_name"`
	SHA256       string          `json:"sha256"`
	Report       artifact.Report `json:"report"`
	EnvSnapshot  string          `json:"-"` // sealed JSON of []EnvEntry
	JavaVersion  *int            `json:"java_version"`
	JVMOpts      string          `json:"jvm_opts"`
	InternalPort int             `json:"internal_port"`
	HostPort     int             `json:"host_port"`
	ContainerID  string          `json:"container_id"`
	Status       DeployStatus    `json:"status"`
	FailureLog   string          `json:"failure_log"`
	// Steps is the rollout timeline; Problem the diagnosis of a failed deploy
	// or a crash; ContainerLog the container's last output, kept after the
	// container is gone (served by its own endpoint, not in listings).
	Steps        []DeployStep `json:"steps"`
	Problem      *Problem     `json:"problem"`
	ContainerLog string       `json:"-"`
	CreatedBy    *int64       `json:"created_by"`
	CreatedAt    int64        `json:"created_at"`
	FinishedAt   int64        `json:"finished_at"`
}

const appCols = `id, user_id, name, kind, java_version, jvm_opts, internal_port, ip_port, ip_access, memory_mb, cpu_milli, status, current_deployment_id, deploy_mode, created_at, database_id, folders, disk_mb, disk_used_bytes, disk_scanned_at, disk_level`

func scanApp(r scanner) (App, error) {
	var a App
	var rawFolders string
	err := r.Scan(&a.ID, &a.UserID, &a.Name, &a.Kind, &a.JavaVersion, &a.JVMOpts, &a.InternalPort, &a.IPPort, &a.IPAccess,
		&a.MemoryMB, &a.CPUMilli, &a.Status, &a.CurrentDeploymentID, &a.DeployMode, &a.CreatedAt, &a.DatabaseID,
		&rawFolders, &a.DiskMB, &a.DiskUsedBytes, &a.DiskScannedAt, &a.DiskLevel)
	if err != nil {
		return a, err
	}
	a.Folders = []folders.Folder{}
	if err := json.Unmarshal([]byte(rawFolders), &a.Folders); err != nil {
		return a, fmt.Errorf("decode folders of app %d: %w", a.ID, err)
	}
	return a, nil
}

func encodeFolders(list []folders.Folder) string {
	if list == nil {
		list = []folders.Folder{}
	}
	raw, _ := json.Marshal(list) // plain strings: cannot fail
	return string(raw)
}

// CreateApp inserts a; ErrConflict when the name is taken for that user or the IP port is in use.
func (s *Store) CreateApp(ctx context.Context, a App) (App, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO apps(user_id, name, kind, java_version, jvm_opts, internal_port, ip_port, ip_access, memory_mb, cpu_milli, status, deploy_mode, created_at, database_id, folders, disk_mb)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.UserID, a.Name, a.Kind, a.JavaVersion, a.JVMOpts, a.InternalPort, a.IPPort, a.IPAccess, a.MemoryMB, a.CPUMilli, a.Status, a.DeployMode, a.CreatedAt, a.DatabaseID, encodeFolders(a.Folders), a.DiskMB)
	if isUnique(err) {
		return App{}, ErrConflict
	}
	if err != nil {
		return App{}, fmt.Errorf("create app: %w", err)
	}
	a.ID, err = res.LastInsertId()
	if err != nil {
		return App{}, fmt.Errorf("create app: %w", err)
	}
	if a.Folders == nil {
		a.Folders = []folders.Folder{}
	}
	return a, nil
}

func (s *Store) App(ctx context.Context, id int64) (App, error) {
	a, err := scanApp(s.db.QueryRowContext(ctx, `SELECT `+appCols+` FROM apps WHERE id = ?`, id))
	return a, notFound(err, "app")
}

func (s *Store) queryApps(ctx context.Context, query string, args ...any) ([]App, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, fmt.Errorf("list apps: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}
	return out, nil
}

func (s *Store) AppsByUser(ctx context.Context, userID int64) ([]App, error) {
	return s.queryApps(ctx, `SELECT `+appCols+` FROM apps WHERE user_id = ? ORDER BY name`, userID)
}

func (s *Store) AllApps(ctx context.Context) ([]App, error) {
	return s.queryApps(ctx, `SELECT `+appCols+` FROM apps ORDER BY user_id, name`)
}

// AppPatch lists the changeable app fields; nil means unchanged.
type AppPatch struct {
	JavaVersion  *int
	JVMOpts      *string
	InternalPort *int
	MemoryMB     *int
	CPUMilli     *int
	IPAccess     *bool
	DeployMode   *string
	DiskMB       *int
	Folders      *[]folders.Folder
}

func (s *Store) UpdateApp(ctx context.Context, id int64, p AppPatch) (App, error) {
	var out App
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		a, err := scanApp(tx.QueryRowContext(ctx, `SELECT `+appCols+` FROM apps WHERE id = ?`, id))
		if err := notFound(err, "app"); err != nil {
			return err
		}
		if p.JavaVersion != nil {
			a.JavaVersion = p.JavaVersion
		}
		if p.JVMOpts != nil {
			a.JVMOpts = *p.JVMOpts
		}
		if p.InternalPort != nil {
			a.InternalPort = *p.InternalPort
		}
		if p.MemoryMB != nil {
			a.MemoryMB = *p.MemoryMB
		}
		if p.CPUMilli != nil {
			a.CPUMilli = *p.CPUMilli
		}
		if p.IPAccess != nil {
			a.IPAccess = *p.IPAccess
		}
		if p.DeployMode != nil {
			a.DeployMode = *p.DeployMode
		}
		if p.DiskMB != nil {
			a.DiskMB = *p.DiskMB
		}
		if p.Folders != nil {
			a.Folders = *p.Folders
			if a.Folders == nil {
				a.Folders = []folders.Folder{}
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE apps SET java_version=?, jvm_opts=?, internal_port=?, memory_mb=?, cpu_milli=?, ip_access=?, deploy_mode=?, disk_mb=?, folders=? WHERE id=?`,
			a.JavaVersion, a.JVMOpts, a.InternalPort, a.MemoryMB, a.CPUMilli, a.IPAccess, a.DeployMode, a.DiskMB, encodeFolders(a.Folders), id); err != nil {
			return fmt.Errorf("update app: %w", err)
		}
		out = a
		return nil
	})
	return out, err
}

func (s *Store) DeleteApp(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM apps WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete app: %w", err)
	}
	return nil
}

func (s *Store) SetAppStatus(ctx context.Context, id int64, status AppStatus) error {
	res, err := s.db.ExecContext(ctx, `UPDATE apps SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("set app status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAppDiskUsage records the result of a workspace scan.
func (s *Store) SetAppDiskUsage(ctx context.Context, id, usedBytes, at int64, level DiskLevel) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE apps SET disk_used_bytes = ?, disk_scanned_at = ?, disk_level = ? WHERE id = ?`, usedBytes, at, level, id); err != nil {
		return fmt.Errorf("set app disk usage: %w", err)
	}
	return nil
}

// ClearCurrentDeployment detaches the app from its active deployment and
// records the new status (used when the container is gone).
func (s *Store) ClearCurrentDeployment(ctx context.Context, id int64, status AppStatus) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE apps SET current_deployment_id = NULL, status = ? WHERE id = ?`, status, id); err != nil {
		return fmt.Errorf("clear current deployment: %w", err)
	}
	return nil
}

// ReplaceEnv atomically replaces the app's whole environment.
func (s *Store) ReplaceEnv(ctx context.Context, appID int64, env []EnvEntry) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_env WHERE app_id = ?`, appID); err != nil {
			return fmt.Errorf("replace env: %w", err)
		}
		for _, e := range env {
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_env(app_id, name, value, secret) VALUES(?,?,?,?)`,
				appID, e.Name, e.Value, e.Secret); err != nil {
				return fmt.Errorf("replace env: %w", err)
			}
		}
		return nil
	})
}

// Env returns the app's environment sorted by name.
func (s *Store) Env(ctx context.Context, appID int64) ([]EnvEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, value, secret FROM app_env WHERE app_id = ? ORDER BY name`, appID)
	if err != nil {
		return nil, fmt.Errorf("read env: %w", err)
	}
	defer rows.Close()
	out := []EnvEntry{}
	for rows.Next() {
		var e EnvEntry
		if err := rows.Scan(&e.Name, &e.Value, &e.Secret); err != nil {
			return nil, fmt.Errorf("read env: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read env: %w", err)
	}
	return out, nil
}

const deploymentCols = `id, app_id, number, artifact_path, file_name, sha256, report, env_snapshot, java_version, jvm_opts, internal_port, host_port, container_id, status, failure_log, created_by, created_at, finished_at, steps, problem, container_log`

func scanDeployment(r scanner) (Deployment, error) {
	var (
		d                 Deployment
		raw, steps, probl string
	)
	err := r.Scan(&d.ID, &d.AppID, &d.Number, &d.ArtifactPath, &d.FileName, &d.SHA256, &raw, &d.EnvSnapshot, &d.JavaVersion, &d.JVMOpts,
		&d.InternalPort, &d.HostPort, &d.ContainerID, &d.Status, &d.FailureLog, &d.CreatedBy, &d.CreatedAt, &d.FinishedAt,
		&steps, &probl, &d.ContainerLog)
	if err != nil {
		return Deployment{}, err
	}
	if err := json.Unmarshal([]byte(raw), &d.Report); err != nil {
		return Deployment{}, fmt.Errorf("decode deployment report: %w", err)
	}
	if err := json.Unmarshal([]byte(steps), &d.Steps); err != nil {
		return Deployment{}, fmt.Errorf("decode deployment steps: %w", err)
	}
	if d.Steps == nil {
		d.Steps = []DeployStep{}
	}
	if probl != "" {
		d.Problem = new(Problem)
		if err := json.Unmarshal([]byte(probl), d.Problem); err != nil {
			return Deployment{}, fmt.Errorf("decode deployment problem: %w", err)
		}
	}
	return d, nil
}

// CreateDeployment inserts d with the app's next deployment number.
func (s *Store) CreateDeployment(ctx context.Context, d Deployment) (Deployment, error) {
	raw, err := json.Marshal(d.Report)
	if err != nil {
		return Deployment{}, fmt.Errorf("encode deployment report: %w", err)
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(number), 0) + 1 FROM deployments WHERE app_id = ?`, d.AppID).Scan(&d.Number); err != nil {
			return fmt.Errorf("next deployment number: %w", err)
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO deployments(app_id, number, artifact_path, file_name, sha256, report, env_snapshot, java_version, jvm_opts, internal_port, host_port, container_id, status, failure_log, created_by, created_at, finished_at)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.AppID, d.Number, d.ArtifactPath, d.FileName, d.SHA256, string(raw), d.EnvSnapshot, d.JavaVersion, d.JVMOpts, d.InternalPort,
			d.HostPort, d.ContainerID, d.Status, d.FailureLog, d.CreatedBy, d.CreatedAt, d.FinishedAt)
		if err != nil {
			return fmt.Errorf("create deployment: %w", err)
		}
		d.ID, err = res.LastInsertId()
		return err
	})
	return d, err
}

func (s *Store) Deployment(ctx context.Context, id int64) (Deployment, error) {
	d, err := scanDeployment(s.db.QueryRowContext(ctx, `SELECT `+deploymentCols+` FROM deployments WHERE id = ?`, id))
	return d, notFound(err, "deployment")
}

// Deployments lists the app's deployments, newest first.
func (s *Store) Deployments(ctx context.Context, appID int64) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deploymentCols+` FROM deployments WHERE app_id = ? ORDER BY number DESC`, appID)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, fmt.Errorf("list deployments: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	return out, nil
}

// DeploymentPatch lists the changeable deployment fields; nil means unchanged.
type DeploymentPatch struct {
	Status       *DeployStatus
	HostPort     *int
	ContainerID  *string
	FailureLog   *string
	FinishedAt   *int64
	ArtifactPath *string
	Steps        []DeployStep // nil means unchanged
	// Problem replaces the diagnosis; a Problem with an empty Kind clears it.
	Problem      *Problem
	ContainerLog *string
}

func (s *Store) UpdateDeployment(ctx context.Context, id int64, p DeploymentPatch) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		d, err := scanDeployment(tx.QueryRowContext(ctx, `SELECT `+deploymentCols+` FROM deployments WHERE id = ?`, id))
		if err := notFound(err, "deployment"); err != nil {
			return err
		}
		if p.Status != nil {
			d.Status = *p.Status
		}
		if p.HostPort != nil {
			d.HostPort = *p.HostPort
		}
		if p.ContainerID != nil {
			d.ContainerID = *p.ContainerID
		}
		if p.FailureLog != nil {
			d.FailureLog = *p.FailureLog
		}
		if p.FinishedAt != nil {
			d.FinishedAt = *p.FinishedAt
		}
		if p.ArtifactPath != nil {
			d.ArtifactPath = *p.ArtifactPath
		}
		if p.Steps != nil {
			d.Steps = p.Steps
		}
		if p.Problem != nil {
			d.Problem = p.Problem
			if p.Problem.Kind == "" {
				d.Problem = nil
			}
		}
		if p.ContainerLog != nil {
			d.ContainerLog = *p.ContainerLog
		}
		steps, err := json.Marshal(d.Steps)
		if err != nil {
			return fmt.Errorf("encode deployment steps: %w", err)
		}
		problem := ""
		if d.Problem != nil {
			b, err := json.Marshal(d.Problem)
			if err != nil {
				return fmt.Errorf("encode deployment problem: %w", err)
			}
			problem = string(b)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE deployments SET status=?, host_port=?, container_id=?, failure_log=?, finished_at=?, artifact_path=?, steps=?, problem=?, container_log=? WHERE id=?`,
			d.Status, d.HostPort, d.ContainerID, d.FailureLog, d.FinishedAt, d.ArtifactPath, string(steps), problem, d.ContainerLog, id); err != nil {
			return fmt.Errorf("update deployment: %w", err)
		}
		return nil
	})
}

// DeleteDeployment removes one deployment row.
func (s *Store) DeleteDeployment(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM deployments WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete deployment: %w", err)
	}
	return nil
}

// ActivateDeployment marks the deployment active, every other active one of
// the app superseded, and points the app at it with status running.
func (s *Store) ActivateDeployment(ctx context.Context, appID, deploymentID int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE deployments SET status = ? WHERE app_id = ? AND status = ? AND id != ?`,
			DeploySuperseded, appID, DeployActive, deploymentID); err != nil {
			return fmt.Errorf("supersede deployments: %w", err)
		}
		res, err := tx.ExecContext(ctx, `UPDATE deployments SET status = ? WHERE id = ? AND app_id = ?`, DeployActive, deploymentID, appID)
		if err != nil {
			return fmt.Errorf("activate deployment: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `UPDATE apps SET current_deployment_id = ?, status = ? WHERE id = ?`, deploymentID, AppRunning, appID); err != nil {
			return fmt.Errorf("point app at deployment: %w", err)
		}
		return nil
	})
}

// PruneDeployments deletes old deployments so at most keep remain. The active
// deployment is never deleted and counts toward keep; the rest of the slots go
// to the newest. The deleted rows are returned so the caller can remove files.
func (s *Store) PruneDeployments(ctx context.Context, appID int64, keep int) ([]Deployment, error) {
	var removed []Deployment
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+deploymentCols+` FROM deployments WHERE app_id = ? ORDER BY number DESC`, appID)
		if err != nil {
			return fmt.Errorf("prune deployments: %w", err)
		}
		var all []Deployment
		for rows.Next() {
			d, err := scanDeployment(rows)
			if err != nil {
				rows.Close()
				return fmt.Errorf("prune deployments: %w", err)
			}
			all = append(all, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("prune deployments: %w", err)
		}
		slots := keep
		for _, d := range all {
			if d.Status == DeployActive {
				slots--
			}
		}
		for _, d := range all {
			switch {
			case d.Status == DeployActive:
			case slots > 0:
				slots--
			default:
				removed = append(removed, d)
			}
		}
		for _, d := range removed {
			if _, err := tx.ExecContext(ctx, `DELETE FROM deployments WHERE id = ?`, d.ID); err != nil {
				return fmt.Errorf("prune deployments: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

func (s *Store) usedPorts(ctx context.Context, query string, args ...any) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("used ports: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("used ports: %w", err)
		}
		out[p] = true
	}
	return out, rows.Err()
}

// UsedHostPorts returns the host ports of deployments that are pending, running or active.
func (s *Store) UsedHostPorts(ctx context.Context) (map[int]bool, error) {
	return s.usedPorts(ctx, `SELECT host_port FROM deployments WHERE host_port > 0 AND status IN (?,?,?)`,
		DeployPending, DeployRunning, DeployActive)
}

func (s *Store) UsedIPPorts(ctx context.Context) (map[int]bool, error) {
	return s.usedPorts(ctx, `SELECT ip_port FROM apps`)
}

// AppUsage is what a user's apps have allocated, and the disk they used at the last scans.
type AppUsage struct {
	Apps, MemoryMB, CPUMilli, DiskMB int
	DiskUsedBytes                    int64
}

// UserUsage sums the user's apps and their allocated memory, CPU and disk.
func (s *Store) UserUsage(ctx context.Context, userID int64) (AppUsage, error) {
	var u AppUsage
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*), coalesce(sum(memory_mb), 0), coalesce(sum(cpu_milli), 0), coalesce(sum(disk_mb), 0), coalesce(sum(disk_used_bytes), 0) FROM apps WHERE user_id = ?`, userID).
		Scan(&u.Apps, &u.MemoryMB, &u.CPUMilli, &u.DiskMB, &u.DiskUsedBytes)
	if err != nil {
		err = fmt.Errorf("user usage: %w", err)
	}
	return u, err
}

// NetIndex returns the user's Docker network slot (1–254), allocating the
// lowest free one on first use; ErrNoNetIndex when all are taken.
func (s *Store) NetIndex(ctx context.Context, userID int64) (int, error) {
	var idx int
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var cur sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT net_index FROM users WHERE id = ?`, userID).Scan(&cur)
		if err := notFound(err, "user"); err != nil {
			return err
		}
		if cur.Valid {
			idx = int(cur.Int64)
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT net_index FROM users WHERE net_index IS NOT NULL`)
		if err != nil {
			return fmt.Errorf("net indices: %w", err)
		}
		var taken []int
		for rows.Next() {
			var n int
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return fmt.Errorf("net indices: %w", err)
			}
			taken = append(taken, n)
		}
		rows.Close()
		for n := 1; n <= maxNetIndex; n++ {
			if !slices.Contains(taken, n) {
				idx = n
				break
			}
		}
		if idx == 0 {
			return ErrNoNetIndex
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET net_index = ? WHERE id = ?`, idx, userID); err != nil {
			return fmt.Errorf("allocate net index: %w", err)
		}
		return nil
	})
	return idx, err
}
