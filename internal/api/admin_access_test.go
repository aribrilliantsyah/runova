package api_test

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"runova/internal/store"
)

const ownerOnly = "Hanya pemilik aplikasi yang bisa melakukan ini"

// The admin access matrix (docs/superpowers/notes/admin-view.md): an admin
// sees every app and runs it (restart, stop, start, domains), but changes to
// what the owner deployed, configured or stored stay with the owner. Another
// user gets 404 everywhere, so IDs reveal nothing.
func TestAdminAccessMatrix(t *testing.T) {
	e := newAppEnv(t, 0)
	withCaddy(e)
	e.addUser("andi", "andi-password-1", store.RoleUser)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	a := e.login("andi", "andi-password-1")
	b := e.login("budi", "budi-password-1")
	admin := e.login("admin", adminPassword)

	id, _ := a.createApp("billing", obj{"env": []obj{{"name": "API_KEY", "value": "s3cret-value", "secret": true}}})
	a.waitStatus(id, "running")
	jarID := a.createJarApp("javaapp")
	a.waitStatus(jarID, "running")
	if err := os.WriteFile(filepath.Join(e.dataDir, "data", "andi", "billing", "notes.txt"), []byte("halo"), 0o600); err != nil {
		t.Fatal(err)
	}
	var ds []store.Deployment
	a.do("GET", appPath(id, "/deployments"), nil).into(t, &ds)
	did := strconv.FormatInt(ds[0].ID, 10)

	ct, upload := multipartBody(t, "file", "x.txt", []byte("x"))
	forbidden := []struct {
		method, path string
		body         any
	}{
		{"PATCH", appPath(id, ""), obj{"ip_access": false}},
		{"DELETE", appPath(id, ""), obj{"confirm": "billing"}},
		{"PUT", appPath(id, "/env"), []obj{}},
		{"POST", appPath(id, "/env/API_KEY/reveal"), nil},
		{"POST", appPath(id, "/versions"), obj{"upload_id": "x"}},
		{"POST", appPath(id, "/deploy"), nil},
		{"POST", appPath(id, "/deployments/"+did+"/rollback"), nil},
		{"POST", appPath(id, "/files/folder"), obj{"path": "", "name": "baru"}},
		{"POST", appPath(id, "/files/move"), obj{"from": "notes.txt", "to": "n.txt"}},
		{"DELETE", appPath(id, "/files"), obj{"paths": []string{"notes.txt"}}},
		{"GET", appPath(jarID, "/terminal"), nil},
	}
	for _, tc := range forbidden {
		if r := admin.do(tc.method, tc.path, tc.body); r.status != 403 || r.errMsg(t) != ownerOnly {
			t.Errorf("admin %s %s = %d %s, want 403 %q", tc.method, tc.path, r.status, r.body, ownerOnly)
		}
		if r := b.do(tc.method, tc.path, tc.body); r.status != 404 || r.errMsg(t) != "Aplikasi tidak ditemukan" {
			t.Errorf("other user %s %s = %d %s, want 404", tc.method, tc.path, r.status, r.body)
		}
	}
	if r := admin.raw("POST", appPath(id, "/files?path="), ct, upload, nil); r.status != 403 {
		t.Errorf("admin file upload = %d %s, want 403", r.status, r.body)
	}
	if _, resp, err := admin.dialWS(appPath(jarID, "/terminal")); err == nil || resp == nil || resp.StatusCode != 403 {
		t.Errorf("admin terminal websocket: err=%v resp=%v, want a 403 handshake rejection", err, resp)
	}
	if got := e.engine.CallsWithPrefix("exec "); len(got) != 0 {
		t.Errorf("exec calls = %v, want none", got)
	}

	// Nothing the refused requests touched changed.
	if _, err := os.Stat(filepath.Join(e.dataDir, "data", "andi", "billing", "notes.txt")); err != nil {
		t.Errorf("notes.txt: %v", err)
	}
	if r := a.do("POST", appPath(id, "/env/API_KEY/reveal"), nil); !strings.Contains(string(r.body), "s3cret-value") {
		t.Errorf("owner reveal after admin attempts = %d %s", r.status, r.body)
	}
	if r := admin.do("GET", appPath(id, ""), nil); r.status != 200 || strings.Contains(string(r.body), "s3cret-value") {
		t.Errorf("admin GET = %d %s, want 200 without the secret", r.status, r.body)
	}

	// Viewing.
	for _, p := range []string{"", "/deployments", "/deployments/" + did + "/log", "/domains", "/files?path=", "/files/info?path=notes.txt", "/files/zip?path=notes.txt&check=1"} {
		if r := admin.do("GET", appPath(id, p), nil); r.status != 200 {
			t.Errorf("admin GET %s = %d %s", p, r.status, r.body)
		}
		if r := b.do("GET", appPath(id, p), nil); r.status != 404 {
			t.Errorf("other user GET %s = %d", p, r.status)
		}
	}
	if r := admin.raw("GET", appPath(id, "/files/download?path=notes.txt"), "", nil, nil); r.status != 200 || string(r.body) != "halo" {
		t.Errorf("admin download = %d %q", r.status, r.body)
	}
	conn, _, err := admin.dialWS(appPath(id, "/logs"))
	if err != nil {
		t.Fatalf("admin logs websocket: %v", err)
	}
	conn.CloseNow()
	if _, resp, err := b.dialWS(appPath(id, "/logs")); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Errorf("other user logs websocket: err=%v resp=%v, want 404", err, resp)
	}

	// Running it.
	for _, action := range []string{"stop", "start", "restart"} {
		if r := admin.do("POST", appPath(id, "/"+action), nil); r.status != 202 {
			t.Fatalf("admin %s = %d %s", action, r.status, r.body)
		}
		a.waitStatus(id, map[string]string{"stop": "stopped", "start": "running", "restart": "running"}[action])
	}
	r := admin.do("POST", domainsPath(id), obj{"host": "andi.example.com"})
	if r.status != 201 {
		t.Fatalf("admin adds a domain = %d %s", r.status, r.body)
	}
	var dom store.Domain
	r.into(t, &dom)
	if r := admin.do("POST", domainsPath(id)+"/"+strconv.FormatInt(dom.ID, 10)+"/check", nil); r.status != 200 {
		t.Errorf("admin checks the domain = %d %s", r.status, r.body)
	}
	if r := admin.do("DELETE", domainsPath(id)+"/"+strconv.FormatInt(dom.ID, 10), nil); r.status != 200 {
		t.Errorf("admin removes the domain = %d %s", r.status, r.body)
	}

	// Every admin action names the owner in its target and is searchable by it.
	var res struct {
		Items []store.Activity `json:"items"`
	}
	admin.do("GET", "/api/activity/search?q="+url.QueryEscape("target:andi/billing pelaku:admin"), nil).into(t, &res)
	got := map[string]bool{}
	for _, it := range res.Items {
		got[it.Action] = true
	}
	for _, want := range []string{store.ActStop, store.ActStart, store.ActRestart, store.ActDomainAdd, store.ActDomainRemove, store.ActFileDownload, store.ActLogView} {
		if !got[want] {
			t.Errorf("activity target:andi/billing lacks %s: %+v", want, res.Items)
		}
	}
	// The owner's own actions keep the plain app name.
	if acts := activityActions(t, a, ""); !contains(acts, store.ActAppCreate) {
		t.Errorf("owner activity = %v", acts)
	}

	// The owner is told about every change of state an admin made.
	var notes struct {
		Notifications []store.Notification `json:"notifications"`
	}
	a.do("GET", "/api/notifications", nil).into(t, &notes)
	told := 0
	for _, n := range notes.Notifications {
		if t := strings.ToLower(n.Title); strings.Contains(t, "admin") && strings.Contains(t, "billing") {
			told++
		}
	}
	if told != 5 {
		t.Errorf("owner notifications about admin actions = %d, want 5 (stop, start, restart, domain add, domain remove): %+v", told, notes.Notifications)
	}
}

