package api

import "net/http"

// updateStatus tells an admin whether a newer Runova release exists.
func (s *server) updateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.update.Status(r.Context()))
}
