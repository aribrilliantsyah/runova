package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"runova/internal/store"
)

func TestAddDomainUniqueAcrossApps(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := mustUser(t, s, "andi", store.RoleUser)
	b := mustUser(t, s, "budi", store.RoleUser)
	appA := newApp(t, s, a, "one", 20000, 128)
	appB := newApp(t, s, b, "two", 20001, 128)

	d, err := s.AddDomain(ctx, appA.ID, "x.co", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID == 0 || d.AppID != appA.ID || d.Host != "x.co" || d.Status != store.DomainPending || d.CreatedAt != 5000 || d.CheckedAt != 0 {
		t.Fatalf("domain = %+v", d)
	}
	if _, err := s.AddDomain(ctx, appB.ID, "x.co", 5001); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same host on another app: %v, want ErrConflict", err)
	}
	if _, err := s.AddDomain(ctx, appA.ID, "x.co", 5001); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same host on the same app: %v, want ErrConflict", err)
	}
}

func TestAddDomainLimitPerApp(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	app := newApp(t, s, u, "one", 20000, 128)
	other := newApp(t, s, u, "two", 20001, 128)
	for i := range store.MaxDomainsPerApp {
		if _, err := s.AddDomain(ctx, app.ID, fmt.Sprintf("d%d.co", i), 1); err != nil {
			t.Fatalf("domain %d: %v", i, err)
		}
	}
	if _, err := s.AddDomain(ctx, app.ID, "extra.co", 1); !errors.Is(err, store.ErrDomainLimit) {
		t.Fatalf("sixth domain: %v, want ErrDomainLimit", err)
	}
	if _, err := s.AddDomain(ctx, other.ID, "extra.co", 1); err != nil {
		t.Fatalf("the limit is per app: %v", err)
	}
}

func TestRemoveDomainChecksOwnerApp(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	appA := newApp(t, s, u, "one", 20000, 128)
	appB := newApp(t, s, u, "two", 20001, 128)
	d, _ := s.AddDomain(ctx, appA.ID, "x.co", 1)

	if err := s.RemoveDomain(ctx, appB.ID, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("remove through another app: %v, want ErrNotFound", err)
	}
	if err := s.RemoveDomain(ctx, appA.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Domains(ctx, appA.ID); len(got) != 0 {
		t.Fatalf("domains after remove: %+v", got)
	}
	if _, err := s.AddDomain(ctx, appB.ID, "x.co", 2); err != nil {
		t.Fatalf("host not free after remove: %v", err)
	}
}

func TestDomainsListedAndCascaded(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	appA := newApp(t, s, u, "one", 20000, 128)
	appB := newApp(t, s, u, "two", 20001, 128)
	s.AddDomain(ctx, appA.ID, "b.co", 1)
	s.AddDomain(ctx, appA.ID, "a.co", 2)
	s.AddDomain(ctx, appB.ID, "c.co", 3)

	got, err := s.Domains(ctx, appA.ID)
	if err != nil || len(got) != 2 || got[0].Host != "a.co" || got[1].Host != "b.co" {
		t.Fatalf("Domains = %+v, %v; want a.co, b.co", got, err)
	}
	all, err := s.AllDomains(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("AllDomains = %+v, %v", all, err)
	}
	if err := s.DeleteApp(ctx, appA.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.AllDomains(ctx); len(all) != 1 || all[0].Host != "c.co" {
		t.Fatalf("domains after deleting the app: %+v", all)
	}
}

func TestSetDomainStatus(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	app := newApp(t, s, u, "one", 20000, 128)
	d, _ := s.AddDomain(ctx, app.ID, "x.co", 1)

	if err := s.SetDomainStatus(ctx, d.ID, store.DomainError, "no cert", 9000); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Domains(ctx, app.ID)
	if got[0].Status != store.DomainError || got[0].Detail != "no cert" || got[0].CheckedAt != 9000 {
		t.Fatalf("domain = %+v", got[0])
	}
	if err := s.SetDomainStatus(ctx, 999, store.DomainActive, "", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown domain: %v", err)
	}
}

func TestDomainHostTaken(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	app := newApp(t, s, u, "one", 20000, 128)
	s.AddDomain(ctx, app.ID, "x.co", 1)
	if taken, err := s.DomainHostTaken(ctx, "x.co"); err != nil || !taken {
		t.Fatalf("x.co: %v, %v", taken, err)
	}
	if taken, err := s.DomainHostTaken(ctx, "y.co"); err != nil || taken {
		t.Fatalf("y.co: %v, %v", taken, err)
	}
}

func TestPanelSettingsValidate(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	base, _ := s.Settings(ctx)
	cases := []struct {
		name string
		edit func(*store.Settings)
		ok   bool
	}{
		{"domain", func(v *store.Settings) { v.PanelDomain = "panel.a.co" }, true},
		{"port", func(v *store.Settings) { v.PanelPort = 8443 }, true},
		{"not normalized domain", func(v *store.Settings) { v.PanelDomain = "Panel.A.co" }, false},
		{"single label domain", func(v *store.Settings) { v.PanelDomain = "panel" }, false},
		{"port 443", func(v *store.Settings) { v.PanelPort = 443 }, false},
		{"port below 1024", func(v *store.Settings) { v.PanelPort = 22 }, false},
		{"port above 65535", func(v *store.Settings) { v.PanelPort = 70000 }, false},
		{"port inside the app IP range", func(v *store.Settings) { v.PanelPort = 20500 }, false},
	}
	for _, c := range cases {
		v := base
		c.edit(&v)
		if err := v.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}
