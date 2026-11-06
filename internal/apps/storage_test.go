package apps_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"runova/internal/apps"
	"runova/internal/artifact"
	"runova/internal/folders"
	"runova/internal/store"
)

func folderReport(file string, fs ...folders.Folder) artifact.Report {
	rep := staticReport(file)
	for _, f := range fs {
		rep.Folders = append(rep.Folders, artifact.DetectedFolder{Folder: f, Source: artifact.SourceEnvExample, Key: "X"})
	}
	return rep
}

func TestContainerSpecMountsSavedFolders(t *testing.T) {
	e := newTestEnv(t)
	owner := store.User{ID: 2, Username: "budi"}
	app := store.App{ID: 7, Name: "starboom", Kind: artifact.KindStatic, MemoryMB: 128,
		Folders: []folders.Folder{{Path: "storage", Dir: "storage"}, {Path: "/app/media", Dir: "media"}}}
	d := store.Deployment{ID: 9, FileName: "svc", ArtifactPath: "/a/svc", InternalPort: 80}
	spec := e.svc.ContainerSpec(app, owner, d, nil, nil)
	src := e.svc.DataDir + "/mounts/budi/starboom/media"
	want := []string{
		"/a/svc:/app/svc:ro",
		e.svc.DataDir + "/data/budi/starboom:/data",
		src + ":/app/media",
		src + ":/data/media",
	}
	if !slices.Equal(spec.Binds, want) {
		t.Fatalf("binds = %v\nwant    %v", spec.Binds, want)
	}
	if !spec.ReadOnly || spec.WorkingDir != "/data" {
		t.Errorf("rootfs read-only = %v, workdir = %q", spec.ReadOnly, spec.WorkingDir)
	}
}

func TestCreateUsesDetectedFoldersAndDefaultDisk(t *testing.T) {
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := e.create(u, "starboom", folderReport("svc", folders.Folder{Path: "storage", Dir: "storage"}), apps.CreateInput{})
	if !slices.Equal(app.Folders, []folders.Folder{{Path: "storage", Dir: "storage"}}) {
		t.Errorf("folders = %+v, want the detected ones", app.Folders)
	}
	if app.DiskMB != 256 {
		t.Errorf("disk = %d MB, want the 256 MB default", app.DiskMB)
	}
	other, _ := e.create(u, "plain", folderReport("svc", folders.Folder{Path: "storage", Dir: "storage"}),
		apps.CreateInput{Folders: []folders.Folder{}, DiskMB: 512})
	if len(other.Folders) != 0 || other.DiskMB != 512 {
		t.Errorf("explicit empty folders and 512 MB: %+v", other)
	}
}

func TestCreateFolderAndDiskValidation(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	q := roomyQuota
	q.DiskMB = 512
	u := e.addUser("budi", store.RoleUser, q)
	try := func(in apps.CreateInput) error {
		in.UploadID, in.Name, in.MemoryMB = e.upload(u, staticReport("svc")), "one", 128
		_, _, err := e.svc.Create(ctx, u, in)
		return err
	}
	if err := try(apps.CreateInput{Folders: []folders.Folder{{Path: "/etc"}}}); !errors.Is(err, apps.ErrValidation) {
		t.Errorf("system folder: %v", err)
	}
	if err := try(apps.CreateInput{Folders: []folders.Folder{{Path: "/app/svc"}}}); !errors.Is(err, apps.ErrValidation) {
		t.Errorf("over the artifact: %v", err)
	}
	if err := try(apps.CreateInput{DiskMB: 100}); !errors.Is(err, apps.ErrValidation) || !strings.Contains(err.Error(), "kelipatan 64") {
		t.Errorf("disk step: %v", err)
	}
	if err := try(apps.CreateInput{DiskMB: 1024}); !errors.Is(err, apps.ErrQuota) || !strings.Contains(err.Error(), "Disk melebihi sisa kuota (sisa 512 MB)") {
		t.Errorf("disk over quota: %v", err)
	}
	if err := try(apps.CreateInput{DiskMB: 512}); err != nil {
		t.Fatalf("exact fit: %v", err)
	}
	in := apps.CreateInput{UploadID: e.upload(u, staticReport("svc")), Name: "two", MemoryMB: 128}
	if _, _, err := e.svc.Create(ctx, u, in); !errors.Is(err, apps.ErrQuota) || !strings.Contains(err.Error(), "Kuota disk habis") {
		t.Errorf("default disk with nothing left: %v", err)
	}
}

