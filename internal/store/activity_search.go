package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Outcome narrows a search to failed or successful entries. A failed entry is a
// refused login, the only action Runova records for something that did not happen.
type Outcome string

const (
	OutcomeAny    Outcome = ""
	OutcomeOK     Outcome = "ok"
	OutcomeFailed Outcome = "failed"
)

// failedActions are the actions counted as "gagal".
var failedActions = []string{ActLoginFailed}

// activityFields maps a search field to its SQL expression. The panel writes
// "sistem" for entries without an actor, so pelaku:sistem finds those too.
var activityFields = map[string]string{
	"aksi":   "action",
	"pelaku": "CASE WHEN actor_name = '' THEN 'sistem' ELSE actor_name END",
	"target": "target",
	"ip":     "ip",
}

// ActivityQuery is a parsed search: words and phrases are ANDed over every text
// column, -word excludes, field:value matches one column exactly (value* is a prefix),
// several values of the same field are ORed, -field:value excludes.
type ActivityQuery struct {
	Terms     []string
	Exclude   []string
	Fields    map[string][]string
	NotFields map[string][]string
	Outcome   Outcome
}

// ParseActivityQuery parses the search box syntax. Unknown field names are plain words.
func ParseActivityQuery(s string) (ActivityQuery, error) {
	var q ActivityQuery
	outcomes := map[Outcome]bool{}
	for _, tok := range tokenize(s) {
		field, value := "", tok.value
		if !tok.quoted {
			if i := strings.IndexByte(value, ':'); i > 0 {
				if f := strings.ToLower(value[:i]); f == "hasil" || activityFields[f] != "" {
					field, value = f, value[i+1:]
					if value == "" && tok.fieldQuoted != "" {
						value = tok.fieldQuoted
					}
				}
			}
		}
		if value == "" {
			continue
		}
		switch {
		case field == "hasil":
			var o Outcome
			switch strings.ToLower(value) {
			case "gagal":
				o = OutcomeFailed
			case "berhasil":
				o = OutcomeOK
			default:
				return ActivityQuery{}, errors.New("hasil: hanya berhasil atau gagal")
			}
			if tok.neg {
				o = map[Outcome]Outcome{OutcomeFailed: OutcomeOK, OutcomeOK: OutcomeFailed}[o]
			}
			outcomes[o] = true
			q.Outcome = o
		case field != "" && tok.neg:
			q.NotFields = addField(q.NotFields, field, value)
		case field != "":
			q.Fields = addField(q.Fields, field, value)
		case tok.neg:
			q.Exclude = append(q.Exclude, value)
		default:
			q.Terms = append(q.Terms, value)
		}
	}
	if len(outcomes) > 1 {
		return ActivityQuery{}, errors.New("hasil: pilih berhasil atau gagal, bukan keduanya")
	}
	return q, nil
}

func addField(m map[string][]string, k, v string) map[string][]string {
	if m == nil {
		m = map[string][]string{}
	}
	m[k] = append(m[k], v)
	return m
}

type token struct {
	neg, quoted bool
	value       string
	fieldQuoted string // for field:"a b", the quoted part
}

// tokenize splits on spaces, keeping "quoted phrases" (an unclosed quote runs to the end)
// and field:"quoted values" together.
func tokenize(s string) []token {
	var out []token
	r := []rune(s)
	for i := 0; i < len(r); {
		if r[i] == ' ' || r[i] == '\t' || r[i] == '\n' {
			i++
			continue
		}
		var t token
		if r[i] == '-' {
			t.neg = true
			i++
		}
		if i < len(r) && r[i] == '"' {
			t.quoted = true
			t.value, i = quoted(r, i)
			out = append(out, t)
			continue
		}
		start := i
		for i < len(r) && r[i] != ' ' && r[i] != '\t' && r[i] != '\n' {
			if r[i] == '"' && i > start && r[i-1] == ':' {
				t.value = string(r[start:i])
				t.fieldQuoted, i = quoted(r, i)
				break
			}
			i++
		}
		if t.fieldQuoted == "" {
			t.value = string(r[start:i])
		}
		out = append(out, t)
	}
	return out
}

// quoted reads a phrase starting at the quote r[i]; it returns the text and the index after it.
func quoted(r []rune, i int) (string, int) {
	end := i + 1
	for end < len(r) && r[end] != '"' {
		end++
	}
	v := strings.TrimSpace(string(r[i+1 : end]))
	if end < len(r) {
		end++
	}
	return v, end
}

