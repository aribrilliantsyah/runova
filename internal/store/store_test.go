package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"runova/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustUser(t *testing.T, s *store.Store, username string, role store.Role) store.User {
	t.Helper()
	u, err := s.CreateUser(context.Background(), store.NewUser{
		Username: username, DisplayName: strings.ToUpper(username), Email: username + "@x.id",
		PasswordHash: "hash-" + username, Role: role, Quota: store.Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 20},
	}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestValidateUsername(t *testing.T) {
	ok := []string{"budi", "b2", "andi2026"}
	bad := []string{"", "b", "Budi", "2budi", "budi_x", "budi-x", "postgres", "runova", "root", "public", "template", strings.Repeat("a", 32)}
	for _, s := range ok {
		if err := store.ValidateUsername(s); err != nil {
			t.Errorf("ValidateUsername(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range bad {
		if err := store.ValidateUsername(s); err == nil {
			t.Errorf("ValidateUsername(%q) = nil, want error", s)
		}
	}
	if err := store.ValidateUsername(strings.Repeat("a", 31)); err != nil {
		t.Errorf("31 chars: %v", err)
	}
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		pw string
		ok bool
	}{
		{strings.Repeat("a", 9), false},
		{strings.Repeat("a", 10), true},
		{strings.Repeat("é", 9), false},
		{strings.Repeat("é", 10), true},
		{strings.Repeat("a", 200), true},
		{strings.Repeat("a", 201), false},
	}
	for _, c := range cases {
		if err := store.ValidatePassword(c.pw); (err == nil) != c.ok {
			t.Errorf("ValidatePassword(%d runes) = %v, ok want %v", len([]rune(c.pw)), err, c.ok)
		}
	}
}

func TestCreateUserConflict(t *testing.T) {
	s := newStore(t)
	mustUser(t, s, "budi", store.RoleUser)
	_, err := s.CreateUser(context.Background(), store.NewUser{Username: "budi", PasswordHash: "x", Role: store.RoleUser}, 1)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestUserLookup(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := mustUser(t, s, "budi", store.RoleUser)
	mustUser(t, s, "andi", store.RoleAdmin)

	got, hash, err := s.UserByUsername(ctx, "budi")
	if err != nil || hash != "hash-budi" || !reflect.DeepEqual(got, b) {
		t.Fatalf("UserByUsername = %+v, %q, %v", got, hash, err)
	}
	if _, _, err := s.UserByUsername(ctx, "Budi"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("case variant: err = %v, want ErrNotFound", err)
	}
	if _, err := s.User(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("User(999): err = %v", err)
	}
	all, err := s.Users(ctx)
	if err != nil || len(all) != 2 || all[0].Username != "andi" || all[1].Username != "budi" {
		t.Fatalf("Users = %+v, %v", all, err)
	}
}

func TestUpdateQuotaAndProfile(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := mustUser(t, s, "budi", store.RoleUser)

	q := store.Quota{Apps: 5, MemoryMB: 2048, CPUMilli: 2000, Databases: 4, PGConnections: 30}
	got, err := s.UpdateUser(ctx, u.ID, store.UserPatch{Quota: &q})
	if err != nil {
		t.Fatal(err)
	}
	if got.Quota != q || got.DisplayName != u.DisplayName || got.Email != u.Email || got.Role != u.Role {
		t.Fatalf("after quota patch: %+v", got)
	}
	name := "Budi Santoso"
	got, err = s.UpdateUser(ctx, u.ID, store.UserPatch{DisplayName: &name})
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != name || got.Quota != q || got.Email != u.Email {
		t.Fatalf("after name patch: %+v", got)
	}
	stored, _ := s.User(ctx, u.ID)
	if !reflect.DeepEqual(stored, got) {
		t.Fatalf("stored %+v != returned %+v", stored, got)
	}
	if _, err := s.UpdateUser(ctx, 999, store.UserPatch{DisplayName: &name}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user: err = %v", err)
	}
}

func TestLastAdminInStore(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := mustUser(t, s, "andi", store.RoleAdmin)
	user := store.RoleUser

	if _, err := s.UpdateUser(ctx, a.ID, store.UserPatch{Role: &user}); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("demote last admin: err = %v", err)
	}
	if err := s.DeleteUser(ctx, a.ID); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("delete last admin: err = %v", err)
	}
	if got, _ := s.User(ctx, a.ID); got.Role != store.RoleAdmin {
		t.Fatalf("role changed despite rejection: %v", got.Role)
	}

	b := mustUser(t, s, "bayu", store.RoleAdmin)
	if got, err := s.UpdateUser(ctx, a.ID, store.UserPatch{Role: &user}); err != nil || got.Role != store.RoleUser {
		t.Fatalf("demote with second admin: %+v, %v", got, err)
	}
	if err := s.DeleteUser(ctx, b.ID); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("delete remaining admin: err = %v", err)
	}
	if err := s.DeleteUser(ctx, a.ID); err != nil {
		t.Fatalf("delete plain user: %v", err)
	}
	if err := s.DeleteUser(ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete twice: err = %v", err)
	}
}

