package api_test

import (
	"bytes"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"runova/internal/dbs"
	"runova/internal/pg"
	"runova/internal/store"
)

// panelDB is budi with database budi_billing holding table invoices (3 rows).
func panelDB(t *testing.T) (*dbEnv, *client, int64) {
	t.Helper()
	e := newDBEnv(t)
	_, c := e.userWithRole("budi")
	id := c.createDB("billing")
	e.pg.Users().AddTable("budi_billing", "invoices", []string{"id", "memo"}, [][]string{{"1", "a"}, {"2", "b"}, {"3", "c"}})
	return e, c, id
}

func TestTablesAndData(t *testing.T) {
	_, c, id := panelDB(t)
	var tables []pg.Table
	r := c.do("GET", dbPath(id, "/tables"), nil)
	r.into(t, &tables)
	if r.status != 200 || len(tables) != 1 || tables[0].Name != "invoices" || tables[0].Rows != 3 || len(tables[0].Columns) != 2 || !tables[0].Columns[0].PrimaryKey {
		t.Fatalf("tables = %d %s", r.status, r.body)
	}

	var page pg.Result
	r = c.do("GET", dbPath(id, "/tables/invoices/data?offset=1&limit=1"), nil)
	r.into(t, &page)
	if r.status != 200 || len(page.Rows) != 1 || *page.Rows[0][0] != "2" || !page.Truncated {
		t.Fatalf("data = %d %s", r.status, r.body)
	}
	r = c.do("GET", dbPath(id, "/tables/invoices/data"), nil)
	r.into(t, &page)
	if r.status != 200 || len(page.Rows) != 3 {
		t.Fatalf("default page = %d %s", r.status, r.body)
	}
	for _, q := range []string{"?offset=-1", "?limit=101", "?limit=abc", "?offset=x"} {
		if r := c.do("GET", dbPath(id, "/tables/invoices/data"+q), nil); r.status != 400 {
			t.Errorf("data%s = %d %s", q, r.status, r.body)
		}
	}
	for _, name := range []string{"nope", url.PathEscape("pg_authid"), url.PathEscape(`invoices"; DROP TABLE invoices; --`)} {
		r := c.do("GET", dbPath(id, "/tables/"+name+"/data"), nil)
		if r.status != 404 || r.errMsg(t) != "Tabel tidak ditemukan" {
			t.Errorf("data of %s = %d %s", name, r.status, r.body)
		}
	}
}

func TestTruncateAndDropTable(t *testing.T) {
	e, c, id := panelDB(t)
	if r := c.do("POST", dbPath(id, "/tables/invoices/truncate"), obj{"confirm": "x"}); r.status != 400 {
		t.Fatalf("truncate wrong confirm = %d %s", r.status, r.body)
	}
	if r := c.do("POST", dbPath(id, "/tables/invoices/truncate"), obj{"confirm": "invoices"}); r.status != 200 {
		t.Fatalf("truncate = %d %s", r.status, r.body)
	}
	if r := c.do("DELETE", dbPath(id, "/tables/invoices"), obj{"confirm": "invoices"}); r.status != 200 {
		t.Fatalf("drop = %d %s", r.status, r.body)
	}
	if r := c.do("DELETE", dbPath(id, "/tables/invoices"), obj{"confirm": "invoices"}); r.status != 404 {
		t.Fatalf("drop again = %d %s", r.status, r.body)
	}
	acts := activityActions(t, c, "")
	for _, want := range []string{store.ActDBTableTruncate, store.ActDBTableDrop} {
		if !slices.Contains(acts, want) {
			t.Errorf("activity %v lacks %s", acts, want)
		}
	}
	_ = e
}

