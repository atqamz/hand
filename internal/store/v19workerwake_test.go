package store

import (
	"context"
	"errors"
	"testing"
)

func TestPrepareCanonicalV19WorkerWakePersistsExactRequestAndClaim(t *testing.T) {
	fixture, session, launch := canonicalV19ExecutorBindingFixture(t)
	workerInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-wake-1", "instruction", "digest-worker-input-wake-1")
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, workerInput); err != nil {
		t.Fatal(err)
	}

	input := canonicalV19WorkerWakePrepareInput(launch, "operation-worker-wake-1", 1)
	request, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if request.ProjectID != "project-1" || request.TaskID != "task-1" || request.PlanID != "plan-root" ||
		request.AttemptID != launch.AttemptID || request.SessionBindingID != session.BindingID ||
		request.ExecutorBindingID != launch.BindingID || request.AdapterRef != session.AdapterRef ||
		request.ProviderExecutorKey != "provider-executor-1" || request.PendingThroughOrdinal != 1 ||
		request.WakeReason != input.WakeReason || request.DoorbellDigest != input.DoorbellDigest ||
		len(request.RequestDigest) != 64 {
		t.Fatalf("derived WorkerWake request = %#v", request)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var kind, adapter, scopeKind, scopeKey, state, attemptID, sessionID, executorID, wakeReason, doorbellDigest string
	var pendingThrough int64
	if err := db.sql.QueryRow(`SELECT o.kind,o.adapter_ref,o.primary_scope_kind,o.primary_scope_key,o.state,
		w.attempt_id,w.session_binding_id,w.executor_binding_id,w.pending_through_ordinal,w.wake_reason,w.doorbell_digest
		FROM external_operation o JOIN worker_wake_operation w ON w.operation_id=o.id WHERE o.id=?`, request.OperationID).Scan(
		&kind, &adapter, &scopeKind, &scopeKey, &state, &attemptID, &sessionID, &executorID,
		&pendingThrough, &wakeReason, &doorbellDigest,
	); err != nil {
		t.Fatal(err)
	}
	if kind != "worker-wake" || adapter != request.AdapterRef || scopeKind != "executor-control" ||
		scopeKey != request.ExecutorBindingID || state != "prepared" || attemptID != request.AttemptID ||
		sessionID != request.SessionBindingID || executorID != request.ExecutorBindingID || pendingThrough != 1 ||
		wakeReason != request.WakeReason || doorbellDigest != request.DoorbellDigest {
		t.Fatalf("persisted WorkerWake = %q/%q/%q/%q/%q/%q/%q/%q/%d/%q/%q",
			kind, adapter, scopeKind, scopeKey, state, attemptID, sessionID, executorID,
			pendingThrough, wakeReason, doorbellDigest)
	}
	var claims int
	if err := db.sql.QueryRow(`SELECT count(*) FROM operation_scope_claim
		WHERE operation_id=? AND scope_kind='executor-control' AND scope_key=?`,
		request.OperationID, request.ExecutorBindingID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if claims != 1 {
		t.Fatalf("WorkerWake executor-control claims = %d, want 1", claims)
	}
}

func TestPrepareCanonicalV19WorkerWakeRequiresPendingInput(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	input := canonicalV19WorkerWakePrepareInput(launch, "operation-worker-wake-no-input", 1)
	if _, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19WorkerWakeConflict) {
		t.Fatalf("WorkerWake without pending input error = %v, want %v", err, ErrCanonicalV19WorkerWakeConflict)
	}
}

func TestPrepareCanonicalV19WorkerWakeRejectsBoundaryBeyondInput(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	workerInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-wake-boundary", "instruction", "digest-worker-input-wake-boundary")
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, workerInput); err != nil {
		t.Fatal(err)
	}
	input := canonicalV19WorkerWakePrepareInput(launch, "operation-worker-wake-boundary", 2)
	if _, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19WorkerWakeConflict) {
		t.Fatalf("WorkerWake beyond input boundary error = %v, want %v", err, ErrCanonicalV19WorkerWakeConflict)
	}
}

func TestPreparedCanonicalV19WorkerWakeBlocksCompetingInterrupt(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	workerInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-wake-block-interrupt", "instruction", "digest-worker-input-wake-block-interrupt")
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, workerInput); err != nil {
		t.Fatal(err)
	}
	wake := canonicalV19WorkerWakePrepareInput(launch, "operation-worker-wake-block-interrupt", 1)
	if _, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, wake); err != nil {
		t.Fatal(err)
	}
	interrupt := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-blocked-by-worker-wake")
	if _, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, interrupt); !errors.Is(err, ErrCanonicalV19InterruptConflict) {
		t.Fatalf("Interrupt competing with WorkerWake error = %v, want %v", err, ErrCanonicalV19InterruptConflict)
	}
}

func TestPreparedCanonicalV19InterruptBlocksWorkerWake(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	workerInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-wake-blocked", "instruction", "digest-worker-input-wake-blocked")
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, workerInput); err != nil {
		t.Fatal(err)
	}
	interrupt := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-block-worker-wake")
	if _, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, interrupt); err != nil {
		t.Fatal(err)
	}
	wake := canonicalV19WorkerWakePrepareInput(launch, "operation-worker-wake-blocked", 1)
	if _, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, wake); !errors.Is(err, ErrCanonicalV19WorkerWakeConflict) {
		t.Fatalf("WorkerWake competing with Interrupt error = %v, want %v", err, ErrCanonicalV19WorkerWakeConflict)
	}
}

