package api_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"runova/internal/api"
	"runova/internal/apps"
	"runova/internal/docker"
	"runova/internal/docker/dockertest"
	"runova/internal/secret"
	"runova/internal/store"
)

type appEnv struct {
	*env
	engine *dockertest.Fake
	svc    *apps.Service
}

// newAppEnv is newEnv with the application endpoints on a fake Docker engine.
func newAppEnv(t *testing.T, idle time.Duration) *appEnv {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(filepath.Join(dataDir, "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dataDir, "key")
	if err := secret.GenerateKey(keyPath); err != nil {
		t.Fatal(err)
	}
	box, err := secret.LoadKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)}
	engine := dockertest.New()
	svc := &apps.Service{
		Store: st, Engine: engine, Box: box, DataDir: dataDir, Now: clk.Now,
		PortFree: func(int) bool { return true },
		Chown:    func(string, int) error { return nil },
		Dial: func(string, string, time.Duration) (net.Conn, error) {
			c1, c2 := net.Pipe()
			c2.Close()
			return c1, nil
		},
		HealthTimeout: time.Second, HealthInterval: time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(api.New(api.Config{Store: st, Now: clk.Now, DataDir: dataDir, Apps: svc, Context: ctx, TerminalIdle: idle}))
	t.Cleanup(func() { cancel(); srv.Close(); st.Close() })
	e := &env{t: t, srv: srv, store: st, clock: clk, dataDir: dataDir}
	e.addUser("admin", adminPassword, store.RoleAdmin)
	return &appEnv{env: e, engine: engine, svc: svc}
}

// createApp uploads a static binary and creates the app through the API.
func (c *client) createApp(name string, extra obj) (appID int64, body obj) {
	c.e.t.Helper()
	up := c.upload(name, staticELF())
	if up.status != 201 {
		c.e.t.Fatalf("upload: %d %s", up.status, up.body)
	}
	var u uploadResult
	up.into(c.e.t, &u)
	req := obj{"upload_id": u.ID, "name": name, "memory_mb": 256, "cpu_milli": 250}
	for k, v := range extra {
		req[k] = v
	}
	r := c.do("POST", "/api/apps", req)
	if r.status != 201 {
		c.e.t.Fatalf("create app: %d %s", r.status, r.body)
	}
	r.into(c.e.t, &body)
	return int64(body["app"].(obj)["id"].(float64)), body
}

func (c *client) createJarApp(name string) int64 {
	c.e.t.Helper()
	up := c.upload(name+".jar", jarWithMajor(c.e.t, 61))
	var u uploadResult
	up.into(c.e.t, &u)
	r := c.do("POST", "/api/apps", obj{"upload_id": u.ID, "name": name, "java_version": 17, "memory_mb": 256, "cpu_milli": 250})
	if r.status != 201 {
		c.e.t.Fatalf("create jar app: %d %s", r.status, r.body)
	}
	var body obj
	r.into(c.e.t, &body)
	return int64(body["app"].(obj)["id"].(float64))
}

func (c *client) appStatus(id int64) string {
	c.e.t.Helper()
	r := c.do("GET", "/api/apps/"+strconv.FormatInt(id, 10), nil)
	if r.status != 200 {
		c.e.t.Fatalf("get app: %d %s", r.status, r.body)
	}
	var b obj
	r.into(c.e.t, &b)
	return b["app"].(obj)["status"].(string)
}

func (c *client) waitStatus(id int64, want string) {
	c.e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := c.appStatus(id)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			c.e.t.Fatalf("app %d status = %s, want %s", id, got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func appPath(id int64, rest string) string { return "/api/apps/" + strconv.FormatInt(id, 10) + rest }

// dialWS opens a WebSocket to path using the client's session cookie.
func (c *client) dialWS(path string) (*websocket.Conn, *http.Response, error) {
	c.e.t.Helper()
	u, _ := url.Parse(c.e.srv.URL)
	hdr := http.Header{}
	for _, ck := range c.c.Jar.Cookies(u) {
		hdr.Add("Cookie", ck.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	c.e.t.Cleanup(cancel)
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(c.e.srv.URL, "http")+path, &websocket.DialOptions{HTTPHeader: hdr})
}

func TestCreateAppFlow(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")

	id, body := c.createApp("billing", nil)
	app := body["app"].(obj)
	if app["name"] != "billing" || app["kind"] != "static" || app["internal_port"] != float64(8080) || app["ip_port"] != float64(20000) {
		t.Fatalf("app = %v", app)
	}
	if d := body["deployment"].(obj); d["number"] != float64(1) {
		t.Fatalf("deployment = %v", d)
	}
	c.waitStatus(id, "running")

	r := c.do("GET", "/api/apps", nil)
	var list struct {
		Apps  []store.App `json:"apps"`
		Usage struct {
			Apps, MemoryMB int
			CPUMilli       int
			Quota          store.Quota
		} `json:"usage"`
	}
	r.into(t, &list)
	if len(list.Apps) != 1 || list.Usage.Apps != 1 || list.Usage.Quota.Apps != 3 {
		t.Fatalf("list = %+v", list)
	}
	if got := e.engine.CallsWithPrefix("start "); len(got) != 1 {
		t.Fatalf("starts = %v", got)
	}
	acts := activityActions(t, c, "")
	if !contains(acts, store.ActAppCreate) || !contains(acts, store.ActDeploy) {
		t.Fatalf("activity = %v", acts)
	}

	for _, action := range []string{"stop", "start", "restart"} {
		if r := c.do("POST", appPath(id, "/"+action), nil); r.status != 202 {
			t.Fatalf("%s = %d %s", action, r.status, r.body)
		}
	}
	if got := c.appStatus(id); got != "running" {
		t.Fatalf("status after stop/start/restart = %s", got)
	}
	if r := c.do("POST", appPath(id, "/deploy"), nil); r.status != 202 {
		t.Fatalf("deploy = %d %s", r.status, r.body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := c.do("GET", appPath(id, "/deployments"), nil)
		var ds []store.Deployment
		r.into(t, &ds)
		if len(ds) == 2 && ds[0].Status == store.DeployActive {
			if r := c.do("POST", appPath(id, "/deployments/"+strconv.FormatInt(ds[1].ID, 10)+"/rollback"), nil); r.status != 202 {
				t.Fatalf("rollback = %d %s", r.status, r.body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("redeploy did not finish: %+v", ds)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func TestAppOwnership(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("andi", "andi-password-1", store.RoleUser)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	a := e.login("andi", "andi-password-1")
	b := e.login("budi", "budi-password-1")
	admin := e.login("admin", adminPassword)

	id, _ := a.createApp("billing", nil)
	a.waitStatus(id, "running")
	jarID := a.createJarApp("javaapp")
	a.waitStatus(jarID, "running")

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", appPath(id, ""), nil},
		{"PATCH", appPath(id, ""), obj{"memory_mb": 512}},
		{"DELETE", appPath(id, ""), obj{"confirm": "billing"}},
		{"PUT", appPath(id, "/env"), []obj{}},
		{"POST", appPath(id, "/env/KEY_X/reveal"), nil},
		{"POST", appPath(id, "/versions"), obj{"upload_id": "x"}},
		{"POST", appPath(id, "/deploy"), nil},
		{"POST", appPath(id, "/restart"), nil},
		{"POST", appPath(id, "/stop"), nil},
		{"POST", appPath(id, "/start"), nil},
		{"POST", appPath(id, "/deployments/1/rollback"), nil},
		{"GET", appPath(id, "/deployments"), nil},
		{"GET", appPath(id, "/logs"), nil},
		{"GET", appPath(jarID, "/terminal"), nil},
		{"GET", appPath(9999, ""), nil},
	} {
		r := b.do(tc.method, tc.path, tc.body)
		if r.status != 404 || r.errMsg(t) != "Aplikasi tidak ditemukan" {
			t.Errorf("B %s %s = %d %s, want 404 Aplikasi tidak ditemukan", tc.method, tc.path, r.status, r.body)
		}
	}
	if _, resp, err := b.dialWS(appPath(id, "/logs")); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Errorf("B logs websocket: err=%v resp=%v, want a 404 handshake rejection", err, resp)
	}
	if _, resp, err := b.dialWS(appPath(jarID, "/terminal")); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Errorf("B terminal websocket: err=%v resp=%v, want a 404 handshake rejection", err, resp)
	}
	if got := a.appStatus(id); got != "running" {
		t.Fatalf("A's app was touched: %s", got)
	}

	if r := admin.do("GET", appPath(id, ""), nil); r.status != 200 {
		t.Errorf("admin GET = %d %s", r.status, r.body)
	}
	if r := admin.do("PATCH", appPath(id, ""), obj{"jvm_opts": nil, "ip_access": true}); r.status != 200 {
		t.Errorf("admin PATCH = %d %s", r.status, r.body)
	}
	if r := admin.do("GET", "/api/apps?all=1", nil); r.status != 200 || !strings.Contains(string(r.body), "javaapp") {
		t.Errorf("admin list all = %d %s", r.status, r.body)
	}
	if r := b.do("GET", "/api/apps?all=1", nil); strings.Contains(string(r.body), "billing") {
		t.Errorf("non-admin saw others' apps with all=1: %s", r.body)
	}
	conn, _, err := admin.dialWS(appPath(id, "/logs"))
	if err != nil {
		t.Fatalf("admin logs websocket: %v", err)
	}
	conn.CloseNow()
}

func TestSecretNotLeaked(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id, _ := c.createApp("billing", obj{"env": []obj{
		{"name": "API_KEY", "value": "s3cret-value", "secret": true},
		{"name": "MODE", "value": "prod", "secret": false},
	}})
	c.waitStatus(id, "running")

	r := c.do("GET", appPath(id, ""), nil)
	if strings.Contains(string(r.body), "s3cret-value") {
		t.Fatalf("secret leaked in GET: %s", r.body)
	}
	var got struct{ Env []store.EnvEntry }
	r.into(t, &got)
	if len(got.Env) != 2 || got.Env[0].Name != "API_KEY" || got.Env[0].Value != "" || !got.Env[0].Secret || got.Env[1].Value != "prod" {
		t.Fatalf("env = %+v", got.Env)
	}

	if r := c.do("PUT", appPath(id, "/env"), []obj{{"name": "API_KEY", "value": "", "secret": true}, {"name": "MODE", "value": "dev"}}); r.status != 200 {
		t.Fatalf("PUT env = %d %s", r.status, r.body)
	}
	r = c.do("POST", appPath(id, "/env/API_KEY/reveal"), nil)
	var rev struct{ Name, Value string }
	r.into(t, &rev)
	if r.status != 200 || rev.Value != "s3cret-value" {
		t.Fatalf("reveal = %d %s", r.status, r.body)
	}
	if !contains(activityActions(t, c, ""), store.ActEnvReveal) || !contains(activityActions(t, c, ""), store.ActEnvUpdate) {
		t.Fatalf("activity = %v", activityActions(t, c, ""))
	}
	if r := c.do("POST", appPath(id, "/env/NOPE_X/reveal"), nil); r.status != 400 {
		t.Fatalf("reveal unknown = %d", r.status)
	}
	if r := c.do("PUT", appPath(id, "/env"), []obj{{"name": "lower", "value": "1"}}); r.status != 400 {
		t.Fatalf("invalid env name = %d", r.status)
	}
}

func TestPatchRedeployRequired(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id, _ := c.createApp("billing", nil)
	c.waitStatus(id, "running")

	check := func(patch obj, want bool) {
		t.Helper()
		r := c.do("PATCH", appPath(id, ""), patch)
		var b struct {
			RedeployRequired bool `json:"redeploy_required"`
		}
		r.into(t, &b)
		if r.status != 200 || b.RedeployRequired != want {
			t.Errorf("PATCH %v = %d %s, want redeploy_required=%v", patch, r.status, r.body, want)
		}
	}
	check(obj{"memory_mb": 512}, true)
	check(obj{"ip_access": true}, false)
	check(obj{"internal_port": 9000}, true)
	if r := c.do("PATCH", appPath(id, ""), obj{"memory_mb": 4096}); r.status != 409 || !strings.Contains(r.errMsg(t), "Memori melebihi sisa kuota (sisa 512 MB)") {
		t.Errorf("over quota = %d %s", r.status, r.body)
	}
	if r := c.do("PATCH", appPath(id, ""), obj{"memory_mb": 100}); r.status != 400 {
		t.Errorf("bad step = %d %s", r.status, r.body)
	}
}

func TestDeleteNeedsConfirm(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id, _ := c.createApp("billing", nil)
	c.waitStatus(id, "running")

	if r := c.do("DELETE", appPath(id, ""), obj{"confirm": "x"}); r.status != 400 {
		t.Fatalf("wrong confirm = %d %s", r.status, r.body)
	}
	if r := c.do("DELETE", appPath(id, ""), obj{}); r.status != 400 {
		t.Fatalf("missing confirm = %d %s", r.status, r.body)
	}
	if got := c.appStatus(id); got != "running" {
		t.Fatalf("app changed by refused delete: %s", got)
	}
	if r := c.do("DELETE", appPath(id, ""), obj{"confirm": "billing"}); r.status != 200 {
		t.Fatalf("delete = %d %s", r.status, r.body)
	}
	if r := c.do("GET", appPath(id, ""), nil); r.status != 404 {
		t.Fatalf("GET after delete = %d", r.status)
	}
	if len(e.engine.Containers()) != 0 {
		t.Fatalf("containers left: %+v", e.engine.Containers())
	}
	if !contains(activityActions(t, c, ""), store.ActAppDelete) {
		t.Fatalf("no app.delete in activity")
	}
}

func TestTerminalStaticRejected(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id, _ := c.createApp("billing", nil)
	c.waitStatus(id, "running")

	r := c.do("GET", appPath(id, "/terminal"), nil)
	if r.status != 409 || r.errMsg(t) != "Binary statis tidak punya shell" {
		t.Fatalf("terminal on static = %d %s", r.status, r.body)
	}
	_, resp, err := c.dialWS(appPath(id, "/terminal"))
	if err == nil || resp == nil || resp.StatusCode != 409 {
		t.Fatalf("websocket on static: err=%v resp=%v, want a 409 handshake rejection", err, resp)
	}
	if got := e.engine.CallsWithPrefix("exec "); len(got) != 0 {
		t.Fatalf("exec was attempted: %v", got)
	}
}

func TestTerminalSession(t *testing.T) {
	e := newAppEnv(t, 0)
	user := e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id := c.createJarApp("javaapp")
	c.waitStatus(id, "running")

	conn, _, err := c.dialWS(appPath(id, "/terminal"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("ls\n")); err != nil {
		t.Fatal(err)
	}
	typ, data, err := conn.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(data) != "ls\n" {
		t.Fatalf("echo = %v %q %v", typ, data, err)
	}
	if err := wsjson.Write(ctx, conn, obj{"type": "resize", "rows": 30, "cols": 100}); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("after resize")); err != nil {
		t.Fatal(err)
	}
	if _, data, err = conn.Read(ctx); err != nil || string(data) != "after resize" {
		t.Fatalf("session broke after resize: %q %v", data, err)
	}

	uid := strconv.Itoa(100000 + int(user.ID))
	if got := e.engine.CallsWithPrefix("exec "); len(got) != 1 || !strings.HasSuffix(got[0], " "+uid+":"+uid+" /bin/sh") {
		t.Fatalf("exec calls = %v, want /bin/sh as %s:%s", got, uid, uid)
	}
	if !contains(activityActions(t, c, ""), store.ActTerminal) {
		t.Fatalf("no terminal.open in activity: %v", activityActions(t, c, ""))
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalIdleTimeout(t *testing.T) {
	e := newAppEnv(t, 150*time.Millisecond)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id := c.createJarApp("javaapp")
	c.waitStatus(id, "running")

	conn, _, err := c.dialWS(appPath(id, "/terminal"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("read err = %v, want close status going away", err)
	}
	if d := time.Since(start); d < 100*time.Millisecond || d > 3*time.Second {
		t.Fatalf("closed after %v, want about 150ms", d)
	}
}

func TestLogsWebSocket(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id, _ := c.createApp("billing", nil)
	c.waitStatus(id, "running")

	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	e.engine.Update(e.engine.Containers()[0].ID, func(ct *dockertest.Container) {
		ct.Logs = []docker.LogLine{
			{Stream: "stdout", Time: now, Text: "listening on :8080"},
			{Stream: "stderr", Time: now.Add(time.Second), Text: "warn: slow"},
			{Stream: "stdout", Time: now.Add(2 * time.Second), Text: "third"},
		}
	})

	conn, _, err := c.dialWS(appPath(id, "/logs?tail=2"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got []docker.LogLine
	for range 2 {
		var l docker.LogLine
		if err := wsjson.Read(ctx, conn, &l); err != nil {
			t.Fatalf("read: %v (got %+v)", err, got)
		}
		got = append(got, l)
	}
	if got[0].Text != "warn: slow" || got[0].Stream != "stderr" || got[1].Text != "third" || got[1].Stream != "stdout" || !got[1].Time.Equal(now.Add(2*time.Second)) {
		t.Fatalf("lines = %+v, want the last two lines in order", got)
	}
	conn.Close(websocket.StatusNormalClosure, "")

	if r := c.do("GET", appPath(id, "/logs?tail=abc"), nil); r.status != 400 {
		t.Fatalf("bad tail = %d", r.status)
	}
}

func TestNotificationsEndpoints(t *testing.T) {
	e := newAppEnv(t, 0)
	u := e.addUser("budi", "budi-password-1", store.RoleUser)
	other := e.addUser("andi", "andi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	ctx := context.Background()
	e.store.Notify(ctx, store.Notification{UserID: u.ID, At: 1, Level: store.LevelError, Title: "Deploy x #1 gagal"})
	e.store.Notify(ctx, store.Notification{UserID: other.ID, At: 2, Level: store.LevelInfo, Title: "not mine"})

	var got struct {
		Notifications []store.Notification
		Unread        int
	}
	c.do("GET", "/api/notifications", nil).into(t, &got)
	if got.Unread != 1 || len(got.Notifications) != 1 || got.Notifications[0].Title != "Deploy x #1 gagal" {
		t.Fatalf("notifications = %+v", got)
	}
	if r := c.do("POST", "/api/notifications/read", nil); r.status != 200 {
		t.Fatalf("read = %d", r.status)
	}
	c.do("GET", "/api/notifications", nil).into(t, &got)
	if got.Unread != 0 || len(got.Notifications) != 1 || !got.Notifications[0].Read {
		t.Fatalf("after read = %+v", got)
	}
	if n, _ := e.store.UnreadCount(ctx, other.ID); n != 1 {
		t.Fatalf("other user's notification was marked read")
	}
}

func TestAppEndpointsNeedLogin(t *testing.T) {
	e := newAppEnv(t, 0)
	anon := e.client()
	for _, p := range []string{"/api/apps", "/api/apps/1", "/api/apps/1/logs", "/api/apps/1/terminal", "/api/notifications"} {
		if r := anon.do("GET", p, nil); r.status != 401 {
			t.Errorf("GET %s without login = %d", p, r.status)
		}
	}
	if _, resp, err := anon.dialWS("/api/apps/1/logs"); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Errorf("anonymous websocket: err=%v resp=%v, want 401 before upgrade", err, resp)
	}
}

// Logging out (or losing the account) kills the user's sessions; a log or
// terminal stream opened earlier must end with them.
func TestWebSocketsEndWithSession(t *testing.T) {
	defer api.SetSessionRecheck(20 * time.Millisecond)()
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("budi", "budi-password-1")
	id := c.createJarApp("javaapp")
	c.waitStatus(id, "running")

	for _, path := range []string{"/logs", "/terminal"} {
		conn, _, err := c.dialWS(appPath(id, path))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if r := c.do("POST", "/api/logout", nil); r.status != 200 {
			t.Fatalf("logout: %d", r.status)
		}
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				if got := websocket.CloseStatus(err); got != websocket.StatusPolicyViolation {
					t.Errorf("%s: closed with %v (%v), want policy violation after logout", path, got, err)
				}
				break
			}
		}
		c = e.login("budi", "budi-password-1")
	}
}
