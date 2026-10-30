package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serve runs h on a temporary Unix socket and returns a client talking to it.
func serve(t *testing.T, h http.Handler) *Client {
	t.Helper()
	// Unix socket paths are short-limited; t.TempDir() can exceed that.
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return New("unix://" + sock)
}

func TestCreateContainerHardening(t *testing.T) {
	var gotPath, gotName string
	var body map[string]any
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotName = r.URL.Path, r.URL.Query().Get("name")
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(201)
		io.WriteString(w, `{"Id":"abc123"}`)
	}))
	id, err := c.CreateContainer(context.Background(), "x", ContainerSpec{
		Image: "img", Cmd: []string{"/app/x"}, Env: []string{"PORT=8080"}, User: "100001:100001",
		Labels: map[string]string{"runova.app": "1"}, Network: "runova-u1", InternalPort: 8080, HostPort: 30001,
		Binds: []string{"/a:/app/x:ro"}, Tmpfs: map[string]string{"/tmp": "size=64m"},
		MemoryBytes: 256 << 20, NanoCPUs: 500_000_000, ReadOnly: true, ExtraHosts: []string{"host.docker.internal:host-gateway"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "abc123" || gotPath != "/v1.44/containers/create" || gotName != "x" {
		t.Fatalf("id=%q path=%q name=%q", id, gotPath, gotName)
	}
	cfg := body
	host := body["HostConfig"].(map[string]any)
	if cfg["User"] != "100001:100001" || cfg["Image"] != "img" {
		t.Errorf("config: %v", cfg)
	}
	if got := jsonOf(host["CapDrop"]); got != `["ALL"]` {
		t.Errorf("CapDrop = %s", got)
	}
	if got := jsonOf(host["SecurityOpt"]); got != `["no-new-privileges"]` {
		t.Errorf("SecurityOpt = %s", got)
	}
	if host["PidsLimit"] != float64(512) || host["ReadonlyRootfs"] != true {
		t.Errorf("pids/readonly: %v %v", host["PidsLimit"], host["ReadonlyRootfs"])
	}
	if host["Memory"] != float64(256<<20) || host["MemorySwap"] != host["Memory"] || host["NanoCpus"] != float64(500_000_000) {
		t.Errorf("resources: mem=%v swap=%v cpu=%v", host["Memory"], host["MemorySwap"], host["NanoCpus"])
	}
	if got := jsonOf(host["RestartPolicy"]); got != `{"MaximumRetryCount":0,"Name":"on-failure"}` {
		t.Errorf("RestartPolicy = %s", got)
	}
	if got := jsonOf(host["LogConfig"]); got != `{"Config":{"max-file":"3","max-size":"10m"},"Type":"json-file"}` {
		t.Errorf("LogConfig = %s", got)
	}
	if got := jsonOf(host["PortBindings"]); got != `{"8080/tcp":[{"HostIp":"127.0.0.1","HostPort":"30001"}]}` {
		t.Errorf("PortBindings = %s", got)
	}
	if host["NetworkMode"] != "runova-u1" {
		t.Errorf("NetworkMode = %v", host["NetworkMode"])
	}
	if got := jsonOf(body["NetworkingConfig"]); got != `{"EndpointsConfig":{"runova-u1":{}}}` {
		t.Errorf("NetworkingConfig = %s", got)
	}
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func frame(stream byte, payload string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(payload)))
	return append(h, payload...)
}

func TestLogsDemux(t *testing.T) {
	var stream []byte
	stream = append(stream, frame(1, "2026-10-30T09:00:00.000000001Z hello\n")...)
	stream = append(stream, frame(2, "2026-10-30T09:00:01.5Z oops\n2026-10-30T09:00:02Z again with  spaces\n")...)
	stream = append(stream, frame(1, "2026-10-30T09:00:03Z last")...)

	var got []LogLine
	err := ReadLogs(bytes.NewReader(stream), func(l LogLine) error { got = append(got, l); return nil })
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ stream, text, ts string }{
		{"stdout", "hello", "2026-10-30T09:00:00.000000001Z"},
		{"stderr", "oops", "2026-10-30T09:00:01.5Z"},
		{"stderr", "again with  spaces", "2026-10-30T09:00:02Z"},
		{"stdout", "last", "2026-10-30T09:00:03Z"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines: %+v", len(got), got)
	}
	for i, w := range want {
		ts, _ := time.Parse(time.RFC3339Nano, w.ts)
		if got[i].Stream != w.stream || got[i].Text != w.text || !got[i].Time.Equal(ts) {
			t.Errorf("line %d = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestLogsDemuxTruncatedFrame(t *testing.T) {
	data := frame(1, "2026-10-30T09:00:00Z hello\n")
	err := ReadLogs(bytes.NewReader(data[:len(data)-3]), func(LogLine) error { return nil })
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want a truncation error", err)
	}
}

func TestLogsRequest(t *testing.T) {
	var q map[string][]string
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		w.Write(frame(1, "2026-10-30T09:00:00Z hi\n"))
	}))
	rc, err := c.Logs(context.Background(), "abc", 200, true)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var got []string
	if err := ReadLogs(rc, func(l LogLine) error { got = append(got, l.Text); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "hi" {
		t.Fatalf("lines = %v", got)
	}
	if q["tail"][0] != "200" || q["follow"][0] != "1" || q["timestamps"][0] != "1" || q["stdout"][0] != "1" || q["stderr"][0] != "1" {
		t.Errorf("query = %v", q)
	}
}

func TestPullImageStreamError(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":"Pulling from library/nope"}`+"\n"+`{"error":"manifest unknown","errorDetail":{"message":"manifest unknown"}}`+"\n")
	}))
	err := c.PullImage(context.Background(), "nope:1")
	if err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("err = %v", err)
	}
}

