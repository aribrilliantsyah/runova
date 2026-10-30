package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"runova/internal/store"
)

// Two admins demoting each other at the same time must not leave zero admins.
func TestLastAdminHoldsUnderConcurrency(t *testing.T) {
	for range 20 {
		s := newStore(t)
		ctx := context.Background()
		a := mustUser(t, s, "andi", store.RoleAdmin)
		b := mustUser(t, s, "bayu", store.RoleAdmin)
		user := store.RoleUser

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, id := range []int64{a.ID, b.ID} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = s.UpdateUser(ctx, id, store.UserPatch{Role: &user})
			}()
		}
		wg.Wait()

		users, err := s.Users(ctx)
		if err != nil {
			t.Fatal(err)
		}
		admins := 0
		for _, u := range users {
			if u.IsAdmin() {
				admins++
			}
		}
		if admins != 1 || (errs[0] == nil) == (errs[1] == nil) {
			t.Fatalf("admins = %d, errs = %v", admins, errs)
		}
		for _, err := range errs {
			if err != nil && !errors.Is(err, store.ErrLastAdmin) {
				t.Fatalf("unexpected error %v", err)
			}
		}
	}
}

// The database holds password hashes and encrypted secrets: the file and its
// WAL/SHM siblings must never be group- or world-readable.
func TestDatabaseFilesPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runova.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustUser(t, s, "budi", store.RoleUser)

	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if st.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s mode %v exposes data to group/other", filepath.Base(p), st.Mode().Perm())
		}
	}
}

// A database created earlier with loose permissions (restored backup, manual
// touch) is tightened on open.
func TestOpenTightensExistingFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runova.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	st, _ := os.Stat(path)
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
}
