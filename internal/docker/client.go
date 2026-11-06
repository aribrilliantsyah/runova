// Package docker is a minimal Docker Engine API client over net/http.
package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultHost is the Engine socket used when no host is configured.
	DefaultHost = "unix:///var/run/docker.sock"
	apiVersion  = "v1.44"
	baseURL     = "http://docker/" + apiVersion
)

// Client talks to one Docker Engine. It is safe for concurrent use.
type Client struct {
	hc      *http.Client
	network string
	addr    string
}

var _ Engine = (*Client)(nil)

// New returns a client for host ("unix:///path" or "tcp://host:port"); an
// empty host means DefaultHost. No connection is made until the first call.
func New(host string) *Client {
	if host == "" {
		host = DefaultHost
	}
	c := &Client{network: "unix", addr: "/var/run/docker.sock"}
	switch {
	case strings.HasPrefix(host, "unix://"):
		c.addr = strings.TrimPrefix(host, "unix://")
	case strings.HasPrefix(host, "tcp://"):
		c.network, c.addr = "tcp", strings.TrimPrefix(host, "tcp://")
	default:
		c.addr = host
	}
	c.hc = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return c.dial(ctx)
		},
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
	}}
	return c
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, c.network, c.addr)
}

// do sends one request and returns the response when its status is 2xx or in
// accept. Other statuses become *Error with the Engine's message.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any, accept ...int) (*http.Response, error) {
	u := baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("docker: encode request: %w", err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, fmt.Errorf("docker: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker: %s %s: %w", method, path, err)
	}
	if resp.StatusCode/100 == 2 || slices.Contains(accept, resp.StatusCode) {
		return resp, nil
	}
	defer resp.Body.Close()
	return nil, readError(resp)
}

func readError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	msg := strings.TrimSpace(string(raw))
	var e struct{ Message string }
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		msg = e.Message
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return &Error{Status: resp.StatusCode, Message: msg}
}

// call is do for requests whose response body is ignored or decoded into out.
func (c *Client) call(ctx context.Context, method, path string, q url.Values, body, out any, accept ...int) error {
	resp, err := c.do(ctx, method, path, q, body, accept...)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("docker: decode %s %s: %w", method, path, err)
	}
	return nil
}

func (c *Client) Ping(ctx context.Context) error {
	return c.call(ctx, http.MethodGet, "/_ping", nil, nil, nil)
}

