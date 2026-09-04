package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateCanonicalV19AttemptPersistsExactResolvedProvenance(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	input := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	ordinal, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 {
		t.Fatalf("Attempt ordinal = %d, want 1", ordinal)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var got CanonicalV19AttemptCreateInput
	var gotOrdinal int64
	var lifecycle, terminalAt string
	if err := db.sql.QueryRow(`SELECT id,plan_id,ordinal,worker_harness_ref,worker_harness_version,
		worker_profile_ref,model_ref,effort_ref,session_adapter_ref,lifecycle,created_at,terminal_at
		FROM attempt WHERE id=?`, input.ID).Scan(
		&got.ID, &got.PlanID, &gotOrdinal, &got.WorkerHarnessRef, &got.WorkerHarnessVersion,
		&got.WorkerProfileRef, &got.ModelRef, &got.EffortRef, &got.SessionAdapterRef,
		&lifecycle, &got.CreatedAt, &terminalAt,
	); err != nil {
		t.Fatal(err)
	}
	if got != input || gotOrdinal != 1 || lifecycle != "active" || terminalAt != "" {
		t.Fatalf("persisted Attempt = %#v ordinal=%d lifecycle=%q terminal_at=%q", got, gotOrdinal, lifecycle, terminalAt)
	}
}

func TestCreateCanonicalV19AttemptAllowsEmptyOptionalResolvedProvenance(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	input := canonicalV19AttemptWriterInput("attempt-optional", "plan-root")
	input.WorkerHarnessVersion = ""
	input.WorkerProfileRef = ""
	input.ModelRef = ""
	input.EffortRef = ""
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}
}

func TestCreateCanonicalV19AttemptRefusesSecondActiveAttempt(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	_, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-2", "plan-root"))
	if !errors.Is(err, ErrCanonicalV19AttemptConflict) {
		t.Fatalf("second active Attempt error = %v, want %v", err, ErrCanonicalV19AttemptConflict)
	}
	if got := canonicalV19AttemptWriterCount(t, fixture.Home); got != 1 {
		t.Fatalf("Attempt rows after active conflict = %d, want 1", got)
	}
}

