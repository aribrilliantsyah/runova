package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"runova/internal/auth"
	"runova/internal/dbs"
	"runova/internal/store"
)

func (s *server) backupRoutes(mux *http.ServeMux, user func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/databases/{id}/backups", user(s.listBackups))
	mux.HandleFunc("POST /api/databases/{id}/backups", user(s.createBackup))
	mux.HandleFunc("GET /api/databases/{id}/backups/{bid}", user(s.downloadBackup))
	mux.HandleFunc("POST /api/databases/{id}/backups/{bid}/restore", user(s.restoreBackup))
}

// backupError maps pg_dump and pg_restore failures to 502 with their stderr, otherwise as dbError.
func backupError(w http.ResponseWriter, r *http.Request, err error) {
	if te, ok := errors.AsType[*dbs.ToolError](err); ok {
		writeError(w, http.StatusBadGateway, te.Error())
		return
	}
	dbError(w, r, err)
}

func (s *server) listBackups(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := dbID(w, r)
	if !ok {
		return
	}
	list, err := s.dbs.Backups(r.Context(), u, id)
	if err != nil {
		dbError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *server) createBackup(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := dbID(w, r)
	if !ok {
		return
	}
	b, err := s.dbs.Backup(r.Context(), u, id)
	if err != nil {
		backupError(w, r, err)
		return
	}
	d, err := s.store.Database(r.Context(), id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	s.record(r, u, store.ActDBBackup, d.Name, b.ID)
	writeJSON(w, http.StatusCreated, b)
}

func (s *server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := dbID(w, r)
	if !ok {
		return
	}
	path, err := s.dbs.BackupPath(r.Context(), u, id, r.PathValue("bid"))
	if err != nil {
		dbError(w, r, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		serverError(w, r, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(filepath.Dir(path))+"-"+filepath.Base(path)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func (s *server) restoreBackup(w http.ResponseWriter, r *http.Request) {
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
	d, err := s.store.Database(r.Context(), id)
	if err != nil || d.UserID != u.ID {
		writeError(w, http.StatusNotFound, dbNotFound)
		return
	}
	if body.Confirm != d.Name {
		writeError(w, http.StatusBadRequest, "Ketik nama database untuk konfirmasi")
		return
	}
	bid := r.PathValue("bid")
	if err := s.dbs.Restore(r.Context(), u, id, bid); err != nil {
		backupError(w, r, err)
		return
	}
	s.record(r, u, store.ActDBRestore, d.Name, bid)
	writeJSON(w, http.StatusOK, okBody)
}