func TestPullImageRequest(t *testing.T) {
	var got []string
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Query().Get("fromImage")+"|"+r.URL.Query().Get("tag"))
		io.WriteString(w, `{"status":"done"}`+"\n")
	}))
	for _, ref := range []string{"gcr.io/distroless/static-debian12", "localhost:5000/app:v2", "ubuntu:26.04"} {
		if err := c.PullImage(context.Background(), ref); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"gcr.io/distroless/static-debian12|latest", "localhost:5000/app|v2", "ubuntu|26.04"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNotFound(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"message":"No such container: x"}`)
	}))
	ctx := context.Background()
	_, err := c.InspectContainer(ctx, "x")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "No such container") {
		t.Fatalf("inspect err = %v", err)
	}
	if err := c.RemoveContainer(ctx, "x"); err != nil {
		t.Fatalf("remove 404 = %v, want nil", err)
	}
	if err := c.StopContainer(ctx, "x", time.Second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stop err = %v", err)
	}
	if ok, err := c.ImageExists(ctx, "x"); err != nil || ok {
		t.Fatalf("ImageExists = %v, %v", ok, err)
	}
}

func TestErrorMessage(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, `{"message":"boom"}`)
	}))
	err := c.StartContainer(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "boom") || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestStopAndStartAlreadyDone(t *testing.T) {
	var path string
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path + "?" + r.URL.RawQuery
		w.WriteHeader(http.StatusNotModified)
	}))
	if err := c.StopContainer(context.Background(), "x", 10*time.Second); err != nil {
		t.Fatalf("stop 304 = %v", err)
	}
	if path != "/v1.44/containers/x/stop?t=10" {
		t.Errorf("path = %s", path)
	}
	if err := c.StartContainer(context.Background(), "x"); err != nil {
		t.Fatalf("start 304 = %v", err)
	}
}

func TestEnsureNetwork(t *testing.T) {
	var creates []map[string]any
	exists := false
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1.44/networks/runova-u1":
			if exists {
				io.WriteString(w, `{"Name":"runova-u1"}`)
				return
			}
			w.WriteHeader(404)
			io.WriteString(w, `{"message":"network runova-u1 not found"}`)
		case r.Method == "POST" && r.URL.Path == "/v1.44/networks/create":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			creates = append(creates, b)
			exists = true
			w.WriteHeader(201)
			io.WriteString(w, `{"Id":"n1"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	}))
	for range 2 {
		if err := c.EnsureNetwork(context.Background(), "runova-u1", "172.30.1.0/24"); err != nil {
			t.Fatal(err)
		}
	}
	if len(creates) != 1 {
		t.Fatalf("creates = %d, want 1", len(creates))
	}
	b := creates[0]
	if b["Driver"] != "bridge" || jsonOf(b["Labels"]) != `{"runova":"1"}` ||
		jsonOf(b["IPAM"]) != `{"Config":[{"Subnet":"172.30.1.0/24"}]}` {
		t.Errorf("create body = %v", b)
	}
}

