package api_test

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
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
	if r := andi.do("DELETE", appPath(id, "/files"), obj{"paths": []string{"notes.txt"}}); r.status != 404 {
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

	if r := budi.do("DELETE", appPath(id, "/files"), obj{"paths": []string{"notes.txt"}}); r.status != 200 {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(filepath.Join(data, "notes.txt")); !os.IsNotExist(err) {
		t.Errorf("deleted file still there: %v", err)
	}
	if r := budi.do("DELETE", appPath(id, "/files"), obj{"paths": []string{"storage"}}); r.status != 400 {
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

func TestFileManagerAPI(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	e.addUser("andi", "andi-password-1", store.RoleUser)
	budi := e.login("budi", "budi-password-1")
	andi := e.login("andi", "andi-password-1")
	id, _ := budi.createApp("starboom", obj{"folders": []obj{{"path": "/app/storage"}}, "disk_mb": 64})
	budi.waitStatus(id, "running")
	data := filepath.Join(e.dataDir, "data", "budi", "starboom")
	src := filepath.Join(e.dataDir, "mounts", "budi", "starboom", "storage")
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")
	for name, b := range map[string][]byte{"logo.png": png, "page.png": []byte("<html><script>alert(1)</script>"), "icon.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), "notes.txt": []byte("halo")} {
		if err := os.WriteFile(filepath.Join(data, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc", filepath.Join(data, "etc")); err != nil {
		t.Fatal(err)
	}

	// Several files in one upload.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, n := range []string{"a.txt", "b.txt"} {
		fw, _ := mw.CreateFormFile("file", n)
		fw.Write([]byte(n))
	}
	mw.Close()
	if r := budi.raw("POST", appPath(id, "/files?path=storage"), mw.FormDataContentType(), &buf, nil); r.status != 201 || !strings.Contains(string(r.body), `"saved":2`) {
		t.Fatalf("multi upload: %d %s", r.status, r.body)
	}

	// Preview: whitelisted images inline with their sniffed type, sandboxed; text as text/plain.
	r := budi.raw("GET", appPath(id, "/files/preview?path=logo.png"), "", nil, nil)
	if r.status != 200 || r.header.Get("Content-Type") != "image/png" || r.header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(r.header.Get("Content-Security-Policy"), "sandbox") || !strings.HasPrefix(r.header.Get("Content-Disposition"), "inline") {
		t.Fatalf("image preview: %d %v", r.status, r.header)
	}
	r = budi.raw("GET", appPath(id, "/files/preview?path=page.png"), "", nil, nil)
	if r.status != 200 || r.header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("HTML named .png: %d %v (must be plain text, never HTML or an image)", r.status, r.header)
	}
	for _, p := range []string{"icon.svg", "etc%2Fpasswd", "..%2Frunova.db", "storage"} {
		if r := budi.raw("GET", appPath(id, "/files/preview?path="+p), "", nil, nil); r.status == 200 {
			t.Errorf("preview %s = 200 %s", p, r.header.Get("Content-Type"))
		}
	}
	var info struct {
		MIME    string `json:"mime"`
		Preview string `json:"preview"`
	}
	budi.do("GET", appPath(id, "/files/info?path=icon.svg"), nil).into(t, &info)
	if info.MIME != "image/svg+xml" || info.Preview != "" {
		t.Errorf("svg info = %+v", info)
	}

	// New folder, rename, move; existing names and saved folders refused.
	if r := budi.do("POST", appPath(id, "/files/folder"), obj{"path": "", "name": "docs"}); r.status != 201 {
		t.Fatalf("mkdir: %d %s", r.status, r.body)
	}
	for _, c := range []obj{{"path": "", "name": "docs"}, {"path": "", "name": "../x"}, {"path": "etc", "name": "x"}, {"path": "..", "name": "x"}} {
		if r := budi.do("POST", appPath(id, "/files/folder"), c); r.status != 400 {
			t.Errorf("mkdir %v: %d", c, r.status)
		}
	}
	if r := budi.do("POST", appPath(id, "/files/move"), obj{"from": "notes.txt", "to": "docs/catatan.txt"}); r.status != 200 {
		t.Fatalf("move: %d %s", r.status, r.body)
	}
	for _, c := range []obj{{"from": "logo.png", "to": "docs/catatan.txt"}, {"from": "storage", "to": "media"}, {"from": "logo.png", "to": "storage"}, {"from": "logo.png", "to": "../logo.png"}, {"from": "etc/passwd", "to": "passwd"}} {
		if r := budi.do("POST", appPath(id, "/files/move"), c); r.status != 400 {
			t.Errorf("move %v: %d %s", c, r.status, r.body)
		}
	}
	if r := andi.do("POST", appPath(id, "/files/move"), obj{"from": "logo.png", "to": "x.png"}); r.status != 404 {
		t.Errorf("andi moves: %d", r.status)
	}
	if r := andi.do("POST", appPath(id, "/files/folder"), obj{"path": "", "name": "x"}); r.status != 404 {
		t.Errorf("andi mkdir: %d", r.status)
	}
	if r := andi.raw("GET", appPath(id, "/files/preview?path=logo.png"), "", nil, nil); r.status != 404 {
		t.Errorf("andi previews: %d", r.status)
	}
	if r := andi.raw("GET", appPath(id, "/files/zip?path=logo.png"), "", nil, nil); r.status != 404 {
		t.Errorf("andi zips: %d", r.status)
	}

	// Zip of a selection: the links are left out.
	r = budi.raw("GET", appPath(id, "/files/zip?path=docs&path=storage&path=etc"), "", nil, nil)
	if r.status != 200 || r.header.Get("Content-Type") != "application/zip" || !strings.Contains(r.header.Get("Content-Disposition"), "starboom.zip") {
		t.Fatalf("zip: %d %v %s", r.status, r.header, r.body)
	}
	zr, err := zip.NewReader(bytes.NewReader(r.body), int64(len(r.body)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "docs/,docs/catatan.txt,storage/,storage/a.txt,storage/b.txt" {
		t.Errorf("zip entries = %v", names)
	}
	if r := budi.raw("GET", appPath(id, "/files/zip?path=..%2F"), "", nil, nil); r.status != 400 {
		t.Errorf("zip outside: %d", r.status)
	}

	// Delete several at once.
	if r := budi.do("DELETE", appPath(id, "/files"), obj{"paths": []string{"logo.png", "storage/a.txt"}}); r.status != 200 {
		t.Fatalf("delete many: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(filepath.Join(src, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("storage/a.txt still there: %v", err)
	}

	acts, _ := e.store.ActivityList(t.Context(), store.ActivityFilter{})
	seen := map[string]bool{}
	for _, a := range acts {
		seen[a.Action] = true
	}
	for _, want := range []string{store.ActFileUpload, store.ActFileMkdir, store.ActFileRename, store.ActFileDelete} {
		if !seen[want] {
			t.Errorf("activity lacks %s", want)
		}
	}
}

func TestZipDownloadLimit(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	budi := e.login("budi", "budi-password-1")
	id, _ := budi.createApp("starboom", obj{"disk_mb": 64})
	budi.waitStatus(id, "running")
	s, _ := e.store.Settings(t.Context())
	s.UploadLimitMB = 1
	if err := e.store.SetSettings(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dataDir, "data", "budi", "starboom", "big.bin"), make([]byte, 4<<20+1), 0o600); err != nil {
		t.Fatal(err)
	}
	r := budi.raw("GET", appPath(id, "/files/zip?path=big.bin"), "", nil, nil)
	if r.status != 400 || !strings.Contains(r.errMsg(t), "melebihi batas 4 MB") {
		t.Errorf("zip over 4× the upload limit: %d %s", r.status, r.body)
	}
	// check=1 answers before the download starts, so the panel can show the reason.
	if r := budi.do("GET", appPath(id, "/files/zip?path=big.bin&check=1"), nil); r.status != 400 {
		t.Errorf("check over the limit: %d %s", r.status, r.body)
	}
	var plan struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if err := os.WriteFile(filepath.Join(e.dataDir, "data", "budi", "starboom", "big.bin"), make([]byte, 10), 0o600); err != nil {
		t.Fatal(err)
	}
	r = budi.do("GET", appPath(id, "/files/zip?path=big.bin&check=1"), nil)
	r.into(t, &plan)
	if r.status != 200 || plan.Name != "big.bin.zip" || plan.Size != 10 || !strings.HasPrefix(r.header.Get("Content-Type"), "application/json") {
		t.Errorf("check = %d %+v %v", r.status, plan, r.header)
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
	// An app from before disk sizes (disk_mb 0) is measured but not counted as given a share.
	old, _ := budi.createApp("legacy", obj{"disk_mb": 64})
	zero := 0
	if _, err := e.store.UpdateApp(t.Context(), old, store.AppPatch{DiskMB: &zero}); err != nil {
		t.Fatal(err)
	}
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
			AppDiskApps     int   `json:"app_disk_apps"`
			AppDiskUsedByte int64 `json:"app_disk_used_bytes"`
		} `json:"host"`
	}
	admin.do("GET", "/api/server", nil).into(t, &st)
	if st.Host.AppDiskAllocMB != 128 || st.Host.AppDiskApps != 1 || st.Host.AppDiskUsedByte < 3<<20 {
		t.Fatalf("server disk = %+v", st.Host)
	}
}
