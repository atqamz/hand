package store

import (
	"context"
	"errors"
	"testing"
)

func TestPrepareCanonicalV19SessionAcquirePersistsExactRequestAndClaims(t *testing.T) {
	fixture, worktree := canonicalV19WorktreeRemoveFixture(t)
	input := canonicalV19SessionAcquirePrepareInput(worktree, "operation-session-acquire-1", "session-binding-1")
	input.RequestedProviderSessionKey = "provider-session-1"
	request, err := PrepareCanonicalV19SessionAcquire(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if request.ProjectID != "project-1" || request.TaskID != "task-1" || request.PlanID != "plan-root" ||
		request.AttemptID != worktree.AttemptID || request.WorktreeBindingID != worktree.BindingID ||
		request.BindingID != input.BindingID || request.AdapterRef != "builtin/session" ||
		request.RequestedProviderSessionKey != input.RequestedProviderSessionKey || len(request.RequestDigest) != 64 {
		t.Fatalf("derived SessionAcquire request = %#v", request)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var kind, adapter, operationKey, requestDigest, projectID, taskID, planID, attemptID string
	var scopeKind, scopeKey, state, createdAt, changedAt string
	if err := db.sql.QueryRow(`SELECT kind,adapter_ref,operation_key,request_digest,project_id,task_id,plan_id,attempt_id,
		primary_scope_kind,primary_scope_key,state,created_at,state_changed_at FROM external_operation WHERE id=?`, request.OperationID).Scan(
		&kind, &adapter, &operationKey, &requestDigest, &projectID, &taskID, &planID, &attemptID,
		&scopeKind, &scopeKey, &state, &createdAt, &changedAt,
	); err != nil {
		t.Fatal(err)
	}
	if kind != "session-acquire" || adapter != request.AdapterRef || operationKey != input.OperationKey ||
		requestDigest != request.RequestDigest || projectID != request.ProjectID || taskID != request.TaskID ||
		planID != request.PlanID || attemptID != request.AttemptID || scopeKind != "session" || scopeKey != request.BindingID ||
		state != "prepared" || createdAt != input.CreatedAt || changedAt != input.CreatedAt {
		t.Fatalf("persisted external operation = %q/%q/%q/%q/%q/%q/%q/%q/%q/%q/%q/%q/%q",
			kind, adapter, operationKey, requestDigest, projectID, taskID, planID, attemptID,
			scopeKind, scopeKey, state, createdAt, changedAt)
	}

	var typedAttemptID, typedWorktreeID, bindingID, providerKey string
	if err := db.sql.QueryRow(`SELECT attempt_id,worktree_binding_id,binding_id,requested_provider_session_key
		FROM session_acquire_operation WHERE operation_id=?`, request.OperationID).Scan(
		&typedAttemptID, &typedWorktreeID, &bindingID, &providerKey,
	); err != nil {
		t.Fatal(err)
	}
	if typedAttemptID != request.AttemptID || typedWorktreeID != request.WorktreeBindingID ||
		bindingID != request.BindingID || providerKey != request.RequestedProviderSessionKey {
		t.Fatalf("typed SessionAcquire = %q/%q/%q/%q", typedAttemptID, typedWorktreeID, bindingID, providerKey)
	}

	var sessionClaims, worktreeClaims int
	if err := db.sql.QueryRow(`SELECT count(*) FROM operation_scope_claim
		WHERE operation_id=? AND scope_kind='session' AND scope_key=?`, request.OperationID, request.BindingID).Scan(&sessionClaims); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT count(*) FROM operation_scope_claim
		WHERE operation_id=? AND scope_kind='worktree' AND scope_key=?`, request.OperationID, request.WorktreeBindingID).Scan(&worktreeClaims); err != nil {
		t.Fatal(err)
	}
	if sessionClaims != 1 || worktreeClaims != 1 {
		t.Fatalf("SessionAcquire claims session/worktree = %d/%d, want 1/1", sessionClaims, worktreeClaims)
	}
}

func TestEstablishCanonicalV19SessionBindingIsAtomic(t *testing.T) {
	fixture, worktree := canonicalV19WorktreeRemoveFixture(t)
	input := canonicalV19SessionAcquirePrepareInput(worktree, "operation-session-acquire-1", "session-binding-1")
	input.RequestedProviderSessionKey = "provider-session-1"
	request, err := PrepareCanonicalV19SessionAcquire(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19SessionAcquire(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T16:51:00Z", "session-submit-1"); err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19SessionBinding(context.Background(), fixture.Home, CanonicalV19SessionBindingEvidence{
		OperationID: request.OperationID, ProviderSessionKey: "provider-session-1",
		EstablishedAt: "2026-09-06T16:52:00Z", EvidenceDigest: "session-established-1",
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
		t.Fatalf("SessionAcquire state = %q, want succeeded", state)
	}
	var attemptID, worktreeID, acquireOperationID, adapter, providerKey, establishedAt string
	var ordinal int64
	if err := db.sql.QueryRow(`SELECT attempt_id,ordinal,worktree_binding_id,acquire_operation_id,adapter_ref,provider_session_key,established_at
		FROM session_binding WHERE id=?`, request.BindingID).Scan(
		&attemptID, &ordinal, &worktreeID, &acquireOperationID, &adapter, &providerKey, &establishedAt,
	); err != nil {
		t.Fatal(err)
	}
	if attemptID != request.AttemptID || ordinal != 1 || worktreeID != request.WorktreeBindingID ||
		acquireOperationID != request.OperationID || adapter != request.AdapterRef || providerKey != "provider-session-1" ||
		establishedAt != "2026-09-06T16:52:00Z" {
		t.Fatalf("SessionBinding = %q/%d/%q/%q/%q/%q/%q", attemptID, ordinal, worktreeID, acquireOperationID, adapter, providerKey, establishedAt)
	}
}

func TestCanonicalV19SessionAcquireNoEffectAllowsReplacement(t *testing.T) {
	fixture, worktree := canonicalV19WorktreeRemoveFixture(t)
	first := canonicalV19SessionAcquirePrepareInput(worktree, "operation-session-acquire-1", "session-binding-1")
	if _, err := PrepareCanonicalV19SessionAcquire(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19SessionAcquire(context.Background(), fixture.Home, CanonicalV19SessionAcquireTransitionInput{
		OperationID: first.OperationID, State: "no-effect", ObservedAt: "2026-09-06T16:51:00Z", EvidenceDigest: "session-no-effect-1",
	}); err != nil {
		t.Fatal(err)
	}
	second := canonicalV19SessionAcquirePrepareInput(worktree, "operation-session-acquire-2", "session-binding-2")
	second.CreatedAt = "2026-09-06T16:52:00Z"
	if _, err := PrepareCanonicalV19SessionAcquire(context.Background(), fixture.Home, second); err != nil {
		t.Fatalf("replacement SessionAcquire after no-effect: %v", err)
	}
}

func TestEstablishCanonicalV19SessionBindingProviderKeyMismatchRollsBack(t *testing.T) {
	fixture, worktree := canonicalV19WorktreeRemoveFixture(t)
	input := canonicalV19SessionAcquirePrepareInput(worktree, "operation-session-acquire-1", "session-binding-1")
	input.RequestedProviderSessionKey = "provider-session-1"
	request, err := PrepareCanonicalV19SessionAcquire(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	err = EstablishCanonicalV19SessionBinding(context.Background(), fixture.Home, CanonicalV19SessionBindingEvidence{
		OperationID: request.OperationID, ProviderSessionKey: "provider-session-2",
		EstablishedAt: "2026-09-06T16:51:00Z", EvidenceDigest: "session-established-wrong",
	})
	if !errors.Is(err, ErrCanonicalV19SessionNotCurrent) {
		t.Fatalf("provider key mismatch error = %v, want %v", err, ErrCanonicalV19SessionNotCurrent)
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
	if state != "prepared" {
		t.Fatalf("SessionAcquire state after mismatch = %q, want prepared", state)
	}
	var bindings int
	if err := db.sql.QueryRow(`SELECT count(*) FROM session_binding WHERE id=?`, request.BindingID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if bindings != 0 {
		t.Fatalf("SessionBinding rows after mismatch = %d, want 0", bindings)
	}
}

func TestPreparedCanonicalV19SessionAcquireBlocksWorktreeRemove(t *testing.T) {
	fixture, worktree := canonicalV19WorktreeRemoveFixture(t)
	acquire := canonicalV19SessionAcquirePrepareInput(worktree, "operation-session-acquire-1", "session-binding-1")
	if _, err := PrepareCanonicalV19SessionAcquire(context.Background(), fixture.Home, acquire); err != nil {
		t.Fatal(err)
	}
	_, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home,
		canonicalV19WorktreeRemovePrepareInput(worktree, "operation-remove-while-session-acquire"))
	if !errors.Is(err, ErrCanonicalV19WorktreeRemoveConflict) {
		t.Fatalf("WorktreeRemove during unresolved SessionAcquire error = %v, want %v", err, ErrCanonicalV19WorktreeRemoveConflict)
	}
}

func TestPrepareCanonicalV19SessionAcquireRefusesReleasedWorktree(t *testing.T) {
	fixture, worktree := canonicalV19WorktreeRemoveFixture(t)
	remove := canonicalV19WorktreeRemovePrepareInput(worktree, "operation-remove-1")
	request, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, remove)
	if err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19WorktreeRemove(context.Background(), fixture.Home, CanonicalV19WorktreeRemovedEvidence{
		OperationID: request.OperationID, RemovedAt: "2026-09-06T16:51:00Z", EvidenceDigest: "removed-1",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = PrepareCanonicalV19SessionAcquire(context.Background(), fixture.Home,
		canonicalV19SessionAcquirePrepareInput(worktree, "operation-session-acquire-1", "session-binding-1"))
	if !errors.Is(err, ErrCanonicalV19SessionNotCurrent) {
		t.Fatalf("SessionAcquire on released worktree error = %v, want %v", err, ErrCanonicalV19SessionNotCurrent)
	}
}

func TestCanonicalV19SessionAcquireUncertainThenExactEstablishmentSucceeds(t *testing.T) {
	fixture, worktree := canonicalV19WorktreeRemoveFixture(t)
	input := canonicalV19SessionAcquirePrepareInput(worktree, "operation-session-acquire-1", "session-binding-1")
	request, err := PrepareCanonicalV19SessionAcquire(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19SessionAcquire(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T16:51:00Z", "session-submit-1"); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19SessionAcquire(context.Background(), fixture.Home, CanonicalV19SessionAcquireTransitionInput{
		OperationID: request.OperationID, State: "uncertain", ObservedAt: "2026-09-06T16:52:00Z", EvidenceDigest: "session-uncertain-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19SessionBinding(context.Background(), fixture.Home, CanonicalV19SessionBindingEvidence{
		OperationID: request.OperationID, ProviderSessionKey: "provider-session-1",
		EstablishedAt: "2026-09-06T16:53:00Z", EvidenceDigest: "session-established-later",
	}); err != nil {
		t.Fatalf("establish after uncertain: %v", err)
	}
}

func TestPrepareCanonicalV19SessionReleasePersistsExactRequest(t *testing.T) {
	fixture, _, session := canonicalV19SessionBindingFixture(t)
	input := CanonicalV19SessionReleasePrepareInput{
		OperationID: "operation-session-release-1", OperationKey: "operation-key-session-release-1",
		SessionBindingID: session.BindingID, CreatedAt: "2026-09-06T16:54:00Z",
	}
	request, err := PrepareCanonicalV19SessionRelease(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if request.SessionBindingID != session.BindingID || request.AttemptID != session.AttemptID ||
		request.WorktreeBindingID != session.WorktreeBindingID || request.AdapterRef != session.AdapterRef ||
		request.ExpectedProviderSessionKey != "provider-session-1" || len(request.RequestDigest) != 64 {
		t.Fatalf("derived SessionRelease request = %#v", request)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var kind, scopeKind, scopeKey, state string
	if err := db.sql.QueryRow(`SELECT kind,primary_scope_kind,primary_scope_key,state FROM external_operation WHERE id=?`, request.OperationID).Scan(
		&kind, &scopeKind, &scopeKey, &state,
	); err != nil {
		t.Fatal(err)
	}
	if kind != "session-release" || scopeKind != "session" || scopeKey != session.BindingID || state != "prepared" {
		t.Fatalf("SessionRelease operation = %q/%q/%q/%q", kind, scopeKind, scopeKey, state)
	}
	var bindingID, providerKey string
	if err := db.sql.QueryRow(`SELECT session_binding_id,expected_provider_session_key FROM session_release_operation WHERE operation_id=?`, request.OperationID).Scan(
		&bindingID, &providerKey,
	); err != nil {
		t.Fatal(err)
	}
	if bindingID != session.BindingID || providerKey != "provider-session-1" {
		t.Fatalf("typed SessionRelease = %q/%q", bindingID, providerKey)
	}
}

func TestCanonicalV19SessionReleaseNoEffectLeavesBindingOpen(t *testing.T) {
	fixture, _, session := canonicalV19SessionBindingFixture(t)
	first := CanonicalV19SessionReleasePrepareInput{
		OperationID: "operation-session-release-1", OperationKey: "operation-key-session-release-1",
		SessionBindingID: session.BindingID, CreatedAt: "2026-09-06T16:54:00Z",
	}
	if _, err := PrepareCanonicalV19SessionRelease(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19SessionRelease(context.Background(), fixture.Home, CanonicalV19SessionReleaseTransitionInput{
		OperationID: first.OperationID, State: "no-effect", ObservedAt: "2026-09-06T16:55:00Z", EvidenceDigest: "release-no-effect-1",
	}); err != nil {
		t.Fatal(err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	var releases int
	if err := db.sql.QueryRow(`SELECT count(*) FROM session_binding_release WHERE session_binding_id=?`, session.BindingID).Scan(&releases); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	if releases != 0 {
		t.Fatalf("SessionBinding release rows = %d, want 0", releases)
	}
	second := first
	second.OperationID = "operation-session-release-2"
	second.OperationKey = "operation-key-session-release-2"
	second.CreatedAt = "2026-09-06T16:56:00Z"
	if _, err := PrepareCanonicalV19SessionRelease(context.Background(), fixture.Home, second); err != nil {
		t.Fatalf("replacement SessionRelease after no-effect: %v", err)
	}
}

func TestCompleteCanonicalV19SessionReleaseIsAtomicAndUnblocksWorktreeRemove(t *testing.T) {
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	input := CanonicalV19SessionReleasePrepareInput{
		OperationID: "operation-session-release-1", OperationKey: "operation-key-session-release-1",
		SessionBindingID: session.BindingID, CreatedAt: "2026-09-06T16:54:00Z",
	}
	request, err := PrepareCanonicalV19SessionRelease(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19SessionRelease(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T16:55:00Z", "release-submit-1"); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19SessionRelease(context.Background(), fixture.Home, CanonicalV19SessionReleasedEvidence{
		OperationID: request.OperationID, ReleasedAt: "2026-09-06T16:56:00Z", EvidenceDigest: "release-succeeded-1",
	}); err != nil {
		t.Fatal(err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	var state, releaseOperationID, releasedAt, evidenceDigest string
	if err := db.sql.QueryRow(`SELECT o.state,r.release_operation_id,r.released_at,r.evidence_digest
		FROM external_operation o JOIN session_binding_release r ON r.release_operation_id=o.id
		WHERE r.session_binding_id=?`, session.BindingID).Scan(&state, &releaseOperationID, &releasedAt, &evidenceDigest); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	if state != "succeeded" || releaseOperationID != request.OperationID || releasedAt != "2026-09-06T16:56:00Z" ||
		evidenceDigest != "release-succeeded-1" {
		t.Fatalf("Session release evidence = %q/%q/%q/%q", state, releaseOperationID, releasedAt, evidenceDigest)
	}
	if _, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home,
		canonicalV19WorktreeRemovePrepareInput(worktree, "operation-remove-after-session-release")); err != nil {
		t.Fatalf("WorktreeRemove after Session release: %v", err)
	}
}

func canonicalV19SessionAcquirePrepareInput(
	worktree CanonicalV19WorktreeCreateRequest,
	operationID string,
	bindingID string,
) CanonicalV19SessionAcquirePrepareInput {
	return CanonicalV19SessionAcquirePrepareInput{
		OperationID: operationID, OperationKey: "operation-key-" + operationID,
		AttemptID: worktree.AttemptID, BindingID: bindingID, CreatedAt: "2026-09-06T16:50:00Z",
	}
}

func canonicalV19SessionBindingFixture(
	t *testing.T,
) (canonicalV19WorktreeCreateTestFixture, CanonicalV19WorktreeCreateRequest, CanonicalV19SessionAcquireRequest) {
	t.Helper()
	fixture, worktree := canonicalV19WorktreeRemoveFixture(t)
	input := canonicalV19SessionAcquirePrepareInput(worktree, "operation-session-acquire-fixture", "session-binding-1")
	input.RequestedProviderSessionKey = "provider-session-1"
	session, err := PrepareCanonicalV19SessionAcquire(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19SessionBinding(context.Background(), fixture.Home, CanonicalV19SessionBindingEvidence{
		OperationID: session.OperationID, ProviderSessionKey: "provider-session-1",
		EstablishedAt: "2026-09-06T16:53:00Z", EvidenceDigest: "session-established-fixture",
	}); err != nil {
		t.Fatal(err)
	}
	return fixture, worktree, session
}
