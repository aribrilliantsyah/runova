package api

import (
	"net/http"
	"strconv"

	"runova/internal/auth"
	"runova/internal/store"
)

func itoa(n int) string { return strconv.Itoa(n) }

func (s *server) getSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) putSettings(w http.ResponseWriter, r *http.Request) {
	var v store.Settings
	if !decode(w, r, &v) {
		return
	}
	if err := v.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SetSettings(r.Context(), v); err != nil {
		serverError(w, r, err)
		return
	}
	saved, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	s.record(r, auth.FromContext(r.Context()), store.ActSettingsUpdate, "", "")
	writeJSON(w, http.StatusOK, saved)
}