func TestUpdateSettingsFoldersAndDisk(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	q := roomyQuota
	q.DiskMB = 1024
	u := e.addUser("budi", store.RoleUser, q)
	app, _ := e.create(u, "one", staticReport("svc"), apps.CreateInput{DiskMB: 512})
	list := []folders.Folder{{Path: "./uploads"}, {Path: "/var/www/files"}}
	got, redeploy, err := e.svc.UpdateSettings(ctx, u, app.ID, apps.SettingsInput{Folders: &list})
	want := []folders.Folder{{Path: "uploads", Dir: "uploads"}, {Path: "/var/www/files", Dir: "files"}}
	if err != nil || !redeploy || !slices.Equal(got.Folders, want) {
		t.Fatalf("folders change = %+v, redeploy=%v, %v", got.Folders, redeploy, err)
	}
	if _, redeploy, _ := e.svc.UpdateSettings(ctx, u, app.ID, apps.SettingsInput{Folders: &list}); redeploy {
		t.Error("the same folders again must not ask for a redeploy")
	}
	bad := []folders.Folder{{Path: "../x"}}
	if _, _, err := e.svc.UpdateSettings(ctx, u, app.ID, apps.SettingsInput{Folders: &bad}); !errors.Is(err, apps.ErrValidation) {
		t.Errorf("bad folder: %v", err)
	}
	disk := 1088
	if _, _, err := e.svc.UpdateSettings(ctx, u, app.ID, apps.SettingsInput{DiskMB: &disk}); !errors.Is(err, apps.ErrQuota) {
		t.Errorf("raise beyond quota: %v", err)
	}
	disk = 1024
	got, redeploy, err = e.svc.UpdateSettings(ctx, u, app.ID, apps.SettingsInput{DiskMB: &disk})
	if err != nil || got.DiskMB != 1024 || redeploy {
		t.Errorf("raise within quota: %+v, redeploy=%v, %v (disk needs no redeploy)", got.DiskMB, redeploy, err)
	}
}

func TestDeployPreparesWorkspaceFolders(t *testing.T) {
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := e.create(u, "starboom", folderReport("svc", folders.Folder{Path: "public/uploads", Dir: "public/uploads"}, folders.Folder{Path: "/app/storage", Dir: "storage"}), apps.CreateInput{})
	if err := e.svc.Deploy(context.Background(), u, app.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	uid := 100000 + int(u.ID)
	data := e.svc.DataDir + "/data/budi/starboom"
	src := e.svc.DataDir + "/mounts/budi/starboom/storage"
	for _, dir := range []string{data + "/public", data + "/public/uploads", src, data + "/storage"} {
		if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
			t.Errorf("%s: %v, %v", dir, fi, err)
		}
		if got := e.chowned[dir]; got != uid {
			t.Errorf("%s chowned to %d, want %d", dir, got, uid)
		}
	}
	if fi, err := os.Stat(e.svc.DataDir + "/mounts/budi/starboom"); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("mount root = %v, %v; want 0700 owned by Runova", fi, err)
	}
	if _, ok := e.chowned[e.svc.DataDir+"/mounts/budi/starboom"]; ok {
		t.Error("the mount root must stay Runova's: the app could otherwise swap its folders for links")
	}
}

func TestDeployRefusesSymlinkedFolder(t *testing.T) {
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, d := e.create(u, "starboom", folderReport("svc", folders.Folder{Path: "storage", Dir: "storage"}), apps.CreateInput{})
	data := e.svc.DataDir + "/data/budi/starboom"
	outside := t.TempDir()
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, data+"/storage"); err != nil {
		t.Fatal(err)
	}
	err := e.svc.Deploy(context.Background(), u, app.ID, d.ID)
	if err == nil || !strings.Contains(err.Error(), "storage") {
		t.Fatalf("deploy = %v, want refused because storage leaves the workspace", err)
	}
	if len(e.engine.CallsWithPrefix("create")) != 0 {
		t.Error("a container was created")
	}
	if _, ok := e.chowned[outside]; ok {
		t.Error("chowned a folder outside the workspace")
	}
}

