// Package api serves Runova's JSON API.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"runova/internal/apps"
	"runova/internal/auth"
	"runova/internal/dbs"
	"runova/internal/store"
	"runova/internal/update"
)

type Config struct {
	Store   *store.Store
	Now     func() time.Time
	DataDir string
	// Apps serves the application endpoints; without it they are not registered.
	Apps *apps.Service
	// Context bounds background work started by requests (deploys); nil means context.Background().
	Context context.Context
	// TerminalIdle closes a terminal WebSocket after this long without traffic; 0 means 30 minutes.
	TerminalIdle time.Duration
	// DBs serves the database endpoints and runs the user role hooks; without it neither exists.
	DBs *dbs.Service
	// Deploys, when set, tracks the background deploys the API starts so the
	// caller can wait for them at shutdown.
	Deploys *sync.WaitGroup
	// Static is the web panel (index.html, css/, js/, ...); without it only the API is served.
	Static fs.FS
	// Health probes Docker, Caddy and PostgreSQL for the Server page; a nil probe reports "not configured".
	Health Health
	// Update serves GET /api/update; without it the endpoint is not registered.
	Update *update.Checker
}

type server struct {
	store    *store.Store
	now      func() time.Time
	dataDir  string
	apps     *apps.Service
	ctx      context.Context
	idle     time.Duration
	sessions *auth.Sessions
	throttle *auth.Throttle
	loginMu  sync.Mutex // serializes the login throttle's check-and-count
	dbs      *dbs.Service
	deploys  *sync.WaitGroup
	stats    statsCache
	health   Health
	update   *update.Checker
}

// New builds the API handler; every route lives under /api.
func New(cfg Config) http.Handler {
	dummyHash() // pay the one-time bcrypt cost now, not on the first unknown-user login
	s := &server{
		store:    cfg.Store,
		now:      cfg.Now,
		dataDir:  cfg.DataDir,
		apps:     cfg.Apps,
		dbs:      cfg.DBs,
		deploys:  cfg.Deploys,
		ctx:      cfg.Context,
		idle:     cfg.TerminalIdle,
		health:   cfg.Health,
		update:   cfg.Update,
		sessions: &auth.Sessions{Store: cfg.Store, Now: cfg.Now},
		throttle: &auth.Throttle{Store: cfg.Store, Now: cfg.Now},
	}
	if s.deploys == nil {
		s.deploys = new(sync.WaitGroup)
	}
	if s.ctx == nil {
		s.ctx = context.Background()
	}
	if s.idle <= 0 {
		s.idle = defaultTerminalIdle
	}
	user := func(h http.HandlerFunc) http.HandlerFunc { return s.sessions.Require(false, writeError, h) }
	admin := func(h http.HandlerFunc) http.HandlerFunc { return s.sessions.Require(true, writeError, h) }

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", user(s.logout))
	mux.HandleFunc("GET /api/me", user(s.me))
	mux.HandleFunc("POST /api/me/password", user(s.changePassword))

	mux.HandleFunc("GET /api/users", admin(s.listUsers))
	mux.HandleFunc("POST /api/users", admin(s.createUser))
	mux.HandleFunc("PATCH /api/users/{id}", admin(s.updateUser))
	mux.HandleFunc("POST /api/users/{id}/password", admin(s.resetPassword))
	mux.HandleFunc("DELETE /api/users/{id}", admin(s.deleteUser))

	mux.HandleFunc("GET /api/settings", admin(s.getSettings))
	mux.HandleFunc("PUT /api/settings", admin(s.putSettings))
	mux.HandleFunc("GET /api/server", admin(s.serverStatus))
	mux.HandleFunc("GET /api/info", user(s.info))
	mux.HandleFunc("GET /api/usage", admin(s.usage))
	if s.update != nil {
		mux.HandleFunc("GET /api/update", admin(s.updateStatus))
	}

	mux.HandleFunc("GET /api/activity", user(s.activity))

	mux.HandleFunc("POST /api/uploads", user(s.upload))
	mux.HandleFunc("DELETE /api/uploads/{id}", user(s.deleteUpload))

	mux.HandleFunc("GET /api/notifications", user(s.notifications))
	mux.HandleFunc("POST /api/notifications/read", user(s.readNotifications))
	if s.apps != nil {
		s.appRoutes(mux, user)
	}
	if s.apps != nil {
		s.hostingRoutes(mux, user, admin)
	}
	if s.dbs != nil {
		s.dbRoutes(mux, user, admin)
	}

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "Tidak ditemukan")
	})
	if cfg.Static != nil {
		newPanel(cfg.Static).routes(mux)
	}
	return sameOrigin(mux)
}

// sameOrigin rejects state-changing requests whose Origin header names another host.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeError(w, http.StatusForbidden, "Origin tidak diizinkan")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

var okBody = struct {
	OK bool `json:"ok"`
}{true}

func serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "Terjadi kesalahan di server")
}

const bodyLimit = 1 << 20

// decode reads a JSON body into v; on failure it writes the error response and returns false.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Body harus berupa JSON (application/json)")
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, bodyLimit)).Decode(v); err != nil {
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			writeError(w, http.StatusRequestEntityTooLarge, "Data terlalu besar")
		} else {
			writeError(w, http.StatusBadRequest, "JSON tidak valid")
		}
		return false
	}
	return true
}

// pathID parses the {id} path value; it writes 404 and returns false when invalid.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "Pengguna tidak ditemukan")
		return 0, false
	}
	return id, true
}

// record appends an activity entry; a failure is logged but never fails the request.
func (s *server) record(r *http.Request, actor store.User, action, target, detail string) {
	a := store.Activity{
		At: s.now().UnixMilli(), ActorName: actor.Username, Action: action,
		Target: target, Detail: detail, IP: auth.ClientIP(r),
	}
	if actor.ID != 0 {
		a.ActorID = &actor.ID
	}
	// The action already happened; log it even if the client has hung up.
	if err := s.store.Log(context.WithoutCancel(r.Context()), a); err != nil {
		slog.Error("record activity", "action", action, "err", err)
	}
}
