package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"runova/internal/apps"
	"runova/internal/auth"
	"runova/internal/docker"
	"runova/internal/store"
)

const (
	appNotFound = "Aplikasi tidak ditemukan"
	statsWait   = 3 * time.Second
)

func (s *server) appRoutes(mux *http.ServeMux, user func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/apps", user(s.listApps))
	mux.HandleFunc("POST /api/apps", user(s.createApp))
	mux.HandleFunc("GET /api/apps/{id}", user(s.getApp))
	mux.HandleFunc("PATCH /api/apps/{id}", user(s.patchApp))
	mux.HandleFunc("DELETE /api/apps/{id}", user(s.deleteApp))
	mux.HandleFunc("PUT /api/apps/{id}/env", user(s.putEnv))
	mux.HandleFunc("POST /api/apps/{id}/env/{name}/reveal", user(s.revealEnv))
	mux.HandleFunc("POST /api/apps/{id}/versions", user(s.newVersion))
	mux.HandleFunc("POST /api/apps/{id}/deploy", user(s.redeploy))
	mux.HandleFunc("POST /api/apps/{id}/restart", user(s.lifecycle(s.appsRestart)))
	mux.HandleFunc("POST /api/apps/{id}/stop", user(s.lifecycle(s.appsStop)))
	mux.HandleFunc("POST /api/apps/{id}/start", user(s.lifecycle(s.appsStart)))
	mux.HandleFunc("POST /api/apps/{id}/deployments/{did}/rollback", user(s.rollback))
	mux.HandleFunc("GET /api/apps/{id}/deployments", user(s.listDeployments))
	mux.HandleFunc("GET /api/apps/{id}/logs", user(s.logs))
	mux.HandleFunc("GET /api/apps/{id}/terminal", user(s.terminal))
	s.domainRoutes(mux, user)
}

func (s *server) appsRestart(ctx context.Context, u store.User, id int64) error {
	return s.apps.Restart(ctx, u, id)
}

func (s *server) appsStop(ctx context.Context, u store.User, id int64) error {
	return s.apps.Stop(ctx, u, id)
}

func (s *server) appsStart(ctx context.Context, u store.User, id int64) error {
	return s.apps.Start(ctx, u, id)
}

// appID parses {id}; any other app, not just a malformed ID, looks the same to the caller.
func appID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, appNotFound)
		return 0, false
	}
	return id, true
}

// appError maps a service error to a response. Messages of the typed errors
// are written for the user; anything else is a server error.
func appError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, apps.ErrAppNotFound):
		writeError(w, http.StatusNotFound, appNotFound)
	case errors.Is(err, apps.ErrValidation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, apps.ErrCaddy):
		writeError(w, http.StatusBadGateway, err.Error())
	case errors.Is(err, apps.ErrQuota), errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		serverError(w, r, err)
	}
}

// deployInBackground runs a prepared deployment under the server context, so
// it outlives the request that asked for it.
func (s *server) deployInBackground(u store.User, appID, deploymentID int64) {
	go func() {
		if err := s.apps.Deploy(s.ctx, u, appID, deploymentID); err != nil {
			slog.Warn("background deploy", "app", appID, "deployment", deploymentID, "err", err)
		}
	}()
}

type usageBody struct {
	Apps     int         `json:"apps"`
	MemoryMB int         `json:"memory_mb"`
	CPUMilli int         `json:"cpu_milli"`
	Quota    store.Quota `json:"quota"`
}

func (s *server) listApps(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	var (
		list []store.App
		err  error
	)
	if u.IsAdmin() && r.URL.Query().Get("all") == "1" {
		list, err = s.store.AllApps(r.Context())
	} else {
		list, err = s.store.AppsByUser(r.Context(), u.ID)
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	used, err := s.apps.UsageOf(r.Context(), u.ID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	summary, err := s.summaries(r.Context(), list)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Apps    []store.App          `json:"apps"`
		Usage   usageBody            `json:"usage"`
		Summary map[int64]appSummary `json:"summary"`
	}{list, usageBody{used.Apps, used.MemoryMB, used.CPUMilli, u.Quota}, summary})
}

func (s *server) createApp(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	var in apps.CreateInput
	if !decode(w, r, &in) {
		return
	}
	app, d, err := s.apps.Create(r.Context(), u, in)
	if err != nil {
		appError(w, r, err)
		return
	}
	s.record(r, u, store.ActAppCreate, app.Name, string(app.Kind))
	s.deployInBackground(u, app.ID, d.ID)
	writeJSON(w, http.StatusCreated, struct {
		App        store.App        `json:"app"`
		Deployment store.Deployment `json:"deployment"`
	}{app, d})
}

// maskSecrets blanks secret values: the API never sends them except by reveal.
func maskSecrets(env []store.EnvEntry) []store.EnvEntry {
	out := make([]store.EnvEntry, len(env))
	for i, e := range env {
		if e.Secret {
			e.Value = ""
		}
		out[i] = e
	}
	return out
}

