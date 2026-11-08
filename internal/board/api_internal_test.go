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