// ActivitySearch is one page of the Activity explorer. ActorID pins the search to
// one actor (a non-admin sees only their own entries) and nothing in Query can widen it.
// From and To are a half-open range [From, To) in Unix milliseconds; 0 means unbounded.
type ActivitySearch struct {
	ActorID     *int64
	Query       ActivityQuery
	From, To    int64
	Limit       int
	Offset      int
	TZOffsetMin int  // the client's UTC offset (UTC+7 = 420), for day-aligned buckets
	WithActors  bool // include the actor facet (admins only)
}

type FacetCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type ActivityFacets struct {
	Action  []FacetCount `json:"action"`
	Actor   []FacetCount `json:"actor,omitempty"`
	Target  []FacetCount `json:"target"`
	Outcome struct {
		OK     int `json:"ok"`
		Failed int `json:"failed"`
	} `json:"outcome"`
}

type HistogramBucket struct {
	Start  int64 `json:"start"`
	Count  int   `json:"count"`
	Failed int   `json:"failed"`
}

// ActivityHistogram counts matching entries per bucket over [From, To). Buckets are
// aligned to the client's local time and include empty ones.
type ActivityHistogram struct {
	From     int64             `json:"from"`
	To       int64             `json:"to"`
	BucketMS int64             `json:"bucket_ms"`
	Buckets  []HistogramBucket `json:"buckets"`
}

type ActivityResult struct {
	Items     []Activity        `json:"items"`
	Total     int               `json:"total"`
	Limit     int               `json:"limit"`
	Offset    int               `json:"offset"`
	Facets    ActivityFacets    `json:"facets"`
	Histogram ActivityHistogram `json:"histogram"`
}

const (
	searchDefaultLimit = 50
	searchMaxLimit     = 100
	facetMax           = 20
	histogramMax       = 60
)

// bucketSizes are the histogram widths, smallest first; the first one that fits the
// range in histogramMax buckets wins.
var bucketSizes = []int64{
	1_000, 5_000, 10_000, 30_000,
	60_000, 5 * 60_000, 10 * 60_000, 15 * 60_000, 30 * 60_000,
	3_600_000, 3 * 3_600_000, 6 * 3_600_000, 12 * 3_600_000,
	86_400_000, 7 * 86_400_000, 30 * 86_400_000,
}

// likeEscape makes s a literal LIKE pattern (escape character '\').
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

const haystack = `LOWER(actor_name || char(10) || action || char(10) || target || char(10) || detail || char(10) || ip)`