func (c *Client) ImageExists(ctx context.Context, ref string) (bool, error) {
	err := c.call(ctx, http.MethodGet, "/images/"+ref+"/json", nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// splitRef separates an image reference into the repository and the tag (or
// digest) the images/create endpoint expects.
func splitRef(ref string) (repo, tag string) {
	if repo, digest, ok := strings.Cut(ref, "@"); ok {
		return repo, digest
	}
	slash := strings.LastIndexByte(ref, '/')
	if colon := strings.LastIndexByte(ref, ':'); colon > slash {
		return ref[:colon], ref[colon+1:]
	}
	return ref, "latest"
}

// PullImage pulls ref and reads the progress stream to its end; an "error"
// message inside the stream (HTTP status is still 200) becomes the returned error.
func (c *Client) PullImage(ctx context.Context, ref string) error {
	repo, tag := splitRef(ref)
	resp, err := c.do(ctx, http.MethodPost, "/images/create", url.Values{"fromImage": {repo}, "tag": {tag}}, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var m struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&m); err == io.EOF {
			return nil
		} else if err != nil {
			return fmt.Errorf("docker: pull %s: %w", ref, err)
		}
		if m.Error != "" {
			return fmt.Errorf("docker: pull %s: %s", ref, m.Error)
		}
	}
}

func (c *Client) EnsureNetwork(ctx context.Context, name, subnet string) error {
	err := c.call(ctx, http.MethodGet, "/networks/"+url.PathEscape(name), nil, nil, nil)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	body := map[string]any{
		"Name":   name,
		"Driver": "bridge",
		"Labels": map[string]string{"runova": "1"},
		"IPAM":   map[string]any{"Config": []map[string]string{{"Subnet": subnet}}},
	}
	err = c.call(ctx, http.MethodPost, "/networks/create", nil, body, nil)
	if e, ok := errors.AsType[*Error](err); ok && e.Status == http.StatusConflict {
		return nil // created concurrently
	}
	return err
}

// RemoveNetwork deletes the network; one that does not exist is not an error.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	err := c.call(ctx, http.MethodDelete, "/networks/"+url.PathEscape(name), nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

type (
	portBinding struct{ HostIp, HostPort string }
	logConfig   struct {
		Type   string
		Config map[string]string
	}
	restartPolicy struct {
		Name              string
		MaximumRetryCount int
	}
)

// ErrInsecureSpec is returned (wrapped) by CreateContainer for a spec that
// lacks what every Runova container must have: a non-root numeric user, a
// memory limit, and a read-only root filesystem. CPU is shared by all apps
// with the engine's default weight, so there is no CPU limit to require.
var ErrInsecureSpec = errors.New("docker: insecure container spec")

// checkSpec refuses specs that would weaken the isolation policy. The client
// already forces cap-drop, no-new-privileges and the pids limit; these fields
// come from the caller, so a zero value would mean "unlimited" or "root".
func checkSpec(spec ContainerSpec) error {
	switch {
	case !nonRootUser(spec.User):
		return fmt.Errorf("%w: user %q is not a non-root uid[:gid]", ErrInsecureSpec, spec.User)
	case spec.MemoryBytes <= 0:
		return fmt.Errorf("%w: no memory limit", ErrInsecureSpec)
	case !spec.ReadOnly:
		return fmt.Errorf("%w: root filesystem must be read-only", ErrInsecureSpec)
	}
	return nil
}

// nonRootUser reports whether user is "uid" or "uid:gid" with nonzero numeric
// ids. Names ("root", "") are refused: the image decides what they mean.
func nonRootUser(user string) bool {
	uid, gid, hasGID := strings.Cut(user, ":")
	ids := []string{uid}
	if hasGID {
		ids = append(ids, gid)
	}
	for _, id := range ids {
		if n, err := strconv.ParseUint(id, 10, 32); err != nil || n == 0 {
			return false
		}
	}
	return true
}

func (c *Client) CreateContainer(ctx context.Context, name string, spec ContainerSpec) (string, error) {
	if err := checkSpec(spec); err != nil {
		return "", err
	}
	cfg := map[string]any{
		"Image":  spec.Image,
		"Cmd":    spec.Cmd,
		"Env":    spec.Env,
		"User":   spec.User,
		"Labels": spec.Labels,
	}
	if spec.WorkingDir != "" {
		cfg["WorkingDir"] = spec.WorkingDir
	}
	host := map[string]any{
		"Binds":          spec.Binds,
		"Tmpfs":          spec.Tmpfs,
		"ExtraHosts":     spec.ExtraHosts,
		"ReadonlyRootfs": spec.ReadOnly,
		"Memory":         spec.MemoryBytes,
		"MemorySwap":     spec.MemoryBytes,
		"PidsLimit":      512,
		"Init":           true,
		"CapDrop":        []string{"ALL"},
		"SecurityOpt":    []string{"no-new-privileges"},
		"RestartPolicy":  restartPolicy{Name: "on-failure"},
		"LogConfig":      logConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
	}
	if spec.InternalPort > 0 {
		key := strconv.Itoa(spec.InternalPort) + "/tcp"
		cfg["ExposedPorts"] = map[string]struct{}{key: {}}
		if spec.HostPort > 0 {
			host["PortBindings"] = map[string][]portBinding{key: {{HostIp: "127.0.0.1", HostPort: strconv.Itoa(spec.HostPort)}}}
		}
	}
	// The Engine takes the container Config fields at the top level of the body.
	cfg["HostConfig"] = host
	if spec.Network != "" {
		host["NetworkMode"] = spec.Network
		cfg["NetworkingConfig"] = map[string]any{"EndpointsConfig": map[string]struct{}{spec.Network: {}}}
	}

	var out struct{ Id string }
	if err := c.call(ctx, http.MethodPost, "/containers/create", url.Values{"name": {name}}, cfg, &out); err != nil {
		return "", err
	}
	return out.Id, nil
}

// ClearCPULimit lifts the CPU cap of a container created by a Runova that still
// limited CPU per app; the container keeps running. The Engine ignores a zero
// NanoCpus in an update (zero means "unchanged"), so the cap is removed through
// CpuQuota -1, which the Engine stores and reapplies on every restart.
func (c *Client) ClearCPULimit(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+id+"/update", nil, map[string]int{"CpuQuota": -1}, nil)
}

