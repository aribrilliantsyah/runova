package api

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"runova/internal/store"
)

// An audit entry for a mutation that already committed must survive the client
// hanging up (which cancels the request context).
func TestRecordSurvivesCanceledRequest(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &server{store: st, now: time.Now}

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/api/users", nil).WithContext(ctx)
	cancel()
	s.record(r, store.User{Username: "admin"}, store.ActUserCreate, "budi", "")

	got, err := st.ActivityList(context.Background(), store.ActivityFilter{})
	if err != nil || len(got) != 1 || got[0].Action != store.ActUserCreate {
		t.Fatalf("activity = %+v, %v", got, err)
	}
}
