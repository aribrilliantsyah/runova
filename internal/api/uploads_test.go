package api_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"runova/internal/api"
	"runova/internal/artifact"
	"runova/internal/store"
)

func jarWithMajor(t *testing.T, major uint16) []byte {
	t.Helper()
	class := []byte{0xCA, 0xFE, 0xBA, 0xBE, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(class[6:], major)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range map[string][]byte{
		"META-INF/MANIFEST.MF": []byte("Manifest-Version: 1.0\r\nMain-Class: app.Main\r\n\r\n"),
		"app/Main.class":       class,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// staticELF is a minimal x86-64 ELF header with one PT_LOAD and no interpreter.
func staticELF() []byte {
	b := make([]byte, 64+56)
	copy(b, "\x7fELF")
	b[4], b[5], b[6] = 2, 1, 1
	binary.LittleEndian.PutUint16(b[16:], 2)
	binary.LittleEndian.PutUint16(b[18:], 62)
	binary.LittleEndian.PutUint32(b[20:], 1)
	binary.LittleEndian.PutUint64(b[32:], 64)
	binary.LittleEndian.PutUint16(b[52:], 64)
	binary.LittleEndian.PutUint16(b[54:], 56)
	binary.LittleEndian.PutUint16(b[56:], 1)
	binary.LittleEndian.PutUint32(b[64:], 1)
	binary.LittleEndian.PutUint32(b[68:], 4)
	binary.LittleEndian.PutUint64(b[96:], uint64(len(b)))
	binary.LittleEndian.PutUint64(b[104:], uint64(len(b)))
	return b
}

func writeTarGz(t *testing.T, w io.Writer, files map[string][]byte) {
	t.Helper()
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		data := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(data)
	}
	tw.Close()
	gz.Close()
}

func multipartBody(t *testing.T, field, filename string, data []byte) (string, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(data)
	mw.Close()
	return mw.FormDataContentType(), &buf
}

func (c *client) upload(filename string, data []byte) resp {
	c.e.t.Helper()
	ct, body := multipartBody(c.e.t, "file", filename, data)
	return c.raw("POST", "/api/uploads", ct, body, nil)
}

type uploadResult struct {
	ID          string          `json:"id"`
	Report      artifact.Report `json:"report"`
	AllowedJava []int           `json:"allowed_java"`
}

func (e *env) tmpEntries() []string {
	e.t.Helper()
	entries, err := os.ReadDir(filepath.Join(e.dataDir, "tmp"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	return names
}

func (e *env) setSettings(mutate func(*store.Settings)) {
	e.t.Helper()
	s, err := e.store.Settings(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	mutate(&s)
	if err := e.store.SetSettings(context.Background(), s); err != nil {
		e.t.Fatal(err)
	}
}

func TestUploadJar(t *testing.T) {
	e := newEnv(t)
	c := e.login("admin", adminPassword)
	r := c.upload("app.jar", jarWithMajor(t, 61))
	if r.status != 201 {
		t.Fatalf("status = %d %s", r.status, r.body)
	}
	var got uploadResult
	r.into(t, &got)
	if len(got.ID) != 24 || got.Report.Kind != artifact.KindJar || got.Report.MinJava != 17 || !got.Report.OK() {
		t.Errorf("result = %+v", got)
	}
	if got.Report.FileName != "app.jar" || got.Report.Port != 8080 {
		t.Errorf("report = %+v", got.Report)
	}
	if !slices.Equal(got.AllowedJava, []int{17, 21, 25}) {
		t.Errorf("allowed_java = %v", got.AllowedJava)
	}
	if actions := activityActions(t, c, ""); countOf(actions, store.ActUpload) != 1 {
		t.Errorf("activity = %v", actions)
	}
	admin, _, _ := e.store.UserByUsername(context.Background(), "admin")
	up, err := e.store.Upload(context.Background(), got.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(up.ArtifactPath); err != nil {
		t.Errorf("artifact missing: %v", err)
	}
	if info, _ := os.Stat(up.Dir); info.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v", info.Mode().Perm())
	}
}

func TestUploadJavaRuntimesFiltered(t *testing.T) {
	e := newEnv(t)
	e.setSettings(func(s *store.Settings) { s.Runtimes["java21"] = false })
	c := e.login("admin", adminPassword)
	var got uploadResult
	c.upload("app.jar", jarWithMajor(t, 52)).into(t, &got)
	if !slices.Equal(got.AllowedJava, []int{8, 11, 17, 25}) {
		t.Errorf("allowed_java = %v", got.AllowedJava)
	}

	e.setSettings(func(s *store.Settings) {
		for _, id := range []string{"java17", "java25", "java8", "java11"} {
			s.Runtimes[id] = false
		}
	})
	var none uploadResult
	c.upload("app.jar", jarWithMajor(t, 61)).into(t, &none)
	if len(none.AllowedJava) != 0 || none.Report.OK() {
		t.Errorf("result = %+v", none)
	}
}

func TestUploadRuntimeDisabled(t *testing.T) {
	e := newEnv(t)
	e.setSettings(func(s *store.Settings) { s.Runtimes["static"] = false })
	c := e.login("admin", adminPassword)
	r := c.upload("server", staticELF())
	if r.status != 201 {
		t.Fatalf("status = %d %s", r.status, r.body)
	}
	var got uploadResult
	r.into(t, &got)
	if got.Report.Kind != artifact.KindStatic {
		t.Fatalf("kind = %q", got.Report.Kind)
	}
	if !slices.Contains(got.Report.Problems, "Admin menonaktifkan runtime static di server ini") {
		t.Errorf("problems = %v", got.Report.Problems)
	}
	if len(got.AllowedJava) != 0 {
		t.Errorf("allowed_java = %v", got.AllowedJava)
	}
}

func TestUploadUnknownFileIsReportedNotRejected(t *testing.T) {
	c := newEnv(t).login("admin", adminPassword)
	r := c.upload("notes.txt", []byte("hello"))
	if r.status != 201 {
		t.Fatalf("status = %d %s", r.status, r.body)
	}
	var got uploadResult
	r.into(t, &got)
	if got.Report.OK() {
		t.Errorf("report = %+v", got.Report)
	}
}

func TestUploadTooLarge(t *testing.T) {
	e := newEnv(t)
	e.setSettings(func(s *store.Settings) { s.UploadLimitMB = 1 })
	c := e.login("admin", adminPassword)
	for name, size := range map[string]int{"just over": 1<<20 + 1, "double": 2 << 20} {
		r := c.upload("big.bin", make([]byte, size))
		if r.status != 413 || r.errMsg(t) != "File melebihi batas 1 MB" {
			t.Errorf("%s: status = %d %s", name, r.status, r.body)
		}
		if got := e.tmpEntries(); len(got) != 0 {
			t.Errorf("%s: tmp = %v", name, got)
		}
	}
	if r := c.upload("ok.bin", make([]byte, 1<<20)); r.status != 201 {
		t.Errorf("exactly at limit: status = %d %s", r.status, r.body)
	}
}

func TestUploadPendingLimitPerUser(t *testing.T) {
	e := newEnv(t)
	e.setSettings(func(s *store.Settings) { s.MaxPendingUploads = 2 })
	e.addUser("budi", "budi-password-1", store.RoleUser)
	c := e.login("admin", adminPassword)
	var first uploadResult
	for i := range 2 {
		r := c.upload("a.bin", []byte("x"))
		if r.status != 201 {
			t.Fatalf("upload %d: %d %s", i, r.status, r.body)
		}
		if i == 0 {
			r.into(t, &first)
		}
	}
	before := e.tmpEntries()
	r := c.upload("a.bin", []byte("x"))
	if r.status != 409 || r.errMsg(t) != "Hapus unggahan lama dulu (maksimal 2)" {
		t.Fatalf("third upload: %d %s", r.status, r.body)
	}
	if got := e.tmpEntries(); len(got) != len(before) {
		t.Errorf("rejected upload left files: %v -> %v", before, got)
	}

	if r := e.login("budi", "budi-password-1").upload("a.bin", []byte("x")); r.status != 201 {
		t.Errorf("another user is limited by someone else's uploads: %d %s", r.status, r.body)
	}
	if r := c.do("DELETE", "/api/uploads/"+first.ID, nil); r.status != 200 {
		t.Fatalf("delete: %d", r.status)
	}
	if r := c.upload("a.bin", []byte("x")); r.status != 201 {
		t.Errorf("after deleting one: %d %s", r.status, r.body)
	}
}

func TestUploadBodyOverLimitWithoutLengthIsRejected(t *testing.T) {
	e := newEnv(t)
	e.setSettings(func(s *store.Settings) { s.UploadLimitMB = 1 })
	c := e.login("admin", adminPassword)
	ct, body := multipartBody(t, "file", "big.bin", make([]byte, 4<<20))
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/uploads", io.MultiReader(body))
	req.Header.Set("Content-Type", ct)
	res, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 413 {
		t.Errorf("status = %d", res.StatusCode)
	}
	if got := e.tmpEntries(); len(got) != 0 {
		t.Errorf("tmp = %v", got)
	}
}

func TestUploadAbortCleansUp(t *testing.T) {
	e := newEnv(t)
	c := e.login("admin", adminPassword)

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		w, _ := mw.CreateFormFile("file", "app.jar")
		w.Write(bytes.Repeat([]byte{7}, 64<<10))
		time.Sleep(100 * time.Millisecond)
		pw.CloseWithError(io.ErrClosedPipe)
	}()
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/uploads", pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if res, err := c.c.Do(req); err == nil {
		res.Body.Close()
		if res.StatusCode == 201 {
			t.Fatalf("aborted upload succeeded")
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for len(e.tmpEntries()) != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := e.tmpEntries(); len(got) != 0 {
		t.Errorf("tmp = %v", got)
	}
	left, err := e.store.ExpiredUploads(context.Background(), 1<<60)
	if err != nil || len(left) != 0 {
		t.Errorf("upload rows = %+v, err = %v", left, err)
	}
}

func TestUploadRequiresLoginAndMultipart(t *testing.T) {
	e := newEnv(t)
	ct, body := multipartBody(t, "file", "a.jar", jarWithMajor(t, 61))
	if r := e.client().raw("POST", "/api/uploads", ct, body, nil); r.status != 401 {
		t.Errorf("anonymous: status = %d", r.status)
	}
	c := e.login("admin", adminPassword)
	if r := c.do("POST", "/api/uploads", obj{"x": 1}); r.status != 415 {
		t.Errorf("json body: status = %d", r.status)
	}
	ct, body = multipartBody(t, "other", "a.jar", jarWithMajor(t, 61))
	if r := c.raw("POST", "/api/uploads", ct, body, nil); r.status != 400 {
		t.Errorf("wrong field: status = %d %s", r.status, r.body)
	}
	if got := e.tmpEntries(); len(got) != 0 {
		t.Errorf("tmp = %v", got)
	}
}

func TestUploadOwnership(t *testing.T) {
	e := newEnv(t)
	e.addUser("andi", "andi-password-1", store.RoleUser)
	e.addUser("budi", "budi-password-1", store.RoleUser)
	a := e.login("andi", "andi-password-1")
	b := e.login("budi", "budi-password-1")

	var got uploadResult
	a.upload("app.jar", jarWithMajor(t, 61)).into(t, &got)

	if r := b.do("DELETE", "/api/uploads/"+got.ID, nil); r.status != 404 {
		t.Fatalf("other user DELETE: status = %d", r.status)
	}
	if entries := e.tmpEntries(); !slices.Equal(entries, []string{got.ID}) {
		t.Fatalf("tmp after foreign delete = %v", entries)
	}
	if r := a.do("DELETE", "/api/uploads/"+got.ID, nil); r.status != 200 {
		t.Fatalf("owner DELETE: status = %d %s", r.status, r.body)
	}
	if entries := e.tmpEntries(); len(entries) != 0 {
		t.Errorf("tmp after delete = %v", entries)
	}
	if r := a.do("DELETE", "/api/uploads/"+got.ID, nil); r.status != 404 {
		t.Errorf("second DELETE: status = %d", r.status)
	}
}

func TestUploadFromTarGz(t *testing.T) {
	e := newEnv(t)
	c := e.login("admin", adminPassword)
	var tgz bytes.Buffer
	writeTarGz(t, &tgz, map[string][]byte{"server": staticELF(), ".env.example": []byte("PORT=7000\n# secret\nAPI_KEY=\n")})
	var got uploadResult
	r := c.upload("rilis.tar.gz", tgz.Bytes())
	if r.status != 201 {
		t.Fatalf("status = %d %s", r.status, r.body)
	}
	r.into(t, &got)
	if got.Report.Kind != artifact.KindStatic || got.Report.Port != 7000 || got.Report.FileName != "server" {
		t.Errorf("report = %+v", got.Report)
	}
	if len(got.Report.Env) != 2 || !got.Report.Env[0].Secret {
		t.Errorf("env = %+v", got.Report.Env)
	}
}

func TestCleanupExpiredUploads(t *testing.T) {
	e := newEnv(t)
	c := e.login("admin", adminPassword)
	var old, fresh uploadResult
	c.upload("old.jar", jarWithMajor(t, 61)).into(t, &old)
	e.clock.advance(24 * time.Hour)
	c.upload("new.jar", jarWithMajor(t, 61)).into(t, &fresh)
	e.clock.advance(time.Hour)

	if err := api.CleanupUploads(context.Background(), e.store, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if entries := e.tmpEntries(); !slices.Equal(entries, []string{fresh.ID}) {
		t.Errorf("tmp = %v, want only %s", entries, fresh.ID)
	}
	admin, _, _ := e.store.UserByUsername(context.Background(), "admin")
	if _, err := e.store.Upload(context.Background(), old.ID, admin.ID); err == nil {
		t.Error("expired upload row still present")
	}
	if _, err := e.store.Upload(context.Background(), fresh.ID, admin.ID); err != nil {
		t.Errorf("fresh upload row: %v", err)
	}
}