func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+id+"/start", nil, nil, nil, http.StatusNotModified)
}

func (c *Client) StopContainer(ctx context.Context, id string, timeout time.Duration) error {
	q := url.Values{"t": {strconv.Itoa(int(timeout.Round(time.Second) / time.Second))}}
	return c.call(ctx, http.MethodPost, "/containers/"+id+"/stop", q, nil, nil, http.StatusNotModified)
}

func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	err := c.call(ctx, http.MethodDelete, "/containers/"+id, url.Values{"force": {"1"}, "v": {"1"}}, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (c *Client) InspectContainer(ctx context.Context, id string) (ContainerState, error) {
	var out struct {
		Id           string
		RestartCount int
		State        struct {
			Status     string
			Running    bool
			ExitCode   int
			OOMKilled  bool
			StartedAt  time.Time
			FinishedAt time.Time
		}
		Config struct{ Labels map[string]string }
	}
	if err := c.call(ctx, http.MethodGet, "/containers/"+id+"/json", nil, nil, &out); err != nil {
		return ContainerState{}, err
	}
	return ContainerState{
		ID: out.Id, Status: out.State.Status, Running: out.State.Running, ExitCode: out.State.ExitCode, OOMKilled: out.State.OOMKilled,
		RestartCount: out.RestartCount, StartedAt: out.State.StartedAt, FinishedAt: out.State.FinishedAt,
		Labels: out.Config.Labels,
	}, nil
}

// labelFilters renders a label map as the Engine's "label" filter list: "key"
// for an empty value, "key=value" otherwise, sorted for stable requests.
func labelFilters(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v == "" {
			out = append(out, k)
		} else {
			out = append(out, k+"="+v)
		}
	}
	slices.Sort(out)
	return out
}

func filterJSON(f map[string][]string) string {
	raw, _ := json.Marshal(f)
	return string(raw)
}

func (c *Client) ListContainers(ctx context.Context, labelFilter map[string]string) ([]ContainerSummary, error) {
	q := url.Values{"all": {"1"}}
	if len(labelFilter) > 0 {
		q.Set("filters", filterJSON(map[string][]string{"label": labelFilters(labelFilter)}))
	}
	var out []struct {
		Id      string
		Names   []string
		Image   string
		State   string
		Created int64
		Labels  map[string]string
	}
	if err := c.call(ctx, http.MethodGet, "/containers/json", q, nil, &out); err != nil {
		return nil, err
	}
	list := make([]ContainerSummary, len(out))
	for i, o := range out {
		list[i] = ContainerSummary{ID: o.Id, Names: o.Names, Image: o.Image, State: o.State, Labels: o.Labels, Created: time.Unix(o.Created, 0)}
	}
	return list, nil
}

// Logs returns the container's multiplexed stdout/stderr stream with
// timestamps; decode it with ReadLogs. With follow it stays open until closed.
func (c *Client) Logs(ctx context.Context, id string, tail int, follow bool) (io.ReadCloser, error) {
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}, "tail": {"all"}}
	if tail >= 0 {
		q.Set("tail", strconv.Itoa(tail))
	}
	if follow {
		q.Set("follow", "1")
	}
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+id+"/logs", q, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

const (
	maxFrame = 1 << 20
	// maxLine is the longest log line delivered whole; longer ones are cut into pieces of this size.
	maxLine = 64 << 10
)

