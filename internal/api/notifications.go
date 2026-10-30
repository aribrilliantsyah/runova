package api

import (
	"net/http"
	"strconv"

	"runova/internal/auth"
)

func (s *server) notifications(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "Parameter limit tidak valid")
			return
		}
		limit = n
	}
	list, err := s.store.Notifications(r.Context(), u.ID, limit)
	if err != nil {
		serverError(w, r, err)
		return
	}
	unread, err := s.store.UnreadCount(r.Context(), u.ID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Notifications any `json:"notifications"`
		Unread        int `json:"unread"`
	}{list, unread})
}

func (s *server) readNotifications(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	if err := s.store.MarkNotificationsRead(r.Context(), u.ID); err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody)
}
