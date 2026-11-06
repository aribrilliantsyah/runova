package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrNotFound is returned (wrapped) when the Engine answers 404.
var ErrNotFound = errors.New("docker: not found")

// Error is a non-2xx answer from the Engine API.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("docker: %d %s", e.Status, e.Message) }

func (e *Error) Is(target error) bool { return target == ErrNotFound && e.Status == 404 }

// Engine is the part of the Docker Engine API Runova uses. *Client implements
// it; other packages fake it in tests.
type Engine interface {
	Ping(ctx context.Context) error
	ImageExists(ctx context.Context, ref string) (bool, error)
	PullImage(ctx context.Context, ref string) error
	EnsureNetwork(ctx context.Context, name, subnet string) error
	RemoveNetwork(ctx context.Context, name string) error
	CreateContainer(ctx context.Context, name string, spec ContainerSpec) (id string, err error)
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string, timeout time.Duration) error
	RemoveContainer(ctx context.Context, id string) error
	InspectContainer(ctx context.Context, id string) (ContainerState, error)
	ListContainers(ctx context.Context, labelFilter map[string]string) ([]ContainerSummary, error)
	Logs(ctx context.Context, id string, tail int, follow bool) (io.ReadCloser, error)
	Events(ctx context.Context, labelFilter map[string]string) (<-chan Event, <-chan error)
	Exec(ctx context.Context, id string, user string, cmd []string) (ExecSession, error)
	Stats(ctx context.Context, id string) (Stats, error)
	// ClearCPULimit removes a CPU cap set on a container by an older Runova.
	ClearCPULimit(ctx context.Context, id string) error
}

// ContainerSpec describes a container. The client always applies the security
// and resource policy (dropped capabilities, no-new-privileges, pids limit,
// restart policy, log rotation); callers cannot turn it off.
type ContainerSpec struct {
	Image        string
	Cmd          []string
	Env          []string
	User         string
	Labels       map[string]string
	WorkingDir   string
	Network      string
	InternalPort int
	HostPort     int
	Binds        []string
	Tmpfs        map[string]string
	MemoryBytes  int64
	ReadOnly     bool
	ExtraHosts   []string
}

type ContainerState struct {
	ID           string
	Status       string
	Running      bool
	ExitCode     int
	OOMKilled    bool
	RestartCount int
	StartedAt    time.Time
	FinishedAt   time.Time
	Labels       map[string]string
}

type ContainerSummary struct {
	ID      string
	Names   []string
	Image   string
	State   string
	Labels  map[string]string
	Created time.Time
}

// LogLine is one demultiplexed line of container output.
type LogLine struct {
	Stream string    `json:"stream"`
	Time   time.Time `json:"time"`
	Text   string    `json:"text"`
}

type Event struct {
	ContainerID string
	Action      string
	Labels      map[string]string
	Time        time.Time
}

type Stats struct {
	MemoryBytes uint64 `json:"memory_bytes"`
	MemoryLimit uint64 `json:"memory_limit"`
	// CPUPercent is percent of one core (200 = two cores busy). CPU is shared,
	// so the ceiling is OnlineCPUs × 100, the whole server.
	CPUPercent float64 `json:"cpu_percent"`
	OnlineCPUs int     `json:"online_cpus"`
}

// ExecSession is an interactive TTY attached to an exec instance.
type ExecSession interface {
	io.ReadWriteCloser
	Resize(ctx context.Context, rows, cols uint) error
}
