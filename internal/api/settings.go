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
	was, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	panelChanged := false
	if s.apps != nil {
		var err error
		if panelChanged, err = s.apps.SaveSettings(r.Context(), v); err != nil {
			appError(w, r, err)
			return
		}
	} else if err := s.store.SetSettings(r.Context(), v); err != nil {
		serverError(w, r, err)
		return
	}
	saved, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	if s.update != nil && saved.UpdateCheck && !was.UpdateCheck {
		s.update.Poke()
	}
	actor := auth.FromContext(r.Context())
	s.record(r, actor, store.ActSettingsUpdate, "", "")
	if panelChanged {
		s.record(r, actor, store.ActPanelAddress, "", panelAddress(saved))
	}
	writeJSON(w, http.StatusOK, saved)
}

// panelAddress describes where the panel is reachable, for the activity log.
func panelAddress(v store.Settings) string {
	switch {
	case v.PanelDomain != "" && v.PanelPort != 0:
		return v.PanelDomain + " + :" + itoa(v.PanelPort)
	case v.PanelDomain != "":
		return v.PanelDomain
	case v.PanelPort != 0:
		return ":" + itoa(v.PanelPort)
	default:
		return "-"
	}
}