func writeFile(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScanDiskLevelsNotifyAndRefuseDeploy(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirstWith(t, e, u, "starboom", folderReport("svc", folders.Folder{Path: "/app/storage", Dir: "storage"}), apps.CreateInput{DiskMB: 64})
	data := e.svc.DataDir + "/data/budi/starboom"
	src := e.svc.DataDir + "/mounts/budi/starboom/storage"

	writeFile(t, data+"/a.bin", 30<<20)
	writeFile(t, src+"/b.bin", 22<<20)
	if err := os.Symlink("/", data+"/root-link"); err != nil { // never followed
		t.Fatal(err)
	}
	if err := e.svc.ScanDisk(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := e.store.App(ctx, app.ID)
	if got.DiskUsedBytes < 52<<20 || got.DiskUsedBytes > 53<<20 || got.DiskLevel != store.DiskWarn || got.DiskScannedAt == 0 {
		t.Fatalf("after 52 MB of 64: used=%d level=%q at=%d", got.DiskUsedBytes, got.DiskLevel, got.DiskScannedAt)
	}
	notes := func() []store.Notification {
		list, _ := e.store.Notifications(ctx, u.ID, 50)
		return slices.DeleteFunc(list, func(n store.Notification) bool { return !strings.Contains(n.Title, "Disk") })
	}
	if n := notes(); len(n) != 1 || n[0].Level != store.LevelWarning {
		t.Fatalf("notifications at 80%% = %+v", n)
	}
	e.svc.ScanDisk(ctx)
	if n := notes(); len(n) != 1 {
		t.Fatalf("a second scan at the same level notified again: %+v", n)
	}

	writeFile(t, src+"/c.bin", 13<<20)
	e.svc.ScanDisk(ctx)
	got, _ = e.store.App(ctx, app.ID)
	if got.DiskLevel != store.DiskFull {
		t.Fatalf("level = %q at %d bytes, want full", got.DiskLevel, got.DiskUsedBytes)
	}
	if n := notes(); len(n) != 2 || n[0].Level != store.LevelError {
		t.Fatalf("notifications at 100%% = %+v", n)
	}
	if got.Status != store.AppRunning || len(e.engine.CallsWithPrefix("stop")) != 0 {
		t.Errorf("a full disk must not stop the app: status %s, calls %v", got.Status, e.engine.Calls())
	}
	if _, err := e.svc.PrepareRedeploy(ctx, u, app.ID); !errors.Is(err, apps.ErrQuota) || !strings.Contains(err.Error(), "Disk aplikasi penuh") {
		t.Errorf("redeploy with a full disk: %v", err)
	}
	if _, err := e.svc.NewVersion(ctx, u, app.ID, e.upload(u, staticReport("svc"))); !errors.Is(err, apps.ErrQuota) {
		t.Errorf("new version with a full disk: %v", err)
	}

	// Freeing space is seen at once by the next deploy, without waiting for the scan.
	os.Remove(src + "/c.bin")
	if _, err := e.svc.PrepareRedeploy(ctx, u, app.ID); err != nil {
		t.Errorf("redeploy after freeing space: %v", err)
	}
}

func deployFirstWith(t *testing.T, e *testEnv, u store.User, name string, rep artifact.Report, in apps.CreateInput) (store.App, store.Deployment) {
	t.Helper()
	app, d := e.create(u, name, rep, in)
	if err := e.svc.Deploy(context.Background(), u, app.ID, d.ID); err != nil {
		t.Fatalf("Deploy %s: %v", name, err)
	}
	return app, d
}

func TestUnlimitedLegacyAppIsMeasuredNotLimited(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirst(t, e, u, "old")
	zero := 0
	if _, err := e.store.UpdateApp(ctx, app.ID, store.AppPatch{DiskMB: &zero}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, e.svc.DataDir+"/data/budi/old/x", 1<<20)
	e.svc.ScanDisk(ctx)
	got, _ := e.store.App(ctx, app.ID)
	if got.DiskUsedBytes < 1<<20 || got.DiskLevel != store.DiskOK {
		t.Fatalf("legacy app: used=%d level=%q", got.DiskUsedBytes, got.DiskLevel)
	}
	if _, err := e.svc.PrepareRedeploy(ctx, u, app.ID); err != nil {
		t.Errorf("legacy app redeploy: %v", err)
	}
}

func TestFilesBrowseDownloadDeleteUpload(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	other := e.addUser("andi", store.RoleUser, roomyQuota)
	admin := e.addUser("root1", store.RoleAdmin, roomyQuota)
	app, _ := deployFirstWith(t, e, u, "starboom", folderReport("svc", folders.Folder{Path: "/app/storage", Dir: "storage"}), apps.CreateInput{DiskMB: 64})
	data := e.svc.DataDir + "/data/budi/starboom"
	src := e.svc.DataDir + "/mounts/budi/starboom/storage"
	writeFile(t, data+"/notes.txt", 5)
	writeFile(t, src+"/foto.png", 7)
	outside := t.TempDir()
	writeFile(t, outside+"/secret", 3)
	if err := os.Symlink(outside, data+"/escape"); err != nil {
		t.Fatal(err)
	}

	root, err := e.svc.ListFiles(ctx, u, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]apps.FileEntry{}
	for _, f := range root {
		names[f.Name] = f
	}
	if names["storage"].Type != "dir" || !names["storage"].Saved || names["notes.txt"].Size != 5 || names["escape"].Type != "link" {
		t.Fatalf("root listing = %+v", root)
	}
	inStorage, err := e.svc.ListFiles(ctx, u, app.ID, "storage")
	if err != nil || len(inStorage) != 1 || inStorage[0].Name != "foto.png" {
		t.Fatalf("storage listing = %+v, %v (the mounted folder's files)", inStorage, err)
	}

	f, _, err := e.svc.OpenFile(ctx, u, app.ID, "storage/foto.png")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if len(b) != 7 {
		t.Errorf("download = %d bytes", len(b))
	}

	for _, p := range []string{"../budi", "storage/../../x", "/etc/passwd", "escape/secret", "escape", "storage/../../../mounts"} {
		if _, _, err := e.svc.OpenFile(ctx, u, app.ID, p); err == nil {
			t.Errorf("OpenFile(%q) succeeded", p)
		}
		if _, err := e.svc.ListFiles(ctx, u, app.ID, p); err == nil {
			t.Errorf("ListFiles(%q) succeeded", p)
		}
	}
	if err := e.svc.DeleteFiles(ctx, u, app.ID, []string{"escape/secret"}); err == nil {
		t.Error("deleted through a link")
	}
	if _, err := os.Stat(outside + "/secret"); err != nil {
		t.Fatalf("file outside the workspace is gone: %v", err)
	}
	for _, p := range []string{"", ".", "storage"} {
		if err := e.svc.DeleteFiles(ctx, u, app.ID, []string{p}); !errors.Is(err, apps.ErrValidation) {
			t.Errorf("DeleteFiles(%q) = %v, want refused (workspace or saved folder itself)", p, err)
		}
	}

	if _, err := e.svc.ListFiles(ctx, other, app.ID, ""); !errors.Is(err, apps.ErrAppNotFound) {
		t.Errorf("another user lists: %v", err)
	}
	if _, _, err := e.svc.OpenFile(ctx, other, app.ID, "notes.txt"); !errors.Is(err, apps.ErrAppNotFound) {
		t.Errorf("another user downloads: %v", err)
	}
	if err := e.svc.DeleteFiles(ctx, other, app.ID, []string{"notes.txt"}); !errors.Is(err, apps.ErrAppNotFound) {
		t.Errorf("another user deletes: %v", err)
	}
	if _, err := e.svc.ListFiles(ctx, admin, app.ID, ""); err != nil {
		t.Errorf("admin lists: %v", err)
	}

	if err := e.svc.SaveFile(ctx, u, app.ID, "storage", "baru.txt", strings.NewReader("halo")); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(src + "/baru.txt"); err != nil || string(b) != "halo" {
		t.Fatalf("uploaded file = %q, %v", b, err)
	}
	if got := e.chowned[src+"/baru.txt"]; got != 100000+int(u.ID) {
		t.Errorf("uploaded file chowned to %d", got)
	}
	for _, name := range []string{"", "..", "a/b", ".", "x\x00"} {
		if err := e.svc.SaveFile(ctx, u, app.ID, "storage", name, strings.NewReader("x")); !errors.Is(err, apps.ErrValidation) {
			t.Errorf("SaveFile name %q = %v", name, err)
		}
	}
	if err := e.svc.SaveFile(ctx, u, app.ID, "escape", "x", strings.NewReader("x")); err == nil {
		t.Error("uploaded through a link")
	}
	big := strings.NewReader(strings.Repeat("x", 65<<20))
	if err := e.svc.SaveFile(ctx, u, app.ID, "", "big.bin", big); !errors.Is(err, apps.ErrQuota) {
		t.Errorf("upload beyond the app's disk: %v", err)
	}
	if _, err := os.Stat(data + "/big.bin"); !os.IsNotExist(err) {
		t.Errorf("a refused upload left its file: %v", err)
	}

	if err := e.svc.DeleteFiles(ctx, u, app.ID, []string{"storage/foto.png"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src + "/foto.png"); !os.IsNotExist(err) {
		t.Errorf("deleted file still there: %v", err)
	}
	writeFile(t, data+"/dir/sub/f", 1)
	if err := e.svc.DeleteFiles(ctx, u, app.ID, []string{"dir"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(data + "/dir"); !os.IsNotExist(err) {
		t.Errorf("deleted folder still there: %v", err)
	}
}

func TestDeleteAppRemovesMountedFolders(t *testing.T) {
	e := newTestEnv(t)
	u := e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ := deployFirstWith(t, e, u, "starboom", folderReport("svc", folders.Folder{Path: "/app/storage", Dir: "storage"}), apps.CreateInput{})
	if err := e.svc.Delete(context.Background(), u, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.svc.DataDir + "/mounts/budi/starboom"); !os.IsNotExist(err) {
		t.Errorf("mounted folders survived Delete: %v", err)
	}
}

// filesEnv deploys starboom with a mounted saved folder (storage) and a relative one (public/uploads),
// plus a link out of the workspace, and returns the app and its two host folders.
func filesEnv(t *testing.T) (e *testEnv, u store.User, app store.App, data, src, outside string) {
	t.Helper()
	e = newTestEnv(t)
	u = e.addUser("budi", store.RoleUser, roomyQuota)
	app, _ = deployFirstWith(t, e, u, "starboom", folderReport("svc", folders.Folder{Path: "/app/storage", Dir: "storage"}, folders.Folder{Path: "public/uploads", Dir: "public/uploads"}), apps.CreateInput{DiskMB: 64})
	data = e.svc.DataDir + "/data/budi/starboom"
	src = e.svc.DataDir + "/mounts/budi/starboom/storage"
	outside = t.TempDir()
	writeFile(t, outside+"/secret", 3)
	if err := os.Symlink(outside, data+"/escape"); err != nil {
		t.Fatal(err)
	}
	return
}

func TestFilesMakeDir(t *testing.T) {
	ctx := context.Background()
	e, u, app, data, src, outside := filesEnv(t)
	other := e.addUser("andi", store.RoleUser, roomyQuota)

	if err := e.svc.MakeDir(ctx, u, app.ID, "", "laporan"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(data + "/laporan"); err != nil || !fi.IsDir() {
		t.Fatalf("folder not created: %v", err)
	}
	if got := e.chowned[data+"/laporan"]; got != 100000+int(u.ID) {
		t.Errorf("new folder chowned to %d", got)
	}
	if err := e.svc.MakeDir(ctx, u, app.ID, "storage", "2026"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(src + "/2026"); err != nil || !fi.IsDir() {
		t.Fatalf("folder in the mounted folder: %v", err)
	}
	if err := e.svc.MakeDir(ctx, u, app.ID, "", "laporan"); !errors.Is(err, apps.ErrValidation) {
		t.Errorf("existing name: %v", err)
	}
	for _, c := range []struct{ dir, name string }{
		{"", ""}, {"", "."}, {"", ".."}, {"", "a/b"}, {"", "a\\b"}, {"", "x\x00"}, {"", strings.Repeat("a", 256)},
		{"..", "x"}, {"/etc", "x"}, {"storage/../..", "x"}, {"escape", "x"}, {"nope", "x"},
	} {
		if err := e.svc.MakeDir(ctx, u, app.ID, c.dir, c.name); err == nil {
			t.Errorf("MakeDir(%q, %q) succeeded", c.dir, c.name)
		}
	}
	if _, err := os.Stat(outside + "/x"); !os.IsNotExist(err) {
		t.Errorf("created through a link: %v", err)
	}
	if err := e.svc.MakeDir(ctx, other, app.ID, "", "x"); !errors.Is(err, apps.ErrAppNotFound) {
		t.Errorf("another user: %v", err)
	}
	if acts := fileActs(t, e); !slices.Contains(acts, "file.mkdir laporan") || !slices.Contains(acts, "file.mkdir storage/2026") {
		t.Errorf("activity = %v", acts)
	}
}

func fileActs(t *testing.T, e *testEnv) []string {
	t.Helper()
	list, err := e.store.ActivityList(context.Background(), store.ActivityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range list {
		if strings.HasPrefix(a.Action, "file.") {
			out = append(out, a.Action+" "+a.Detail)
		}
	}
	return out
}

func TestFilesMove(t *testing.T) {
	ctx := context.Background()
	e, u, app, data, src, outside := filesEnv(t)
	other := e.addUser("andi", store.RoleUser, roomyQuota)
	writeFile(t, data+"/a.txt", 3)
	writeFile(t, data+"/b.txt", 4)
	writeFile(t, data+"/docs/c.txt", 5)
	writeFile(t, src+"/foto.png", 7)

	// Rename in place, move into a folder, move across into the mounted folder and back out.
	if err := e.svc.MoveFile(ctx, u, app.ID, "a.txt", "catatan.txt"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.MoveFile(ctx, u, app.ID, "catatan.txt", "docs/catatan.txt"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.MoveFile(ctx, u, app.ID, "docs", "storage/docs"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(src + "/docs/catatan.txt"); err != nil || len(b) != 3 {
		t.Fatalf("moved into the mounted folder: %d bytes, %v", len(b), err)
	}
	if err := e.svc.MoveFile(ctx, u, app.ID, "storage/foto.png", "public/uploads/foto.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(data + "/public/uploads/foto.png"); err != nil {
		t.Fatalf("moved out of the mounted folder: %v", err)
	}

	refused := []struct{ from, to string }{
		{"b.txt", "storage/docs/catatan.txt"}, // destination exists
		{"b.txt", "storage"},                  // a saved folder
		{"b.txt", "public/uploads"},           // a saved folder
		{"storage", "media"},                  // moving a saved folder
		{"public/uploads", "up"},              // moving a saved folder
		{"public", "pub"},                     // holds a saved folder
		{"", "x"},
		{"storage/docs", "storage/docs/in"}, // into itself
		{"b.txt", "nope/b.txt"},             // no such folder
		{"b.txt", "escape/b.txt"},           // through a link
		{"escape/secret", "secret"},         // out through a link
		{"../x", "x"}, {"b.txt", "../b.txt"}, {"b.txt", "/tmp/b.txt"}, {"b.txt", "storage/../../b.txt"},
		{"b.txt", "a/.."}, {"b.txt", "x\x00"}, {"b.txt", "a\\b"}, {"b.txt", strings.Repeat("a", 256)},
		{"missing", "x"},
	}
	for _, c := range refused {
		if err := e.svc.MoveFile(ctx, u, app.ID, c.from, c.to); err == nil {
			t.Errorf("MoveFile(%q, %q) succeeded", c.from, c.to)
		}
	}
	if b, err := os.ReadFile(data + "/b.txt"); err != nil || len(b) != 4 {
		t.Errorf("a refused move touched b.txt: %v", err)
	}
	if b, err := os.ReadFile(src + "/docs/catatan.txt"); err != nil || len(b) != 3 {
		t.Errorf("an existing destination was replaced: %d bytes, %v", len(b), err)
	}
	if _, err := os.Stat(outside + "/secret"); err != nil {
		t.Errorf("the file outside the workspace moved: %v", err)
	}
	if err := e.svc.MoveFile(ctx, other, app.ID, "b.txt", "c.txt"); !errors.Is(err, apps.ErrAppNotFound) {
		t.Errorf("another user: %v", err)
	}
	if acts := fileActs(t, e); !slices.Contains(acts, "file.rename a.txt → catatan.txt") {
		t.Errorf("activity = %v", acts)
	}
}

func TestFilesDeleteMany(t *testing.T) {
	ctx := context.Background()
	e, u, app, data, src, outside := filesEnv(t)
	writeFile(t, data+"/a.txt", 3)
	writeFile(t, data+"/dir/sub/f", 1)
	writeFile(t, src+"/foto.png", 7)

	// One bad path refuses the whole request before anything is removed.
	for _, bad := range [][]string{{"a.txt", "storage"}, {"a.txt", "public"}, {"a.txt", ""}, {"a.txt", "escape/secret"}, {"a.txt", "../x"}, {}} {
		if err := e.svc.DeleteFiles(ctx, u, app.ID, bad); err == nil {
			t.Errorf("DeleteFiles(%q) succeeded", bad)
		}
	}
	if _, err := os.Stat(data + "/a.txt"); err != nil {
		t.Fatalf("a refused request removed a.txt: %v", err)
	}
	if _, err := os.Stat(outside + "/secret"); err != nil {
		t.Fatalf("file outside the workspace is gone: %v", err)
	}
	if err := e.svc.DeleteFiles(ctx, u, app.ID, []string{"a.txt", "dir", "storage/foto.png", "escape"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{data + "/a.txt", data + "/dir", src + "/foto.png", data + "/escape"} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s still there: %v", p, err)
		}
	}
	if _, err := os.Stat(outside + "/secret"); err != nil {
		t.Errorf("deleting the link removed its target: %v", err)
	}
	if acts := fileActs(t, e); !slices.Contains(acts, "file.delete storage/foto.png") || !slices.Contains(acts, "file.delete dir") {
		t.Errorf("activity = %v", acts)
	}
}

func TestFilesZip(t *testing.T) {
	ctx := context.Background()
	e, u, app, data, src, _ := filesEnv(t)
	other := e.addUser("andi", store.RoleUser, roomyQuota)
	if err := os.WriteFile(data+"/a.txt", []byte("halo"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, data+"/docs/sub/b.bin", 10)
	if err := os.Symlink("/etc", data+"/docs/etc-link"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, src+"/foto.png", 7)

	read := func(plan *apps.ZipPlan) map[string]int {
		t.Helper()
		var buf bytes.Buffer
		if err := plan.Write(&buf); err != nil {
			t.Fatal(err)
		}
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int{}
		for _, f := range zr.File {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			out[f.Name] = len(b)
		}
		return out
	}

	plan, err := e.svc.PrepareZip(ctx, u, app.ID, []string{"a.txt", "docs", "escape"}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	got := read(plan)
	want := map[string]int{"a.txt": 4, "docs/": 0, "docs/sub/": 0, "docs/sub/b.bin": 10}
	if !maps.Equal(got, want) {
		t.Errorf("zip = %v, want %v (links are left out)", got, want)
	}
	if plan.Name != "starboom.zip" || plan.Size != 14 {
		t.Errorf("plan name %q size %d", plan.Name, plan.Size)
	}

	// The whole workspace includes the mounted folder's files under its workspace name.
	plan, err = e.svc.PrepareZip(ctx, u, app.ID, []string{""}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(plan); got["starboom/storage/foto.png"] != 7 || got["starboom/a.txt"] != 4 {
		t.Errorf("workspace zip = %v", got)
	}
	plan, err = e.svc.PrepareZip(ctx, u, app.ID, []string{"storage"}, 1<<20)
	if err != nil || plan.Name != "storage.zip" {
		t.Fatalf("one folder: %+v %v", plan, err)
	}

	if _, err := e.svc.PrepareZip(ctx, u, app.ID, []string{"a.txt", "docs"}, 13); !errors.Is(err, apps.ErrValidation) || !strings.Contains(err.Error(), "melebihi") {
		t.Errorf("over the limit: %v", err)
	}
	for _, bad := range [][]string{{}, {"../x"}, {"escape/secret"}, {"a.txt", "docs/sub/b.bin"}, {"missing"}, {"a.txt", "a.txt"}} {
		if _, err := e.svc.PrepareZip(ctx, u, app.ID, bad, 1<<20); err == nil {
			t.Errorf("PrepareZip(%q) succeeded", bad)
		}
	}
	if _, err := e.svc.PrepareZip(ctx, other, app.ID, []string{"a.txt"}, 1<<20); !errors.Is(err, apps.ErrAppNotFound) {
		t.Errorf("another user: %v", err)
	}
}

func TestFileInfoDetectsTypeAndPreview(t *testing.T) {
	ctx := context.Background()
	e, u, app, data, _, _ := filesEnv(t)
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")
	files := map[string][]byte{
		"logo.png":   png,
		"fake.png":   []byte("<html><script>alert(1)</script></html>"),
		"icon.svg":   []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"notes.txt":  []byte("halo\ndunia\n"),
		"conf.json":  []byte(`{"a":1}`),
		"doc.pdf":    []byte("%PDF-1.7\n"),
		"data.bin":   {0, 1, 2, 3, 0, 0, 7},
		"pic.gif":    []byte("GIF89a\x01\x00\x01\x00"),
		"huge.log":   bytes.Repeat([]byte("x"), 1<<20+1),
		"photo.webp": []byte("RIFF\x00\x00\x00\x00WEBPVP8 "),
	}
	for name, b := range files {
		if err := os.WriteFile(data+"/"+name, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct{ name, mime, preview string }{
		{"logo.png", "image/png", "image"},
		{"pic.gif", "image/gif", "image"},
		{"photo.webp", "image/webp", "image"},
		{"fake.png", "text/html; charset=utf-8", "text"}, // sniffed, never trusted from the name
		{"icon.svg", "image/svg+xml", ""},                // never rendered; download only
		{"notes.txt", "text/plain; charset=utf-8", "text"},
		{"conf.json", "text/plain; charset=utf-8", "text"},
		{"doc.pdf", "application/pdf", ""},
		{"data.bin", "application/octet-stream", ""},
		{"huge.log", "text/plain; charset=utf-8", ""}, // text over 1 MB
	}
	for _, c := range cases {
		got, err := e.svc.FileInfo(ctx, u, app.ID, c.name)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got.MIME != c.mime || got.Preview != c.preview || got.Size != int64(len(files[c.name])) || got.Name != c.name {
			t.Errorf("%s: %+v, want mime %q preview %q", c.name, got, c.mime, c.preview)
		}
	}
	for _, p := range []string{"escape/secret", "escape", "../x", "", "public"} {
		if _, err := e.svc.FileInfo(ctx, u, app.ID, p); err == nil {
			t.Errorf("FileInfo(%q) succeeded", p)
		}
	}
}

func TestListFilesCountsFolderItems(t *testing.T) {
	ctx := context.Background()
	e, u, app, data, src, _ := filesEnv(t)
	writeFile(t, data+"/docs/a", 1)
	writeFile(t, data+"/docs/b", 1)
	writeFile(t, src+"/foto.png", 1)
	list, err := e.svc.ListFiles(ctx, u, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	items := map[string]int{}
	for _, f := range list {
		items[f.Name] = f.Items
	}
	if items["docs"] != 2 || items["storage"] != 1 || items["public"] != 1 {
		t.Errorf("items = %v", items)
	}
}
