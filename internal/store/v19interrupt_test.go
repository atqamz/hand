package store

import (
	"context"
	"errors"
	"testing"
)

func TestPrepareCanonicalV19InterruptPersistsExactRequestAndClaim(t *testing.T) {
	fixture, session, launch := canonicalV19ExecutorBindingFixture(t)
	input := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-1")
	request, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if request.ProjectID != "project-1" || request.TaskID != "task-1" || request.PlanID != "plan-root" ||
		request.AttemptID != launch.AttemptID || request.SessionBindingID != session.BindingID ||
		request.ExecutorBindingID != launch.BindingID || request.AdapterRef != session.AdapterRef ||
		request.ProviderExecutorKey != "provider-executor-1" || request.ReasonCode != "operator-request" ||
		len(request.RequestDigest) != 64 {
		t.Fatalf("derived Interrupt request = %#v", request)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var kind, adapter, scopeKind, scopeKey, state, attemptID, executorID, reasonCode string
	if err := db.sql.QueryRow(`SELECT o.kind,o.adapter_ref,o.primary_scope_kind,o.primary_scope_key,o.state,
		i.attempt_id,i.executor_binding_id,i.reason_code
		FROM external_operation o JOIN interrupt_operation i ON i.operation_id=o.id WHERE o.id=?`, request.OperationID).Scan(
		&kind, &adapter, &scopeKind, &scopeKey, &state, &attemptID, &executorID, &reasonCode,
	); err != nil {
		t.Fatal(err)
	}
	if kind != "interrupt" || adapter != request.AdapterRef || scopeKind != "executor-control" ||
		scopeKey != request.ExecutorBindingID || state != "prepared" || attemptID != request.AttemptID ||
		executorID != request.ExecutorBindingID || reasonCode != request.ReasonCode {
		t.Fatalf("persisted Interrupt = %q/%q/%q/%q/%q/%q/%q/%q",
			kind, adapter, scopeKind, scopeKey, state, attemptID, executorID, reasonCode)
	}
	var claims int
	if err := db.sql.QueryRow(`SELECT count(*) FROM operation_scope_claim
		WHERE operation_id=? AND scope_kind='executor-control' AND scope_key=?`,
		request.OperationID, request.ExecutorBindingID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if claims != 1 {
		t.Fatalf("Interrupt executor-control claims = %d, want 1", claims)
	}
}

func TestPreparedCanonicalV19InterruptBlocksCompetingInterrupt(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	first := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-1")
	if _, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	second := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-2")
	second.CreatedAt = "2026-09-06T17:06:00Z"
	_, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, second)
	if !errors.Is(err, ErrCanonicalV19InterruptConflict) {
		t.Fatalf("competing Interrupt error = %v, want %v", err, ErrCanonicalV19InterruptConflict)
	}
}

func TestCanonicalV19InterruptNoEffectLeavesExecutorOpenAndAllowsReplacement(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	first := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-1")
	if _, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19Interrupt(context.Background(), fixture.Home, CanonicalV19InterruptTransitionInput{
		OperationID: first.OperationID, State: "no-effect", ObservedAt: "2026-09-06T17:06:00Z",
		EvidenceDigest: "interrupt-no-effect-1",
	}); err != nil {
		t.Fatal(err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	var terminations int
	if err := db.sql.QueryRow(`SELECT count(*) FROM executor_binding_termination WHERE executor_binding_id=?`, launch.BindingID).Scan(&terminations); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	if terminations != 0 {
		t.Fatalf("termination rows after no-effect = %d, want 0", terminations)
	}

	second := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-2")
	second.CreatedAt = "2026-09-06T17:07:00Z"
	if _, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, second); err != nil {
		t.Fatalf("replacement Interrupt after no-effect: %v", err)
	}
}

func TestCanonicalV19InterruptUncertainThenCessationIsAtomic(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	input := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-1")
	request, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T17:06:00Z", "interrupt-submit-1"); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19Interrupt(context.Background(), fixture.Home, CanonicalV19InterruptTransitionInput{
		OperationID: request.OperationID, State: "uncertain", ObservedAt: "2026-09-06T17:07:00Z",
		EvidenceDigest: "interrupt-uncertain-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19Interrupt(context.Background(), fixture.Home, CanonicalV19ExecutorInterruptedEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-06T17:08:00Z", EvidenceDigest: "executor-ceased-1",
	}); err != nil {
		t.Fatal(err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var state, terminalKind, interruptOperationID, observedAt, evidenceDigest string
	if err := db.sql.QueryRow(`SELECT o.state,t.terminal_kind,t.interrupt_operation_id,t.observed_at,t.evidence_digest
		FROM external_operation o JOIN executor_binding_termination t ON t.interrupt_operation_id=o.id
		WHERE t.executor_binding_id=?`, launch.BindingID).Scan(
		&state, &terminalKind, &interruptOperationID, &observedAt, &evidenceDigest,
	); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || terminalKind != "interrupted" || interruptOperationID != request.OperationID ||
		observedAt != "2026-09-06T17:08:00Z" || evidenceDigest != "executor-ceased-1" {
		t.Fatalf("Interrupt termination evidence = %q/%q/%q/%q/%q",
			state, terminalKind, interruptOperationID, observedAt, evidenceDigest)
	}
}

func TestSuccessfulCanonicalV19InterruptUnblocksSessionRelease(t *testing.T) {
	fixture, session, launch := canonicalV19ExecutorBindingFixture(t)
	input := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-1")
	request, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T17:06:00Z", "interrupt-submit-1"); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19Interrupt(context.Background(), fixture.Home, CanonicalV19ExecutorInterruptedEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-06T17:07:00Z", EvidenceDigest: "executor-ceased-1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareCanonicalV19SessionRelease(context.Background(), fixture.Home, CanonicalV19SessionReleasePrepareInput{
		OperationID: "operation-session-release-after-interrupt", OperationKey: "operation-key-session-release-after-interrupt",
		SessionBindingID: session.BindingID, CreatedAt: "2026-09-06T17:08:00Z",
	}); err != nil {
		t.Fatalf("SessionRelease after successful Interrupt: %v", err)
	}
}

