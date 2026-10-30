package api_test

import (
	"strconv"
	"strings"
	"testing"
)

type listSummary struct {
	Apps    []struct{ ID int64 } `json:"apps"`
	Summary map[string]struct {
		Domains []struct {
			Host   string `json:"host"`
			Status string `json:"status"`
		} `json:"domains"`
		Version    int   `json:"version"`
		DeployedAt int64 `json:"deployed_at"`
	} `json:"summary"`
}

func TestListAppsSummary(t *testing.T) {
	e := newAppEnv(t, 0)
	e.addUser("budi", "budi-password-1", "user")
	e.addUser("sari", "sari-password-1", "user")
	budi := e.login("budi", "budi-password-1")
	sari := e.login("sari", "sari-password-1")

	id, _ := budi.createApp("billing", nil)
	budi.waitStatus(id, "running")
	other, _ := sari.createApp("secret-app", nil)
	sari.waitStatus(other, "running")
	if r := budi.do("POST", appPath(id, "/domains"), obj{"host": "billing.example.com"}); r.status != 201 {
		t.Fatalf("add domain: %d %s", r.status, r.body)
	}
	if r := sari.do("POST", appPath(other, "/domains"), obj{"host": "private.example.com"}); r.status != 201 {
		t.Fatalf("add domain: %d %s", r.status, r.body)
	}
	spare, _ := budi.createApp("worker", nil)

	r := budi.do("GET", "/api/apps", nil)
	var got listSummary
	r.into(t, &got)

	key := strconv.FormatInt(id, 10)
	s, ok := got.Summary[key]
	if !ok {
		t.Fatalf("no summary for app %d: %s", id, r.body)
	}
	if len(s.Domains) != 1 || s.Domains[0].Host != "billing.example.com" {
		t.Errorf("domains = %+v", s.Domains)
	}
	if s.Version != 1 || s.DeployedAt == 0 {
		t.Errorf("version %d deployed_at %d, want version 1 and a time", s.Version, s.DeployedAt)
	}
	if _, leaked := got.Summary[strconv.FormatInt(other, 10)]; leaked {
		t.Errorf("summary includes another user's app: %s", r.body)
	}
	if w := got.Summary[strconv.FormatInt(spare, 10)]; len(w.Domains) != 0 {
		t.Errorf("worker domains = %+v, want none", w.Domains)
	}
	if strings.Contains(string(r.body), "private.example.com") {
		t.Errorf("response leaks another user's domain")
	}
}
