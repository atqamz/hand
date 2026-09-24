package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
)

// Historical resolution survives a terminal owner; schema observation failure
// must remain an error rather than being presented as merely stale ownership.
func TestCanonicalTaskHoldReadPreservesHistoricalResolution(t *testing.T) {
	home := canonicalV19TaskHoldWriterFixture(t)
	hold := canonicalV19TaskHoldWriterInput("hold-history")
	hold.Kind, hold.BlockedOnTaskID = "blocked", "task-2"
	hold.RecheckNotBefore = "2026-09-05T05:00:00Z"
	ctx := context.Background()
	if _, err := CreateCanonicalV19TaskHold(ctx, home, hold); err != nil {
		t.Fatal(err)
	}
	resolution := CanonicalV19TaskHoldResolveInput{
		HoldID: hold.ID, Resolution: "released", ResolvedAt: "2026-09-05T05:01:00Z", EvidenceDigest: "historical fixture resolution",
	}
	if err := ResolveCanonicalV19TaskHold(ctx, home, resolution); err != nil {
		t.Fatal(err)
	}
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE task SET lifecycle='satisfied',terminal_at='2026-09-05T05:02:00Z' WHERE id='task-1'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	view, err := ReadCanonicalV19TaskHold(ctx, home, hold.ID)
	if err != nil || view.Hold != hold || view.Ordinal != 1 || view.OwnerCurrent || view.Resolution == nil || *view.Resolution != resolution {
		t.Fatalf("historical exact read: %#v, %v", view, err)
	}
	if _, err := ReadCanonicalV19TaskHold(ctx, home, "missing"); !errors.Is(err, ErrCanonicalV19TaskHoldNotCurrent) {
		t.Fatalf("missing exact Hold = %v", err)
	}
	after, err := os.ReadFile(Path(home))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("historical read mutated canonical DB: %v", err)
	}
	db, err = open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE unexpected_hold_evidence(id TEXT)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCanonicalV19TaskHold(ctx, home, hold.ID); !errors.Is(err, ErrCanonicalV19SchemaMismatch) {
		t.Fatalf("schema observation failure became historical success: %v", err)
	}
}
