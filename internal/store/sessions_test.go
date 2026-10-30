package store_test

import (
	"context"
	"errors"
	"testing"

	"runova/internal/store"
)

func TestSessionExpiry(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := mustUser(t, s, "budi", store.RoleUser)
	if err := s.CreateSession(ctx, "h1", u.ID, 1000); err != nil {
		t.Fatal(err)
	}
	got, err := s.SessionUser(ctx, "h1", 999)
	if err != nil || got.ID != u.ID || got.Username != "budi" {
		t.Fatalf("SessionUser(999) = %+v, %v", got, err)
	}
	if _, err := s.SessionUser(ctx, "h1", 1000); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SessionUser(1000): err = %v, want ErrNotFound", err)
	}
	if _, err := s.SessionUser(ctx, "nope", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown token: err = %v", err)
	}
}

func TestDeleteSessions(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := mustUser(t, s, "budi", store.RoleUser)
	for _, c := range []struct {
		hash string
		exp  int64
	}{{"old", 100}, {"live", 5000}, {"gone", 5000}} {
		if err := s.CreateSession(ctx, c.hash, u.ID, c.exp); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteSession(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "gone", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted session still valid: %v", err)
	}
	if err := s.DeleteExpiredSessions(ctx, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "old", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired session survived cleanup: %v", err)
	}
	if _, err := s.SessionUser(ctx, "live", 1000); err != nil {
		t.Fatalf("live session removed: %v", err)
	}
}

func TestSessionGoneAfterSetPasswordAndDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := mustUser(t, s, "andi", store.RoleAdmin)
	b := mustUser(t, s, "budi", store.RoleUser)
	c := mustUser(t, s, "cici", store.RoleUser)
	for hash, id := range map[string]int64{"a": a.ID, "b": b.ID, "c": c.ID} {
		if err := s.CreateSession(ctx, hash, id, 5000); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetPassword(ctx, b.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "b", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after SetPassword: err = %v", err)
	}
	if _, hash, _ := s.UserByUsername(ctx, "budi"); hash != "new-hash" {
		t.Fatalf("hash = %q", hash)
	}
	if err := s.DeleteUser(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "c", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after DeleteUser: err = %v", err)
	}
	if _, err := s.SessionUser(ctx, "a", 1); err != nil {
		t.Fatalf("unrelated session removed: %v", err)
	}
	if err := s.SetPassword(ctx, 999, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetPassword unknown user: err = %v", err)
	}
}

func TestLoginFailuresWindow(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, at := range []int64{100, 200, 300} {
		if err := s.RecordLoginFailure(ctx, "budi", "1.1.1.1", at); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordLoginFailure(ctx, "budi", "2.2.2.2", 250); err != nil {
		t.Fatal(err)
	}
	n, last, err := s.LoginFailures(ctx, "budi", "1.1.1.1", 150)
	if err != nil || n != 2 || last != 300 {
		t.Fatalf("LoginFailures = %d, %d, %v; want 2, 300", n, last, err)
	}
	n, last, _ = s.LoginFailures(ctx, "budi", "2.2.2.2", 0)
	if n != 1 || last != 250 {
		t.Fatalf("other ip = %d, %d", n, last)
	}
	if n, last, _ = s.LoginFailures(ctx, "budi", "1.1.1.1", 1000); n != 0 || last != 0 {
		t.Fatalf("empty window = %d, %d", n, last)
	}
	if err := s.ClearLoginFailures(ctx, "budi", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if n, _, _ = s.LoginFailures(ctx, "budi", "1.1.1.1", 0); n != 0 {
		t.Fatalf("after clear = %d", n)
	}
	if n, _, _ = s.LoginFailures(ctx, "budi", "2.2.2.2", 0); n != 1 {
		t.Fatalf("clear touched another ip: %d", n)
	}
}

func TestPruneLoginFailures(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, at := range []int64{100, 200, 300} {
		s.RecordLoginFailure(ctx, "budi", "1.1.1.1", at)
	}
	if err := s.PruneLoginFailures(ctx, 250); err != nil {
		t.Fatal(err)
	}
	if n, last, _ := s.LoginFailures(ctx, "budi", "1.1.1.1", 0); n != 1 || last != 300 {
		t.Fatalf("after prune = %d, %d", n, last)
	}
}
