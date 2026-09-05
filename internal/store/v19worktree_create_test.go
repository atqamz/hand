package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestPrepareCanonicalV19WorktreeCreatePersistsExactRequestAndClaims(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-1", "binding-1")
	request, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if request.ProjectID != "project-1" || request.TaskID != "task-1" || request.PlanID != "plan-root" ||
		request.AttemptID != "attempt-1" || request.WorkspaceBindingID != "workspace-1" {
		t.Fatalf("derived owner lineage = %#v", request)
	}
	if request.BindingID != input.BindingID || request.RequestedPath != input.RequestedPath ||
		request.ExpectedCommonGitDir != "projects/demo/.git" || request.ExpectedLockReason != "hand:v1:binding-1" ||
		request.BasisRevision == "" || len(request.RequestDigest) != 64 {
		t.Fatalf("derived request = %#v", request)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var kind, adapter, operationKey, requestDigest, projectID, taskID, planID, attemptID string
	var scopeKind, scopeKey, state, createdAt, changedAt, stateEvidence, submittedAt, finalizedAt string
	if err := db.sql.QueryRow(`SELECT kind,adapter_ref,operation_key,request_digest,project_id,task_id,plan_id,attempt_id,
		primary_scope_kind,primary_scope_key,state,created_at,state_changed_at,state_evidence_digest,submitted_at,finalized_at
		FROM external_operation WHERE id=?`, request.OperationID).Scan(
		&kind, &adapter, &operationKey, &requestDigest, &projectID, &taskID, &planID, &attemptID,
		&scopeKind, &scopeKey, &state, &createdAt, &changedAt, &stateEvidence, &submittedAt, &finalizedAt,
	); err != nil {
		t.Fatal(err)
	}
	if kind != "worktree-create" || adapter != canonicalV19GitWorktreeAdapterRef || operationKey != input.OperationKey ||
		requestDigest != request.RequestDigest || projectID != request.ProjectID || taskID != request.TaskID ||
		planID != request.PlanID || attemptID != request.AttemptID || scopeKind != "worktree" || scopeKey != request.BindingID ||
		state != "prepared" || createdAt != input.CreatedAt || changedAt != input.CreatedAt || stateEvidence != "" ||
		submittedAt != "" || finalizedAt != "" {
		t.Fatalf("persisted external operation = kind=%q adapter=%q key=%q digest=%q owner=%q/%q/%q/%q scope=%q/%q state=%q created=%q changed=%q evidence=%q submitted=%q finalized=%q",
			kind, adapter, operationKey, requestDigest, projectID, taskID, planID, attemptID, scopeKind, scopeKey,
			state, createdAt, changedAt, stateEvidence, submittedAt, finalizedAt)
	}

	var typedAttempt, typedBinding, requestedPath, basisRevision, commonGitDir, lockReason string
	if err := db.sql.QueryRow(`SELECT attempt_id,binding_id,requested_path,basis_revision,expected_common_git_dir,expected_lock_reason
		FROM worktree_create_operation WHERE operation_id=?`, request.OperationID).Scan(
		&typedAttempt, &typedBinding, &requestedPath, &basisRevision, &commonGitDir, &lockReason,
	); err != nil {
		t.Fatal(err)
	}
	if typedAttempt != request.AttemptID || typedBinding != request.BindingID || requestedPath != request.RequestedPath ||
		basisRevision != request.BasisRevision || commonGitDir != request.ExpectedCommonGitDir || lockReason != request.ExpectedLockReason {
		t.Fatalf("typed WorktreeCreate = %q/%q/%q/%q/%q/%q", typedAttempt, typedBinding, requestedPath, basisRevision, commonGitDir, lockReason)
	}

	var claimCount, eventCount int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM operation_scope_claim WHERE operation_id=?`, request.OperationID).Scan(&claimCount); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM external_operation_event WHERE operation_id=? AND from_state='' AND to_state='prepared'`, request.OperationID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if claimCount != 2 || eventCount != 1 {
		t.Fatalf("claims/events = %d/%d, want 2/1", claimCount, eventCount)
	}
	for scope, key := range map[string]string{"worktree": request.BindingID, "workspace": request.WorkspaceBindingID} {
		var count int
		if err := db.sql.QueryRow(`SELECT COUNT(*) FROM operation_scope_claim WHERE operation_id=? AND scope_kind=? AND scope_key=?`, request.OperationID, scope, key).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("claim %q/%q count = %d, want 1", scope, key, count)
		}
	}
}