func TestCreateCanonicalV19AttemptRetriesSamePlanAfterTerminalAttempt(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	first := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	canonicalV19AttemptWriterTerminalize(t, fixture.Home, first.ID, "failed", "2026-09-04T09:01:00Z")

	second := canonicalV19AttemptWriterInput("attempt-2", "plan-root")
	second.CreatedAt = "2026-09-04T09:02:00Z"
	ordinal, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, second)
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 2 {
		t.Fatalf("retry Attempt ordinal = %d, want 2", ordinal)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var firstLifecycle, firstTerminal, secondLifecycle, secondTerminal string
	if err := db.sql.QueryRow(`SELECT lifecycle,terminal_at FROM attempt WHERE id=?`, first.ID).Scan(&firstLifecycle, &firstTerminal); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT lifecycle,terminal_at FROM attempt WHERE id=?`, second.ID).Scan(&secondLifecycle, &secondTerminal); err != nil {
		t.Fatal(err)
	}
	if firstLifecycle != "failed" || firstTerminal != "2026-09-04T09:01:00Z" || secondLifecycle != "active" || secondTerminal != "" {
		t.Fatalf("retry state = first %q/%q second %q/%q", firstLifecycle, firstTerminal, secondLifecycle, secondTerminal)
	}
}

func TestCreateCanonicalV19AttemptDuplicateIdentityAfterTerminalRefusesWithoutNewRow(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	first := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	canonicalV19AttemptWriterTerminalize(t, fixture.Home, first.ID, "failed", "2026-09-04T09:01:00Z")

	_, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput(first.ID, "plan-root"))
	if !errors.Is(err, ErrCanonicalV19AttemptConflict) {
		t.Fatalf("duplicate Attempt identity error = %v, want %v", err, ErrCanonicalV19AttemptConflict)
	}
	if got := canonicalV19AttemptWriterCount(t, fixture.Home); got != 1 {
		t.Fatalf("Attempt rows after duplicate identity = %d, want 1", got)
	}
}

func TestCreateCanonicalV19AttemptRefusesStalePlanWithoutRetarget(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	successor := canonicalV19PlanWriterInput("plan-replan")
	successor.CreatedAt = "2026-09-04T09:03:00Z"
	if _, err := ReplanCanonicalV19Plan(context.Background(), fixture.Home, CanonicalV19PlanReplanInput{
		PredecessorPlanID: "plan-root",
		Successor:         successor,
		SupersededAt:      "2026-09-04T09:02:59Z",
	}); err != nil {
		t.Fatal(err)
	}

	_, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-stale", "plan-root"))
	if !errors.Is(err, ErrCanonicalV19AttemptNotCurrent) {
		t.Fatalf("stale Plan Attempt error = %v, want %v", err, ErrCanonicalV19AttemptNotCurrent)
	}
	if got := canonicalV19AttemptWriterCount(t, fixture.Home); got != 0 {
		t.Fatalf("Attempt rows after stale Plan refusal = %d, want 0", got)
	}
}

func TestReplanCanonicalV19PlanRefusesActiveAttempt(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	successor := canonicalV19PlanWriterInput("plan-replan")
	successor.CreatedAt = "2026-09-04T09:03:00Z"
	_, err := ReplanCanonicalV19Plan(context.Background(), fixture.Home, CanonicalV19PlanReplanInput{
		PredecessorPlanID: "plan-root",
		Successor:         successor,
		SupersededAt:      "2026-09-04T09:02:59Z",
	})
	if !errors.Is(err, ErrCanonicalV19PlanNotCurrent) {
		t.Fatalf("replan with active Attempt error = %v, want %v", err, ErrCanonicalV19PlanNotCurrent)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var lifecycle, terminalAt string
	if err := db.sql.QueryRow(`SELECT lifecycle,terminal_at FROM plan WHERE id='plan-root'`).Scan(&lifecycle, &terminalAt); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "active" || terminalAt != "" {
		t.Fatalf("predecessor changed despite active Attempt: lifecycle=%q terminal_at=%q", lifecycle, terminalAt)
	}
	var successorCount int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM plan WHERE id=?`, successor.ID).Scan(&successorCount); err != nil {
		t.Fatal(err)
	}
	if successorCount != 0 {
		t.Fatalf("successor Plan rows = %d, want 0", successorCount)
	}
}

type canonicalV19AttemptWriterTestFixture struct {
	Home string
}

func canonicalV19AttemptWriterFixture(t *testing.T) canonicalV19AttemptWriterTestFixture {
	t.Helper()
	fixture := canonicalV19PlanWriterFixture(t, "")
	if _, err := CreateCanonicalV19RootPlan(context.Background(), fixture.Home, canonicalV19PlanWriterInput("plan-root")); err != nil {
		t.Fatal(err)
	}
	return canonicalV19AttemptWriterTestFixture{Home: fixture.Home}
}

func canonicalV19AttemptWriterInput(id, planID string) CanonicalV19AttemptCreateInput {
	return CanonicalV19AttemptCreateInput{
		ID:                   id,
		PlanID:               planID,
		WorkerHarnessRef:     "worker-harness/codex",
		WorkerHarnessVersion: "1.0.0",
		WorkerProfileRef:     "profile/default",
		ModelRef:             "model/example",
		EffortRef:            "medium",
		SessionAdapterRef:    "builtin/session",
		CreatedAt:            "2026-09-04T09:00:00Z",
	}
}

func canonicalV19AttemptWriterTerminalize(t *testing.T, home, attemptID, lifecycle, terminalAt string) {
	t.Helper()
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE attempt SET lifecycle=?,terminal_at=? WHERE id=?`, lifecycle, terminalAt, attemptID); err != nil {
		t.Fatal(err)
	}
}

func canonicalV19AttemptWriterCount(t *testing.T, home string) int {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM attempt`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