func TestUserIDsAreNeverReused(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustUser(t, s, "andi", store.RoleAdmin)
	b := mustUser(t, s, "budi", store.RoleUser)
	if err := s.DeleteUser(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	c := mustUser(t, s, "candra", store.RoleUser)
	if c.ID <= b.ID {
		t.Fatalf("new user got ID %d after %d was deleted; reuse would hand over UID, network and leftovers", c.ID, b.ID)
	}
}

func TestSettingsDefaults(t *testing.T) {
	s := newStore(t)
	got, err := s.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultQuota != (store.Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 20}) {
		t.Errorf("DefaultQuota = %+v", got.DefaultQuota)
	}
	if got.UploadLimitMB != 500 || got.PortMin != 20000 || got.PortMax != 20999 || got.PublicIP != "" {
		t.Errorf("settings = %+v", got)
	}
	if len(got.Runtimes) != len(store.RuntimeIDs) {
		t.Errorf("Runtimes = %v", got.Runtimes)
	}
	for _, id := range store.RuntimeIDs {
		if !got.Runtimes[id] {
			t.Errorf("runtime %s not enabled", id)
		}
	}
	if err := got.Validate(); err != nil {
		t.Errorf("defaults invalid: %v", err)
	}
}

func TestSettingsPersist(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	want, _ := s.Settings(ctx)
	want.UploadLimitMB = 80
	want.PublicIP = "203.0.113.5"
	want.DefaultQuota.Apps = 7
	want.Runtimes["java8"] = false
	if err := s.SetSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Settings(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Settings = %+v, %v; want %+v", got, err, want)
	}
	want.UploadLimitMB = 90
	if err := s.SetSettings(ctx, want); err != nil {
		t.Fatalf("second SetSettings: %v", err)
	}
	if got, _ := s.Settings(ctx); got.UploadLimitMB != 90 {
		t.Fatalf("UploadLimitMB = %d", got.UploadLimitMB)
	}
	want.PortMin = 10
	if err := s.SetSettings(ctx, want); err == nil {
		t.Fatal("SetSettings accepted invalid settings")
	}
}

func TestSettingsValidate(t *testing.T) {
	base := func() store.Settings {
		rt := map[string]bool{}
		for _, id := range store.RuntimeIDs {
			rt[id] = true
		}
		return store.Settings{
			DefaultQuota:  store.Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 20},
			UploadLimitMB: 500, MaxPendingUploads: 5, PortMin: 20000, PortMax: 20999, Runtimes: rt,
		}
	}
	cases := []struct {
		name string
		mut  func(*store.Settings)
		ok   bool
	}{
		{"base", func(*store.Settings) {}, true},
		{"min>max", func(s *store.Settings) { s.PortMin, s.PortMax = 21000, 20000 }, false},
		{"min<1024", func(s *store.Settings) { s.PortMin = 1023 }, false},
		{"max>65535", func(s *store.Settings) { s.PortMax = 65536 }, false},
		{"min==max", func(s *store.Settings) { s.PortMin, s.PortMax = 20000, 20000 }, true},
		{"upload 0", func(s *store.Settings) { s.UploadLimitMB = 0 }, false},
		{"pending 0", func(s *store.Settings) { s.MaxPendingUploads = 0 }, false},
		{"pending 51", func(s *store.Settings) { s.MaxPendingUploads = 51 }, false},
		{"pending 1", func(s *store.Settings) { s.MaxPendingUploads = 1 }, true},
		{"pending 50", func(s *store.Settings) { s.MaxPendingUploads = 50 }, true},
		{"unknown runtime", func(s *store.Settings) { s.Runtimes["java7"] = true }, false},
		{"all runtimes off", func(s *store.Settings) { s.Runtimes = map[string]bool{} }, true},
		{"ip abc", func(s *store.Settings) { s.PublicIP = "abc" }, false},
		{"ip ok", func(s *store.Settings) { s.PublicIP = "203.0.113.5" }, true},
		{"ipv6 ok", func(s *store.Settings) { s.PublicIP = "2001:db8::1" }, true},
		{"bad quota", func(s *store.Settings) { s.DefaultQuota.PGConnections = 0 }, false},
	}
	for _, c := range cases {
		s := base()
		c.mut(&s)
		if err := s.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate = %v, ok want %v", c.name, err, c.ok)
		}
	}
}

func TestQuotaValidate(t *testing.T) {
	ok := store.Quota{Apps: 0, MemoryMB: 0, CPUMilli: 0, Databases: 0, PGConnections: 1}
	if err := ok.Validate(); err != nil {
		t.Fatalf("lower bounds: %v", err)
	}
	hi := store.Quota{Apps: 100, MemoryMB: 1048576, CPUMilli: 256000, Databases: 100, PGConnections: 500}
	if err := hi.Validate(); err != nil {
		t.Fatalf("upper bounds: %v", err)
	}
	for name, mut := range map[string]func(*store.Quota){
		"pg 0":         func(q *store.Quota) { q.PGConnections = 0 },
		"pg 501":       func(q *store.Quota) { q.PGConnections = 501 },
		"apps -1":      func(q *store.Quota) { q.Apps = -1 },
		"apps 101":     func(q *store.Quota) { q.Apps = 101 },
		"mem over":     func(q *store.Quota) { q.MemoryMB = 1048577 },
		"cpu over":     func(q *store.Quota) { q.CPUMilli = 256001 },
		"db over":      func(q *store.Quota) { q.Databases = 101 },
		"cpu negative": func(q *store.Quota) { q.CPUMilli = -1 },
	} {
		q := hi
		mut(&q)
		if err := q.Validate(); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