func TestQueryEndpoint(t *testing.T) {
	e, c, id := panelDB(t)
	var res dbs.QueryResult
	r := c.do("POST", dbPath(id, "/query"), obj{"sql": "SELECT * FROM invoices", "mode": "read"})
	r.into(t, &res)
	if r.status != 200 || res.Error != "" || len(res.Rows) != 3 || len(res.Columns) != 2 {
		t.Fatalf("query = %d %s", r.status, r.body)
	}
	r = c.do("POST", dbPath(id, "/query"), obj{"sql": "DELETE FROM invoices"})
	r.into(t, &res)
	if r.status != 200 || !strings.Contains(res.Error, "mode Tulis") {
		t.Fatalf("default mode is read: %d %s", r.status, r.body)
	}
	res = dbs.QueryResult{}
	r = c.do("POST", dbPath(id, "/query"), obj{"sql": "DELETE FROM invoices", "mode": "write"})
	r.into(t, &res)
	if r.status != 200 || res.Error != "" || res.Affected != 1 {
		t.Fatalf("write = %d %s", r.status, r.body)
	}
	for _, body := range []obj{{"sql": "SELECT 1", "mode": "admin"}, {"sql": 5}} {
		if r := c.do("POST", dbPath(id, "/query"), body); r.status != 400 {
			t.Errorf("%v = %d %s", body, r.status, r.body)
		}
	}
	res = dbs.QueryResult{}
	r = c.do("POST", dbPath(id, "/query"), obj{"sql": "COPY invoices TO PROGRAM 'id'", "mode": "write"})
	r.into(t, &res)
	if r.status != 200 || !strings.Contains(res.Error, "COPY") {
		t.Fatalf("copy = %d %s", r.status, r.body)
	}

	long := "SELECT '" + strings.Repeat("x", 300) + "'"
	c.do("POST", dbPath(id, "/query"), obj{"sql": long})
	var list []store.Activity
	c.do("GET", "/api/activity", nil).into(t, &list)
	var logged []store.Activity
	for _, a := range list {
		if a.Action == store.ActDBQuery {
			logged = append(logged, a)
		}
	}
	if len(logged) != 5 {
		t.Fatalf("%d db.query entries: %+v", len(logged), logged)
	}
	newest := logged[0]
	if newest.Target != "budi_billing" || !strings.HasPrefix(newest.Detail, "read · ") || strings.Contains(newest.Detail, strings.Repeat("x", 200)) || !strings.Contains(newest.Detail, "1 baris") {
		t.Fatalf("newest entry = %+v", newest)
	}
	if copyEntry := logged[1]; !strings.Contains(copyEntry.Detail, "ditolak") || !strings.Contains(copyEntry.Detail, "COPY invoices") {
		t.Fatalf("refusal entry = %+v", copyEntry)
	}
	_ = e
}

func TestPanelEndpointsOwnership(t *testing.T) {
	e, _, id := panelDB(t)
	_, andi := e.userWithRole("andi")
	before := len(e.pg.CallsWithPrefix("user "))
	ct, body := restoreBody(t, "budi_billing", "PGDMP")
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", dbPath(id, "/tables"), nil},
		{"GET", dbPath(id, "/tables/invoices/data"), nil},
		{"POST", dbPath(id, "/tables/invoices/truncate"), obj{"confirm": "invoices"}},
		{"DELETE", dbPath(id, "/tables/invoices"), obj{"confirm": "invoices"}},
		{"POST", dbPath(id, "/query"), obj{"sql": "SELECT 1"}},
		{"GET", dbPath(id, "/export"), nil},
	} {
		r := andi.do(tc.method, tc.path, tc.body)
		if r.status != 404 || r.errMsg(t) != "Database tidak ditemukan" {
			t.Errorf("%s %s = %d %s", tc.method, tc.path, r.status, r.body)
		}
	}
	if r := andi.raw("POST", dbPath(id, "/restore-upload"), ct, body, nil); r.status != 404 {
		t.Errorf("restore-upload = %d %s", r.status, r.body)
	}
	if got := e.pg.CallsWithPrefix("user "); len(got) != before {
		t.Fatalf("foreign requests reached PostgreSQL: %v", got[before:])
	}
}

func restoreBody(t *testing.T, confirm, data string) (string, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if confirm != "" {
		mw.WriteField("confirm", confirm)
	}
	w, _ := mw.CreateFormFile("file", "../../etc/passwd")
	w.Write([]byte(data))
	mw.Close()
	return mw.FormDataContentType(), &buf
}

