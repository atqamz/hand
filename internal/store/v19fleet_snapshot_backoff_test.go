package store

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestReadCanonicalV19FleetSnapshotShowsOnlyCurrentOpenAttemptBackoffs(t *testing.T) {
	ctx := context.Background()
	fixture := canonicalV19AttemptBackoffWriterFixture(t)
	resolved := canonicalV19AttemptBackoffWriterInput("backoff-resolved", "attempt-1")
	if _, err := CreateCanonicalV19AttemptBackoff(ctx, fixture.Home, resolved); err != nil {
		t.Fatal(err)
	}
	if err := ResolveCanonicalV19AttemptBackoff(ctx, fixture.Home, CanonicalV19AttemptBackoffResolveInput{
		BackoffID: resolved.ID, Resolution: "resumed", ResolvedAt: "2026-09-24T00:04:00Z", EvidenceDigest: "digest-resolution",
	}); err != nil {
		t.Fatal(err)
	}
	openBackoff := canonicalV19AttemptBackoffWriterInput("backoff-open", "attempt-1")
	openBackoff.Reason = "usage-limit"
	openBackoff.NotBefore = "2026-09-25T00:00:00Z"
	openBackoff.EvidenceDigest = "digest-open"
	openBackoff.CreatedAt = "2026-09-24T00:05:00Z"
	if _, err := CreateCanonicalV19AttemptBackoff(ctx, fixture.Home, openBackoff); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(ctx, fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(Path(fixture.Home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("AttemptBackoff snapshot mutated canonical database: %v", err)
	}
	if len(snapshot.CurrentOpenAttemptBackoffs) != 1 {
		t.Fatalf("current open AttemptBackoffs = %#v", snapshot.CurrentOpenAttemptBackoffs)
	}
	got := snapshot.CurrentOpenAttemptBackoffs[0]
	if got.ID != openBackoff.ID || got.ProjectID != "project-1" || got.TaskID != "task-1" || got.PlanID != "plan-root" ||
		got.AttemptID != "attempt-1" || got.Reason != openBackoff.Reason || got.NotBefore != openBackoff.NotBefore ||
		got.EvidenceDigest != openBackoff.EvidenceDigest || got.CreatedAt != openBackoff.CreatedAt {
		t.Fatalf("AttemptBackoff snapshot lost exact typed relations: %#v", got)
	}
	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE project SET retired_at='2026-09-24T00:06:00Z' WHERE id='project-1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	retired, err := ReadCanonicalV19FleetSnapshot(ctx, fixture.Home)
	if err != nil || len(retired.CurrentOpenAttemptBackoffs) != 1 || retired.CurrentOpenAttemptBackoffs[0].ID != openBackoff.ID {
		t.Fatalf("retired Project hid active Attempt Backoff: %#v, %v", retired.CurrentOpenAttemptBackoffs, err)
	}
}

func TestCanonicalV19SnapshotCurrentAttemptBackoffsQueryUsesActiveIndexes(t *testing.T) {
	fixture := canonicalV19AttemptBackoffWriterFixture(t)
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.sql.Query("EXPLAIN QUERY PLAN " + canonicalV19SnapshotCurrentAttemptBackoffsQuery)
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
	for _, index := range []string{"task_active_by_project", "plan_active_by_task", "attempt_active_by_plan", "attempt_backoff_attempt_history"} {
		if !strings.Contains(plan.String(), index) {
			t.Fatalf("current AttemptBackoff query missed %s:\n%s", index, plan.String())
		}
	}
	if strings.Contains(plan.String(), "SCAN p ") || strings.Contains(plan.String(), "SCAN a ") ||
		strings.Contains(plan.String(), "SCAN b ") || strings.Contains(plan.String(), "SCAN r ") {
		t.Fatalf("current AttemptBackoff query scans lineage or history instead of exact seeks:\n%s", plan.String())
	}
}
