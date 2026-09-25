package store

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestReadCanonicalV19FleetSnapshotShowsOnlyCurrentOpenTaskHolds(t *testing.T) {
	ctx := context.Background()
	home := canonicalV19TaskHoldWriterFixture(t)
	if err := CreateCanonicalV19Decision(ctx, home, CanonicalV19DecisionCreateInput{
		ID: "decision-1", TaskID: "task-1", ScopeKind: "task", Question: "Who can unblock this task?",
		CreatedAt: "2026-09-24T00:02:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	resolved := canonicalV19TaskHoldWriterInput("hold-resolved")
	if _, err := CreateCanonicalV19TaskHold(ctx, home, resolved); err != nil {
		t.Fatal(err)
	}
	if err := ResolveCanonicalV19TaskHold(ctx, home, CanonicalV19TaskHoldResolveInput{
		HoldID: resolved.ID, Resolution: "released", ResolvedAt: "2026-09-24T00:04:00Z", EvidenceDigest: "digest-resolution",
	}); err != nil {
		t.Fatal(err)
	}
	blocked := CanonicalV19TaskHoldCreateInput{
		ID: "hold-blocked", TaskID: "task-1", Kind: "blocked", Reason: "waiting for task-2",
		EvidenceDigest: "digest-blocked", CreatedAt: "2026-09-24T00:05:00Z",
		BlockedOnTaskID: "task-2", DecisionID: "decision-1", RecheckNotBefore: "2026-09-25T00:00:00Z",
	}
	if _, err := CreateCanonicalV19TaskHold(ctx, home, blocked); err != nil {
		t.Fatal(err)
	}
	other := canonicalV19TaskHoldWriterInput("hold-other")
	other.TaskID = "task-2"
	if _, err := CreateCanonicalV19TaskHold(ctx, home, other); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(Path(home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("TaskHold snapshot mutated canonical database: %v", err)
	}
	if len(snapshot.CurrentOpenTaskHolds) != 2 {
		t.Fatalf("current open TaskHolds = %#v", snapshot.CurrentOpenTaskHolds)
	}
	first, second := snapshot.CurrentOpenTaskHolds[0], snapshot.CurrentOpenTaskHolds[1]
	if first.ID != blocked.ID || first.TaskID != "task-1" || first.Ordinal != 2 || first.Kind != "blocked" ||
		first.EvidenceDigest != blocked.EvidenceDigest || first.CreatedAt != blocked.CreatedAt ||
		first.BlockedOnTaskID != "task-2" || first.DecisionID != "decision-1" || first.RecheckNotBefore != blocked.RecheckNotBefore ||
		second.ID != other.ID || second.TaskID != "task-2" || second.Ordinal != 1 || second.BlockedOnTaskID != "" ||
		second.DecisionID != "" || second.RecheckNotBefore != "" {
		t.Fatalf("TaskHold snapshot lost exact typed relations or order: %#v", snapshot.CurrentOpenTaskHolds)
	}
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE project SET retired_at='2026-09-24T00:06:00Z' WHERE id='project-1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	retired, err := ReadCanonicalV19FleetSnapshot(ctx, home)
	if err != nil || len(retired.CurrentOpenTaskHolds) != 2 || retired.CurrentOpenTaskHolds[0].ID != blocked.ID ||
		retired.CurrentOpenTaskHolds[1].ID != other.ID {
		t.Fatalf("retired Project hid active Task TaskHolds: %#v, %v", retired.CurrentOpenTaskHolds, err)
	}
}

func TestCanonicalV19SnapshotCurrentTaskHoldsQueryUsesTaskHistoryIndex(t *testing.T) {
	home := canonicalV19TaskHoldWriterFixture(t)
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.sql.Query("EXPLAIN QUERY PLAN " + canonicalV19SnapshotCurrentTaskHoldsQuery)
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
	if !strings.Contains(plan.String(), "SEARCH h USING INDEX task_hold_task_history") ||
		strings.Contains(plan.String(), "SCAN h ") || strings.Contains(plan.String(), "SCAN r ") {
		t.Fatalf("current TaskHold query scans history instead of exact Task seeks: %s", plan.String())
	}
}
