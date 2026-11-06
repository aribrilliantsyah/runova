package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"runova/internal/store"
)

func addHosting(t *testing.T, s *store.Store, name string) store.HostingDomain {
	t.Helper()
	h, err := s.AddHostingDomain(context.Background(), name, "", 1000)
	if err != nil {
		t.Fatalf("AddHostingDomain(%s): %v", name, err)
	}
	return h
}

func TestHostingDomainCRUD(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	h := addHosting(t, s, "porto.ex.id")
	if !h.Enabled || h.DNSStatus != store.DNSUnchecked || h.CreatedAt != 1000 {
		t.Fatalf("new domain = %+v", h)
	}
	if _, err := s.AddHostingDomain(ctx, "porto.ex.id", "", 1); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate name: %v, want ErrConflict", err)
	}
	off, rs := false, "blog,shop"
	got, err := s.UpdateHostingDomain(ctx, h.ID, &off, &rs)
	if err != nil || got.Enabled || got.Reserved != "blog,shop" {
		t.Fatalf("update = %+v, %v", got, err)
	}
	if err := s.SetHostingDNS(ctx, h.ID, store.DNSOK, "", 2000); err != nil {
		t.Fatal(err)
	}
	got, _ = s.HostingDomain(ctx, h.ID)
	if got.DNSStatus != store.DNSOK || got.DNSCheckedAt != 2000 || got.Enabled {
		t.Fatalf("after dns = %+v", got)
	}
	if _, err := s.HostingDomain(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := s.SetHostingDNS(ctx, 999, store.DNSOK, "", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("dns of missing: %v", err)
	}
}

func TestAddHostedDomainQuotaCountsAcrossAppsAndDisabledBase(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "budi", store.RoleUser)
	other := mustUser(t, s, "andi", store.RoleUser)
	one := newApp(t, s, u, "one", 20000, 128)
	two := newApp(t, s, u, "two", 20001, 128)
	three := newApp(t, s, u, "three", 20002, 128)
	andi := newApp(t, s, other, "andi-app", 20003, 128)
	h := addHosting(t, s, "porto.ex.id")

	if _, err := s.AddHostedDomain(ctx, one.ID, h.ID, "a-one", "a-one.porto.ex.id", 2, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddHostedDomain(ctx, two.ID, h.ID, "a-two", "a-two.porto.ex.id", 2, 1); err != nil {
		t.Fatal(err)
	}
	_, err := s.AddHostedDomain(ctx, three.ID, h.ID, "a-three", "a-three.porto.ex.id", 2, 1)
	var q *store.SubdomainQuotaError
	if !errors.Is(err, store.ErrSubdomainQuota) || !errors.As(err, &q) || q.Used != 2 || q.Limit != 2 {
		t.Fatalf("third subdomain: %v, want quota 2 of 2", err)
	}
	if _, err := s.AddHostedDomain(ctx, andi.ID, h.ID, "a-andi", "a-andi.porto.ex.id", 2, 1); err != nil {
		t.Fatalf("quota is per user: %v", err)
	}

	off := false
	if _, err := s.UpdateHostingDomain(ctx, h.ID, &off, nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.SubdomainCount(ctx, u.ID); n != 2 {
		t.Fatalf("SubdomainCount with disabled base = %d, want 2", n)
	}
	if err := s.DeleteApp(ctx, one.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.SubdomainCount(ctx, u.ID); n != 1 {
		t.Fatalf("SubdomainCount after deleting an app = %d, want 1", n)
	}
}

func TestAddHostedDomainRejectsDisabledOrMissingBase(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "budi", store.RoleUser)
	app := newApp(t, s, u, "one", 20000, 128)
	h := addHosting(t, s, "porto.ex.id")
	off := false
	s.UpdateHostingDomain(ctx, h.ID, &off, nil)
	if _, err := s.AddHostedDomain(ctx, app.ID, h.ID, "abc", "abc.porto.ex.id", 5, 1); !errors.Is(err, store.ErrHostingUnavailable) {
		t.Fatalf("disabled base: %v", err)
	}
	if _, err := s.AddHostedDomain(ctx, app.ID, 999, "abc", "abc.x.id", 5, 1); !errors.Is(err, store.ErrHostingUnavailable) {
		t.Fatalf("missing base: %v", err)
	}
}

func TestHostedHostConflictsWithFreeDomain(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	admin := mustUser(t, s, "admin", store.RoleAdmin)
	u := mustUser(t, s, "budi", store.RoleUser)
	adminApp := newApp(t, s, admin, "site", 20000, 128)
	app := newApp(t, s, u, "one", 20001, 128)
	h := addHosting(t, s, "porto.ex.id")
	if _, err := s.AddDomain(ctx, adminApp.ID, "abc.porto.ex.id", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddHostedDomain(ctx, app.ID, h.ID, "abc", "abc.porto.ex.id", 5, 1); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("hosted host equal to a free domain: %v, want ErrConflict", err)
	}
}

func TestAddHostedDomainLimitPerAppIsShared(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "admin", store.RoleAdmin)
	app := newApp(t, s, u, "one", 20000, 128)
	h := addHosting(t, s, "porto.ex.id")
	for i := range store.MaxDomainsPerApp - 1 {
		if _, err := s.AddDomain(ctx, app.ID, fmt.Sprintf("d%d.co", i), 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddHostedDomain(ctx, app.ID, h.ID, "abc", "abc.porto.ex.id", 9, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddHostedDomain(ctx, app.ID, h.ID, "abd", "abd.porto.ex.id", 9, 1); !errors.Is(err, store.ErrDomainLimit) {
		t.Fatalf("sixth domain: %v, want ErrDomainLimit", err)
	}
}

func TestRemoveHostingDomainRestrict(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "budi", store.RoleUser)
	app := newApp(t, s, u, "one", 20000, 128)
	h := addHosting(t, s, "porto.ex.id")
	d, err := s.AddHostedDomain(ctx, app.ID, h.ID, "abc", "abc.porto.ex.id", 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	err = s.RemoveHostingDomain(ctx, h.ID)
	var inUse *store.HostingInUseError
	if !errors.As(err, &inUse) || inUse.N != 1 || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("remove in use: %v, want HostingInUseError{1}", err)
	}
	if err := s.RemoveDomain(ctx, app.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveHostingDomain(ctx, h.ID); err != nil {
		t.Fatalf("remove unused: %v", err)
	}
	if err := s.RemoveHostingDomain(ctx, h.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("remove twice: %v", err)
	}
}

func TestHostingUsageWindow(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "budi", store.RoleUser)
	app := newApp(t, s, u, "one", 20000, 128)
	h := addHosting(t, s, "porto.ex.id")
	for i, at := range []int64{100, 500, 900} {
		l := fmt.Sprintf("lab%d", i)
		if _, err := s.AddHostedDomain(ctx, app.ID, h.ID, l, l+".porto.ex.id", 9, at); err != nil {
			t.Fatal(err)
		}
	}
	used, recent, err := s.HostingUsage(ctx, 500)
	if err != nil || used[h.ID] != 3 || recent[h.ID] != 2 {
		t.Fatalf("usage = %v, %v, %v", used, recent, err)
	}
}
