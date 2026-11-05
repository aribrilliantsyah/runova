package api

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"runova/internal/auth"
	"runova/internal/dbs"
	"runova/internal/store"
)

const (
	queryLogLen    = 200
	confirmField   = "confirm"
	maxConfirmLen  = 128
	defaultPageLen = 100
)

// Every endpoint here resolves the database through dbs, which answers 404 for
// a database the caller does not own, and runs PostgreSQL work with the
// owner's own role.
func (s *server) dbPanelRoutes(mux *http.ServeMux, user func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/databases/{id}/tables", user(s.listTables))
	mux.HandleFunc("GET /api/databases/{id}/tables/{table}/data", user(s.tableData))
	mux.HandleFunc("POST /api/databases/{id}/tables/{table}/truncate", user(s.truncateTable))
	mux.HandleFunc("DELETE /api/databases/{id}/tables/{table}", user(s.dropTable))
	mux.HandleFunc("POST /api/databases/{id}/query", user(s.runQuery))
	mux.HandleFunc("POST /api/databases/{id}/restore-upload", user(s.restoreUpload))
	mux.HandleFunc("GET /api/databases/{id}/export", user(s.exportDatabase))
}

// dbName is the name of a database the caller already proved to own.
func (s *server) dbName(r *http.Request, id int64) string {
	d, err := s.store.Database(r.Context(), id)
	if err != nil {
		return strconv.FormatInt(id, 10)
	}
	return d.Name
}

func (s *server) listTables(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := dbID(w, r)
	if !ok {
		return
	}
	tables, err := s.dbs.Tables(r.Context(), u, id)
	if err != nil {
		dbError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tables)
}

func intParam(r *http.Request, name string, def int) (int, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	return n, err == nil
}