func TestAdminUserDetail(t *testing.T) {
	e := newAppEnv(t, 0)
	andi := e.addUser("andi", "andi-password-1", store.RoleUser)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	a := e.login("andi", "andi-password-1")
	b := e.login("budi", "budi-password-1")
	admin := e.login("admin", adminPassword)
	id, _ := a.createApp("billing", obj{"memory_mb": 256})
	a.waitStatus(id, "running")
	b.createApp("other", nil)

	path := "/api/users/" + strconv.FormatInt(andi.ID, 10)
	r := admin.do("GET", path, nil)
	if r.status != 200 {
		t.Fatalf("admin GET %s = %d %s", path, r.status, r.body)
	}
	var d struct {
		User    store.UserRow                    `json:"user"`
		Apps    []store.App                      `json:"apps"`
		Summary map[string]struct{ Version int } `json:"summary"`
	}
	r.into(t, &d)
	if d.User.Username != "andi" || d.User.Usage.Apps != 1 || d.User.Usage.MemoryMB != 256 || d.User.LastLoginAt == 0 {
		t.Errorf("user = %+v", d.User)
	}
	if len(d.Apps) != 1 || d.Apps[0].Name != "billing" || d.Summary[strconv.FormatInt(id, 10)].Version != 1 {
		t.Errorf("apps = %+v summary = %+v", d.Apps, d.Summary)
	}
	if strings.Contains(string(r.body), "password") {
		t.Errorf("detail carries a password field: %s", r.body)
	}
	if r := b.do("GET", path, nil); r.status != 403 {
		t.Errorf("non-admin GET %s = %d, want 403", path, r.status)
	}
	if r := admin.do("GET", "/api/users/9999", nil); r.status != 404 {
		t.Errorf("unknown user = %d, want 404", r.status)
	}

	var all struct {
		Owners map[string]string `json:"owners"`
	}
	admin.do("GET", "/api/apps?all=1", nil).into(t, &all)
	if all.Owners[strconv.FormatInt(id, 10)] != "andi" {
		t.Errorf("all=1 owners = %+v", all.Owners)
	}
}
