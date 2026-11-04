package api_test

import (
	"net/url"
	"strconv"
	"testing"
	"time"

	"runova/internal/store"
)

type searchResult struct {
	Items  []store.Activity `json:"items"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
	Facets struct {
		Action  []store.FacetCount `json:"action"`
		Actor   []store.FacetCount `json:"actor"`
		Target  []store.FacetCount `json:"target"`
		Outcome struct{ OK, Failed int }
	} `json:"facets"`
	Histogram store.ActivityHistogram `json:"histogram"`
}

func TestActivitySearchScope(t *testing.T) {
	e := newEnv(t)
	admin := e.login("admin", adminPassword)
	var budiU store.User
	admin.do("POST", "/api/users", newUserBody("budi")).into(t, &budiU)
	var adminU store.User
	admin.do("GET", "/api/me", nil).into(t, &adminU)
	e.clock.advance(time.Second)
	budi := e.login("budi", "rahasia-panjang")
	e.clock.advance(time.Second)
	e.client().do("POST", "/api/login", obj{"username": "budi", "password": "salah-salah-salah"})

	get := func(c *client, query string) searchResult {
		t.Helper()
		r := c.do("GET", "/api/activity/search"+query, nil)
		if r.status != 200 {
			t.Fatalf("%s: %d %s", query, r.status, r.body)
		}
		var out searchResult
		r.into(t, &out)
		return out
	}

	// A user sees only their own entries in the list, the total, every facet and the histogram,
	// whatever the query or the actor parameter says.
	for _, q := range []string{"", "?q=" + url.QueryEscape("pelaku:admin"), "?q=" + url.QueryEscape("-pelaku:budi"), "?actor=" + strconv.FormatInt(adminU.ID, 10), "?q=user.create"} {
		r := get(budi, q)
		for _, a := range r.Items {
			if a.ActorID == nil || *a.ActorID != budiU.ID {
				t.Fatalf("%s: user sees foreign entry %+v", q, a)
			}
		}
		sum := 0
		for _, b := range r.Histogram.Buckets {
			sum += b.Count
		}
		if sum != r.Total || r.Facets.Outcome.OK+r.Facets.Outcome.Failed != r.Total {
			t.Fatalf("%s: histogram %d / outcome %+v disagree with total %d", q, sum, r.Facets.Outcome, r.Total)
		}
		if r.Facets.Actor != nil {
			t.Fatalf("%s: users get no actor facet: %+v", q, r.Facets.Actor)
		}
		for _, f := range r.Facets.Action {
			if f.Value == store.ActUserCreate {
				t.Fatalf("%s: action facet leaks the admin's entries: %+v", q, r.Facets.Action)
			}
		}
	}
	if r := get(budi, ""); r.Total != 2 || r.Facets.Outcome.Failed != 1 {
		t.Fatalf("budi = total %d outcome %+v", r.Total, r.Facets.Outcome)
	}

	// An admin sees everything, gets the actor facet and can use every filter.
	all := get(admin, "")
	if all.Total != 4 || len(all.Facets.Actor) != 2 {
		t.Fatalf("admin = total %d actors %+v", all.Total, all.Facets.Actor)
	}
	if r := get(admin, "?q="+url.QueryEscape("pelaku:budi hasil:gagal")); r.Total != 1 || r.Items[0].Action != store.ActLoginFailed {
		t.Fatalf("admin filtered = %+v", r)
	}
	if r := get(admin, "?actor="+strconv.FormatInt(budiU.ID, 10)); r.Total != 2 {
		t.Fatalf("admin actor param = %d", r.Total)
	}
	if r := get(admin, "?limit=1&offset=1"); len(r.Items) != 1 || r.Items[0].ID != all.Items[1].ID || r.Total != 4 {
		t.Fatalf("page 2 = %+v", r)
	}
	now := e.clock.Now().UnixMilli()
	if r := get(admin, "?from="+strconv.FormatInt(now, 10)); r.Total != 1 {
		t.Fatalf("from now = %d entries, want the failed login only", r.Total)
	}
	if r := get(admin, "?to="+strconv.FormatInt(now, 10)+"&tz=420"); r.Total != 3 {
		t.Fatalf("to now = %d", r.Total)
	}

	for _, q := range []string{"?limit=x", "?offset=-1", "?from=x", "?to=-1", "?tz=abc", "?tz=2000", "?actor=x", "?q=hasil:mungkin", "?from=10&to=5"} {
		if r := admin.do("GET", "/api/activity/search"+q, nil); r.status != 400 || r.errMsg(t) == "" {
			t.Errorf("%s: %d %s", q, r.status, r.body)
		}
	}
	if r := e.client().do("GET", "/api/activity/search", nil); r.status != 401 {
		t.Errorf("anonymous: %d", r.status)
	}
}
