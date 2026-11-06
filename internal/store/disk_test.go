package store_test

import (
	"context"
	"slices"
	"testing"

	"runova/internal/artifact"
	"runova/internal/folders"
	"runova/internal/store"
)

func TestAppDiskAndFoldersRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	a, err := s.CreateApp(ctx, store.App{
		UserID: u.ID, Name: "starboom", Kind: artifact.KindJar, InternalPort: 8080, IPPort: 20000, MemoryMB: 256,
		DiskMB: 512, Folders: []folders.Folder{{Path: "storage", Dir: "storage"}, {Path: "/app/media", Dir: "media"}},
		Status: store.AppStopped, DeployMode: "zero", CreatedAt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.App(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DiskMB != 512 || !slices.Equal(got.Folders, a.Folders) {
		t.Fatalf("app = %+v", got)
	}

	disk := 768
	list := []folders.Folder{{Path: "uploads", Dir: "uploads"}}
	upd, err := s.UpdateApp(ctx, a.ID, store.AppPatch{DiskMB: &disk, Folders: &list})
	if err != nil || upd.DiskMB != 768 || !slices.Equal(upd.Folders, list) {
		t.Fatalf("UpdateApp = %+v, %v", upd, err)
	}
	none := []folders.Folder{}
	upd, err = s.UpdateApp(ctx, a.ID, store.AppPatch{Folders: &none})
	if err != nil || upd.Folders == nil || len(upd.Folders) != 0 || upd.DiskMB != 768 {
		t.Fatalf("clearing folders = %+v, %v", upd, err)
	}

	if err := s.SetAppDiskUsage(ctx, a.ID, 5<<20, 99, store.DiskFull); err != nil {
		t.Fatal(err)
	}
	got, _ = s.App(ctx, a.ID)
	if got.DiskUsedBytes != 5<<20 || got.DiskScannedAt != 99 || got.DiskLevel != store.DiskFull {
		t.Fatalf("disk usage = %d at %d level %q", got.DiskUsedBytes, got.DiskScannedAt, got.DiskLevel)
	}
	byUser, _ := s.AppsByUser(ctx, u.ID)
	if len(byUser) != 1 || byUser[0].DiskUsedBytes != 5<<20 || byUser[0].Folders == nil {
		t.Fatalf("AppsByUser = %+v", byUser)
	}
}

func TestNewAppFoldersAreEmptyList(t *testing.T) {
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	a := newApp(t, s, u, "one", 20000, 128)
	got, _ := s.App(context.Background(), a.ID)
	if got.Folders == nil || len(got.Folders) != 0 {
		t.Fatalf("folders = %#v, want []", got.Folders)
	}
}

func TestUserUsageCountsDisk(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	for i, mb := range []int{256, 512} {
		a, err := s.CreateApp(ctx, store.App{UserID: u.ID, Name: []string{"one", "two"}[i], Kind: artifact.KindStatic, IPPort: 20000 + i,
			MemoryMB: 128, DiskMB: mb, Status: store.AppStopped, DeployMode: "zero"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetAppDiskUsage(ctx, a.ID, int64(i+1)<<20, 1, store.DiskOK); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.UserUsage(ctx, u.ID)
	if err != nil || got.Apps != 2 || got.DiskMB != 768 || got.DiskUsedBytes != 3<<20 {
		t.Fatalf("usage = %+v, %v", got, err)
	}
	page, err := s.UserList(ctx, store.UserListFilter{Sort: "disk", Order: "desc"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Usage.DiskMB != 768 || page.Items[0].Usage.DiskUsedBytes != 3<<20 {
		t.Fatalf("UserList = %+v, %v", page, err)
	}
}

func TestQuotaDiskRoundTripAndDefault(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := mustUser(t, s, "andi", store.RoleUser)
	q := u.Quota
	q.DiskMB = 4096
	got, err := s.UpdateUser(ctx, u.ID, store.UserPatch{Quota: &q})
	if err != nil || got.Quota.DiskMB != 4096 {
		t.Fatalf("UpdateUser = %+v, %v", got, err)
	}
	if stored, _ := s.User(ctx, u.ID); stored.Quota.DiskMB != 4096 {
		t.Fatalf("stored = %+v", stored.Quota)
	}
	settings, _ := s.Settings(ctx)
	if settings.DefaultQuota.DiskMB != 1024 {
		t.Fatalf("default disk quota = %d, want 1024", settings.DefaultQuota.DiskMB)
	}
	bad := q
	bad.DiskMB = -1
	if err := bad.Validate(); err == nil {
		t.Error("negative disk quota accepted")
	}
}
