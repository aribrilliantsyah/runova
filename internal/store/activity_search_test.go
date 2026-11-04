package store_test

import (
	"context"
	"reflect"
	"testing"

	"runova/internal/store"
)

func TestParseActivityQuery(t *testing.T) {
	q, err := store.ParseActivityQuery(`deploy "billing api" -staging aksi:db.create aksi:db.delete -pelaku:budi target:"my app" ip:10.0.* hasil:gagal x:y`)
	if err != nil {
		t.Fatal(err)
	}
	want := store.ActivityQuery{
		Terms:   []string{"deploy", "billing api", "x:y"},
		Exclude: []string{"staging"},
		Fields: map[string][]string{
			"aksi":   {"db.create", "db.delete"},
			"target": {"my app"},
			"ip":     {"10.0.*"},
		},
		NotFields: map[string][]string{"pelaku": {"budi"}},
		Outcome:   store.OutcomeFailed,
	}
	if !reflect.DeepEqual(q, want) {
		t.Fatalf("parsed\n %+v\nwant\n %+v", q, want)
	}

	if q, _ := store.ParseActivityQuery("-hasil:gagal"); q.Outcome != store.OutcomeOK {
		t.Errorf("-hasil:gagal = %q, want ok", q.Outcome)
	}
	if q, _ := store.ParseActivityQuery(`  "unterminated phrase  `); !reflect.DeepEqual(q.Terms, []string{"unterminated phrase"}) {
		t.Errorf("unterminated quote terms = %q", q.Terms)
	}
	if q, _ := store.ParseActivityQuery(`- "" aksi:`); len(q.Terms)+len(q.Exclude)+len(q.Fields) != 0 {
		t.Errorf("empty pieces must be dropped: %+v", q)
	}
	for _, bad := range []string{"hasil:mungkin", "hasil:berhasil hasil:gagal"} {
		if _, err := store.ParseActivityQuery(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

// searchFixture logs a fixed set of entries for two users and a deleted/system actor.
func searchFixture(t *testing.T) (*store.Store, store.User, store.User) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	a := mustUser(t, s, "andi", store.RoleAdmin)
	b := mustUser(t, s, "budi", store.RoleUser)
	add := func(u *store.User, at int64, action, target, detail, ip string) {
		t.Helper()
		e := store.Activity{At: at, Action: action, Target: target, Detail: detail, IP: ip, ActorName: "sistem"}
		if u != nil {
			e.ActorID, e.ActorName = &u.ID, u.Username
		}
		if err := s.Log(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	add(&a, 1_000, store.ActLogin, "", "", "10.0.0.1")
	add(&a, 2_000, store.ActUserCreate, "budi", "role=user", "10.0.0.1")
	add(&b, 3_000, store.ActLoginFailed, "", "", "203.0.113.9")
	add(&b, 4_000, store.ActLogin, "", "", "203.0.113.9")
	add(&b, 5_000, store.ActDeploy, "billing", "#1", "203.0.113.9")
	add(&b, 6_000, store.ActDeploy, "billing", "#2", "203.0.113.9")
	add(&b, 7_000, store.ActDBCreate, "budi_billing", "", "203.0.113.9")
	add(&a, 8_000, store.ActDeploy, "portal", "#1 staging", "10.0.0.1")
	add(nil, 9_000, store.ActRestart, "portal", "", "")
	return s, a, b
}

func search(t *testing.T, s *store.Store, f store.ActivitySearch) store.ActivityResult {
	t.Helper()
	r, err := s.SearchActivity(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustQuery(t *testing.T, q string) store.ActivityQuery {
	t.Helper()
	p, err := store.ParseActivityQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func ats(items []store.Activity) []int64 {
	out := make([]int64, len(items))
	for i, a := range items {
		out[i] = a.At
	}
	return out
}

func TestSearchActivityFilters(t *testing.T) {
	s, _, b := searchFixture(t)
	for _, tc := range []struct {
		q    string
		want []int64
	}{
		{"", []int64{9000, 8000, 7000, 6000, 5000, 4000, 3000, 2000, 1000}},
		{"billing", []int64{7000, 6000, 5000}},                        // target and detail, substring
		{"BILLING #2", []int64{6000}},                                 // words are ANDed, case-insensitive
		{`"#1 staging"`, []int64{8000}},                               // phrase
		{"deploy -staging", []int64{6000, 5000}},                      // exclusion
		{"aksi:deploy", []int64{8000, 6000, 5000}},                    // exact field
		{"aksi:DEPLOY aksi:restart", []int64{9000, 8000, 6000, 5000}}, // same field ORs
		{"aksi:db.*", []int64{7000}},                                  // prefix
		{"aksi:dep", nil},                                             // field match is exact
		{"-aksi:deploy -aksi:login*", []int64{9000, 7000, 2000}},
		{"pelaku:budi target:billing", []int64{6000, 5000}},
		{"pelaku:sistem", []int64{9000}},
		{"ip:203.0.113.*", []int64{7000, 6000, 5000, 4000, 3000}},
		{"hasil:gagal", []int64{3000}},
		{"-hasil:gagal pelaku:budi", []int64{7000, 6000, 5000, 4000}},
		{"100%", nil},            // LIKE wildcards are literal
		{"budi_", []int64{7000}}, // "_" is literal too: only budi_billing
	} {
		r := search(t, s, store.ActivitySearch{Query: mustQuery(t, tc.q)})
		if got := ats(r.Items); !reflect.DeepEqual(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
			t.Errorf("%q: got %v, want %v", tc.q, got, tc.want)
		}
		if r.Total != len(tc.want) {
			t.Errorf("%q: total %d, want %d", tc.q, r.Total, len(tc.want))
		}
	}

	// The actor scope can never be widened by the query.
	r := search(t, s, store.ActivitySearch{ActorID: &b.ID, Query: mustQuery(t, "pelaku:andi")})
	if r.Total != 0 || len(r.Items) != 0 {
		t.Fatalf("scoped to budi, pelaku:andi = %+v", r)
	}
	r = search(t, s, store.ActivitySearch{ActorID: &b.ID})
	for _, a := range r.Items {
		if a.ActorID == nil || *a.ActorID != b.ID {
			t.Fatalf("scoped search leaked %+v", a)
		}
	}
	if r.Total != 5 {
		t.Fatalf("budi total = %d, want 5", r.Total)
	}
}

func TestSearchActivityRangeAndPaging(t *testing.T) {
	s, _, _ := searchFixture(t)
	r := search(t, s, store.ActivitySearch{From: 3000, To: 7000})
	if got := ats(r.Items); !reflect.DeepEqual(got, []int64{6000, 5000, 4000, 3000}) {
		t.Fatalf("range [3000,7000) = %v", got)
	}
	r = search(t, s, store.ActivitySearch{Limit: 4, Offset: 4})
	if got := ats(r.Items); !reflect.DeepEqual(got, []int64{5000, 4000, 3000, 2000}) || r.Total != 9 || r.Limit != 4 || r.Offset != 4 {
		t.Fatalf("page 2 of 4 = %v total %d limit %d offset %d", got, r.Total, r.Limit, r.Offset)
	}
	r = search(t, s, store.ActivitySearch{Limit: 4, Offset: 8})
	if got := ats(r.Items); !reflect.DeepEqual(got, []int64{1000}) {
		t.Fatalf("last page = %v", got)
	}
	if r = search(t, s, store.ActivitySearch{Limit: 1000}); r.Limit != 100 || len(r.Items) != 9 {
		t.Fatalf("limit is capped at 100: %d", r.Limit)
	}
	if r = search(t, s, store.ActivitySearch{}); r.Limit != 50 {
		t.Fatalf("default limit = %d, want 50", r.Limit)
	}
	if r = search(t, s, store.ActivitySearch{Offset: -5, Limit: 2}); r.Offset != 0 || len(r.Items) != 2 {
		t.Fatalf("negative offset = %d", r.Offset)
	}
}

func TestSearchActivityFacets(t *testing.T) {
	s, _, b := searchFixture(t)
	r := search(t, s, store.ActivitySearch{WithActors: true})
	wantAction := []store.FacetCount{{Value: "deploy", Count: 3}, {Value: "login", Count: 2}, {Value: "db.create", Count: 1}, {Value: "login_failed", Count: 1}, {Value: "restart", Count: 1}, {Value: "user.create", Count: 1}}
	if !reflect.DeepEqual(r.Facets.Action, wantAction) {
		t.Errorf("action facet = %+v", r.Facets.Action)
	}
	wantActor := []store.FacetCount{{Value: "budi", Count: 5}, {Value: "andi", Count: 3}, {Value: "sistem", Count: 1}}
	if !reflect.DeepEqual(r.Facets.Actor, wantActor) {
		t.Errorf("actor facet = %+v", r.Facets.Actor)
	}
	wantTarget := []store.FacetCount{{Value: "billing", Count: 2}, {Value: "portal", Count: 2}, {Value: "budi", Count: 1}, {Value: "budi_billing", Count: 1}}
	if !reflect.DeepEqual(r.Facets.Target, wantTarget) {
		t.Errorf("target facet (no empty target) = %+v", r.Facets.Target)
	}
	if r.Facets.Outcome.OK != 8 || r.Facets.Outcome.Failed != 1 {
		t.Errorf("outcome = %+v", r.Facets.Outcome)
	}

	// Facets follow the filter and the scope; the actor facet only exists when asked for.
	r = search(t, s, store.ActivitySearch{ActorID: &b.ID, Query: mustQuery(t, "aksi:deploy")})
	if r.Facets.Actor != nil {
		t.Errorf("actor facet without WithActors = %+v", r.Facets.Actor)
	}
	if !reflect.DeepEqual(r.Facets.Action, []store.FacetCount{{Value: "deploy", Count: 2}}) {
		t.Errorf("scoped action facet = %+v", r.Facets.Action)
	}
}

func TestSearchActivityHistogram(t *testing.T) {
	s, _, _ := searchFixture(t)
	const min = 60_000
	// Ten minutes of entries: a 1-minute bucket, aligned to the client's zone.
	ctx := context.Background()
	for i, at := range []int64{10 * min, 10*min + 5, 12*min + 1, 19*min + 59_999} {
		action := store.ActDeploy
		if i == 1 {
			action = store.ActLoginFailed
		}
		if err := s.Log(ctx, store.Activity{At: at, ActorName: "x", Action: action}); err != nil {
			t.Fatal(err)
		}
	}
	// One hour: 1-minute buckets, the empty ones included.
	r := search(t, s, store.ActivitySearch{From: 10 * min, To: 70 * min})
	h := r.Histogram
	if h.BucketMS != min || h.From != 10*min || h.To != 70*min {
		t.Fatalf("histogram frame = %+v", h)
	}
	if len(h.Buckets) != 60 {
		t.Fatalf("%d buckets, want 60 (zeros included)", len(h.Buckets))
	}
	if b := h.Buckets[0]; b.Start != 10*min || b.Count != 2 || b.Failed != 1 {
		t.Errorf("bucket 0 = %+v", b)
	}
	if b := h.Buckets[2]; b.Start != 12*min || b.Count != 1 || b.Failed != 0 {
		t.Errorf("bucket 2 = %+v", b)
	}
	if b := h.Buckets[1]; b.Count != 0 {
		t.Errorf("bucket 1 should be empty: %+v", b)
	}
	if b := h.Buckets[9]; b.Count != 1 {
		t.Errorf("last bucket = %+v", b)
	}

	// A day is cut into nice buckets, never more than 60.
	day := int64(24 * 60 * min)
	r = search(t, s, store.ActivitySearch{From: day, To: 2 * day})
	if r.Histogram.BucketMS != 30*min || len(r.Histogram.Buckets) != 48 {
		t.Errorf("24h: bucket %d ms × %d", r.Histogram.BucketMS, len(r.Histogram.Buckets))
	}
	// Day buckets start at the client's local midnight (UTC+7 → 17:00 UTC).
	r = search(t, s, store.ActivitySearch{From: day, To: 60 * day, TZOffsetMin: 420})
	if r.Histogram.BucketMS != day {
		t.Fatalf("59d bucket = %d", r.Histogram.BucketMS)
	}
	if got := (r.Histogram.Buckets[0].Start + 7*60*min) % day; got != 0 {
		t.Errorf("day bucket starts %d ms after local midnight", got)
	}

	// No From: the histogram starts at the oldest matching entry.
	r = search(t, s, store.ActivitySearch{To: 20 * min, Query: mustQuery(t, "aksi:deploy")})
	if r.Histogram.From != 5_000 {
		t.Errorf("open range starts at %d, want the oldest match 5000", r.Histogram.From)
	}
	// Nothing matches: an empty frame, no error.
	r = search(t, s, store.ActivitySearch{To: 20 * min, Query: mustQuery(t, "tidakada")})
	if len(r.Histogram.Buckets) != 0 || r.Total != 0 {
		t.Errorf("empty search = %+v", r.Histogram)
	}
}
