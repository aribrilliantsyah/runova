package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"runova/internal/artifact"
	"runova/internal/docker"
	"runova/internal/store"
)

// Step IDs of a deployment's timeline, in rollout order. The panel names them.
const (
	stepArtifact = "artifact" // the upload was inspected when the deployment was made
	stepImage    = "image"    // check or pull the runtime image
	stepPrepare  = "prepare"  // network, data folder, host port, environment
	stepStopOld  = "stop_old" // stop-start: the old container goes first
	stepCreate   = "create"
	stepStart    = "start"
	stepHealth   = "health" // wait until the app accepts connections
	stepRoute    = "route"  // activate the deployment and point Caddy at it
	stepRetire   = "retire" // zero-downtime: drain and remove the old container
)

const (
	// keptLogLines and keptLogBytes bound the container output stored with a deployment.
	keptLogLines = 2000
	keptLogBytes = 256 << 10
	// problemDetailBytes bounds the technical detail of a stored problem.
	problemDetailBytes = 4 << 10
	// healthSaveEvery throttles how often the waiting step's progress is written.
	healthSaveEvery = 2 * time.Second
)

// tracer records the steps of one deployment as they happen. Writes are best
// effort: a failed write is logged and never fails the deploy.
type tracer struct {
	s     *Service
	id    int64
	steps []store.DeployStep
	saved time.Time
}

// newTracer lays out the steps the rollout will take: with replaceFirst the
// old container is stopped before the new one is created, otherwise (a
// zero-downtime deploy over a serving version) it is retired at the end.
func (s *Service) newTracer(ctx context.Context, d store.Deployment, hasOld, replaceFirst bool) *tracer {
	ids := []string{stepArtifact, stepImage, stepPrepare}
	if hasOld && replaceFirst {
		ids = append(ids, stepStopOld)
	}
	ids = append(ids, stepCreate, stepStart, stepHealth, stepRoute)
	if hasOld && !replaceFirst {
		ids = append(ids, stepRetire)
	}
	t := &tracer{s: s, id: d.ID}
	for _, id := range ids {
		t.steps = append(t.steps, store.DeployStep{ID: id, Status: store.StepPending})
	}
	now := s.now().UnixMilli()
	t.steps[0] = store.DeployStep{ID: stepArtifact, Status: store.StepDone, StartedAt: d.CreatedAt, FinishedAt: now, Detail: d.FileName}
	t.save(ctx)
	return t
}

func (t *tracer) find(id string) *store.DeployStep {
	for i := range t.steps {
		if t.steps[i].ID == id {
			return &t.steps[i]
		}
	}
	return nil
}

func (t *tracer) save(ctx context.Context) {
	t.saved = time.Now()
	if err := t.s.Store.UpdateDeployment(context.WithoutCancel(ctx), t.id, store.DeploymentPatch{Steps: t.steps}); err != nil {
		slog.Error("record deployment steps", "deployment", t.id, "err", err)
	}
}

// begin marks a step running.
func (t *tracer) begin(ctx context.Context, id, detail string) {
	if st := t.find(id); st != nil {
		st.Status, st.StartedAt, st.Detail = store.StepRunning, t.s.now().UnixMilli(), detail
		t.save(ctx)
	}
}

// progress updates the running step's detail, at most every healthSaveEvery.
func (t *tracer) progress(ctx context.Context, id, detail string) {
	st := t.find(id)
	if st == nil {
		return
	}
	st.Detail = detail
	if time.Since(t.saved) >= healthSaveEvery {
		t.save(ctx)
	}
}

// done marks a step finished; an empty detail keeps the one it had.
func (t *tracer) done(ctx context.Context, id, detail string) {
	if st := t.find(id); st != nil {
		st.Status, st.FinishedAt = store.StepDone, t.s.now().UnixMilli()
		if detail != "" {
			st.Detail = detail
		}
		t.save(ctx)
	}
}