func TestUnresolvedCanonicalV19InterruptBlocksAttemptTerminalization(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	input := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-1")
	request, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T17:06:00Z", "interrupt-submit-1"); err != nil {
		t.Fatal(err)
	}
	terminal := CanonicalV19AttemptTerminalizeInput{
		AttemptID: launch.AttemptID, Lifecycle: "interrupted", TerminalAt: "2026-09-06T17:07:00Z",
	}
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, terminal); !errors.Is(err, ErrCanonicalV19AttemptConflict) {
		t.Fatalf("terminalization with unresolved Interrupt error = %v, want %v", err, ErrCanonicalV19AttemptConflict)
	}
	if err := CompleteCanonicalV19Interrupt(context.Background(), fixture.Home, CanonicalV19ExecutorInterruptedEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-06T17:08:00Z", EvidenceDigest: "executor-ceased-1",
	}); err != nil {
		t.Fatal(err)
	}
	terminal.TerminalAt = "2026-09-06T17:09:00Z"
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, terminal); err != nil {
		t.Fatalf("terminalize Attempt after exact Interrupt cessation: %v", err)
	}
}

func TestPrepareCanonicalV19InterruptRefusesTerminatedExecutor(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	first := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-1")
	request, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T17:06:00Z", "interrupt-submit-1"); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19Interrupt(context.Background(), fixture.Home, CanonicalV19ExecutorInterruptedEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-06T17:07:00Z", EvidenceDigest: "executor-ceased-1",
	}); err != nil {
		t.Fatal(err)
	}
	second := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-2")
	second.CreatedAt = "2026-09-06T17:08:00Z"
	_, err = PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, second)
	if !errors.Is(err, ErrCanonicalV19InterruptNotCurrent) {
		t.Fatalf("Interrupt against terminated executor error = %v, want %v", err, ErrCanonicalV19InterruptNotCurrent)
	}
}

func canonicalV19InterruptPrepareInput(launch CanonicalV19LaunchRequest, operationID string) CanonicalV19InterruptPrepareInput {
	return CanonicalV19InterruptPrepareInput{
		OperationID: operationID, OperationKey: "operation-key-" + operationID,
		ExecutorBindingID: launch.BindingID, ReasonCode: "operator-request", CreatedAt: "2026-09-06T17:05:00Z",
	}
}

func canonicalV19ExecutorBindingFixture(
	t *testing.T,
) (canonicalV19WorktreeCreateTestFixture, CanonicalV19SessionAcquireRequest, CanonicalV19LaunchRequest) {
	t.Helper()
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	input := canonicalV19LaunchPrepareInput(worktree, session, "operation-launch-fixture", "executor-binding-1")
	launch, err := PrepareCanonicalV19Launch(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19ExecutorBinding(context.Background(), fixture.Home, CanonicalV19ExecutorBindingEvidence{
		OperationID: launch.OperationID, ProviderExecutorKey: "provider-executor-1",
		EstablishedAt: "2026-09-06T17:04:00Z", EvidenceDigest: "executor-established-fixture",
	}); err != nil {
		t.Fatal(err)
	}
	return fixture, session, launch
}
