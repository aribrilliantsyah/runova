package api

import (
	"net/http"
	"strconv"

	"runova/internal/auth"
)

func (s *server) hostingRoutes(mux *http.ServeMux, user, admin func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/hosting-domains", user(s.listHosting))
	mux.HandleFunc("POST /api/hosting-domains", admin(s.addHosting))
	mux.HandleFunc("PATCH /api/hosting-domains/{id}", admin(s.updateHosting))
	mux.HandleFunc("POST /api/hosting-domains/{id}/check", admin(s.checkHosting))
	mux.HandleFunc("DELETE /api/hosting-domains/{id}", admin(s.removeHosting))
	mux.HandleFunc("GET /api/hosting-domains/{id}/available", user(s.hostingAvailable))
}

// hostingID parses {id}; it writes 404 and returns false when invalid.
func hostingID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "Domain hosting tidak ditemukan")
		return 0, false
	}
	return id, true
}

func (s *server) listHosting(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	list, err := s.apps.HostingDomains(r.Context(), u)
	if err != nil {
		appError(w, r, err)
		return
	}
	used, limit, err := s.apps.SubdomainQuota(r.Context(), u)
	if err != nil {
		appError(w, r, err)
		return
	}
	type quota struct {
		Used  int `json:"used"`
		Limit int `json:"limit"`
	}
	body := struct {
		Domains  any    `json:"domains"`
		Quota    quota  `json:"quota"`
		PublicIP string `json:"public_ip,omitempty"`
	}{Quota: quota{used, limit}}
	if u.IsAdmin() {
		settings, err := s.store.Settings(r.Context())
		if err != nil {
			serverError(w, r, err)
			return
		}
		body.Domains, body.PublicIP = list, settings.PublicIP
	} else {
		slim := make([]struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}, len(list))
		for i, h := range list {
			slim[i].ID, slim[i].Name = h.ID, h.Name
		}
		body.Domains = slim
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *server) addHosting(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	var in struct {
		Name     string `json:"name"`
		Reserved string `json:"reserved"`
	}
	if !decode(w, r, &in) {
		return
	}
	h, err := s.apps.AddHostingDomain(r.Context(), u, in.Name, in.Reserved)
	if err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, h)
}

func (s *server) updateHosting(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := hostingID(w, r)
	if !ok {
		return
	}
	var in struct {
		Enabled  *bool   `json:"enabled"`
		Reserved *string `json:"reserved"`
	}
	if !decode(w, r, &in) {
		return
	}
	h, err := s.apps.UpdateHostingDomain(r.Context(), u, id, in.Enabled, in.Reserved)
	if err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *server) checkHosting(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := hostingID(w, r)
	if !ok {
		return
	}
	h, err := s.apps.CheckHostingDNS(r.Context(), u, id)
	if err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *server) removeHosting(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := hostingID(w, r)
	if !ok {
		return
	}
	if err := s.apps.RemoveHostingDomain(r.Context(), u, id); err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody)
}

func (s *server) hostingAvailable(w http.ResponseWriter, r *http.Request) {
	id, ok := hostingID(w, r)
	if !ok {
		return
	}
	available, reason, err := s.apps.HostingAvailable(r.Context(), id, r.URL.Query().Get("label"))
	if err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}{available, reason})
}
