// Package dockertest provides an in-memory docker.Engine for tests of the
// packages that sit on top of the Docker client.
package dockertest

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"runova/internal/docker"
)

// Container is a fake container; tests may read and change it through Fake.Update.
type Container struct {
	ID    string
	Name  string
	Spec  docker.ContainerSpec
	State docker.ContainerState
	Logs  []docker.LogLine
}

// Fake records every call in order (Calls) and can be programmed through its
// exported fields. All methods are safe for concurrent use.
type Fake struct {
	mu         sync.Mutex
	next       int
	calls      []string
	images     map[string]bool
	containers map[string]*Container
	subs       []*subscription

	// PingErr, PullErr, CreateErr, StartErr and EnsureNetworkErr make the matching call fail.
	PingErr, PullErr, CreateErr, StartErr, EnsureNetworkErr, RemoveNetworkErr error
	// OnStart runs (without the lock) after a container started.
	OnStart func(c Container)
	// StatsResult is returned by Stats for running containers.
	StatsResult docker.Stats
	// ExecHandler serves Exec sessions: it gets the container side of the pipe.
	ExecHandler func(user string, cmd []string, conn io.ReadWriteCloser)
}

type subscription struct {
	ch     chan docker.Event
	errc   chan error
	labels map[string]string
	done   chan struct{}
}

func New() *Fake {
	return &Fake{images: map[string]bool{}, containers: map[string]*Container{}}
}

var _ docker.Engine = (*Fake)(nil)

func (f *Fake) record(format string, a ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
}

// Calls returns the recorded calls: "pull <ref>", "network <name>", "create <name>",
// "start <name>", "stop <name>", "remove <name>" (container names, not IDs).
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// CallsWithPrefix returns the recorded calls that start with prefix.
func (f *Fake) CallsWithPrefix(prefix string) []string {
	var out []string
	for _, c := range f.Calls() {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// AddImage marks ref as already present.
func (f *Fake) AddImage(ref string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images[ref] = true
}

// Containers returns a snapshot of all containers, including stopped ones.
func (f *Fake) Containers() []Container {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Container, 0, len(f.containers))
	for _, c := range f.containers {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b Container) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// Container returns the container with the given ID or name.
func (f *Fake) Container(idOrName string) (Container, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(idOrName)
	if c == nil {
		return Container{}, false
	}
	return *c, true
}

// Update changes a container under the lock; it reports whether it exists.
func (f *Fake) Update(idOrName string, fn func(c *Container)) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(idOrName)
	if c == nil {
		return false
	}
	fn(c)
	return true
}

// Add inserts a container as if it had been created earlier.
func (f *Fake) Add(c Container) Container {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c.ID == "" {
		f.next++
		c.ID = fmt.Sprintf("c%d", f.next)
	}
	if c.State.ID == "" {
		c.State.ID = c.ID
	}
	if c.State.Labels == nil {
		c.State.Labels = c.Spec.Labels
	}
	cp := c
	f.containers[c.ID] = &cp
	return cp
}

func (f *Fake) find(idOrName string) *Container {
	if c, ok := f.containers[idOrName]; ok {
		return c
	}
	for _, c := range f.containers {
		if c.Name == idOrName {
			return c
		}
	}
	return nil
}

func notFound(id string) error {
	return &docker.Error{Status: 404, Message: "No such container: " + id}
}

func (f *Fake) Ping(context.Context) error { return f.PingErr }

func (f *Fake) ImageExists(_ context.Context, ref string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.images[ref], nil
}

func (f *Fake) PullImage(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("pull %s", ref)
	if f.PullErr != nil {
		return f.PullErr
	}
	f.images[ref] = true
	return nil
}

func (f *Fake) EnsureNetwork(_ context.Context, name, subnet string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("network %s %s", name, subnet)
	return f.EnsureNetworkErr
}

func (f *Fake) RemoveNetwork(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("network rm %s", name)
	return f.RemoveNetworkErr
}

func (f *Fake) CreateContainer(_ context.Context, name string, spec docker.ContainerSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("create %s", name)
	if f.CreateErr != nil {
		return "", f.CreateErr
	}
	for _, c := range f.containers {
		if c.Name == name {
			return "", &docker.Error{Status: 409, Message: "name already in use"}
		}
	}
	f.next++
	id := fmt.Sprintf("c%d", f.next)
	f.containers[id] = &Container{
		ID: id, Name: name, Spec: spec,
		State: docker.ContainerState{ID: id, Status: "created", Labels: spec.Labels},
	}
	return id, nil
}

func (f *Fake) StartContainer(_ context.Context, id string) error {
	f.mu.Lock()
	c := f.find(id)
	if c == nil {
		f.mu.Unlock()
		return notFound(id)
	}
	f.record("start %s", c.Name)
	if f.StartErr != nil {
		f.mu.Unlock()
		return f.StartErr
	}
	c.State.Running, c.State.Status = true, "running"
	c.State.StartedAt = time.Now()
	snapshot, hook := *c, f.OnStart
	f.mu.Unlock()
	if hook != nil {
		hook(snapshot)
	}
	return nil
}

func (f *Fake) StopContainer(_ context.Context, id string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return notFound(id)
	}
	f.record("stop %s", c.Name)
	c.State.Running, c.State.Status = false, "exited"
	c.State.FinishedAt = time.Now()
	return nil
}

func (f *Fake) RemoveContainer(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return nil
	}
	f.record("remove %s", c.Name)
	delete(f.containers, c.ID)
	return nil
}