func TestInspectContainer(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"Id":"abc","RestartCount":2,"State":{"Status":"exited","Running":false,"ExitCode":137,
			"StartedAt":"2026-10-30T09:00:00Z","FinishedAt":"2026-10-30T09:05:00.5Z"},"Config":{"Labels":{"runova.app":"7"}}}`)
	}))
	st, err := c.InspectContainer(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if st.ID != "abc" || st.Status != "exited" || st.Running || st.ExitCode != 137 || st.RestartCount != 2 ||
		st.Labels["runova.app"] != "7" || st.FinishedAt.Sub(st.StartedAt) != 5*time.Minute+500*time.Millisecond {
		t.Fatalf("state = %+v", st)
	}
}

func TestListContainersFilter(t *testing.T) {
	var q string
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query().Get("filters") + "|" + r.URL.Query().Get("all")
		io.WriteString(w, `[{"Id":"a","Names":["/runova-x"],"Image":"img","State":"exited","Created":1793350000,"Labels":{"runova.app":"3"}}]`)
	}))
	list, err := c.ListContainers(context.Background(), map[string]string{"runova.app": "", "runova.user": "budi"})
	if err != nil {
		t.Fatal(err)
	}
	if q != `{"label":["runova.app","runova.user=budi"]}|1` {
		t.Errorf("query = %s", q)
	}
	if len(list) != 1 || list[0].ID != "a" || list[0].State != "exited" || list[0].Labels["runova.app"] != "3" || list[0].Created.Unix() != 1793350000 {
		t.Fatalf("list = %+v", list)
	}
}

func TestEvents(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("filters"); got != `{"event":["die"],"label":["runova.app"],"type":["container"]}` &&
			got != `{"label":["runova.app"],"type":["container"]}` {
			t.Errorf("filters = %s", got)
		}
		io.WriteString(w, `{"Type":"container","Action":"die","Actor":{"ID":"c1","Attributes":{"runova.app":"5","exitCode":"1"}},"timeNano":1793350000000000000}`+"\n")
		io.WriteString(w, `{"Type":"container","Action":"start","Actor":{"ID":"c1","Attributes":{"runova.app":"5"}},"timeNano":1793350001000000000}`+"\n")
	}))
	ev, errc := c.Events(context.Background(), map[string]string{"runova.app": ""})
	var got []Event
	for e := range ev {
		got = append(got, e)
	}
	if err := <-errc; err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 2 || got[0].Action != "die" || got[0].ContainerID != "c1" || got[0].Labels["runova.app"] != "5" ||
		got[0].Time.UnixNano() != 1793350000000000000 || got[1].Action != "start" {
		t.Fatalf("events = %+v", got)
	}
}

func TestEventsStopOnCancel(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	ev, errc := c.Events(ctx, nil)
	cancel()
	select {
	case _, ok := <-ev:
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("events channel not closed after cancel")
	}
	if err := <-errc; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestStats(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("stream") != "false" {
			t.Errorf("stream = %q", r.URL.Query().Get("stream"))
		}
		io.WriteString(w, `{"memory_stats":{"usage":104857600,"limit":268435456,"stats":{"inactive_file":4194304}},
			"cpu_stats":{"cpu_usage":{"total_usage":300},"system_cpu_usage":2000,"online_cpus":4},
			"precpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1000}}`)
	}))
	st, err := c.Stats(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if st.MemoryBytes != 96<<20 || st.MemoryLimit != 256<<20 {
		t.Errorf("memory = %d / %d", st.MemoryBytes, st.MemoryLimit)
	}
	// (300-100)/(2000-1000) * 4 cpus * 100
	if st.CPUPercent != 80 {
		t.Errorf("cpu = %v, want 80", st.CPUPercent)
	}
}