// where builds the WHERE clause shared by the list, the counts and the histogram.
func (f ActivitySearch) where() (string, []any) {
	var conds []string
	var args []any
	if f.ActorID != nil {
		conds, args = append(conds, "actor_id = ?"), append(args, *f.ActorID)
	}
	if f.From > 0 {
		conds, args = append(conds, "at >= ?"), append(args, f.From)
	}
	if f.To > 0 {
		conds, args = append(conds, "at < ?"), append(args, f.To)
	}
	for _, t := range f.Query.Terms {
		conds, args = append(conds, haystack+` LIKE ? ESCAPE '\'`), append(args, "%"+likeEscape(strings.ToLower(t))+"%")
	}
	for _, t := range f.Query.Exclude {
		conds, args = append(conds, haystack+` NOT LIKE ? ESCAPE '\'`), append(args, "%"+likeEscape(strings.ToLower(t))+"%")
	}
	match := func(col, v string) (string, any) {
		if p, ok := strings.CutSuffix(v, "*"); ok {
			return "LOWER(" + col + `) LIKE ? ESCAPE '\'`, likeEscape(strings.ToLower(p)) + "%"
		}
		return "LOWER(" + col + ") = ?", strings.ToLower(v)
	}
	for _, name := range sortedKeys(f.Query.Fields) {
		var ors []string
		for _, v := range f.Query.Fields[name] {
			c, a := match(activityFields[name], v)
			ors, args = append(ors, c), append(args, a)
		}
		conds = append(conds, "("+strings.Join(ors, " OR ")+")")
	}
	for _, name := range sortedKeys(f.Query.NotFields) {
		for _, v := range f.Query.NotFields[name] {
			c, a := match(activityFields[name], v)
			conds, args = append(conds, "NOT ("+c+")"), append(args, a)
		}
	}
	switch f.Query.Outcome {
	case OutcomeFailed:
		conds = append(conds, "action IN ("+failedList()+")")
	case OutcomeOK:
		conds = append(conds, "action NOT IN ("+failedList()+")")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func failedList() string {
	q := make([]string, len(failedActions))
	for i, a := range failedActions {
		q[i] = "'" + a + "'"
	}
	return strings.Join(q, ",")
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		if activityFields[k] != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// SearchActivity returns one page of matching entries (newest first) with the total,
// facet counts and a histogram, all over the same filter.
func (s *Store) SearchActivity(ctx context.Context, f ActivitySearch) (ActivityResult, error) {
	res := ActivityResult{Limit: f.Limit, Offset: max(f.Offset, 0), Items: []Activity{}}
	switch {
	case res.Limit <= 0:
		res.Limit = searchDefaultLimit
	case res.Limit > searchMaxLimit:
		res.Limit = searchMaxLimit
	}
	where, args := f.where()
	fail := func(err error) (ActivityResult, error) {
		return ActivityResult{}, fmt.Errorf("search activity: %w", err)
	}

	var oldest, newest int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(action IN (`+failedList()+`)), 0), COALESCE(MIN(at), 0), COALESCE(MAX(at), 0) FROM activity`+where, args...).
		Scan(&res.Total, &res.Facets.Outcome.Failed, &oldest, &newest); err != nil {
		return fail(err)
	}
	res.Facets.Outcome.OK = res.Total - res.Facets.Outcome.Failed

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, at, actor_id, actor_name, action, target, detail, ip FROM activity`+where+
			` ORDER BY at DESC, id DESC LIMIT ? OFFSET ?`, append(args, res.Limit, res.Offset)...)
	if err != nil {
		return fail(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.At, &a.ActorID, &a.ActorName, &a.Action, &a.Target, &a.Detail, &a.IP); err != nil {
			return fail(err)
		}
		res.Items = append(res.Items, a)
	}
	if err := rows.Err(); err != nil {
		return fail(err)
	}

	if res.Facets.Action, err = s.facet(ctx, "action", where, args); err != nil {
		return fail(err)
	}
	if f.WithActors {
		if res.Facets.Actor, err = s.facet(ctx, activityFields["pelaku"], where, args); err != nil {
			return fail(err)
		}
	}
	if res.Facets.Target, err = s.facet(ctx, "target", where, args); err != nil {
		return fail(err)
	}
	if res.Histogram, err = s.histogram(ctx, f, where, args, oldest, newest, res.Total); err != nil {
		return fail(err)
	}
	return res, nil
}

// facet counts the facetMax most frequent non-empty values of expr, most frequent first.
func (s *Store) facet(ctx context.Context, expr, where string, args []any) ([]FacetCount, error) {
	cond := " WHERE "
	if where != "" {
		cond = where + " AND "
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+expr+` AS v, COUNT(*) AS n FROM activity`+cond+expr+` <> '' GROUP BY v ORDER BY n DESC, v LIMIT ?`,
		append(append([]any{}, args...), facetMax)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FacetCount{}
	for rows.Next() {
		var c FacetCount
		if err := rows.Scan(&c.Value, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) histogram(ctx context.Context, f ActivitySearch, where string, args []any, oldest, newest int64, total int) (ActivityHistogram, error) {
	h := ActivityHistogram{From: f.From, To: f.To, Buckets: []HistogramBucket{}}
	if h.From <= 0 {
		if total == 0 {
			return h, nil
		}
		h.From = oldest
	}
	if h.To <= 0 {
		h.To = newest + 1
	}
	if h.To <= h.From {
		return h, nil
	}
	span := h.To - h.From
	h.BucketMS = bucketSizes[len(bucketSizes)-1]
	for _, b := range bucketSizes {
		if (span+b-1)/b <= histogramMax {
			h.BucketMS = b
			break
		}
	}
	tz := int64(f.TZOffsetMin) * 60_000
	align := func(t int64) int64 {
		v := t + tz
		q := v / h.BucketMS
		if v%h.BucketMS < 0 {
			q--
		}
		return q*h.BucketMS - tz
	}
	first := align(h.From)
	n := int((h.To - first + h.BucketMS - 1) / h.BucketMS)
	h.Buckets = make([]HistogramBucket, n)
	for i := range h.Buckets {
		h.Buckets[i].Start = first + int64(i)*h.BucketMS
	}
	if total == 0 {
		return h, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT (at - ?) / ?, COUNT(*), SUM(action IN (`+failedList()+`)) FROM activity`+where+` GROUP BY 1`,
		append([]any{first, h.BucketMS}, args...)...)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		var i int64
		var c, fl int
		if err := rows.Scan(&i, &c, &fl); err != nil {
			return h, err
		}
		if i >= 0 && i < int64(n) {
			h.Buckets[i].Count, h.Buckets[i].Failed = c, fl
		}
	}
	return h, rows.Err()
}
