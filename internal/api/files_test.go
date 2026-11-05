package api_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"runova/internal/store"
)

func TestFilesAPI(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	e.addUser("andi", "andi-password-1", store.RoleUser)
	budi := e.login("budi", "budi-password-1")
	andi := e.login("andi", "andi-password-1")
	id, _ := budi.createApp("starboom", obj{"folders": []obj{{"path": "/app/storage"}}, "disk_mb": 64})
	budi.waitStatus(id, "running")

	data := filepath.Join(e.dataDir, "data", "budi", "starboom")
	if err := os.WriteFile(filepath.Join(data, "notes.txt"), []byte("halo"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := budi.do("GET", appPath(id, "/files?path="), nil)
	if r.status != 200 {
		t.Fatalf("list: %d %s", r.status, r.body)
	}
	var list struct {
		Root    string `json:"root"`
		Path    string `json:"path"`
		Entries []struct {
			Name  string `json:"name"`
			Type  string `json:"type"`
			Saved bool   `json:"saved"`
		} `json:"entries"`
	}
	r.into(t, &list)
	if list.Root != "starboom" || len(list.Entries) != 2 || list.Entries[0].Name != "storage" || !list.Entries[0].Saved {
		t.Fatalf("list = %+v", list)
	}

	ct, body := multipartBody(t, "file", "foto.png", []byte("PNGDATA"))
	if r := budi.raw("POST", appPath(id, "/files?path=storage"), ct, body, nil); r.status != 201 {
		t.Fatalf("upload: %d %s", r.status, r.body)
	}
	if b, err := os.ReadFile(filepath.Join(e.dataDir, "mounts", "budi", "starboom", "storage", "foto.png")); err != nil || string(b) != "PNGDATA" {
		t.Fatalf("uploaded = %q, %v", b, err)
	}
	r = budi.raw("GET", appPath(id, "/files/download?path=storage/foto.png"), "", nil, nil)
	if r.status != 200 || string(r.body) != "PNGDATA" || !strings.Contains(r.header.Get("Content-Disposition"), `attachment; filename=foto.png`) {
		t.Fatalf("download: %d %q %v", r.status, r.body, r.header)
	}
	if r.header.Get("Content-Type") != "application/octet-stream" || r.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("download headers = %v (never rendered by the browser)", r.header)
	}

	for _, p := range []string{"../", "..%2F..%2Frunova.db", "%2Fetc%2Fpasswd", "storage%2F..%2F..%2F..%2Frunova.db"} {
		if r := budi.raw("GET", appPath(id, "/files/download?path="+p), "", nil, nil); r.status == 200 {
			t.Errorf("download %s = 200 %q", p, r.body)
		}
		if r := budi.do("GET", appPath(id, "/files?path="+p), nil); r.status == 200 {
			t.Errorf("list %s = 200", p)
		}
	}

	if r := andi.do("GET", appPath(id, "/files?path="), nil); r.status != 404 {
		t.Errorf("andi lists budi's files: %d", r.status)
	}
	if r := andi.raw("GET", appPath(id, "/files/download?path=notes.txt"), "", nil, nil); r.status != 404 {
		t.Errorf("andi downloads budi's file: %d %s", r.status, r.body)
	}
	if r := andi.do("DELETE", appPath(id, "/files"), obj{"path": "notes.txt"}); r.status != 404 {
		t.Errorf("andi deletes budi's file: %d", r.status)
	}
	ct, body = multipartBody(t, "file", "x.txt", []byte("x"))
	if r := andi.raw("POST", appPath(id, "/files?path="), ct, body, nil); r.status != 404 {
		t.Errorf("andi uploads into budi's app: %d", r.status)
	}
	if _, err := os.Stat(filepath.Join(data, "notes.txt")); err != nil {
		t.Fatalf("budi's file touched: %v", err)
	}

	big := strings.Repeat("x", 65<<20)
	ct, body = multipartBody(t, "file", "big.bin", []byte(big))
	if r := budi.raw("POST", appPath(id, "/files?path="), ct, body, nil); r.status != 409 {
		t.Errorf("upload beyond the app's disk: %d %s", r.status, r.body)
	}

	if r := budi.do("DELETE", appPath(id, "/files"), obj{"path": "notes.txt"}); r.status != 200 {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(filepath.Join(data, "notes.txt")); !os.IsNotExist(err) {
		t.Errorf("deleted file still there: %v", err)
	}
	if r := budi.do("DELETE", appPath(id, "/files"), obj{"path": "storage"}); r.status != 400 {
		t.Errorf("delete a saved folder: %d", r.status)
	}

	acts, _ := e.store.ActivityList(t.Context(), store.ActivityFilter{})
	var seen []string
	for _, a := range acts {
		if strings.HasPrefix(a.Action, "file.") {
			seen = append(seen, a.Action+" "+a.Detail)
		}
	}
	if strings.Join(seen, ",") != "file.delete notes.txt,file.upload storage/foto.png" {
		t.Errorf("activity = %v", seen)
	}
}

func TestAppDetailAndListCarryStorage(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	budi := e.login("budi", "budi-password-1")
	id, _ := budi.createApp("starboom", obj{"folders": []obj{{"path": "./storage"}}, "disk_mb": 128})

	var detail struct {
		App struct {
			Folders []struct{ Path, Dir string } `json:"folders"`
			DiskMB  int                          `json:"disk_mb"`
		} `json:"app"`
	}
	budi.do("GET", appPath(id, ""), nil).into(t, &detail)
	if detail.App.DiskMB != 128 || len(detail.App.Folders) != 1 || detail.App.Folders[0].Path != "storage" {
		t.Fatalf("detail = %+v", detail)
	}

	var listing struct {
		Usage struct {
			DiskMB        int   `json:"disk_mb"`
			DiskUsedBytes int64 `json:"disk_used_bytes"`
			Quota         struct {
				DiskMB int `json:"disk_mb"`
			} `json:"quota"`
		} `json:"usage"`
	}
	budi.do("GET", "/api/apps", nil).into(t, &listing)
	if listing.Usage.DiskMB != 128 || listing.Usage.Quota.DiskMB != 4096 {
		t.Fatalf("usage = %+v", listing.Usage)
	}

	r := budi.do("PATCH", appPath(id, ""), obj{"folders": []obj{{"path": "/etc"}}})
	if r.status != 400 {
		t.Errorf("system folder: %d %s", r.status, r.body)
	}
	r = budi.do("PATCH", appPath(id, ""), obj{"folders": []obj{{"path": "/var/www/uploads"}}})
	var patched struct {
		RedeployRequired bool `json:"redeploy_required"`
	}
	r.into(t, &patched)
	if r.status != 200 || !patched.RedeployRequired {
		t.Errorf("folders change: %d %s", r.status, r.body)
	}
}

func TestServerStatusReportsAppDisk(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	budi := e.login("budi", "budi-password-1")
	id, _ := budi.createApp("starboom", obj{"disk_mb": 128})
	budi.waitStatus(id, "running")
	if err := os.WriteFile(filepath.Join(e.dataDir, "data", "budi", "starboom", "x"), make([]byte, 3<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ScanDisk(t.Context()); err != nil {
		t.Fatal(err)
	}
	admin := e.login("admin", adminPassword)
	var st struct {
		Host struct {
			AppDiskAllocMB  int   `json:"app_disk_alloc_mb"`
			AppDiskUsedByte int64 `json:"app_disk_used_bytes"`
		} `json:"host"`
	}
	admin.do("GET", "/api/server", nil).into(t, &st)
	if st.Host.AppDiskAllocMB != 128 || st.Host.AppDiskUsedByte < 3<<20 {
		t.Fatalf("server disk = %+v", st.Host)
	}
}