func (t *tracer) skip(ctx context.Context, id, detail string) {
	if st := t.find(id); st != nil {
		st.Status, st.Detail = store.StepSkipped, detail
		t.save(ctx)
	}
}

// failRunning marks the running step failed (or the first pending one, when
// the failure came between steps) and the steps after it skipped; it returns
// the failed step's ID.
func (t *tracer) failRunning(detail string) string {
	failed := ""
	now := t.s.now().UnixMilli()
	for i := range t.steps {
		st := &t.steps[i]
		switch {
		case failed == "" && (st.Status == store.StepRunning || st.Status == store.StepPending):
			if st.StartedAt == 0 {
				st.StartedAt = now
			}
			st.Status, st.FinishedAt, st.Detail, failed = store.StepFailed, now, detail, st.ID
		case failed != "" && st.Status == store.StepPending:
			st.Status = store.StepSkipped
		}
	}
	return failed
}

// stepError is a deploy failure in a known step, with what the diagnosis needs.
type stepError struct {
	step  string
	err   error
	state *docker.ContainerState // the container's state when it stopped during start or health
	// waited and port describe a health timeout: how long and which container port.
	waited time.Duration
	port   int
}

func (e *stepError) Error() string { return e.err.Error() }
func (e *stepError) Unwrap() error { return e.err }

func atStep(step string, err error) error {
	if err == nil {
		return nil
	}
	var se *stepError
	if errors.As(err, &se) {
		return err
	}
	return &stepError{step: step, err: err}
}