func TestPrepareCanonicalV19WorktreeCreateRefusesSecondUnresolvedCreate(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	first := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-1", "binding-1")
	if _, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	second := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-2", "binding-2")
	_, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, second)
	if !errors.Is(err, ErrCanonicalV19WorktreeCreateConflict) {
		t.Fatalf("second unresolved create error = %v, want %v", err, ErrCanonicalV19WorktreeCreateConflict)
	}
	if got := canonicalV19WorktreeCreateOperationCount(t, fixture.Home); got != 1 {
		t.Fatalf("external operation rows = %d, want 1", got)
	}
}

func TestCanonicalV19WorktreeCreateTerminalNoEffectAllowsReplacement(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	first := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-1", "binding-1")
	if _, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19WorktreeCreate(context.Background(), fixture.Home, CanonicalV19WorktreeCreateTransitionInput{
		OperationID: "operation-1", State: "no-effect", ObservedAt: "2026-09-05T15:01:00Z", EvidenceDigest: "no-effect-evidence-1",
	}); err != nil {
		t.Fatal(err)
	}

	second := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-2", "binding-2")
	second.CreatedAt = "2026-09-05T15:02:00Z"
	if _, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, second); err != nil {
		t.Fatal(err)
	}
	if got := canonicalV19WorktreeCreateOperationCount(t, fixture.Home); got != 2 {
		t.Fatalf("external operation rows = %d, want 2", got)
	}
}

