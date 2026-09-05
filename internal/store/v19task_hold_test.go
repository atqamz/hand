package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateCanonicalV19TaskHoldPersistsExactTypedEvidence(t *testing.T) {
	home := canonicalV19TaskHoldWriterFixture(t)
	input := CanonicalV19TaskHoldCreateInput{
		ID:               "hold-1",
		TaskID:           "task-1",
		Kind:             "blocked",
		Reason:           "waiting on predecessor",
		EvidenceDigest:   "digest-hold-1",
		CreatedAt:        "2026-09-05T03:00:00Z",
		BlockedOnTaskID:  "task-2",
		RecheckNotBefore: "2026-09-05T04:00:00Z",
	}
	ordinal, err := CreateCanonicalV19TaskHold(context.Background(), home, input)
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 {
		t.Fatalf("TaskHold ordinal = %d, want 1", ordinal)
	}

	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var got CanonicalV19TaskHoldCreateInput
	var gotOrdinal int64
	if err := db.sql.QueryRow(`SELECT id,task_id,ordinal,kind,reason,evidence_digest,created_at
		FROM task_hold WHERE id=?`, input.ID).Scan(
		&got.ID, &got.TaskID, &gotOrdinal, &got.Kind, &got.Reason,
		&got.EvidenceDigest, &got.CreatedAt,
	); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT blocked_on_task_id FROM task_hold_blocked_on_task WHERE hold_id=?`, input.ID).
		Scan(&got.BlockedOnTaskID); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT not_before FROM task_hold_recheck WHERE hold_id=?`, input.ID).
		Scan(&got.RecheckNotBefore); err != nil {
		t.Fatal(err)
	}
	if got != input || gotOrdinal != 1 {
		t.Fatalf("persisted TaskHold = %#v ordinal=%d, want %#v ordinal=1", got, gotOrdinal, input)
	}
}

func TestCreateCanonicalV19TaskHoldAllowsMultipleUnresolvedHolds(t *testing.T) {
	home := canonicalV19TaskHoldWriterFixture(t)
	first := canonicalV19TaskHoldWriterInput("hold-1")
	ordinal, err := CreateCanonicalV19TaskHold(context.Background(), home, first)
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 {
		t.Fatalf("first TaskHold ordinal = %d, want 1", ordinal)
	}

	second := canonicalV19TaskHoldWriterInput("hold-2")
	second.Reason = "second independent operator deferral"
	second.EvidenceDigest = "digest-hold-2"
	second.CreatedAt = "2026-09-05T03:01:00Z"
	ordinal, err = CreateCanonicalV19TaskHold(context.Background(), home, second)
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 2 {
		t.Fatalf("second TaskHold ordinal = %d, want 2", ordinal)
	}
	if got := canonicalV19TaskHoldWriterCount(t, home); got != 2 {
		t.Fatalf("TaskHold rows = %d, want 2 unresolved rows", got)
	}
}

func TestCreateCanonicalV19TaskHoldRollsBackBaseWhenTypedChildConflicts(t *testing.T) {
	home := canonicalV19TaskHoldWriterFixture(t)
	input := canonicalV19TaskHoldWriterInput("hold-rollback")
	input.DecisionID = "decision-missing"
	_, err := CreateCanonicalV19TaskHold(context.Background(), home, input)
	if !errors.Is(err, ErrCanonicalV19TaskHoldConflict) {
		t.Fatalf("missing Decision relation error = %v, want %v", err, ErrCanonicalV19TaskHoldConflict)
	}
	if got := canonicalV19TaskHoldWriterCount(t, home); got != 0 {
		t.Fatalf("TaskHold rows after typed-child rollback = %d, want 0", got)
	}
}