// Patterns in an app's output that name the port it listens on.
var listenPortRE = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:tomcat|jetty|netty|undertow) started on port\(?s?\)?:?\s*(\d{2,5})`),
	regexp.MustCompile(`(?i)\blistening on (?:port\s*)?(?:[\w.\[\]:]*:)?(\d{2,5})\b`),
	regexp.MustCompile(`(?i)\bserver started on (?:port\s*)?(?:[\w.\[\]:]*:)?(\d{2,5})\b`),
	regexp.MustCompile(`(?i)\bhttp server started on \S*:(\d{2,5})\b`),
	regexp.MustCompile(`(?i)\bstarted server on \S*:(\d{2,5})\b`),
}

var unsupportedClassRE = regexp.MustCompile(`class file version (\d+)\.\d+`)

// portInLog returns the last listening port the output mentions, or 0.
func portInLog(lines []docker.LogLine) int {
	for i := len(lines) - 1; i >= 0; i-- {
		for _, re := range listenPortRE {
			if m := re.FindStringSubmatch(lines[i].Text); m != nil {
				if p, err := strconv.Atoi(m[1]); err == nil && p > 0 && p < 65536 {
					return p
				}
			}
		}
	}
	return 0
}

func logContains(lines []docker.LogLine, subs ...string) string {
	for _, l := range lines {
		for _, s := range subs {
			if strings.Contains(l.Text, s) {
				return l.Text
			}
		}
	}
	return ""
}

// diagnose explains a failed deployment in plain words: what went wrong, what
// to do, and the technical detail. lines is the failed container's last output.
func diagnose(app store.App, d store.Deployment, err error, lines []docker.LogLine, at int64) store.Problem {
	p := store.Problem{Kind: "deploy", Detail: clip(err.Error(), problemDetailBytes), At: at}
	var se *stepError
	if errors.As(err, &se) {
		p.Step = se.step
	}
	var ue *userError
	if errors.As(err, &ue) && p.Step != stepRoute {
		p.Title = ue.msg
		return p
	}
	if se != nil && se.state != nil {
		code := se.state.ExitCode
		p.ExitCode = &code
		return exitProblem(p, app, *se.state, lines)
	}
	switch p.Step {
	case stepImage:
		p.Title = "Image runtime tidak bisa disiapkan"
		p.Hint = "Server perlu menjangkau Docker Hub untuk mengunduh image pertama kali. Periksa koneksi internet server, lalu deploy ulang."
	case stepPrepare:
		p.Title = "Persiapan deploy gagal"
	case stepStopOld:
		p.Title = "Versi lama tidak bisa dihentikan"
	case stepCreate:
		p.Title = "Container tidak bisa dibuat"
		p.Hint = "Biasanya karena Docker kehabisan ruang disk atau nama container bentrok. Coba deploy ulang; bila tetap gagal, periksa server."
	case stepStart:
		p.Title = "Container tidak bisa dijalankan"
	case stepHealth:
		p.Kind = "health_timeout"
		want := app.InternalPort
		if se != nil && se.port != 0 {
			want = se.port
		}
		secs := 0
		if se != nil {
			secs = int(se.waited.Round(time.Second) / time.Second)
		}
		p.Title = fmt.Sprintf("Aplikasi tidak membuka port %d dalam %d detik", want, secs)
		switch seen := portInLog(lines); {
		case seen != 0 && seen != want:
			p.Hint = fmt.Sprintf("Log aplikasi menyebut port %d, sedangkan Runova menunggu port %d. Ubah Port aplikasi di tab Lanjutan menjadi %d, atau atur aplikasi agar memakai variabel PORT.", seen, want, seen)
		case seen == want && seen != 0:
			p.Hint = fmt.Sprintf("Log menyebut port %d, tetapi port itu tidak menerima koneksi. Pastikan aplikasi mendengarkan di 0.0.0.0, bukan 127.0.0.1.", want)
		case app.Kind == artifact.KindJar:
			p.Hint = fmt.Sprintf("Runova mengisi SERVER_PORT dan PORT dengan %d. Bila aplikasi memakai port lain (server.port), samakan Port aplikasi di tab Lanjutan. Aplikasi yang lambat start bisa butuh CPU lebih besar.", want)
		default:
			p.Hint = fmt.Sprintf("Runova mengisi PORT dengan %d. Pastikan aplikasi membaca PORT atau samakan Port aplikasi di tab Lanjutan.", want)
		}
	case stepRoute:
		p.Title = "Caddy menolak alamat aplikasi"
		p.Hint = "Versi yang berjalan sebelumnya tetap melayani. Periksa domain aplikasi, lalu deploy ulang."
	case stepRetire:
		p.Title = "Versi lama tidak bisa dihentikan"
	default:
		p.Title = "Deploy gagal"
	}
	return p
}

// exitProblem explains a container that exited on its own.
func exitProblem(p store.Problem, app store.App, st docker.ContainerState, lines []docker.LogLine) store.Problem {
	if p.Kind == "deploy" {
		p.Kind = "exited"
	}
	switch {
	case st.OOMKilled || st.ExitCode == 137:
		p.Title = "Memori habis (OOMKilled)"
		p.Hint = fmt.Sprintf("Aplikasi memakai lebih dari %d MB yang diberikan. Naikkan memori di tab Lanjutan, atau batasi heap Java dengan -Xmx.", app.MemoryMB)
	case logContains(lines, "UnsupportedClassVersionError") != "":
		p.Title = "Versi Java terlalu lama untuk aplikasi ini"
		if m := unsupportedClassRE.FindStringSubmatch(logContains(lines, "UnsupportedClassVersionError")); m != nil {
			if v, err := strconv.Atoi(m[1]); err == nil && v > 44 {
				p.Title = fmt.Sprintf("Butuh Java %d", v-44)
			}
		}
		p.Hint = "Pilih versi Java yang lebih baru di tab Lanjutan, lalu deploy ulang."
	case logContains(lines, "address already in use", "Address already in use") != "":
		p.Title = "Port sudah dipakai di dalam container"
		p.Hint = "Aplikasi mencoba membuka port yang sama dua kali. Periksa konfigurasi port aplikasi."
	case logContains(lines, "Could not resolve placeholder", "APPLICATION FAILED TO START") != "":
		p.Title = "Aplikasi gagal start karena konfigurasi"
		p.Hint = "Biasanya ada variabel environment yang belum diisi. Lihat baris ERROR di log, lengkapi di tab Environment, lalu deploy ulang."
	default:
		p.Title = fmt.Sprintf("Aplikasi berhenti sendiri (exit code %d)", st.ExitCode)
		p.Hint = "Lihat baris terakhir log di bawah untuk penyebabnya."
	}
	return p
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// readLines returns the last n lines of a container's output.
func (s *Service) readLines(ctx context.Context, containerID string, n int) []docker.LogLine {
	rc, err := s.Engine.Logs(ctx, containerID, n, false)
	if err != nil {
		return nil
	}
	defer rc.Close()
	var lines []docker.LogLine
	if err := docker.ReadLogs(rc, func(l docker.LogLine) error {
		lines = append(lines, l)
		return nil
	}); err != nil {
		slog.Warn("read container logs", "container", containerID, "err", err)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// encodeLog stores lines within keptLogBytes, dropping the oldest first.
func encodeLog(lines []docker.LogLine) string {
	if len(lines) > keptLogLines {
		lines = lines[len(lines)-keptLogLines:]
	}
	for {
		b, err := json.Marshal(lines)
		if err != nil {
			return "[]"
		}
		if len(b) <= keptLogBytes || len(lines) == 0 {
			return string(b)
		}
		lines = lines[len(lines)/4+1:]
	}
}

// keepLog saves the container's last output on the deployment, so it stays
// readable after the container is removed.
func (s *Service) keepLog(ctx context.Context, d store.Deployment, lines []docker.LogLine) {
	if d.ContainerID == "" && lines == nil {
		return
	}
	if lines == nil {
		lines = s.readLines(ctx, d.ContainerID, keptLogLines)
	}
	if len(lines) == 0 {
		return
	}
	log := encodeLog(lines)
	if err := s.Store.UpdateDeployment(context.WithoutCancel(ctx), d.ID, store.DeploymentPatch{ContainerLog: &log}); err != nil {
		slog.Error("keep container log", "deployment", d.ID, "err", err)
	}
}

// DeploymentLog is a deployment's container output: live from Docker while
// the container exists, otherwise the copy saved when it was removed.
type DeploymentLog struct {
	Source string           `json:"source"` // "container", "saved" or "none"
	Lines  []docker.LogLine `json:"lines"`
}

// DeploymentLog returns the last tail lines of the deployment's container output.
func (s *Service) DeploymentLog(ctx context.Context, user store.User, appID, deploymentID int64, tail int) (DeploymentLog, error) {
	app, _, err := s.load(ctx, user, appID)
	if err != nil {
		return DeploymentLog{}, err
	}
	d, err := s.Store.Deployment(ctx, deploymentID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.AppID != app.ID) {
		return DeploymentLog{}, invalid("Deployment tidak ditemukan")
	}
	if err != nil {
		return DeploymentLog{}, err
	}
	if d.ContainerID != "" {
		if _, ierr := s.Engine.InspectContainer(ctx, d.ContainerID); ierr == nil {
			return DeploymentLog{Source: "container", Lines: orEmpty(s.readLines(ctx, d.ContainerID, tail))}, nil
		}
	}
	var lines []docker.LogLine
	if d.ContainerLog != "" {
		if err := json.Unmarshal([]byte(d.ContainerLog), &lines); err != nil {
			return DeploymentLog{}, fmt.Errorf("decode saved log: %w", err)
		}
	}
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	if len(lines) == 0 {
		return DeploymentLog{Source: "none", Lines: []docker.LogLine{}}, nil
	}
	return DeploymentLog{Source: "saved", Lines: lines}, nil
}

func orEmpty(l []docker.LogLine) []docker.LogLine {
	if l == nil {
		return []docker.LogLine{}
	}
	return l
}