func TestSubmitCanonicalV19WorktreeCreatePersistsAuthorization(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-1", "binding-1")
	prepared, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := SubmitCanonicalV19WorktreeCreate(context.Background(), fixture.Home, prepared.OperationID,
		"2026-09-05T15:01:00Z", "submit-evidence-1")
	if err != nil {
		t.Fatal(err)
	}
	if submitted != prepared {
		t.Fatalf("submitted request = %#v, want %#v", submitted, prepared)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var state, changedAt, evidence, submittedAt, finalizedAt string
	if err := db.sql.QueryRow(`SELECT state,state_changed_at,state_evidence_digest,submitted_at,finalized_at
		FROM external_operation WHERE id=?`, prepared.OperationID).Scan(&state, &changedAt, &evidence, &submittedAt, &finalizedAt); err != nil {
		t.Fatal(err)
	}
	if state != "submitted" || changedAt != "2026-09-05T15:01:00Z" || evidence != "submit-evidence-1" ||
		submittedAt != changedAt || finalizedAt != "" {
		t.Fatalf("submitted state = %q/%q/%q/%q/%q", state, changedAt, evidence, submittedAt, finalizedAt)
	}
	var events int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM external_operation_event WHERE operation_id=?`, prepared.OperationID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Fatalf("event rows = %d, want 2", events)
	}

	if _, err := SubmitCanonicalV19WorktreeCreate(context.Background(), fixture.Home, prepared.OperationID,
		"2026-09-05T15:02:00Z", "submit-evidence-2"); !errors.Is(err, ErrCanonicalV19WorktreeCreateTransition) {
		t.Fatalf("second submit error = %v, want %v", err, ErrCanonicalV19WorktreeCreateTransition)
	}
}

func TestClassifyCanonicalV19WorktreeCreateUncertainThenNoEffect(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-1", "binding-1")
	if _, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input.OperationID,
		"2026-09-05T15:01:00Z", "submit-evidence-1"); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19WorktreeCreate(context.Background(), fixture.Home, CanonicalV19WorktreeCreateTransitionInput{
		OperationID: input.OperationID, State: "uncertain", ObservedAt: "2026-09-05T15:02:00Z", EvidenceDigest: "uncertain-evidence-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19WorktreeCreate(context.Background(), fixture.Home, CanonicalV19WorktreeCreateTransitionInput{
		OperationID: input.OperationID, State: "no-effect", ObservedAt: "2026-09-05T15:03:00Z", EvidenceDigest: "no-effect-evidence-1",
	}); err != nil {
		t.Fatal(err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var state, finalizedAt string
	if err := db.sql.QueryRow(`SELECT state,finalized_at FROM external_operation WHERE id=?`, input.OperationID).Scan(&state, &finalizedAt); err != nil {
		t.Fatal(err)
	}
	if state != "no-effect" || finalizedAt != "2026-09-05T15:03:00Z" {
		t.Fatalf("terminal state = %q/%q", state, finalizedAt)
	}
	var events int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM external_operation_event WHERE operation_id=?`, input.OperationID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 4 {
		t.Fatalf("event rows = %d, want 4", events)
	}
}

func TestEstablishCanonicalV19WorktreeBindingIsAtomic(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-1", "binding-1")
	request, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID,
		"2026-09-05T15:01:00Z", "submit-evidence-1"); err != nil {
		t.Fatal(err)
	}
	evidence := canonicalV19WorktreeBindingEvidence(request, "worktree-physical-1")
	if err := EstablishCanonicalV19WorktreeBinding(context.Background(), fixture.Home, evidence); err != nil {
		t.Fatal(err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var state, finalizedAt string
	if err := db.sql.QueryRow(`SELECT state,finalized_at FROM external_operation WHERE id=?`, request.OperationID).Scan(&state, &finalizedAt); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || finalizedAt != evidence.EstablishedAt {
		t.Fatalf("operation success state = %q/%q", state, finalizedAt)
	}
	var attemptID, createOperationID, path, commonGitDir, privateGitDir, lockReason, basisRevision, headRevision, physicalIdentity, establishedAt string
	if err := db.sql.QueryRow(`SELECT attempt_id,create_operation_id,path,common_git_dir,private_git_dir,lock_reason,
		basis_revision,head_revision,physical_identity_digest,established_at FROM attempt_worktree_binding WHERE id=?`, request.BindingID).Scan(
		&attemptID, &createOperationID, &path, &commonGitDir, &privateGitDir, &lockReason,
		&basisRevision, &headRevision, &physicalIdentity, &establishedAt,
	); err != nil {
		t.Fatal(err)
	}
	if attemptID != request.AttemptID || createOperationID != request.OperationID || path != evidence.Path ||
		commonGitDir != evidence.CommonGitDir || privateGitDir != evidence.PrivateGitDir || lockReason != evidence.LockReason ||
		basisRevision != evidence.BasisRevision || headRevision != evidence.HeadRevision || physicalIdentity != evidence.PhysicalIdentityDigest ||
		establishedAt != evidence.EstablishedAt {
		t.Fatalf("persisted WorktreeBinding = %q/%q/%q/%q/%q/%q/%q/%q/%q/%q",
			attemptID, createOperationID, path, commonGitDir, privateGitDir, lockReason, basisRevision, headRevision, physicalIdentity, establishedAt)
	}

	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
		AttemptID: request.AttemptID, Lifecycle: "completed", TerminalAt: "2026-09-05T15:03:00Z",
	}); err != nil {
		t.Fatalf("terminalize after resolved WorktreeCreate: %v", err)
	}
}

