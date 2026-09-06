package store

import (
	"context"
	"errors"
	"testing"
)

func TestPrepareCanonicalV19WorktreeRemovePersistsExactRequestAndClaims(t *testing.T) {
	fixture, binding := canonicalV19WorktreeRemoveFixture(t)
	input := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-1")
	request, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if request.ProjectID != "project-1" || request.TaskID != "task-1" || request.PlanID != "plan-root" ||
		request.AttemptID != binding.AttemptID || request.WorkspaceBindingID != "workspace-1" ||
		request.BindingID != binding.BindingID || request.Path != binding.RequestedPath ||
		request.ExpectedPhysicalIdentityDigest != "worktree-physical-1" ||
		request.ExpectedCommonGitDir != binding.ExpectedCommonGitDir ||
		request.ExpectedPrivateGitDir == "" || request.ExpectedLockReason != binding.ExpectedLockReason ||
		request.ExpectedHeadRevision != input.ExpectedHeadRevision || len(request.RequestDigest) != 64 {
		t.Fatalf("derived WorktreeRemove request = %#v", request)
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
	if kind != "worktree-remove" || adapter != canonicalV19GitWorktreeAdapterRef || operationKey != input.OperationKey ||
		requestDigest != request.RequestDigest || projectID != request.ProjectID || taskID != request.TaskID ||
		planID != request.PlanID || attemptID != request.AttemptID || scopeKind != "worktree" || scopeKey != request.BindingID ||
		state != "prepared" || createdAt != input.CreatedAt || changedAt != input.CreatedAt {
		t.Fatalf("persisted external operation = %q/%q/%q/%q/%q/%q/%q/%q/%q/%q/%q/%q/%q",
			kind, adapter, operationKey, requestDigest, projectID, taskID, planID, attemptID,
			scopeKind, scopeKey, state, createdAt, changedAt)
	}

	var bindingID, physicalIdentity, commonGitDir, privateGitDir, lockReason, expectedHead string
	if err := db.sql.QueryRow(`SELECT binding_id,expected_physical_identity_digest,expected_common_git_dir,
		expected_private_git_dir,expected_lock_reason,expected_head_revision
		FROM worktree_remove_operation WHERE operation_id=?`, request.OperationID).Scan(
		&bindingID, &physicalIdentity, &commonGitDir, &privateGitDir, &lockReason, &expectedHead,
	); err != nil {
		t.Fatal(err)
	}
	if bindingID != request.BindingID || physicalIdentity != request.ExpectedPhysicalIdentityDigest ||
		commonGitDir != request.ExpectedCommonGitDir || privateGitDir != request.ExpectedPrivateGitDir ||
		lockReason != request.ExpectedLockReason || expectedHead != request.ExpectedHeadRevision {
		t.Fatalf("typed WorktreeRemove = %q/%q/%q/%q/%q/%q", bindingID, physicalIdentity, commonGitDir, privateGitDir, lockReason, expectedHead)
	}

	var claims int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM operation_scope_claim WHERE operation_id=?`, request.OperationID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if claims != 2 {
		t.Fatalf("scope claims = %d, want 2", claims)
	}
}

func TestPrepareCanonicalV19WorktreeRemoveAllowsTerminalAttemptCleanup(t *testing.T) {
	fixture, binding := canonicalV19WorktreeRemoveFixture(t)
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
		AttemptID: binding.AttemptID, Lifecycle: "completed", TerminalAt: "2026-09-05T15:03:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home,
		canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-1")); err != nil {
		t.Fatalf("prepare cleanup after terminal Attempt: %v", err)
	}
}

func TestCanonicalV19WorktreeRemoveNoEffectLeavesBindingOpen(t *testing.T) {
	fixture, binding := canonicalV19WorktreeRemoveFixture(t)
	first := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-1")
	if _, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19WorktreeRemove(context.Background(), fixture.Home, CanonicalV19WorktreeRemoveTransitionInput{
		OperationID: first.OperationID, State: "no-effect", ObservedAt: "2026-09-05T15:04:00Z", EvidenceDigest: "remove-no-effect-1",
	}); err != nil {
		t.Fatal(err)
	}

	second := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-2")
	second.CreatedAt = "2026-09-05T15:05:00Z"
	if _, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, second); err != nil {
		t.Fatalf("replacement remove after no-effect: %v", err)
	}
}

func TestSubmitAndReconcileCanonicalV19WorktreeRemove(t *testing.T) {
	fixture, binding := canonicalV19WorktreeRemoveFixture(t)
	input := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-1")
	prepared, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := SubmitCanonicalV19WorktreeRemove(context.Background(), fixture.Home, prepared.OperationID,
		"2026-09-05T15:04:00Z", "remove-submit-proof-1")
	if err != nil {
		t.Fatal(err)
	}
	if submitted != prepared {
		t.Fatalf("submitted request = %#v, want %#v", submitted, prepared)
	}
	if err := ClassifyCanonicalV19WorktreeRemove(context.Background(), fixture.Home, CanonicalV19WorktreeRemoveTransitionInput{
		OperationID: prepared.OperationID, State: "uncertain", ObservedAt: "2026-09-05T15:05:00Z", EvidenceDigest: "remove-uncertain-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19WorktreeRemove(context.Background(), fixture.Home, CanonicalV19WorktreeRemovedEvidence{
		OperationID: prepared.OperationID, RemovedAt: "2026-09-05T15:06:00Z", EvidenceDigest: "remove-positive-absence-1",
	}); err != nil {
		t.Fatal(err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var state, finalizedAt string
	if err := db.sql.QueryRow(`SELECT state,finalized_at FROM external_operation WHERE id=?`, prepared.OperationID).Scan(&state, &finalizedAt); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || finalizedAt != "2026-09-05T15:06:00Z" {
		t.Fatalf("remove terminal state = %q/%q", state, finalizedAt)
	}
}

func TestCanonicalV19WorktreeRemoveSuccessClosesBindingDerivationally(t *testing.T) {
	fixture, binding := canonicalV19WorktreeRemoveFixture(t)
	input := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-1")
	if _, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19WorktreeRemove(context.Background(), fixture.Home, CanonicalV19WorktreeRemovedEvidence{
		OperationID: input.OperationID, RemovedAt: "2026-09-05T15:04:00Z", EvidenceDigest: "remove-already-absent-1",
	}); err != nil {
		t.Fatal(err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	var bindingRows int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM attempt_worktree_binding WHERE id=?`, binding.BindingID).Scan(&bindingRows); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	if bindingRows != 1 {
		t.Fatalf("immutable WorktreeBinding rows = %d, want 1", bindingRows)
	}

	second := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-2")
	second.CreatedAt = "2026-09-05T15:05:00Z"
	_, err = PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, second)
	if !errors.Is(err, ErrCanonicalV19WorktreeRemoveNotCurrent) {
		t.Fatalf("remove already closed binding error = %v, want %v", err, ErrCanonicalV19WorktreeRemoveNotCurrent)
	}
}