func TestCanonicalV19WorkerWakeNoEffectAllowsReplacement(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	workerInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-wake-replacement", "instruction", "digest-worker-input-wake-replacement")
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, workerInput); err != nil {
		t.Fatal(err)
	}
	first := canonicalV19WorkerWakePrepareInput(launch, "operation-worker-wake-no-effect-1", 1)
	if _, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19WorkerWake(context.Background(), fixture.Home, CanonicalV19WorkerWakeTransitionInput{
		OperationID: first.OperationID, State: "no-effect", ObservedAt: "2026-09-09T05:16:00Z",
		EvidenceDigest: "worker-wake-no-effect-1",
	}); err != nil {
		t.Fatal(err)
	}
	second := canonicalV19WorkerWakePrepareInput(launch, "operation-worker-wake-no-effect-2", 1)
	second.CreatedAt = "2026-09-09T05:17:00Z"
	if _, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, second); err != nil {
		t.Fatalf("replacement WorkerWake after no-effect: %v", err)
	}
}

func TestCanonicalV19WorkerWakeUncertainThenSucceededDoesNotAcknowledgeOrTerminate(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	workerInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-wake-succeed", "instruction", "digest-worker-input-wake-succeed")
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, workerInput); err != nil {
		t.Fatal(err)
	}
	input := canonicalV19WorkerWakePrepareInput(launch, "operation-worker-wake-succeed", 1)
	request, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19WorkerWake(context.Background(), fixture.Home, request.OperationID,
		"2026-09-09T05:16:00Z", "worker-wake-submitted"); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19WorkerWake(context.Background(), fixture.Home, CanonicalV19WorkerWakeTransitionInput{
		OperationID: request.OperationID, State: "uncertain", ObservedAt: "2026-09-09T05:17:00Z",
		EvidenceDigest: "worker-wake-uncertain",
	}); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19WorkerWake(context.Background(), fixture.Home, CanonicalV19WorkerWakeSucceededEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-09T05:18:00Z", EvidenceDigest: "worker-wake-mechanism-succeeded",
	}); err != nil {
		t.Fatal(err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var state string
	if err := db.sql.QueryRow(`SELECT state FROM external_operation WHERE id=?`, request.OperationID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" {
		t.Fatalf("WorkerWake state = %q, want succeeded", state)
	}
	var acknowledgements, terminations int
	if err := db.sql.QueryRow(`SELECT count(*) FROM worker_input_acknowledgement WHERE worker_input_id=?`, workerInput.ID).Scan(&acknowledgements); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT count(*) FROM executor_binding_termination WHERE executor_binding_id=?`, launch.BindingID).Scan(&terminations); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 0 || terminations != 0 {
		t.Fatalf("WorkerWake semantic side effects = acknowledgements:%d terminations:%d, want 0/0", acknowledgements, terminations)
	}
}

func TestCanonicalV19WorkerWakeAllowsCoalescedPendingBoundary(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	first := canonicalV19WorkerInputCreateInput(launch, "worker-input-wake-coalesce-1", "first", "digest-worker-input-wake-coalesce-1")
	createdFirst, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, first)
	if err != nil {
		t.Fatal(err)
	}
	second := canonicalV19WorkerInputCreateInput(launch, "worker-input-wake-coalesce-2", "second", "digest-worker-input-wake-coalesce-2")
	second.CreatedAt = "2026-09-09T05:16:00Z"
	createdSecond, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home, CanonicalV19WorkerInputAcknowledgementCreateInput{
		WorkerInputID: createdFirst.ID, ExecutorBindingID: launch.BindingID,
		ObservedAt: "2026-09-09T05:17:00Z", EvidenceDigest: "worker-observed-first-coalesced-input",
	}); err != nil {
		t.Fatal(err)
	}
	wake := canonicalV19WorkerWakePrepareInput(launch, "operation-worker-wake-coalesced", createdSecond.Ordinal)
	request, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, wake)
	if err != nil {
		t.Fatal(err)
	}
	if request.PendingThroughOrdinal != 2 {
		t.Fatalf("coalesced WorkerWake pending-through ordinal = %d, want 2", request.PendingThroughOrdinal)
	}
}

func TestPrepareCanonicalV19WorkerWakeRefusesTerminatedExecutor(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	workerInput := canonicalV19WorkerInputCreateInput(launch, "worker-input-wake-after-interrupt", "instruction", "digest-worker-input-wake-after-interrupt")
	if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, workerInput); err != nil {
		t.Fatal(err)
	}
	interrupt := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-before-worker-wake")
	request, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, interrupt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), fixture.Home, request.OperationID,
		"2026-09-09T05:16:00Z", "interrupt-submitted-before-worker-wake"); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19Interrupt(context.Background(), fixture.Home, CanonicalV19ExecutorInterruptedEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-09T05:17:00Z", EvidenceDigest: "executor-ceased-before-worker-wake",
	}); err != nil {
		t.Fatal(err)
	}
	wake := canonicalV19WorkerWakePrepareInput(launch, "operation-worker-wake-after-interrupt", 1)
	wake.CreatedAt = "2026-09-09T05:18:00Z"
	if _, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home, wake); !errors.Is(err, ErrCanonicalV19WorkerWakeNotCurrent) {
		t.Fatalf("WorkerWake after executor termination error = %v, want %v", err, ErrCanonicalV19WorkerWakeNotCurrent)
	}
}

func canonicalV19WorkerWakePrepareInput(
	launch CanonicalV19LaunchRequest,
	operationID string,
	pendingThroughOrdinal int64,
) CanonicalV19WorkerWakePrepareInput {
	return CanonicalV19WorkerWakePrepareInput{
		OperationID: operationID, OperationKey: "operation-key-" + operationID,
		ExecutorBindingID: launch.BindingID, PendingThroughOrdinal: pendingThroughOrdinal,
		WakeReason: "pending-input", DoorbellDigest: "hand-worker-wake-doorbell-v1",
		CreatedAt: "2026-09-09T05:15:00Z",
	}
}