func (s *server) getApp(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	app, owner, err := s.apps.Get(r.Context(), u, id)
	if err != nil {
		appError(w, r, err)
		return
	}
	env, err := s.store.Env(r.Context(), app.ID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	var active *store.Deployment
	if app.CurrentDeploymentID != nil {
		d, err := s.store.Deployment(r.Context(), *app.CurrentDeploymentID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			serverError(w, r, err)
			return
		}
		if err == nil {
			active = &d
		}
	}
	var stats *docker.Stats
	sctx, cancel := context.WithTimeout(r.Context(), statsWait)
	defer cancel()
	switch st, running, err := s.apps.Stats(sctx, u, app.ID); {
	case err != nil:
		slog.Warn("app stats", "app", app.Name, "err", err)
	case running:
		stats = &st
	}
	writeJSON(w, http.StatusOK, struct {
		App        store.App         `json:"app"`
		Owner      string            `json:"owner"`
		Env        []store.EnvEntry  `json:"env"`
		Deployment *store.Deployment `json:"deployment"`
		Stats      *docker.Stats     `json:"stats"`
		Busy       bool              `json:"busy"`
	}{app, owner.Username, maskSecrets(env), active, stats, s.apps.Busy(app.ID)})
}

func (s *server) patchApp(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	var in apps.SettingsInput
	if !decode(w, r, &in) {
		return
	}
	app, redeploy, err := s.apps.UpdateSettings(r.Context(), u, id, in)
	if err != nil {
		appError(w, r, err)
		return
	}
	s.record(r, u, store.ActAppUpdate, app.Name, "")
	writeJSON(w, http.StatusOK, struct {
		App              store.App `json:"app"`
		RedeployRequired bool      `json:"redeploy_required"`
	}{app, redeploy})
}

func (s *server) putEnv(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	var env []store.EnvEntry
	if !decode(w, r, &env) {
		return
	}
	if err := s.apps.SetEnv(r.Context(), u, id, env); err != nil {
		appError(w, r, err)
		return
	}
	app, _, err := s.apps.Get(r.Context(), u, id)
	if err != nil {
		appError(w, r, err)
		return
	}
	s.record(r, u, store.ActEnvUpdate, app.Name, "")
	writeJSON(w, http.StatusOK, struct {
		RedeployRequired bool `json:"redeploy_required"`
	}{app.CurrentDeploymentID != nil})
}

func (s *server) revealEnv(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	value, err := s.apps.RevealEnv(r.Context(), u, id, name)
	if err != nil {
		appError(w, r, err)
		return
	}
	app, _, err := s.apps.Get(r.Context(), u, id)
	if err != nil {
		appError(w, r, err)
		return
	}
	s.record(r, u, store.ActEnvReveal, app.Name, name)
	writeJSON(w, http.StatusOK, struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}{name, value})
}

func (s *server) newVersion(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	var in struct {
		UploadID string `json:"upload_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	d, err := s.apps.NewVersion(r.Context(), u, id, in.UploadID)
	if err != nil {
		appError(w, r, err)
		return
	}
	s.deployInBackground(u, id, d.ID)
	writeJSON(w, http.StatusCreated, d)
}

func (s *server) redeploy(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	if s.apps.Busy(id) {
		appError(w, r, apps.ErrBusy)
		return
	}
	d, err := s.apps.PrepareRedeploy(r.Context(), u, id)
	if err != nil {
		appError(w, r, err)
		return
	}
	s.deployInBackground(u, id, d.ID)
	writeJSON(w, http.StatusAccepted, okBody)
}

func (s *server) rollback(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	did, err := strconv.ParseInt(r.PathValue("did"), 10, 64)
	if err != nil || did <= 0 {
		writeError(w, http.StatusBadRequest, "Deployment tidak ditemukan")
		return
	}
	if s.apps.Busy(id) {
		appError(w, r, apps.ErrBusy)
		return
	}
	nd, err := s.apps.PrepareRollback(r.Context(), u, id, did)
	if err != nil {
		appError(w, r, err)
		return
	}
	s.deployInBackground(u, id, nd.ID)
	writeJSON(w, http.StatusAccepted, okBody)
}

// lifecycle serves restart, stop and start: the work is quick, so it runs
// within the request and the answer reports failure.
func (s *server) lifecycle(do func(context.Context, store.User, int64) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.FromContext(r.Context())
		id, ok := appID(w, r)
		if !ok {
			return
		}
		if err := do(r.Context(), u, id); err != nil {
			appError(w, r, err)
			return
		}
		writeJSON(w, http.StatusAccepted, okBody)
	}
}

func (s *server) listDeployments(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	app, _, err := s.apps.Get(r.Context(), u, id)
	if err != nil {
		appError(w, r, err)
		return
	}
	list, err := s.store.Deployments(r.Context(), app.ID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *server) deleteApp(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	var in struct {
		Confirm string `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return
	}
	app, _, err := s.apps.Get(r.Context(), u, id)
	if err != nil {
		appError(w, r, err)
		return
	}
	if in.Confirm != app.Name {
		writeError(w, http.StatusBadRequest, "Ketik nama aplikasi untuk mengonfirmasi penghapusan")
		return
	}
	if err := s.apps.Delete(r.Context(), u, id); err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody)
}