// ReadLogs demultiplexes a Docker log stream (8-byte frame headers) into
// lines, calling fn for each in order. A line without a trailing newline at
// the end of the stream is delivered too. fn's error stops reading.
func ReadLogs(r io.Reader, fn func(LogLine) error) error {
	br := bufio.NewReader(r)
	partial := map[byte][]byte{}
	emit := func(stream byte, line []byte) error {
		return fn(parseLogLine(stream, line))
	}
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("docker: read log frame header: %w", err)
		}
		size := binary.BigEndian.Uint32(hdr[4:])
		if size > maxFrame {
			return fmt.Errorf("docker: log frame of %d bytes is too large", size)
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(br, payload); err != nil {
			return fmt.Errorf("docker: read log frame: %w", err)
		}
		stream := hdr[0]
		buf := append(partial[stream], payload...)
		for {
			i := bytes.IndexByte(buf, '\n')
			if i < 0 {
				break
			}
			if err := emit(stream, buf[:i]); err != nil {
				return err
			}
			buf = buf[i+1:]
		}
		// A program that never prints a newline would otherwise grow buf
		// without bound (and each frame re-copies it).
		for len(buf) > maxLine {
			if err := emit(stream, buf[:maxLine]); err != nil {
				return err
			}
			buf = buf[maxLine:]
		}
		partial[stream] = append([]byte(nil), buf...)
	}
	for _, stream := range []byte{1, 2} {
		if rest := partial[stream]; len(rest) > 0 {
			if err := emit(stream, rest); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseLogLine(stream byte, line []byte) LogLine {
	l := LogLine{Stream: "stdout"}
	if stream == 2 {
		l.Stream = "stderr"
	}
	text := string(bytes.TrimSuffix(line, []byte("\r")))
	if ts, rest, ok := strings.Cut(text, " "); ok {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			l.Time, text = t, rest
		}
	}
	l.Text = text
	return l
}

// Events streams container events whose labels match labelFilter. The
// subscription is in place when Events returns (the Engine has answered the
// request), so nothing that happens afterwards is missed. The event channel
// is closed when the stream ends; the error channel then yields the reason
// (nothing when the context was canceled or the daemon closed cleanly).
func (c *Client) Events(ctx context.Context, labelFilter map[string]string) (<-chan Event, <-chan error) {
	events := make(chan Event)
	errc := make(chan error, 1)
	filters := map[string][]string{"type": {"container"}}
	if len(labelFilter) > 0 {
		filters["label"] = labelFilters(labelFilter)
	}
	resp, err := c.do(ctx, http.MethodGet, "/events", url.Values{"filters": {filterJSON(filters)}}, nil)
	if err != nil {
		if ctx.Err() == nil {
			errc <- err
		}
		close(errc)
		close(events)
		return events, errc
	}
	go func() {
		defer close(errc)
		defer close(events)
		defer resp.Body.Close()
		dec := json.NewDecoder(resp.Body)
		for {
			var m struct {
				Action string
				Actor  struct {
					ID         string
					Attributes map[string]string
				}
				TimeNano int64 `json:"timeNano"`
			}
			if err := dec.Decode(&m); err != nil {
				if err != io.EOF && ctx.Err() == nil {
					errc <- fmt.Errorf("docker: read events: %w", err)
				}
				return
			}
			ev := Event{ContainerID: m.Actor.ID, Action: m.Action, Labels: m.Actor.Attributes, Time: time.Unix(0, m.TimeNano)}
			select {
			case events <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, errc
}

func (c *Client) Stats(ctx context.Context, id string) (Stats, error) {
	var out struct {
		MemoryStats struct {
			Usage uint64
			Limit uint64
			Stats map[string]uint64
		} `json:"memory_stats"`
		CPUStats    cpuStats `json:"cpu_stats"`
		PreCPUStats cpuStats `json:"precpu_stats"`
	}
	if err := c.call(ctx, http.MethodGet, "/containers/"+id+"/stats", url.Values{"stream": {"false"}}, nil, &out); err != nil {
		return Stats{}, err
	}
	mem := out.MemoryStats.Usage
	// Page cache is reclaimable; report what `docker stats` reports.
	cache := out.MemoryStats.Stats["inactive_file"]
	if cache == 0 {
		cache = out.MemoryStats.Stats["total_inactive_file"]
	}
	if cache < mem {
		mem -= cache
	}
	st := Stats{MemoryBytes: mem, MemoryLimit: out.MemoryStats.Limit}
	cpuDelta := float64(out.CPUStats.Usage.Total) - float64(out.PreCPUStats.Usage.Total)
	sysDelta := float64(out.CPUStats.System) - float64(out.PreCPUStats.System)
	st.OnlineCPUs = int(out.CPUStats.Online)
	if st.OnlineCPUs == 0 {
		st.OnlineCPUs = 1
	}
	if cpuDelta > 0 && sysDelta > 0 {
		st.CPUPercent = cpuDelta * float64(st.OnlineCPUs) * 100 / sysDelta
	}
	return st, nil
}

type cpuStats struct {
	Usage struct {
		Total uint64 `json:"total_usage"`
	} `json:"cpu_usage"`
	System uint64 `json:"system_cpu_usage"`
	Online uint   `json:"online_cpus"`
}

// Exec starts cmd in the container as user on a TTY and returns the hijacked
// connection. Closing the session closes the connection; the exec'd process
// then receives SIGHUP.
func (c *Client) Exec(ctx context.Context, id string, user string, cmd []string) (ExecSession, error) {
	var created struct{ Id string }
	err := c.call(ctx, http.MethodPost, "/containers/"+id+"/exec", nil, map[string]any{
		"AttachStdin": true, "AttachStdout": true, "AttachStderr": true, "Tty": true,
		"User": user, "Cmd": cmd, "Env": []string{"TERM=xterm-256color"},
	}, &created)
	if err != nil {
		return nil, err
	}

	conn, err := c.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker: exec: %w", err)
	}
	body, _ := json.Marshal(map[string]any{"Detach": false, "Tty": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/exec/"+created.Id+"/start", bytes.NewReader(body))
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("docker: exec: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("docker: exec start: %w", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("docker: exec start: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		defer conn.Close()
		defer resp.Body.Close()
		return nil, readError(resp)
	}
	s := &execSession{c: c, id: created.Id, conn: conn, r: br}
	s.stop = context.AfterFunc(ctx, func() { s.shutdown() })
	return s, nil
}

type execSession struct {
	c    *Client
	id   string
	conn net.Conn
	r    *bufio.Reader // may hold bytes the Engine sent right after the 101 headers
	stop func() bool
	once sync.Once
	err  error
}

func (s *execSession) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *execSession) Write(p []byte) (int, error) { return s.conn.Write(p) }

// hangupGap separates Ctrl-C from Ctrl-D: sent in one write the shell can
// still be busy reading the interrupt and swallow the end-of-file (measured
// against a real Engine), leaving it running.
const hangupGap = 100 * time.Millisecond

// Close ends the session. Dropping the hijacked connection alone leaves the
// exec'd shell running inside the container (the Engine keeps the TTY open
// and never sends SIGHUP), so every closed terminal would leak a process.
// The shell gets Ctrl-C (ends the foreground job) and then Ctrl-D (ends the
// shell at its empty prompt) before the connection is dropped.
func (s *execSession) Close() error {
	s.stop()
	return s.shutdown()
}

func (s *execSession) shutdown() error {
	s.once.Do(func() {
		s.conn.SetWriteDeadline(time.Now().Add(time.Second))
		if _, err := s.conn.Write([]byte{0x03}); err == nil {
			time.Sleep(hangupGap)
			s.conn.Write([]byte{0x04, 0x04})
		}
		s.err = s.conn.Close()
	})
	return s.err
}

func (s *execSession) Resize(ctx context.Context, rows, cols uint) error {
	q := url.Values{"h": {strconv.FormatUint(uint64(rows), 10)}, "w": {strconv.FormatUint(uint64(cols), 10)}}
	return s.c.call(ctx, http.MethodPost, "/exec/"+s.id+"/resize", q, nil, nil)
}