func TestExecHijack(t *testing.T) {
	var createBody map[string]any
	var resize string
	sock := filepath.Join(t.TempDir(), "d.sock")
	if len(sock) > 100 {
		dir, _ := os.MkdirTemp("", "dk")
		t.Cleanup(func() { os.RemoveAll(dir) })
		sock = filepath.Join(dir, "d.sock")
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				br := bufio.NewReader(conn)
				for {
					req, err := http.ReadRequest(br)
					if err != nil {
						return
					}
					switch req.URL.Path {
					case "/v1.44/containers/abc/exec":
						json.NewDecoder(req.Body).Decode(&createBody)
						io.WriteString(conn, "HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nContent-Length: 11\r\n\r\n{\"Id\":\"e1\"}")
					case "/v1.44/exec/e1/start":
						if req.Header.Get("Upgrade") != "tcp" {
							t.Errorf("Upgrade header = %q", req.Header.Get("Upgrade"))
						}
						io.WriteString(conn, "HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\nready$ ")
						io.Copy(io.Discard, req.Body)
						buf := make([]byte, 64)
						n, _ := br.Read(buf)
						conn.Write(append([]byte("echo:"), buf[:n]...))
						return
					case "/v1.44/exec/e1/resize":
						resize = req.URL.RawQuery
						io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
					}
				}
			}()
		}
	}()
	c := New("unix://" + sock)
	s, err := c.Exec(context.Background(), "abc", "100001:100001", []string{"/bin/sh"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if createBody["User"] != "100001:100001" || createBody["Tty"] != true || createBody["AttachStdin"] != true ||
		jsonOf(createBody["Cmd"]) != `["/bin/sh"]` {
		t.Errorf("exec create body = %v", createBody)
	}
	buf := make([]byte, 7)
	if _, err := io.ReadFull(s, buf); err != nil || string(buf) != "ready$ " {
		t.Fatalf("initial read = %q, %v (bytes after the 101 headers must not be lost)", buf, err)
	}
	if _, err := s.Write([]byte("ls")); err != nil {
		t.Fatal(err)
	}
	buf = make([]byte, 7)
	if _, err := io.ReadFull(s, buf); err != nil || string(buf) != "echo:ls" {
		t.Fatalf("echo = %q, %v", buf, err)
	}
	if err := s.Resize(context.Background(), 24, 80); err != nil {
		t.Fatal(err)
	}
	if resize != "h=24&w=80" {
		t.Errorf("resize query = %q", resize)
	}
}

// A closed terminal must tell the shell to quit: dropping the hijacked
// connection alone leaves /bin/sh running in the container.
func TestExecCloseHangsUpShell(t *testing.T) {
	received := make(chan string, 1)
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.44/containers/abc/exec":
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"Id":"e1"}`)
		case "/v1.44/exec/e1/start":
			conn, brw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			io.WriteString(conn, "HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			data, _ := io.ReadAll(brw)
			received <- string(data)
		}
	}))
	s, err := c.Exec(context.Background(), "abc", "100001:100001", []string{"/bin/sh"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
	select {
	case got := <-received:
		if !strings.HasSuffix(got, "\x03\x04\x04") {
			t.Errorf("shell received %q on close, want it to end with Ctrl-C then Ctrl-D", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never saw the session end")
	}
}

// A program that prints without ever ending a line must not make ReadLogs
// buffer its whole output.
func TestLogsDemuxCapsLineLength(t *testing.T) {
	const total = 10 * maxLine
	var stream []byte
	stream = append(stream, frame(1, "2026-10-30T09:00:00Z ")...)
	for range total / (maxLine / 4) {
		stream = append(stream, frame(1, strings.Repeat("x", maxLine/4))...)
	}
	stream = append(stream, frame(1, "\nnext\n")...)

	var got []LogLine
	err := ReadLogs(bytes.NewReader(stream), func(l LogLine) error { got = append(got, l); return nil })
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, l := range got[:len(got)-1] {
		if len(l.Text) > maxLine {
			t.Fatalf("delivered a %d byte line, limit %d", len(l.Text), maxLine)
		}
		sum += len(l.Text)
	}
	if sum != total || got[len(got)-1].Text != "next" || len(got) < 10 {
		t.Errorf("got %d lines, %d bytes before 'next' (want %d bytes, last %q)", len(got), sum, total, got[len(got)-1].Text)
	}
}