func (s *server) tableData(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := dbID(w, r)
	if !ok {
		return
	}
	offset, ok1 := intParam(r, "offset", 0)
	limit, ok2 := intParam(r, "limit", defaultPageLen)
	if !ok1 || !ok2 {
		writeError(w, http.StatusBadRequest, "offset dan limit harus angka")
		return
	}
	res, err := s.dbs.TableRows(r.Context(), u, id, r.PathValue("table"), offset, limit)
	if err != nil {
		dbError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) tableAction(action string, do func(*server, *http.Request, store.User, int64, string, string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.FromContext(r.Context())
		id, ok := dbID(w, r)
		if !ok {
			return
		}
		var body struct {
			Confirm string `json:"confirm"`
		}
		if !decode(w, r, &body) {
			return
		}
		table := r.PathValue("table")
		if err := do(s, r, u, id, table, body.Confirm); err != nil {
			dbError(w, r, err)
			return
		}
		s.record(r, u, action, s.dbName(r, id), table)
		writeJSON(w, http.StatusOK, okBody)
	}
}

func (s *server) truncateTable(w http.ResponseWriter, r *http.Request) {
	s.tableAction(store.ActDBTableTruncate, func(s *server, r *http.Request, u store.User, id int64, table, confirm string) error {
		return s.dbs.TruncateTable(r.Context(), u, id, table, confirm)
	})(w, r)
}

func (s *server) dropTable(w http.ResponseWriter, r *http.Request) {
	s.tableAction(store.ActDBTableDrop, func(s *server, r *http.Request, u store.User, id int64, table, confirm string) error {
		return s.dbs.DropTable(r.Context(), u, id, table, confirm)
	})(w, r)
}

func (s *server) runQuery(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := dbID(w, r)
	if !ok {
		return
	}
	var body struct {
		SQL  string `json:"sql"`
		Mode string `json:"mode"`
	}
	if !decode(w, r, &body) {
		return
	}
	var write bool
	switch body.Mode {
	case "", "read":
	case "write":
		write = true
	default:
		writeError(w, http.StatusBadRequest, `mode harus "read" atau "write"`)
		return
	}
	res, err := s.dbs.Query(r.Context(), u, id, body.SQL, write)
	if err != nil {
		dbError(w, r, err)
		return
	}
	mode := "read"
	if write {
		mode = "write"
	}
	outcome := fmt.Sprintf("%d ms · %d baris", res.DurationMS, len(res.Rows))
	switch {
	case res.Error != "" && res.DurationMS == 0 && res.Command == "":
		outcome = "ditolak: " + firstLine(res.Error)
	case res.Error != "":
		outcome = fmt.Sprintf("%d ms · gagal: %s", res.DurationMS, firstLine(res.Error))
	case len(res.Columns) == 0:
		outcome = fmt.Sprintf("%d ms · %d baris terpengaruh", res.DurationMS, res.Affected)
	}
	s.record(r, u, store.ActDBQuery, s.dbName(r, id), mode+" · "+ellipsis(oneLine(body.SQL), queryLogLen)+" · "+outcome)
	writeJSON(w, http.StatusOK, res)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return ellipsis(line, queryLogLen)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// ellipsis is clip with a mark where the text was cut.
func ellipsis(s string, n int) string {
	if c := clip(s, n); c != s {
		return c + "…"
	}
	return s
}

// restoreUpload restores a pg_dump -Fc archive sent as the multipart "file"
// field, after the "confirm" field named the database. The upload limit is
// the admin's setting for uploads; the client's file name is never used.
func (s *server) restoreUpload(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := dbID(w, r)
	if !ok {
		return
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	limit := int64(settings.UploadLimitMB) << 20
	tooLarge := func() {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("File melebihi batas %d MB", settings.UploadLimitMB))
	}
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "multipart/form-data" {
		writeError(w, http.StatusUnsupportedMediaType, "Unggahan harus berupa multipart/form-data")
		return
	}
	if r.ContentLength > limit+multipartSlack {
		tooLarge()
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+multipartSlack)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Unggahan tidak valid")
		return
	}
	// "confirm" comes first in the form, so the database is checked before the
	// file is read.
	var confirm string
	var file *multipart.Part
	for file == nil {
		part, err := mr.NextPart()
		if err == io.EOF {
			writeError(w, http.StatusBadRequest, "Field \"file\" wajib berisi file dump")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "Unggahan tidak valid")
			return
		}
		switch part.FormName() {
		case confirmField:
			b, err := io.ReadAll(io.LimitReader(part, maxConfirmLen))
			if err != nil {
				writeError(w, http.StatusBadRequest, "Unggahan tidak valid")
				return
			}
			confirm = string(b)
		case uploadFieldName:
			file = part
		}
	}
	if confirm == "" {
		writeError(w, http.StatusBadRequest, "Ketik nama database untuk konfirmasi")
		return
	}
	err = s.dbs.RestoreUpload(r.Context(), u, id, confirm, file, limit)
	switch {
	case errors.Is(err, dbs.ErrTooLarge):
		tooLarge()
		return
	case err != nil:
		if _, big := errors.AsType[*http.MaxBytesError](err); big {
			tooLarge()
			return
		}
		backupError(w, r, err)
		return
	}
	s.record(r, u, store.ActDBRestoreUpload, s.dbName(r, id), "")
	writeJSON(w, http.StatusOK, okBody)
}

// exportDatabase streams a fresh pg_dump -Fc made with the owner's role. The
// headers go out with the dump's first bytes, so a dump that fails at once is
// still answered with an error.
func (s *server) exportDatabase(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := dbID(w, r)
	if !ok {
		return
	}
	name, err := s.dbs.Export(r.Context(), u, id, func(name string) io.Writer {
		h := w.Header()
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", `attachment; filename="`+name+`"`) // name is the database name and a timestamp
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		return w
	})
	switch {
	case errors.Is(err, dbs.ErrExportBroken):
		// The status already went out; aborting the response makes the client
		// see a failed download instead of a short, valid-looking file.
		slog.Warn("export broken off", "database", name, "err", err)
		panic(http.ErrAbortHandler)
	case err != nil:
		backupError(w, r, err)
		return
	}
	s.record(r, u, store.ActDBExport, s.dbName(r, id), name)
}
