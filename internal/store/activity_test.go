package store_test

import (
	"context"
	"testing"

	"runova/internal/store"
)

func TestActivityFilterAndOrder(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := mustUser(t, s, "andi", store.RoleAdmin)
	b := mustUser(t, s, "budi", store.RoleUser)
	log := func(u store.User, at int64, action string) {
		t.Helper()
		id := u.ID
		if err := s.Log(ctx, store.Activity{At: at, ActorID: &id, ActorName: u.Username, Action: action, IP: "1.1.1.1"}); err != nil {
			t.Fatal(err)
		}
	}
	log(a, 10, store.ActLogin)
	log(a, 20, store.ActUserCreate)
	log(b, 25, store.ActLogin)
	log(a, 30, store.ActLogout)

	got, err := s.ActivityList(ctx, store.ActivityFilter{ActorID: &a.ID})
	if err != nil || len(got) != 3 {
		t.Fatalf("filter A = %+v, %v", got, err)
	}
	if got[0].At != 30 || got[1].At != 20 || got[2].At != 10 {
		t.Fatalf("order = %d,%d,%d", got[0].At, got[1].At, got[2].At)
	}
	if got[0].ActorName != "andi" || got[0].IP != "1.1.1.1" || got[0].Action != store.ActLogout {
		t.Fatalf("fields = %+v", got[0])
	}
	got, _ = s.ActivityList(ctx, store.ActivityFilter{ActorID: &a.ID, Limit: 2})
	if len(got) != 2 || got[0].At != 30 {
		t.Fatalf("limit 2 = %+v", got)
	}
	got, _ = s.ActivityList(ctx, store.ActivityFilter{ActorID: &a.ID, Before: 20})
	if len(got) != 1 || got[0].At != 10 {
		t.Fatalf("before 20 = %+v", got)
	}
	got, _ = s.ActivityList(ctx, store.ActivityFilter{})
	if len(got) != 4 {
		t.Fatalf("unfiltered = %d entries", len(got))
	}
}

func TestActivityTieBreakAndLimits(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for range 250 {
		if err := s.Log(ctx, store.Activity{At: 5, ActorName: "x", Action: store.ActLoginFailed}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.ActivityList(ctx, store.ActivityFilter{})
	if len(got) != 50 {
		t.Fatalf("default limit = %d, want 50", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].ID >= got[i-1].ID {
			t.Fatalf("ties not ordered by id desc: %d then %d", got[i-1].ID, got[i].ID)
		}
	}
	if got, _ = s.ActivityList(ctx, store.ActivityFilter{Limit: 1000}); len(got) != 200 {
		t.Fatalf("limit 1000 = %d, want 200", len(got))
	}
	if got, _ = s.ActivityList(ctx, store.ActivityFilter{Limit: -3}); len(got) != 1 {
		t.Fatalf("limit -3 = %d, want 1", len(got))
	}
	if got[0].ActorID != nil {
		t.Fatalf("ActorID = %v, want nil", got[0].ActorID)
	}
}

func TestActivityKeepsNameAfterUserDeleted(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustUser(t, s, "andi", store.RoleAdmin)
	b := mustUser(t, s, "budi", store.RoleUser)
	if err := s.Log(ctx, store.Activity{At: 1, ActorID: &b.ID, ActorName: "budi", Action: store.ActLogin}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.ActivityList(ctx, store.ActivityFilter{})
	if err != nil || len(got) != 1 {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if got[0].ActorID != nil || got[0].ActorName != "budi" {
		t.Fatalf("entry = %+v", got[0])
	}
}
