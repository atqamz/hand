package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
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

func TestListCanonicalV19TaskHoldsKeepsArchivedResolvedHistoryBounded(t *testing.T) {
	home := canonicalV19TaskHoldWriterFixture(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("hold-%d", i)
		if _, err := CreateCanonicalV19TaskHold(ctx, home, canonicalV19TaskHoldWriterInput(id)); err != nil {
			t.Fatal(err)
		}
		if err := ResolveCanonicalV19TaskHold(ctx, home, CanonicalV19TaskHoldResolveInput{
			HoldID: id, Resolution: "released", ResolvedAt: "2026-09-05T04:00:00Z", EvidenceDigest: "resolution-digest",
		}); err != nil {
			t.Fatal(err)
		}
	}
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE task SET lifecycle='satisfied',terminal_at='2026-09-05T05:00:00Z' WHERE id='task-1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ArchiveCanonicalV19Task(ctx, home, CanonicalV19TaskArchiveInput{
		TaskID: "task-1", ActorKind: "operator", ActorRef: "operator-1",
		ArchivedAt: "2026-09-05T06:00:00Z", Reason: "complete", EvidenceDigest: strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	first, err := ListCanonicalV19TaskHolds(ctx, home, "task-1", 0, 2)
	if err != nil || len(first.Items) != 2 || first.Items[0].ID != "hold-1" || first.Items[1].ID != "hold-2" || first.NextAfterOrdinal != 2 {
		t.Fatalf("first archived Hold page = %#v, %v", first, err)
	}
	for _, item := range first.Items {
		if item.Kind != "operator" || item.Resolution != "released" {
			t.Fatalf("archived Hold summary = %#v", item)
		}
	}
	last, err := ListCanonicalV19TaskHolds(ctx, home, "task-1", first.NextAfterOrdinal, 2)
	if err != nil || len(last.Items) != 1 || last.Items[0].ID != "hold-3" || last.NextAfterOrdinal != 0 {
		t.Fatalf("last archived Hold page = %#v, %v", last, err)
	}
	if _, err := ListCanonicalV19TaskHolds(ctx, home, "missing", 0, 2); !errors.Is(err, ErrCanonicalV19TaskNotFound) {
		t.Fatalf("missing Task history = %v", err)
	}
	if _, err := ListCanonicalV19TaskHolds(ctx, home, "task-1", -1, 2); err == nil {
		t.Fatal("negative cursor accepted")
	}
	if _, err := ListCanonicalV19TaskHolds(ctx, home, "task-1", 0, 0); err == nil {
		t.Fatal("zero page limit accepted")
	}
	after, err := os.ReadFile(Path(home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("TaskHold history read mutated database: %v", err)
	}
	roDB, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = roDB.Close() }()
	rows, err := roDB.sql.Query("EXPLAIN QUERY PLAN "+canonicalV19TaskHoldListQuery, "task-1", 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	got := plan.String()
	resolutionPrimary := strings.Contains(got, "SEARCH r USING PRIMARY KEY") ||
		strings.Contains(got, "SEARCH r USING INDEX sqlite_autoindex_task_hold_resolution_") ||
		strings.Contains(got, "SEARCH r USING COVERING INDEX sqlite_autoindex_task_hold_resolution_")
	if !strings.Contains(got, "SEARCH h USING INDEX task_hold_task_history") ||
		!resolutionPrimary || strings.Contains(got, "SCAN h") || strings.Contains(got, "SCAN r") ||
		strings.Contains(got, "TEMP B-TREE") {
		t.Fatalf("TaskHold page is not an indexed seek: %s", got)
	}
}
