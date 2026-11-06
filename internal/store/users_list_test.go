package store_test

import (
	"context"
	"reflect"
	"testing"

	"runova/internal/store"
)

// userListFixture: four users with different usage, creation times and logins.
func userListFixture(t *testing.T) *store.Store {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	mk := func(name, display string, role store.Role, created int64) store.User {
		u, err := s.CreateUser(ctx, store.NewUser{Username: name, DisplayName: display, Email: name + "@contoh.id", PasswordHash: "h", Role: role,
			Quota: store.Quota{Apps: 5, MemoryMB: 4096, Databases: 5, PGConnections: 20}}, created)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	andi := mk("andi", "Andi Wijaya", store.RoleAdmin, 100)
	budi := mk("budi", "Budi Santoso", store.RoleUser, 300)
	citra := mk("citra", "Citra Lestari", store.RoleUser, 200)
	mk("dewi", "", store.RoleAdmin, 400)
	port := 20000
	app := func(u store.User, name string, mem int) {
		port++
		newApp(t, s, u, name, port, mem)
	}
	app(budi, "billing", 512)
	app(budi, "worker", 256)
	app(citra, "blog", 1024)
	app(andi, "portal", 128)
	if _, err := s.CreateDatabase(ctx, store.Database{UserID: citra.ID, Name: "citra_a", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDatabase(ctx, store.Database{UserID: citra.ID, Name: "citra_b", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	login := func(u store.User, at int64, action string) {
		if err := s.Log(ctx, store.Activity{At: at, ActorID: &u.ID, ActorName: u.Username, Action: action}); err != nil {
			t.Fatal(err)
		}
	}
	login(andi, 5000, store.ActLogin)
	login(budi, 7000, store.ActLogin)
	login(budi, 6000, store.ActLogin)
	login(citra, 9000, store.ActLoginFailed) // a refused login is not a login
	return s
}

func names(p store.UserPage) []string {
	out := make([]string, len(p.Items))
	for i, u := range p.Items {
		out[i] = u.Username
	}
	return out
}

func listUsers(t *testing.T, s *store.Store, f store.UserListFilter) store.UserPage {
	t.Helper()
	p, err := s.UserList(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUserListSortsEveryColumn(t *testing.T) {
	s := userListFixture(t)
	for _, tc := range []struct {
		sort, order string
		want        []string
	}{
		{"", "", []string{"andi", "budi", "citra", "dewi"}},
		{"username", "desc", []string{"dewi", "citra", "budi", "andi"}},
		{"role", "asc", []string{"andi", "dewi", "budi", "citra"}}, // admin first, then by username
		{"role", "desc", []string{"budi", "citra", "andi", "dewi"}},
		{"apps", "desc", []string{"budi", "andi", "citra", "dewi"}},
		{"memory", "desc", []string{"citra", "budi", "andi", "dewi"}},
		{"databases", "desc", []string{"citra", "andi", "budi", "dewi"}},
		{"created", "asc", []string{"andi", "citra", "budi", "dewi"}},
		{"last_login", "desc", []string{"budi", "andi", "citra", "dewi"}}, // never logged in sorts last
		{"last_login", "asc", []string{"andi", "budi", "citra", "dewi"}},  // ... in both directions
	} {
		p := listUsers(t, s, store.UserListFilter{Sort: tc.sort, Order: tc.order})
		if got := names(p); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("sort %s %s = %v, want %v", tc.sort, tc.order, got, tc.want)
		}
	}
}

func TestUserListRejectsUnknownSort(t *testing.T) {
	s := userListFixture(t)
	for _, f := range []store.UserListFilter{{Sort: "password_hash"}, {Sort: "username; DROP TABLE users"}, {Order: "sideways"}, {Role: "root"}} {
		if _, err := s.UserList(context.Background(), f); err == nil {
			t.Errorf("%+v: want an error", f)
		}
	}
}

func TestUserListFiltersPagesAndCounts(t *testing.T) {
	s := userListFixture(t)
	p := listUsers(t, s, store.UserListFilter{})
	if p.Total != 4 || p.Roles.All != 4 || p.Roles.Admin != 2 || p.Roles.User != 2 || p.Limit != 25 {
		t.Fatalf("unfiltered = total %d roles %+v limit %d", p.Total, p.Roles, p.Limit)
	}
	budi := p.Items[1]
	if budi.Usage.Apps != 2 || budi.Usage.MemoryMB != 768 || budi.Usage.Databases != 0 || budi.LastLoginAt != 7000 {
		t.Fatalf("budi row = %+v", budi)
	}
	if c := p.Items[2]; c.Usage.Databases != 2 || c.LastLoginAt != 0 {
		t.Fatalf("citra row = %+v", c)
	}

	// q matches username, display name and email, case-insensitively; role counts follow q, not role.
	p = listUsers(t, s, store.UserListFilter{Q: "SANTOSO"})
	if got := names(p); !reflect.DeepEqual(got, []string{"budi"}) || p.Roles.All != 1 || p.Roles.User != 1 {
		t.Fatalf("q=SANTOSO = %v %+v", got, p.Roles)
	}
	p = listUsers(t, s, store.UserListFilter{Q: "i", Role: store.RoleAdmin})
	if got := names(p); !reflect.DeepEqual(got, []string{"andi", "dewi"}) || p.Total != 2 || p.Roles.All != 4 || p.Roles.User != 2 {
		t.Fatalf("q=i role=admin = %v total %d %+v", got, p.Total, p.Roles)
	}
	if p = listUsers(t, s, store.UserListFilter{Q: "%"}); p.Total != 0 {
		t.Fatalf("%% must be literal, got %v", names(p))
	}

	p = listUsers(t, s, store.UserListFilter{Limit: 3, Offset: 3})
	if got := names(p); !reflect.DeepEqual(got, []string{"dewi"}) || p.Total != 4 || p.Offset != 3 {
		t.Fatalf("page 2 = %v total %d", got, p.Total)
	}
	if p = listUsers(t, s, store.UserListFilter{Limit: 500}); p.Limit != 100 {
		t.Fatalf("limit capped = %d", p.Limit)
	}
}