func TestEstablishCanonicalV19WorktreeBindingAliasConflictRollsBackSuccess(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-1", "binding-1")
	request, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID,
		"2026-09-05T15:01:00Z", "submit-evidence-1"); err != nil {
		t.Fatal(err)
	}
	evidence := canonicalV19WorktreeBindingEvidence(request, "physical-digest-1")
	err = EstablishCanonicalV19WorktreeBinding(context.Background(), fixture.Home, evidence)
	if !errors.Is(err, ErrCanonicalV19WorktreeCreateConflict) {
		t.Fatalf("alias binding error = %v, want %v", err, ErrCanonicalV19WorktreeCreateConflict)
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
	if state != "submitted" {
		t.Fatalf("operation state after binding rollback = %q, want submitted", state)
	}
	var bindings int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM attempt_worktree_binding WHERE id=?`, request.BindingID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if bindings != 0 {
		t.Fatalf("binding rows after alias conflict = %d, want 0", bindings)
	}
}

func TestSubmitCanonicalV19WorktreeCreateRefusesSupersededWorkspaceBinding(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-1", "binding-1")
	request, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}

	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE workspace_binding SET superseded_at='2026-09-05T15:00:30Z' WHERE id='workspace-1'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_binding(
		id,project_id,ordinal,repository_locator,repository_identity_digest,common_git_dir,physical_identity_digest,revision,established_at,superseded_at
	) SELECT 'workspace-2',project_id,2,repository_locator,'repo-digest-2',common_git_dir,'physical-digest-2',revision,
		'2026-09-05T15:00:30Z','' FROM workspace_binding WHERE id='workspace-1'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = SubmitCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID,
		"2026-09-05T15:01:00Z", "submit-evidence-1")
	if !errors.Is(err, ErrCanonicalV19WorktreeCreateNotCurrent) {
		t.Fatalf("stale WorkspaceBinding submit error = %v, want %v", err, ErrCanonicalV19WorktreeCreateNotCurrent)
	}

	readDB, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readDB.Close() }()
	var state string
	if err := readDB.sql.QueryRow(`SELECT state FROM external_operation WHERE id=?`, request.OperationID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "prepared" {
		t.Fatalf("stale operation state = %q, want prepared", state)
	}
}

func TestTerminalizeCanonicalV19AttemptRefusesUnresolvedExternalOperation(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-1", "binding-1")
	if _, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}
	err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
		AttemptID: "attempt-1", Lifecycle: "failed", TerminalAt: "2026-09-05T15:01:00Z",
	})
	if !errors.Is(err, ErrCanonicalV19AttemptConflict) {
		t.Fatalf("terminalize with unresolved external operation error = %v, want %v", err, ErrCanonicalV19AttemptConflict)
	}
	if err := ClassifyCanonicalV19WorktreeCreate(context.Background(), fixture.Home, CanonicalV19WorktreeCreateTransitionInput{
		OperationID: input.OperationID, State: "no-effect", ObservedAt: "2026-09-05T15:02:00Z", EvidenceDigest: "no-effect-evidence-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
		AttemptID: "attempt-1", Lifecycle: "failed", TerminalAt: "2026-09-05T15:03:00Z",
	}); err != nil {
		t.Fatalf("terminalize after operation resolution: %v", err)
	}
}

func TestPrepareCanonicalV19WorktreeCreateRefusesTerminalAttempt(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
		AttemptID: "attempt-1", Lifecycle: "failed", TerminalAt: "2026-09-05T15:00:30Z",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home,
		canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-1", "binding-1"))
	if !errors.Is(err, ErrCanonicalV19WorktreeCreateNotCurrent) {
		t.Fatalf("terminal Attempt prepare error = %v, want %v", err, ErrCanonicalV19WorktreeCreateNotCurrent)
	}
}

type canonicalV19WorktreeCreateTestFixture struct {
	Home string
}

func canonicalV19WorktreeCreateFixture(t *testing.T) canonicalV19WorktreeCreateTestFixture {
	t.Helper()
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home,
		canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	return canonicalV19WorktreeCreateTestFixture{Home: fixture.Home}
}

func canonicalV19WorktreeCreatePrepareInput(home, operationID, bindingID string) CanonicalV19WorktreeCreatePrepareInput {
	return CanonicalV19WorktreeCreatePrepareInput{
		OperationID:   operationID,
		OperationKey:  "operation-key-" + operationID,
		AttemptID:     "attempt-1",
		BindingID:     bindingID,
		RequestedPath: filepath.Join(home, "worktrees", bindingID),
		CreatedAt:     "2026-09-05T15:00:00Z",
	}
}

func canonicalV19WorktreeBindingEvidence(request CanonicalV19WorktreeCreateRequest, physicalIdentity string) CanonicalV19WorktreeBindingEvidence {
	return CanonicalV19WorktreeBindingEvidence{
		OperationID:            request.OperationID,
		Path:                   request.RequestedPath,
		CommonGitDir:           request.ExpectedCommonGitDir,
		PrivateGitDir:          filepath.Join("worktrees", request.BindingID, ".git-private"),
		LockReason:             request.ExpectedLockReason,
		BasisRevision:          request.BasisRevision,
		HeadRevision:           request.BasisRevision,
		PhysicalIdentityDigest: physicalIdentity,
		EstablishedAt:          "2026-09-05T15:02:00Z",
		EvidenceDigest:         "binding-evidence-1",
	}
}

func canonicalV19WorktreeCreateOperationCount(t *testing.T, home string) int {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM external_operation`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
