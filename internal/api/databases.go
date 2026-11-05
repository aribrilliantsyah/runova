package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"runova/internal/auth"
	"runova/internal/dbs"
	"runova/internal/store"
)

const dbNotFound = "Database tidak ditemukan"

func (s *server) dbRoutes(mux *http.ServeMux, user, admin func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/databases", user(s.listDatabases))
	mux.HandleFunc("POST /api/databases", user(s.createDatabase))
	mux.HandleFunc("POST /api/databases/password", user(s.rotateDBPassword))
	mux.HandleFunc("DELETE /api/databases/{id}", user(s.deleteDatabase))
	mux.HandleFunc("GET /api/databases/{id}/connection", user(s.databaseConnection(false)))
	mux.HandleFunc("POST /api/databases/{id}/connection/reveal", user(s.databaseConnection(true)))
	mux.HandleFunc("GET /api/admin/databases", admin(s.adminDatabases))
	s.backupRoutes(mux, user)
	s.dbPanelRoutes(mux, user)
}

// dbID parses {id}; any other database, not just a malformed ID, looks the same to the caller.
func dbID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, dbNotFound)
		return 0, false
	}
	return id, true
}

// dbError maps a dbs error to a response. Messages of the typed errors are
// written for the user; anything else is a server error.
func dbError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, dbs.ErrBackupNotFound), errors.Is(err, dbs.ErrTableNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, dbs.ErrNotFound):
		writeError(w, http.StatusNotFound, dbNotFound)
	case errors.Is(err, dbs.ErrValidation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, dbs.ErrQuota), errors.Is(err, dbs.ErrInUse), errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, dbs.ErrPGDown):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, dbs.ErrPG):
		writeError(w, http.StatusBadGateway, "PostgreSQL menolak perintah ini")
	default:
		serverError(w, r, err)
	}
}

func (s *server) listDatabases(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	list, err := s.dbs.List(r.Context(), u)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Databases []dbs.DBView `json:"databases"`
		Used      int          `json:"used"`
		Limit     int          `json:"limit"`
		PGDown    bool         `json:"pg_down"`
	}{list, len(list), u.Quota.Databases, s.dbs.PGDown(r.Context())})
}

func (s *server) createDatabase(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	var body struct {
		Suffix string `json:"suffix"`
	}
	if !decode(w, r, &body) {
		return
	}
	d, err := s.dbs.Create(r.Context(), u, body.Suffix)
	if err != nil {
		dbError(w, r, err)
		return
	}
	s.record(r, u, store.ActDBCreate, d.Name, "")
	writeJSON(w, http.StatusCreated, d)
}

func (s *server) deleteDatabase(w http.ResponseWriter, r *http.Request) {
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
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		serverError(w, r, err)
		return
	}
	if err := s.dbs.Delete(r.Context(), u, id, body.Confirm); err != nil {
		dbError(w, r, err)
		return
	}
	s.record(r, u, store.ActDBDelete, d.Name, "")
	writeJSON(w, http.StatusOK, okBody)
}

func (s *server) databaseConnection(reveal bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.FromContext(r.Context())
		id, ok := dbID(w, r)
		if !ok {
			return
		}
		info, err := s.dbs.Connection(r.Context(), u, id, reveal)
		if err != nil {
			dbError(w, r, err)
			return
		}
		if reveal {
			s.record(r, u, store.ActDBReveal, info.Database, "")
		}
		writeJSON(w, http.StatusOK, info)
	}
}

func (s *server) rotateDBPassword(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	if err := s.dbs.RotatePassword(r.Context(), u); err != nil {
		dbError(w, r, err)
		return
	}
	s.record(r, u, store.ActDBPassword, u.Username, "")
	writeJSON(w, http.StatusOK, okBody)
}

func (s *server) adminDatabases(w http.ResponseWriter, r *http.Request) {
	list, err := s.dbs.ListAll(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Databases []dbs.AdminDBView `json:"databases"`
		PGDown    bool              `json:"pg_down"`
	}{list, s.dbs.PGDown(r.Context())})
}

// userRoleHook runs a PostgreSQL role change that follows a user change. The
// user change already happened and PostgreSQL may be down, so a failure is
// logged and reported to the admins instead of failing the request; the next
// start reconciles roles.
func (s *server) userRoleHook(r *http.Request, failure string, do func() error) {
	if s.dbs == nil {
		return
	}
	if err := do(); err != nil {
		slog.Error("postgres role hook", "problem", failure, "err", err)
		s.dbs.NotifyAdmins(context.WithoutCancel(r.Context()), store.LevelError, failure, err.Error())
	}
}
