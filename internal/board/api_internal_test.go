package board

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func TestTheAPIRejectsThePeerListener(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time {
		return time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateFleet(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	h := New(st, "0123456789abcdef0123456789abcdef", Options{Controls: true})
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req = req.WithContext(context.WithValue(req.Context(), peerKey{}, &peerConn{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("peer listener = %d:\n%s", rec.Code, rec.Body.String())
	}
}

func TestAStalledNeedLeavesOutTheIdleTime(t *testing.T) {
	got := apiNeed(waiting{Kind: "stalled", Title: "Supervisor idle for 1h12m with 2 active tasks and no running attempt"}).Title
	if want := "Supervisor idle with 2 active tasks and no running attempt"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

func TestAPanicWhileReadingTheVersionIsAnError(t *testing.T) {
	b := &Board{}
	if _, err := b.sharedVersion(context.Background(), false); err == nil {
		t.Fatal("a version read without a store did not fail")
	}
}