func TestRestoreUploadEndpoint(t *testing.T) {
	e, c, id := panelDB(t)
	seen := filepath.Join(t.TempDir(), "seen")
	e.dbs.PGRestore = fakeTool(t, `{ echo "$@"; echo "$PGUSER"; } > `+seen)

	ct, body := restoreBody(t, "wrong", "PGDMPxx")
	if r := c.raw("POST", dbPath(id, "/restore-upload"), ct, body, nil); r.status != 400 || !strings.Contains(r.errMsg(t), "Ketik nama database") {
		t.Fatalf("wrong confirm = %d %s", r.status, r.body)
	}
	ct, body = restoreBody(t, "", "PGDMPxx")
	if r := c.raw("POST", dbPath(id, "/restore-upload"), ct, body, nil); r.status != 400 {
		t.Fatalf("no confirm = %d %s", r.status, r.body)
	}
	ct, body = restoreBody(t, "budi_billing", "CREATE TABLE x();")
	if r := c.raw("POST", dbPath(id, "/restore-upload"), ct, body, nil); r.status != 400 || !strings.Contains(r.errMsg(t), "pg_dump -Fc") {
		t.Fatalf("plain SQL = %d %s", r.status, r.body)
	}
	if _, err := os.Stat(seen); err == nil {
		t.Fatal("pg_restore ran for a refused upload")
	}
	if r := c.do("POST", dbPath(id, "/restore-upload"), obj{"confirm": "budi_billing"}); r.status != 415 {
		t.Fatalf("json body = %d %s", r.status, r.body)
	}

	ct, body = restoreBody(t, "budi_billing", "PGDMP-real")
	if r := c.raw("POST", dbPath(id, "/restore-upload"), ct, body, nil); r.status != 200 {
		t.Fatalf("restore = %d %s", r.status, r.body)
	}
	out, _ := os.ReadFile(seen)
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	backupDir := filepath.Join(e.dataDir, "backup")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "--clean --if-exists --no-owner --role=budi -d budi_billing "+backupDir+string(filepath.Separator)+".upload-") || strings.Contains(lines[0], "passwd") || lines[1] != "budi" {
		t.Fatalf("pg_restore saw %q", out)
	}
	if entries, _ := os.ReadDir(backupDir); len(entries) != 0 {
		t.Fatalf("temporary upload left: %v", entries)
	}
	if !slices.Contains(activityActions(t, c, ""), store.ActDBRestoreUpload) {
		t.Fatal("no db.restore_upload activity")
	}
}

func TestRestoreUploadSizeLimit(t *testing.T) {
	e, c, id := panelDB(t)
	s, _ := e.store.Settings(t.Context())
	s.UploadLimitMB = 1
	if err := e.store.SetSettings(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(t.TempDir(), "ran")
	e.dbs.PGRestore = fakeTool(t, "touch "+ran)
	ct, body := restoreBody(t, "budi_billing", "PGDMP"+strings.Repeat("x", 3<<20))
	if r := c.raw("POST", dbPath(id, "/restore-upload"), ct, body, nil); r.status != 413 {
		t.Fatalf("too large = %d %s", r.status, r.body)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("pg_restore ran")
	}
}

func TestExportEndpoint(t *testing.T) {
	e, c, id := panelDB(t)
	e.dbs.PGDump = fakeTool(t, `printf "PGDMP:$PGUSER"`)
	r := c.do("GET", dbPath(id, "/export"), nil)
	if r.status != 200 || string(r.body) != "PGDMP:budi" {
		t.Fatalf("export = %d %q", r.status, r.body)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("content type %q", ct)
	}
	if cd := r.header.Get("Content-Disposition"); cd != `attachment; filename="budi_billing-20261030-090000.dump"` {
		t.Fatalf("content disposition %q", cd)
	}
	if !slices.Contains(activityActions(t, c, ""), store.ActDBExport) {
		t.Fatal("no db.export activity")
	}

	e.dbs.PGDump = fakeTool(t, "echo 'pg_dump: error: boom' >&2; exit 1")
	r = c.do("GET", dbPath(id, "/export"), nil)
	if r.status != 502 || !strings.Contains(r.errMsg(t), "boom") {
		t.Fatalf("failed export = %d %s", r.status, r.body)
	}
}
