package api

import (
	"net/http"
	"strconv"

	"runova/internal/auth"
	"runova/internal/store"
)

func (s *server) activity(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	q := r.URL.Query()
	var f store.ActivityFilter
	var err error

	if v := q.Get("before"); v != "" {
		if f.Before, err = strconv.ParseInt(v, 10, 64); err != nil || f.Before < 0 {
			writeError(w, http.StatusBadRequest, "Parameter before tidak valid")
			return
		}
	}
	if v := q.Get("limit"); v != "" {
		if f.Limit, err = strconv.Atoi(v); err != nil || f.Limit < 0 {
			writeError(w, http.StatusBadRequest, "Parameter limit tidak valid")
			return
		}
	}
	switch v := q.Get("actor"); {
	case !u.IsAdmin():
		f.ActorID = &u.ID
	case v != "":
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Parameter actor tidak valid")
			return
		}
		f.ActorID = &id
	}

	list, err := s.store.ActivityList(r.Context(), f)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
