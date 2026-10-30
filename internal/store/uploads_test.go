package store_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"runova/internal/artifact"
	"runova/internal/store"
)

func sampleUpload(id string, userID, createdAt int64) store.Upload {
	return store.Upload{
		ID: id, UserID: userID, Dir: "/tmp/" + id, ArtifactPath: "/tmp/" + id + "/app.jar", CreatedAt: createdAt,
		Report: artifact.Report{
			Kind: artifact.KindJar, FileName: "app.jar", Size: 10, MinJava: 17, Port: 8080, PortSource: "bawaan",
			Env:      []artifact.EnvVar{{Name: "DB_URL", Source: artifact.SourceProperties}},
			Problems: []string{},
		},
	}
}

func TestUploadRoundTripAndOwnership(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := mustUser(t, s, "andi", store.RoleUser)
	b := mustUser(t, s, "budi", store.RoleUser)

	want := sampleUpload("up1", a.ID, 5000)
	if err := s.CreateUpload(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Upload(ctx, "up1", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if _, err := s.Upload(ctx, "up1", b.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("other user: err = %v, want ErrNotFound", err)
	}
	if _, err := s.Upload(ctx, "absent", a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("absent: err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteUpload(ctx, "up1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(ctx, "up1", a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after delete: err = %v", err)
	}
}

func TestExpiredUploads(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := mustUser(t, s, "andi", store.RoleUser)
	for id, at := range map[string]int64{"old": 100, "edge": 200, "new": 300} {
		if err := s.CreateUpload(ctx, sampleUpload(id, a.ID, at)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ExpiredUploads(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "old" {
		t.Errorf("expired = %+v", got)
	}
}

func TestDeleteUserRemovesUploadRows(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := mustUser(t, s, "andi", store.RoleUser)
	if err := s.CreateUpload(ctx, sampleUpload("up1", a.ID, 100)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.ExpiredUploads(ctx, 1<<40)
	if err != nil || len(got) != 0 {
		t.Errorf("uploads = %+v, err = %v", got, err)
	}
}
