package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReconcileCanonicalV19WorktreeRemoveRemovesExactNativeWorktree(t *testing.T) {
	fixture, binding := canonicalV19NativeWorktreeRemoveFixture(t, "native-remove-success")
	input := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-native")
	input.CreatedAt = "2026-09-07T00:00:00Z"
	request, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}

	state, err := ReconcileCanonicalV19WorktreeRemove(context.Background(), fixture.Home, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" {
		t.Fatalf("reconcile state = %q, want succeeded", state)
	}
	if _, err := os.Stat(request.Path); !os.IsNotExist(err) {
		t.Fatalf("removed worktree path still exists: %v", err)
	}
	privateGitDir := canonicalV19ObservedPath(fixture.Home, request.ExpectedPrivateGitDir)
	if _, err := os.Stat(privateGitDir); !os.IsNotExist(err) {
		t.Fatalf("removed private Git directory still exists: %v", err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var persistedState string
	var releaseCount int
	if err := db.sql.QueryRow(`SELECT state FROM external_operation WHERE id=?`, request.OperationID).Scan(&persistedState); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM worktree_binding_release WHERE binding_id=? AND remove_operation_id=?`,
		request.BindingID, request.OperationID).Scan(&releaseCount); err != nil {
		t.Fatal(err)
	}
	if persistedState != "succeeded" || releaseCount != 1 {
		t.Fatalf("persisted remove = state %q releases %d, want succeeded/1", persistedState, releaseCount)
	}
	for _, transition := range []string{"submitted", "succeeded"} {
		var count int
		if err := db.sql.QueryRow(`SELECT COUNT(*) FROM external_operation_event WHERE operation_id=? AND to_state=?`, request.OperationID, transition).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s transition count = %d, want 1", transition, count)
		}
	}
}

func TestReconcileCanonicalV19WorktreeRemoveSubmitsBeforeFirstMutation(t *testing.T) {
	fixture, binding := canonicalV19WorktreeRemoveFixture(t)
	input := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-boundary")
	request, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}

	observeCalls := 0
	performCalls := 0
	nowCalls := 0
	base := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	deps := canonicalV19WorktreeRemoveNativeDeps{
		observe: func(_ string, got CanonicalV19WorktreeRemoveRequest) canonicalV19GitWorktreeRemoveObservation {
			observeCalls++
			if observeCalls <= 2 {
				return canonicalV19FinalizeGitWorktreeRemoveObservation(got, canonicalV19GitWorktreeRemoveObservation{
					State: canonicalV19GitWorktreeExact, Locked: true, LockReason: got.ExpectedLockReason,
				})
			}
			return canonicalV19FinalizeGitWorktreeRemoveObservation(got, canonicalV19GitWorktreeRemoveObservation{
				State: canonicalV19GitWorktreeAbsent,
			})
		},
		perform: func(_ string, got CanonicalV19WorktreeRemoveRequest) error {
			performCalls++
			db, err := openReadOnly(fixture.Home)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			var state, submittedAt string
			if err := db.sql.QueryRow(`SELECT state,submitted_at FROM external_operation WHERE id=?`, got.OperationID).Scan(&state, &submittedAt); err != nil {
				t.Fatal(err)
			}
			if state != "submitted" || submittedAt == "" {
				t.Fatalf("state at first mutation = %q submitted_at=%q, want submitted with durable authorization", state, submittedAt)
			}
			return nil
		},
		now: func() time.Time {
			nowCalls++
			return base.Add(time.Duration(nowCalls) * time.Second)
		},
	}

	state, err := reconcileCanonicalV19WorktreeRemove(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || performCalls != 1 || observeCalls != 3 {
		t.Fatalf("reconcile = state %q perform=%d observe=%d, want succeeded/1/3", state, performCalls, observeCalls)
	}
}

func TestReconcileSubmittedCanonicalV19WorktreeRemoveDoesNotBlindRetry(t *testing.T) {
	fixture, binding := canonicalV19WorktreeRemoveFixture(t)
	input := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-submitted")
	request, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19WorktreeRemove(context.Background(), fixture.Home, request.OperationID,
		"2026-09-05T15:04:00Z", strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}

	performCalls := 0
	state, err := reconcileCanonicalV19WorktreeRemove(context.Background(), fixture.Home, request.OperationID, canonicalV19WorktreeRemoveNativeDeps{
		observe: func(_ string, got CanonicalV19WorktreeRemoveRequest) canonicalV19GitWorktreeRemoveObservation {
			return canonicalV19FinalizeGitWorktreeRemoveObservation(got, canonicalV19GitWorktreeRemoveObservation{
				State: canonicalV19GitWorktreeExact, Locked: true, LockReason: got.ExpectedLockReason,
			})
		},
		perform: func(string, CanonicalV19WorktreeRemoveRequest) error {
			performCalls++
			return errors.New("must not run")
		},
		now: time.Now,
	})
	if state != "no-effect" || err == nil || performCalls != 0 {
		t.Fatalf("submitted reconcile = %q, %v perform=%d, want no-effect/error/0", state, err, performCalls)
	}
}

func TestReconcileSubmittedCanonicalV19WorktreeRemoveKeepsUnlockCrashResidueUncertain(t *testing.T) {
	fixture, binding := canonicalV19NativeWorktreeRemoveFixture(t, "native-remove-unlock-crash")
	input := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-unlock-crash")
	input.CreatedAt = "2026-09-07T00:00:00Z"
	request, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19WorktreeRemove(context.Background(), fixture.Home, request.OperationID,
		"2026-09-07T00:00:01Z", strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	repository := canonicalV19ObservedPath(fixture.Home, request.RepositoryLocator)
	canonicalV19PlanWriterGit(t, repository, "worktree", "unlock", request.Path)

	state, err := ReconcileCanonicalV19WorktreeRemove(context.Background(), fixture.Home, request.OperationID)
	if state != "uncertain" || err == nil {
		t.Fatalf("unlocked crash residue reconcile = %q, %v, want uncertain/error", state, err)
	}
	if _, err := os.Stat(request.Path); err != nil {
		t.Fatalf("uncertain recovery removed worktree: %v", err)
	}

	canonicalV19PlanWriterGit(t, repository, "worktree", "lock", "--reason", request.ExpectedLockReason, request.Path)
	state, err = ReconcileCanonicalV19WorktreeRemove(context.Background(), fixture.Home, request.OperationID)
	if state != "no-effect" || err == nil {
		t.Fatalf("later exact reconciliation = %q, %v, want no-effect/error", state, err)
	}
	if _, err := os.Stat(request.Path); err != nil {
		t.Fatalf("no-effect reconciliation removed worktree: %v", err)
	}
}

func TestReconcilePreparedCanonicalV19WorktreeRemoveRefusesReallocatedSamePath(t *testing.T) {
	fixture, binding := canonicalV19NativeWorktreeRemoveFixture(t, "native-remove-reallocated")
	input := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-reallocated")
	input.CreatedAt = "2026-09-07T00:00:00Z"
	request, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	repository := canonicalV19ObservedPath(fixture.Home, request.RepositoryLocator)
	canonicalV19PlanWriterGit(t, repository, "worktree", "unlock", request.Path)
	canonicalV19PlanWriterGit(t, repository, "worktree", "remove", request.Path)
	canonicalV19PlanWriterGit(t, repository, "worktree", "add", "--detach", "--lock", "--reason", "hand:v1:foreign",
		request.Path, request.ExpectedHeadRevision)
	sentinel := filepath.Join(request.Path, "foreign.txt")
	if err := os.WriteFile(sentinel, []byte("foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := ReconcileCanonicalV19WorktreeRemove(context.Background(), fixture.Home, request.OperationID)
	if state != "prepared" || err == nil {
		t.Fatalf("reallocated path reconcile = %q, %v, want prepared/error", state, err)
	}
	contents, readErr := os.ReadFile(sentinel)
	if readErr != nil || string(contents) != "foreign\n" {
		t.Fatalf("reallocated worktree was modified: %q, %v", contents, readErr)
	}
}

func TestReconcilePreparedCanonicalV19WorktreeRemoveRefusesIgnoredContent(t *testing.T) {
	fixture, binding := canonicalV19NativeWorktreeRemoveFixture(t, "native-remove-ignored")
	input := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-ignored")
	input.CreatedAt = "2026-09-07T00:00:00Z"
	request, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	commonDir := canonicalV19ObservedPath(fixture.Home, request.ExpectedCommonGitDir)
	if err := os.WriteFile(filepath.Join(commonDir, "info", "exclude"), []byte("scratch.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(request.Path, "scratch.log")
	if err := os.WriteFile(scratch, []byte("preserve me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status := strings.TrimSpace(canonicalV19PlanWriterGit(t, request.Path, "status", "--porcelain", "--untracked-files=all")); status != "" {
		t.Fatalf("fixture is not Git-clean before ignored-content test: %q", status)
	}

	state, err := ReconcileCanonicalV19WorktreeRemove(context.Background(), fixture.Home, request.OperationID)
	if state != "prepared" || err == nil {
		t.Fatalf("ignored content reconcile = %q, %v, want prepared/error", state, err)
	}
	contents, readErr := os.ReadFile(scratch)
	if readErr != nil || string(contents) != "preserve me\n" {
		t.Fatalf("ignored content was removed: %q, %v", contents, readErr)
	}
}

func canonicalV19NativeWorktreeRemoveFixture(t *testing.T, suffix string) (canonicalV19WorktreeCreateTestFixture, CanonicalV19WorktreeCreateRequest) {
	t.Helper()
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-create-"+suffix, "binding-"+suffix)
	if err := os.MkdirAll(filepath.Dir(input.RequestedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	binding, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ReconcileCanonicalV19WorktreeCreate(context.Background(), fixture.Home, binding.OperationID)
	if err != nil || state != "succeeded" {
		t.Fatalf("create native WorktreeBinding = %q, %v", state, err)
	}
	return fixture, binding
}
