package store

// StepStatus is the state of one step of a deployment's rollout.
type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
	StepSkipped StepStatus = "skipped"
)

// DeployStep is one step of the rollout timeline. ID names the step in the
// deploy code (image, prepare, stop_old, create, start, health, route,
// retire); the panel turns it into a label.
type DeployStep struct {
	ID         string     `json:"id"`
	Status     StepStatus `json:"status"`
	StartedAt  int64      `json:"started_at,omitempty"`
	FinishedAt int64      `json:"finished_at,omitempty"`
	Detail     string     `json:"detail,omitempty"`
}

// Problem explains why a deployment failed or its container stopped: a
// plain title, a hint of what to do, and the technical detail.
type Problem struct {
	Kind     string `json:"kind"`
	Step     string `json:"step,omitempty"`
	Title    string `json:"title"`
	Hint     string `json:"hint,omitempty"`
	Detail   string `json:"detail,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	At       int64  `json:"at"`
}
