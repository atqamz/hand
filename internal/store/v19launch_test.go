package store

import (
	"context"
	"errors"
	"testing"
)

func TestPrepareCanonicalV19LaunchPersistsExactStructuredRequestAndClaims(t *testing.T) {
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	input := canonicalV19LaunchPrepareInput(worktree, session, "operation-launch-1", "executor-binding-1")
	request, err := PrepareCanonicalV19Launch(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if request.ProjectID != "project-1" || request.TaskID != "task-1" || request.PlanID != "plan-root" ||
		request.AttemptID != worktree.AttemptID || request.WorktreeBindingID != worktree.BindingID ||
		request.SessionBindingID != session.BindingID || request.BindingID != input.BindingID ||
		request.AdapterRef != session.AdapterRef || request.Spec.Executable != "worker-bin" ||
		request.Spec.Cwd != worktree.RequestedPath || len(request.RequestDigest) != 64 || len(request.LaunchSpecDigest) != 64 {
		t.Fatalf("derived Launch request = %#v", request)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var kind, adapter, scopeKind, scopeKey, state, specDigest string
	if err := db.sql.QueryRow(`SELECT o.kind,o.adapter_ref,o.primary_scope_kind,o.primary_scope_key,o.state,l.launch_spec_digest
		FROM external_operation o JOIN launch_operation l ON l.operation_id=o.id WHERE o.id=?`, request.OperationID).Scan(
		&kind, &adapter, &scopeKind, &scopeKey, &state, &specDigest,
	); err != nil {
		t.Fatal(err)
	}
	if kind != "launch" || adapter != session.AdapterRef || scopeKind != "executor-control" ||
		scopeKey != request.BindingID || state != "prepared" || specDigest != request.LaunchSpecDigest {
		t.Fatalf("persisted Launch = %q/%q/%q/%q/%q/%q", kind, adapter, scopeKind, scopeKey, state, specDigest)
	}

	rows, err := db.sql.Query(`SELECT ordinal,value FROM launch_argument WHERE operation_id=? ORDER BY ordinal`, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var arguments []string
	for rows.Next() {
		var ordinal int
		var value string
		if err := rows.Scan(&ordinal, &value); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if ordinal != len(arguments) {
			_ = rows.Close()
			t.Fatalf("argument ordinal = %d, want %d", ordinal, len(arguments))
		}
		arguments = append(arguments, value)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(arguments) != 2 || arguments[0] != "--mode" || arguments[1] != "execute" {
		t.Fatalf("persisted arguments = %#v", arguments)
	}

	var roleKind, roleMaterial, roleDigest string
	if err := db.sql.QueryRow(`SELECT value_kind,value_material,value_digest FROM launch_environment
		WHERE operation_id=? AND name='HAND_ROLE'`, request.OperationID).Scan(&roleKind, &roleMaterial, &roleDigest); err != nil {
		t.Fatal(err)
	}
	if roleKind != "literal" || roleMaterial != "worker" || roleDigest != "digest-role-worker" {
		t.Fatalf("HAND_ROLE environment = %q/%q/%q", roleKind, roleMaterial, roleDigest)
	}
	var secretKind, secretMaterial, secretDigest string
	if err := db.sql.QueryRow(`SELECT value_kind,value_material,value_digest FROM launch_environment
		WHERE operation_id=? AND name='TOKEN'`, request.OperationID).Scan(&secretKind, &secretMaterial, &secretDigest); err != nil {
		t.Fatal(err)
	}
	if secretKind != "secret-ref" || secretMaterial != "secret://worker/token" || secretDigest != "digest-secret-value" {
		t.Fatalf("TOKEN environment = %q/%q/%q", secretKind, secretMaterial, secretDigest)
	}

	var executorClaims, sessionClaims int
	if err := db.sql.QueryRow(`SELECT count(*) FROM operation_scope_claim
		WHERE operation_id=? AND scope_kind='executor-control' AND scope_key=?`, request.OperationID, request.BindingID).Scan(&executorClaims); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT count(*) FROM operation_scope_claim
		WHERE operation_id=? AND scope_kind='session' AND scope_key=?`, request.OperationID, request.SessionBindingID).Scan(&sessionClaims); err != nil {
		t.Fatal(err)
	}
	if executorClaims != 1 || sessionClaims != 1 {
		t.Fatalf("Launch claims executor/session = %d/%d, want 1/1", executorClaims, sessionClaims)
	}
}

func TestPrepareCanonicalV19LaunchRequiresExactWorktreeCwd(t *testing.T) {
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	input := canonicalV19LaunchPrepareInput(worktree, session, "operation-launch-1", "executor-binding-1")
	input.Spec.Cwd = worktree.RequestedPath + "-other"
	_, err := PrepareCanonicalV19Launch(context.Background(), fixture.Home, input)
	if !errors.Is(err, ErrCanonicalV19LaunchNotCurrent) {
		t.Fatalf("cwd mismatch error = %v, want %v", err, ErrCanonicalV19LaunchNotCurrent)
	}
}

func TestPreparedCanonicalV19LaunchBlocksSessionRelease(t *testing.T) {
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	input := canonicalV19LaunchPrepareInput(worktree, session, "operation-launch-1", "executor-binding-1")
	if _, err := PrepareCanonicalV19Launch(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}
	_, err := PrepareCanonicalV19SessionRelease(context.Background(), fixture.Home, CanonicalV19SessionReleasePrepareInput{
		OperationID: "operation-session-release-during-launch", OperationKey: "operation-key-session-release-during-launch",
		SessionBindingID: session.BindingID, CreatedAt: "2026-09-06T17:04:00Z",
	})
	if !errors.Is(err, ErrCanonicalV19SessionConflict) {
		t.Fatalf("SessionRelease during unresolved Launch error = %v, want %v", err, ErrCanonicalV19SessionConflict)
	}
}

func TestCanonicalV19LaunchNoEffectAllowsReplacement(t *testing.T) {
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	first := canonicalV19LaunchPrepareInput(worktree, session, "operation-launch-1", "executor-binding-1")
	if _, err := PrepareCanonicalV19Launch(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19Launch(context.Background(), fixture.Home, CanonicalV19LaunchTransitionInput{
		OperationID: first.OperationID, State: "no-effect", ObservedAt: "2026-09-06T17:04:00Z", EvidenceDigest: "launch-no-effect-1",
	}); err != nil {
		t.Fatal(err)
	}
	second := canonicalV19LaunchPrepareInput(worktree, session, "operation-launch-2", "executor-binding-2")
	second.CreatedAt = "2026-09-06T17:05:00Z"
	if _, err := PrepareCanonicalV19Launch(context.Background(), fixture.Home, second); err != nil {
		t.Fatalf("replacement Launch after no-effect: %v", err)
	}
}

func TestCanonicalV19LaunchUncertainThenExecutorEstablishmentIsAtomic(t *testing.T) {
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	input := canonicalV19LaunchPrepareInput(worktree, session, "operation-launch-1", "executor-binding-1")
	request, err := PrepareCanonicalV19Launch(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Launch(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T17:04:00Z", "launch-submit-1"); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19Launch(context.Background(), fixture.Home, CanonicalV19LaunchTransitionInput{
		OperationID: request.OperationID, State: "uncertain", ObservedAt: "2026-09-06T17:05:00Z", EvidenceDigest: "launch-uncertain-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19ExecutorBinding(context.Background(), fixture.Home, CanonicalV19ExecutorBindingEvidence{
		OperationID: request.OperationID, ProviderExecutorKey: "provider-executor-1",
		EstablishedAt: "2026-09-06T17:06:00Z", EvidenceDigest: "executor-established-1",
	}); err != nil {
		t.Fatalf("establish ExecutorBinding after uncertain: %v", err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var state, attemptID, sessionID, launchOperationID, adapter, providerKey, establishedAt string
	if err := db.sql.QueryRow(`SELECT o.state,e.attempt_id,e.session_binding_id,e.launch_operation_id,e.adapter_ref,e.provider_executor_key,e.established_at
		FROM external_operation o JOIN executor_binding e ON e.launch_operation_id=o.id WHERE e.id=?`, request.BindingID).Scan(
		&state, &attemptID, &sessionID, &launchOperationID, &adapter, &providerKey, &establishedAt,
	); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || attemptID != request.AttemptID || sessionID != request.SessionBindingID ||
		launchOperationID != request.OperationID || adapter != request.AdapterRef || providerKey != "provider-executor-1" ||
		establishedAt != "2026-09-06T17:06:00Z" {
		t.Fatalf("ExecutorBinding = %q/%q/%q/%q/%q/%q/%q", state, attemptID, sessionID, launchOperationID, adapter, providerKey, establishedAt)
	}
}

func TestSuccessfulCanonicalV19LaunchBlocksReplacementLaunch(t *testing.T) {
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	first := canonicalV19LaunchPrepareInput(worktree, session, "operation-launch-1", "executor-binding-1")
	request, err := PrepareCanonicalV19Launch(context.Background(), fixture.Home, first)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19ExecutorBinding(context.Background(), fixture.Home, CanonicalV19ExecutorBindingEvidence{
		OperationID: request.OperationID, ProviderExecutorKey: "provider-executor-1",
		EstablishedAt: "2026-09-06T17:04:00Z", EvidenceDigest: "executor-established-1",
	}); err != nil {
		t.Fatal(err)
	}
	second := canonicalV19LaunchPrepareInput(worktree, session, "operation-launch-2", "executor-binding-2")
	second.CreatedAt = "2026-09-06T17:05:00Z"
	_, err = PrepareCanonicalV19Launch(context.Background(), fixture.Home, second)
	if !errors.Is(err, ErrCanonicalV19LaunchNotCurrent) {
		t.Fatalf("replacement Launch after ExecutorBinding error = %v, want %v", err, ErrCanonicalV19LaunchNotCurrent)
	}
}

func TestPrepareCanonicalV19LaunchRefusesReleasedSession(t *testing.T) {
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	release, err := PrepareCanonicalV19SessionRelease(context.Background(), fixture.Home, CanonicalV19SessionReleasePrepareInput{
		OperationID: "operation-session-release-1", OperationKey: "operation-key-session-release-1",
		SessionBindingID: session.BindingID, CreatedAt: "2026-09-06T17:04:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19SessionRelease(context.Background(), fixture.Home, CanonicalV19SessionReleasedEvidence{
		OperationID: release.OperationID, ReleasedAt: "2026-09-06T17:05:00Z", EvidenceDigest: "session-released-1",
	}); err != nil {
		t.Fatal(err)
	}
	input := canonicalV19LaunchPrepareInput(worktree, session, "operation-launch-1", "executor-binding-1")
	input.CreatedAt = "2026-09-06T17:06:00Z"
	_, err = PrepareCanonicalV19Launch(context.Background(), fixture.Home, input)
	if !errors.Is(err, ErrCanonicalV19LaunchNotCurrent) {
		t.Fatalf("Launch on released SessionBinding error = %v, want %v", err, ErrCanonicalV19LaunchNotCurrent)
	}
}

func TestSuccessfulCanonicalV19LaunchBlocksSessionReleaseUntilExecutorTerminates(t *testing.T) {
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	input := canonicalV19LaunchPrepareInput(worktree, session, "operation-launch-1", "executor-binding-1")
	request, err := PrepareCanonicalV19Launch(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19ExecutorBinding(context.Background(), fixture.Home, CanonicalV19ExecutorBindingEvidence{
		OperationID: request.OperationID, ProviderExecutorKey: "provider-executor-1",
		EstablishedAt: "2026-09-06T17:04:00Z", EvidenceDigest: "executor-established-1",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = PrepareCanonicalV19SessionRelease(context.Background(), fixture.Home, CanonicalV19SessionReleasePrepareInput{
		OperationID: "operation-session-release-open-executor", OperationKey: "operation-key-session-release-open-executor",
		SessionBindingID: session.BindingID, CreatedAt: "2026-09-06T17:05:00Z",
	})
	if !errors.Is(err, ErrCanonicalV19SessionConflict) {
		t.Fatalf("SessionRelease with open ExecutorBinding error = %v, want %v", err, ErrCanonicalV19SessionConflict)
	}
}

func TestPrepareCanonicalV19LaunchValidatesTypedEnvironment(t *testing.T) {
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	input := canonicalV19LaunchPrepareInput(worktree, session, "operation-launch-1", "executor-binding-1")
	input.Spec.Environment["TOKEN"] = CanonicalV19LaunchEnvironmentValue{
		ValueKind: "secret-ref", ValueMaterial: "", ValueDigest: "digest-secret-value",
	}
	_, err := PrepareCanonicalV19Launch(context.Background(), fixture.Home, input)
	if err == nil {
		t.Fatal("empty secret reference unexpectedly accepted")
	}
}

func canonicalV19LaunchPrepareInput(
	worktree CanonicalV19WorktreeCreateRequest,
	session CanonicalV19SessionAcquireRequest,
	operationID string,
	bindingID string,
) CanonicalV19LaunchPrepareInput {
	return CanonicalV19LaunchPrepareInput{
		OperationID: operationID, OperationKey: "operation-key-" + operationID,
		AttemptID: worktree.AttemptID, SessionBindingID: session.BindingID, BindingID: bindingID,
		Spec: CanonicalV19LaunchSpec{
			Executable: "worker-bin",
			Arguments:  []string{"--mode", "execute"},
			Environment: map[string]CanonicalV19LaunchEnvironmentValue{
				"HAND_ROLE": {ValueKind: "literal", ValueMaterial: "worker", ValueDigest: "digest-role-worker"},
				"TOKEN":     {ValueKind: "secret-ref", ValueMaterial: "secret://worker/token", ValueDigest: "digest-secret-value"},
			},
			Cwd: worktree.RequestedPath,
		},
		CreatedAt: "2026-09-06T17:03:00Z",
	}
}