func TestResolveCanonicalV19TaskHoldPersistsImmutableResolutionAndRefusesReplay(t *testing.T) {
	home := canonicalV19TaskHoldWriterFixture(t)
	hold := canonicalV19TaskHoldWriterInput("hold-1")
	if _, err := CreateCanonicalV19TaskHold(context.Background(), home, hold); err != nil {
		t.Fatal(err)
	}
	first := CanonicalV19TaskHoldResolveInput{
		HoldID:         hold.ID,
		Resolution:     "released",
		ResolvedAt:     "2026-09-05T03:10:00Z",
		EvidenceDigest: "digest-resolution-1",
	}
	if err := ResolveCanonicalV19TaskHold(context.Background(), home, first); err != nil {
		t.Fatal(err)
	}

	second := first
	second.Resolution = "cancelled"
	second.ResolvedAt = "2026-09-05T03:11:00Z"
	second.EvidenceDigest = "digest-resolution-2"
	if err := ResolveCanonicalV19TaskHold(context.Background(), home, second); !errors.Is(err, ErrCanonicalV19TaskHoldNotCurrent) {
		t.Fatalf("replayed TaskHold resolution error = %v, want %v", err, ErrCanonicalV19TaskHoldNotCurrent)
	}

	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var resolution, resolvedAt, evidenceDigest string
	if err := db.sql.QueryRow(`SELECT resolution,resolved_at,evidence_digest
		FROM task_hold_resolution WHERE hold_id=?`, hold.ID).Scan(
		&resolution, &resolvedAt, &evidenceDigest,
	); err != nil {
		t.Fatal(err)
	}
	if resolution != first.Resolution || resolvedAt != first.ResolvedAt || evidenceDigest != first.EvidenceDigest {
		t.Fatalf("TaskHold resolution = %q/%q/%q, want first immutable resolution", resolution, resolvedAt, evidenceDigest)
	}
}

func TestCreateCanonicalV19TaskHoldRefusesTerminalTaskWithoutRetarget(t *testing.T) {
	home := canonicalV19TaskHoldWriterFixture(t)
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE task SET lifecycle='satisfied',terminal_at='2026-09-05T03:20:00Z'
		WHERE id='task-1'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = CreateCanonicalV19TaskHold(context.Background(), home, canonicalV19TaskHoldWriterInput("hold-stale"))
	if !errors.Is(err, ErrCanonicalV19TaskHoldNotCurrent) {
		t.Fatalf("terminal Task Hold error = %v, want %v", err, ErrCanonicalV19TaskHoldNotCurrent)
	}
	if got := canonicalV19TaskHoldWriterCount(t, home); got != 0 {
		t.Fatalf("TaskHold rows after stale Task refusal = %d, want 0", got)
	}
}

func TestCanonicalV19TaskHoldWritersRejectInvalidEnumsWithoutMutation(t *testing.T) {
	home := canonicalV19TaskHoldWriterFixture(t)
	invalid := canonicalV19TaskHoldWriterInput("hold-invalid")
	invalid.Kind = "unknown"
	if _, err := CreateCanonicalV19TaskHold(context.Background(), home, invalid); err == nil {
		t.Fatal("invalid TaskHold kind unexpectedly accepted")
	}
	if got := canonicalV19TaskHoldWriterCount(t, home); got != 0 {
		t.Fatalf("TaskHold rows after invalid kind = %d, want 0", got)
	}

	valid := canonicalV19TaskHoldWriterInput("hold-1")
	if _, err := CreateCanonicalV19TaskHold(context.Background(), home, valid); err != nil {
		t.Fatal(err)
	}
	if err := ResolveCanonicalV19TaskHold(context.Background(), home, CanonicalV19TaskHoldResolveInput{
		HoldID:         valid.ID,
		Resolution:     "unknown",
		ResolvedAt:     "2026-09-05T03:21:00Z",
		EvidenceDigest: "digest-invalid-resolution",
	}); err == nil {
		t.Fatal("invalid TaskHold resolution unexpectedly accepted")
	}
	if got := canonicalV19TaskHoldResolutionCount(t, home); got != 0 {
		t.Fatalf("TaskHold resolution rows after invalid enum = %d, want 0", got)
	}
}

func canonicalV19TaskHoldWriterFixture(t *testing.T) string {
	t.Helper()
	home := canonicalV19TaskWriterFixture(t, false)
	for _, input := range []CanonicalV19TaskCreateInput{
		{
			ID:         "task-1",
			ProjectID:  "project-1",
			Goal:       "primary task",
			GoalDigest: "digest-task-1",
			CreatedAt:  "2026-09-05T02:59:00Z",
		},
		{
			ID:         "task-2",
			ProjectID:  "project-1",
			Goal:       "dependency task",
			GoalDigest: "digest-task-2",
			CreatedAt:  "2026-09-05T02:59:01Z",
		},
	} {
		if _, err := CreateCanonicalV19Task(context.Background(), home, input); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func canonicalV19TaskHoldWriterInput(id string) CanonicalV19TaskHoldCreateInput {
	return CanonicalV19TaskHoldCreateInput{
		ID:             id,
		TaskID:         "task-1",
		Kind:           "operator",
		Reason:         "operator requested deferral",
		EvidenceDigest: "digest-hold-1",
		CreatedAt:      "2026-09-05T03:00:00Z",
	}
}

func canonicalV19TaskHoldWriterCount(t *testing.T, home string) int {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM task_hold`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func canonicalV19TaskHoldResolutionCount(t *testing.T, home string) int {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM task_hold_resolution`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