func TestPrepareCanonicalV19WorktreeRemoveRefusesForeignBinding(t *testing.T) {
	fixture, binding := canonicalV19WorktreeRemoveFixture(t)
	input := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-1")
	input.AttemptID = "attempt-foreign"
	_, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, input)
	if !errors.Is(err, ErrCanonicalV19WorktreeRemoveNotCurrent) {
		t.Fatalf("foreign binding error = %v, want %v", err, ErrCanonicalV19WorktreeRemoveNotCurrent)
	}
}

func canonicalV19WorktreeRemoveFixture(t *testing.T) (canonicalV19WorktreeCreateTestFixture, CanonicalV19WorktreeCreateRequest) {
	t.Helper()
	fixture := canonicalV19WorktreeCreateFixture(t)
	createInput := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-create", "binding-1")
	binding, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, createInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19WorktreeBinding(context.Background(), fixture.Home,
		canonicalV19WorktreeBindingEvidence(binding, "worktree-physical-1")); err != nil {
		t.Fatal(err)
	}
	return fixture, binding
}

func canonicalV19WorktreeRemovePrepareInput(binding CanonicalV19WorktreeCreateRequest, operationID string) CanonicalV19WorktreeRemovePrepareInput {
	return CanonicalV19WorktreeRemovePrepareInput{
		OperationID:          operationID,
		OperationKey:         "operation-key-" + operationID,
		AttemptID:            binding.AttemptID,
		BindingID:            binding.BindingID,
		ExpectedHeadRevision: binding.BasisRevision,
		CreatedAt:            "2026-09-05T15:03:00Z",
	}
}
