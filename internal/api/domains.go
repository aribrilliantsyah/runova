package api

import (
	"net/http"
	"strconv"

	"runova/internal/apps"
	"runova/internal/auth"
)

func (s *server) domainRoutes(mux *http.ServeMux, user func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/apps/{id}/domains", user(s.listDomains))
	mux.HandleFunc("POST /api/apps/{id}/domains", user(s.addDomain))
	mux.HandleFunc("DELETE /api/apps/{id}/domains/{did}", user(s.removeDomain))
	mux.HandleFunc("POST /api/apps/{id}/domains/{did}/check", user(s.checkDomain))
}

// domainID parses {did}; it writes 400 and returns false when invalid.
func domainID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("did"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "Domain tidak ditemukan")
		return 0, false
	}
	return id, true
}

// domainGuide lists the ways to point a domain at the server: without a
// public IP only a Cloudflare Tunnel can reach it.
func domainGuide(publicIP string) []string {
	if publicIP == "" {
		return []string{"tunnel"}
	}
	return []string{"dns", "proxy", "tunnel"}
}

func (s *server) listDomains(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	list, err := s.apps.Domains(r.Context(), u, id)
	if err != nil {
		appError(w, r, err)
		return
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Domains  any      `json:"domains"`
		PublicIP string   `json:"public_ip"`
		Guide    []string `json:"guide"`
	}{list, settings.PublicIP, domainGuide(settings.PublicIP)})
}

func (s *server) addDomain(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	var in apps.DomainInput
	if !decode(w, r, &in) {
		return
	}
	d, err := s.apps.AddDomain(r.Context(), u, id, in)
	if err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *server) removeDomain(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	did, ok := domainID(w, r)
	if !ok {
		return
	}
	if err := s.apps.RemoveDomain(r.Context(), u, id, did); err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody)
}

func (s *server) checkDomain(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	did, ok := domainID(w, r)
	if !ok {
		return
	}
	d, err := s.apps.CheckDomain(r.Context(), u, id, did)
	if err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}
