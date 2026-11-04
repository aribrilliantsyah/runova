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

// activitySearch serves the Activity explorer: a filtered page with its total, facets
// and histogram. A non-admin is always pinned to their own entries; the actor parameter
// and every query field only narrow inside that scope.
func (s *server) activitySearch(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	q := r.URL.Query()
	f := store.ActivitySearch{WithActors: u.IsAdmin()}
	ints := []struct {
		name     string
		min, max int64
		dst      func(int64)
	}{
		{"from", 0, 1 << 53, func(v int64) { f.From = v }},
		{"to", 0, 1 << 53, func(v int64) { f.To = v }},
		{"limit", 0, 1 << 31, func(v int64) { f.Limit = int(v) }},
		{"offset", 0, 1 << 31, func(v int64) { f.Offset = int(v) }},
		{"tz", -14 * 60, 14 * 60, func(v int64) { f.TZOffsetMin = int(v) }},
	}
	for _, p := range ints {
		v := q.Get(p.name)
		if v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < p.min || n > p.max {
			writeError(w, http.StatusBadRequest, "Parameter "+p.name+" tidak valid")
			return
		}
		p.dst(n)
	}
	if f.From > 0 && f.To > 0 && f.To <= f.From {
		writeError(w, http.StatusBadRequest, "Rentang waktu tidak valid: akhir harus sesudah awal")
		return
	}
	var err error
	if f.Query, err = store.ParseActivityQuery(q.Get("q")); err != nil {
		writeError(w, http.StatusBadRequest, "Pencarian tidak valid: "+err.Error())
		return
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
	res, err := s.store.SearchActivity(r.Context(), f)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
