package store_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"runova/internal/store"
)

func TestDatabaseUniqueAndRestrict(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := mustUser(t, s, "budi", store.RoleUser)
	d, err := s.CreateDatabase(ctx, store.Database{UserID: u.ID, Name: "budi_billing", CreatedAt: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDatabase(ctx, store.Database{UserID: u.ID, Name: "budi_billing"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate name: err = %v", err)
	}

	app := newApp(t, s, u, "billing-api", 20000, 256)
	if err := s.SetAppDatabase(ctx, app.ID, &d.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AppDatabaseID(ctx, app.ID); err != nil || got == nil || *got != d.ID {
		t.Fatalf("AppDatabaseID = %v, %v", got, err)
	}
	if err := s.DeleteDatabase(ctx, d.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("delete database in use: err = %v", err)
	}
	links, err := s.DatabaseApps(ctx, u.ID)
	if err != nil || !reflect.DeepEqual(links, []store.DBApp{{DatabaseID: d.ID, AppID: app.ID, AppName: "billing-api"}}) {
		t.Fatalf("DatabaseApps = %v, %v", links, err)
	}

	if err := s.SetAppDatabase(ctx, app.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDatabase(ctx, d.ID); err != nil {
		t.Fatalf("delete unused database: %v", err)
	}
	if _, err := s.Database(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: err = %v", err)
	}
}

func TestDeleteUserRemovesDatabasesAndBoundApps(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := mustUser(t, s, "budi", store.RoleUser)
	mustUser(t, s, "root1", store.RoleAdmin)
	d, err := s.CreateDatabase(ctx, store.Database{UserID: u.ID, Name: "budi_billing"})
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(t, s, u, "billing-api", 20000, 256)
	if err := s.SetAppDatabase(ctx, app.ID, &d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser with bound database: %v", err)
	}
	if _, err := s.Database(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("database survived its user: err = %v", err)
	}
}

func TestPGPasswordRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := mustUser(t, s, "budi", store.RoleUser)
	if got, err := s.PGPassword(ctx, u.ID); err != nil || got != "" {
		t.Fatalf("fresh PGPassword = %q, %v", got, err)
	}
	if err := s.SetPGPassword(ctx, u.ID, "sealed"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.PGPassword(ctx, u.ID); got != "sealed" {
		t.Fatalf("PGPassword = %q", got)
	}
	if err := s.SetPGPassword(ctx, 999, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user: err = %v", err)
	}
}

func TestSettingsPGAllowedNets(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	v, err := s.Settings(ctx)
	if err != nil || v.PGAllowedNets == nil || len(v.PGAllowedNets) != 0 {
		t.Fatalf("default nets = %#v, %v", v.PGAllowedNets, err)
	}
	v.PGAllowedNets = []string{"10.20.0.0/16", "2001:db8::/32"}
	if err := s.SetSettings(ctx, v); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Settings(ctx); !reflect.DeepEqual(got.PGAllowedNets, v.PGAllowedNets) {
		t.Fatalf("nets = %v", got.PGAllowedNets)
	}
	for _, bad := range [][]string{{"10.0.0.1"}, {"nope"}, {"10.0.0.0/33"}} {
		v.PGAllowedNets = bad
		if err := s.SetSettings(ctx, v); err == nil {
			t.Errorf("nets %v accepted", bad)
		}
	}
	v.PGAllowedNets = make([]string, store.MaxPGAllowedNets+1)
	for i := range v.PGAllowedNets {
		v.PGAllowedNets[i] = "10.0.0.0/8"
	}
	if err := s.SetSettings(ctx, v); err == nil {
		t.Error("21 nets accepted")
	}
}

func TestSettingsTrustedProxies(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	v, err := s.Settings(ctx)
	if err != nil || v.TrustedProxies == nil || len(v.TrustedProxies) != 0 {
		t.Fatalf("default proxies = %#v, %v", v.TrustedProxies, err)
	}
	v.TrustedProxies = []string{"10.0.0.1", "10.20.0.0/16", "2001:db8::/32"}
	if err := s.SetSettings(ctx, v); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Settings(ctx); !reflect.DeepEqual(got.TrustedProxies, v.TrustedProxies) {
		t.Fatalf("proxies = %v", got.TrustedProxies)
	}
	v.TrustedProxies = nil
	if err := s.SetSettings(ctx, v); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Settings(ctx); got.TrustedProxies == nil || len(got.TrustedProxies) != 0 {
		t.Fatalf("cleared proxies = %#v", got.TrustedProxies)
	}
	for _, bad := range [][]string{{"nope"}, {"10.0.0.0/33"}, {"0.0.0.0/0"}, {"::/0"}, {"0.0.0.0/00"}, {""}} {
		v.TrustedProxies = bad
		if err := s.SetSettings(ctx, v); err == nil {
			t.Errorf("proxies %v accepted", bad)
		}
	}
	v.TrustedProxies = make([]string, store.MaxTrustedProxies+1)
	for i := range v.TrustedProxies {
		v.TrustedProxies[i] = "10.0.0.1"
	}
	if err := s.SetSettings(ctx, v); err == nil {
		t.Error("11 proxies accepted")
	}
	v.TrustedProxies = []string{"0.0.0.0/0"}
	if err := s.SetSettings(ctx, v); err == nil || !strings.Contains(err.Error(), "seluruh internet") {
		t.Errorf("0.0.0.0/0 error = %v, want a message about the whole internet", err)
	}
}