func (f *Fake) InspectContainer(_ context.Context, id string) (docker.ContainerState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return docker.ContainerState{}, notFound(id)
	}
	return c.State, nil
}

func matches(labels, filter map[string]string) bool {
	for k, v := range filter {
		got, ok := labels[k]
		if !ok || (v != "" && got != v) {
			return false
		}
	}
	return true
}

func (f *Fake) ListContainers(_ context.Context, filter map[string]string) ([]docker.ContainerSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []docker.ContainerSummary
	for _, c := range f.containers {
		if !matches(c.Spec.Labels, filter) {
			continue
		}
		state := "exited"
		if c.State.Running {
			state = "running"
		}
		out = append(out, docker.ContainerSummary{
			ID: c.ID, Names: []string{"/" + c.Name}, Image: c.Spec.Image, State: state, Labels: c.Spec.Labels,
		})
	}
	slices.SortFunc(out, func(a, b docker.ContainerSummary) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// Logs returns the container's lines as a multiplexed stream; with follow the
// stream stays open until it is closed or ctx ends.
func (f *Fake) Logs(ctx context.Context, id string, tail int, follow bool) (io.ReadCloser, error) {
	f.mu.Lock()
	c := f.find(id)
	if c == nil {
		f.mu.Unlock()
		return nil, notFound(id)
	}
	lines := slices.Clone(c.Logs)
	f.mu.Unlock()
	if tail >= 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	var buf bytes.Buffer
	for _, l := range lines {
		stream := byte(1)
		if l.Stream == "stderr" {
			stream = 2
		}
		text := l.Time.UTC().Format(time.RFC3339Nano) + " " + l.Text + "\n"
		hdr := make([]byte, 8)
		hdr[0] = stream
		binary.BigEndian.PutUint32(hdr[4:], uint32(len(text)))
		buf.Write(hdr)
		buf.WriteString(text)
	}
	if !follow {
		return io.NopCloser(&buf), nil
	}
	pr, pw := io.Pipe()
	go func() {
		pw.Write(buf.Bytes())
		<-ctx.Done()
		pw.CloseWithError(ctx.Err())
	}()
	return pr, nil
}

// Events subscribes to events; Emit delivers them, Disconnect ends the stream.
func (f *Fake) Events(ctx context.Context, filter map[string]string) (<-chan docker.Event, <-chan error) {
	sub := &subscription{ch: make(chan docker.Event, 256), errc: make(chan error, 1), labels: filter, done: make(chan struct{})}
	f.mu.Lock()
	f.subs = append(f.subs, sub)
	f.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
			f.drop(sub, nil)
		case <-sub.done:
		}
	}()
	return sub.ch, sub.errc
}

func (f *Fake) drop(sub *subscription, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.Index(f.subs, sub)
	if i < 0 {
		return
	}
	f.subs = slices.Delete(f.subs, i, i+1)
	close(sub.done)
	if err != nil {
		sub.errc <- err
	}
	close(sub.errc)
	close(sub.ch)
}

// Subscribers reports how many event streams are open.
func (f *Fake) Subscribers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs)
}

// Emit sends an event to every open stream whose filter matches the event labels.
func (f *Fake) Emit(ev docker.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.subs {
		if matches(ev.Labels, s.labels) {
			s.ch <- ev
		}
	}
}

// Disconnect ends every open event stream with err.
func (f *Fake) Disconnect(err error) {
	f.mu.Lock()
	subs := slices.Clone(f.subs)
	f.mu.Unlock()
	for _, s := range subs {
		f.drop(s, err)
	}
}

func (f *Fake) Stats(_ context.Context, id string) (docker.Stats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return docker.Stats{}, notFound(id)
	}
	f.record("stats %s", c.Name)
	return f.StatsResult, nil
}

type pipeSession struct {
	io.ReadWriteCloser
	resizes chan [2]uint
}

func (p *pipeSession) Resize(_ context.Context, rows, cols uint) error {
	select {
	case p.resizes <- [2]uint{rows, cols}:
	default:
	}
	return nil
}

type duplex struct {
	io.Reader
	io.Writer
	closers []io.Closer
}

func (d duplex) Close() error {
	for _, c := range d.closers {
		c.Close()
	}
	return nil
}

// Exec runs ExecHandler on the other end of an in-memory pipe; without a
// handler the session echoes what is written to it.
func (f *Fake) Exec(_ context.Context, id string, user string, cmd []string) (docker.ExecSession, error) {
	f.mu.Lock()
	c := f.find(id)
	if c == nil {
		f.mu.Unlock()
		return nil, notFound(id)
	}
	f.record("exec %s %s %s", c.Name, user, strings.Join(cmd, " "))
	handler := f.ExecHandler
	f.mu.Unlock()

	// clientR/serverW carry output to the caller; serverR/clientW carry input.
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	server := duplex{Reader: serverR, Writer: serverW, closers: []io.Closer{serverR, serverW}}
	if handler == nil {
		handler = func(_ string, _ []string, conn io.ReadWriteCloser) {
			defer conn.Close()
			io.Copy(conn, conn)
		}
	}
	go handler(user, cmd, server)
	return &pipeSession{
		ReadWriteCloser: duplex{Reader: clientR, Writer: clientW, closers: []io.Closer{clientR, clientW}},
		resizes:         make(chan [2]uint, 8),
	}, nil
}
