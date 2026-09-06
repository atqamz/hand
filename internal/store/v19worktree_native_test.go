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

func TestReconcileCanonicalV19WorktreeCreateCreatesExactLockedNativeWorktree(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-native-1", "binding-native-1")
	if err := os.MkdirAll(filepath.Dir(input.RequestedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	request, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}

	state, err := ReconcileCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" {
		t.Fatalf("reconcile state = %q, want succeeded", state)
	}
	observed := observeCanonicalV19GitWorktree(fixture.Home, request)
	if observed.State != canonicalV19GitWorktreeExact {
		t.Fatalf("native observation = %#v, want exact", observed)
	}
	if observed.LockReason != request.ExpectedLockReason || observed.HeadRevision != request.BasisRevision ||
		observed.PrivateGitDir == "" || len(observed.PhysicalIdentityDigest) != 64 || len(observed.EvidenceDigest) != 64 {
		t.Fatalf("native exact evidence = %#v", observed)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var operationState, path, commonGitDir, privateGitDir, lockReason, headRevision, physicalIdentity string
	if err := db.sql.QueryRow(`SELECT o.state,b.path,b.common_git_dir,b.private_git_dir,b.lock_reason,b.head_revision,b.physical_identity_digest
		FROM external_operation o JOIN attempt_worktree_binding b ON b.create_operation_id=o.id WHERE o.id=?`, request.OperationID).Scan(
		&operationState, &path, &commonGitDir, &privateGitDir, &lockReason, &headRevision, &physicalIdentity,
	); err != nil {
		t.Fatal(err)
	}
	if operationState != "succeeded" || path != request.RequestedPath || commonGitDir != request.ExpectedCommonGitDir ||
		privateGitDir != observed.PrivateGitDir || lockReason != request.ExpectedLockReason || headRevision != request.BasisRevision ||
		physicalIdentity != observed.PhysicalIdentityDigest {
		t.Fatalf("persisted native binding = state=%q path=%q common=%q private=%q lock=%q HEAD=%q physical=%q",
			operationState, path, commonGitDir, privateGitDir, lockReason, headRevision, physicalIdentity)
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

func TestReconcileCanonicalV19WorktreeCreateSubmitsBeforeFirstMutation(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-boundary", "binding-boundary")
	request, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}

	observeCalls := 0
	performCalls := 0
	nowCalls := 0
	base := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	deps := canonicalV19WorktreeCreateNativeDeps{
		observe: func(_ string, got CanonicalV19WorktreeCreateRequest) canonicalV19GitWorktreeObservation {
			observeCalls++
			if observeCalls <= 2 {
				return canonicalV19FinalizeGitWorktreeObservation(got, canonicalV19GitWorktreeObservation{State: canonicalV19GitWorktreeAbsent})
			}
			return canonicalV19FinalizeGitWorktreeObservation(got, canonicalV19GitWorktreeObservation{
				State:                  canonicalV19GitWorktreeExact,
				PrivateGitDir:          filepath.Join(fixture.Home, ".git", "worktrees", got.BindingID),
				LockReason:             got.ExpectedLockReason,
				HeadRevision:           got.BasisRevision,
				PhysicalIdentityDigest: strings.Repeat("a", 64),
			})
		},
		perform: func(_ string, got CanonicalV19WorktreeCreateRequest) error {
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

	state, err := reconcileCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID, deps)
	if err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || performCalls != 1 || observeCalls != 3 {
		t.Fatalf("reconcile = state %q perform=%d observe=%d, want succeeded/1/3", state, performCalls, observeCalls)
	}
}

func TestReconcileSubmittedCanonicalV19WorktreeCreateDoesNotBlindRetry(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-submitted-absent", "binding-submitted-absent")
	request, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T09:00:01Z", strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}

	state, err := ReconcileCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID)
	if state != "no-effect" || err == nil {
		t.Fatalf("submitted absent reconcile = %q, %v, want no-effect with diagnostic", state, err)
	}
	if _, statErr := os.Stat(request.RequestedPath); !os.IsNotExist(statErr) {
		t.Fatalf("submitted recovery mutated requested path: %v", statErr)
	}
}

func TestReconcileSubmittedCanonicalV19WorktreeCreateConvergesExactCrashResidue(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-submitted-exact", "binding-submitted-exact")
	if err := os.MkdirAll(filepath.Dir(input.RequestedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	request, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T09:00:01Z", strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	repository := canonicalV19ObservedPath(fixture.Home, request.RepositoryLocator)
	canonicalV19PlanWriterGit(t, repository, "worktree", "add", "--detach", "--lock", "--reason", request.ExpectedLockReason,
		request.RequestedPath, request.BasisRevision)

	state, err := ReconcileCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID)
	if err != nil || state != "succeeded" {
		t.Fatalf("submitted exact reconcile = %q, %v, want succeeded", state, err)
	}
}

func TestReconcilePreparedCanonicalV19WorktreeCreateDoesNotOverwriteForeignPath(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-foreign", "binding-foreign")
	if err := os.MkdirAll(input.RequestedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(input.RequestedPath, "foreign.txt")
	if err := os.WriteFile(sentinel, []byte("foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}

	state, err := ReconcileCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID)
	if state != "prepared" || err == nil {
		t.Fatalf("foreign path reconcile = %q, %v, want prepared with diagnostic", state, err)
	}
	contents, readErr := os.ReadFile(sentinel)
	if readErr != nil || string(contents) != "foreign\n" {
		t.Fatalf("foreign path was modified: %q, %v", contents, readErr)
	}
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var persistedState, submittedAt string
	if err := db.sql.QueryRow(`SELECT state,submitted_at FROM external_operation WHERE id=?`, request.OperationID).Scan(&persistedState, &submittedAt); err != nil {
		t.Fatal(err)
	}
	if persistedState != "prepared" || submittedAt != "" {
		t.Fatalf("foreign path persisted state = %q submitted_at=%q, want prepared without submission", persistedState, submittedAt)
	}
}

func TestReconcileSubmittedCanonicalV19WorktreeCreateKeepsForeignPathUncertain(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-submitted-foreign", "binding-submitted-foreign")
	request, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T09:00:01Z", strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(request.RequestedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(request.RequestedPath, "foreign.txt")
	if err := os.WriteFile(sentinel, []byte("foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := ReconcileCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID)
	if state != "uncertain" || err == nil {
		t.Fatalf("submitted foreign reconcile = %q, %v, want uncertain with diagnostic", state, err)
	}
	contents, readErr := os.ReadFile(sentinel)
	if readErr != nil || string(contents) != "foreign\n" {
		t.Fatalf("submitted foreign path was modified: %q, %v", contents, readErr)
	}
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var persistedState string
	if err := db.sql.QueryRow(`SELECT state FROM external_operation WHERE id=?`, request.OperationID).Scan(&persistedState); err != nil {
		t.Fatal(err)
	}
	if persistedState != "uncertain" {
		t.Fatalf("submitted foreign persisted state = %q, want uncertain", persistedState)
	}
}

func TestReconcileUncertainCanonicalV19WorktreeCreateNeverMutates(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-uncertain", "binding-uncertain")
	request, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T09:00:01Z", strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19WorktreeCreate(context.Background(), fixture.Home, CanonicalV19WorktreeCreateTransitionInput{
		OperationID: request.OperationID, State: "uncertain", ObservedAt: "2026-09-06T09:00:02Z", EvidenceDigest: strings.Repeat("e", 64),
	}); err != nil {
		t.Fatal(err)
	}

	performCalls := 0
	state, err := reconcileCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID, canonicalV19WorktreeCreateNativeDeps{
		observe: func(_ string, got CanonicalV19WorktreeCreateRequest) canonicalV19GitWorktreeObservation {
			return canonicalV19FinalizeGitWorktreeObservation(got, canonicalV19GitWorktreeObservation{
				State: canonicalV19GitWorktreeUnknown, Reason: "provider reality unavailable",
			})
		},
		perform: func(string, CanonicalV19WorktreeCreateRequest) error {
			performCalls++
			return errors.New("must not run")
		},
		now: time.Now,
	})
	if state != "uncertain" || err == nil || performCalls != 0 {
		t.Fatalf("uncertain reconcile = %q, %v perform=%d, want uncertain/error/0", state, err, performCalls)
	}
}

func TestReconcileUncertainCanonicalV19WorktreeCreateConvergesFromLaterExactProof(t *testing.T) {
	fixture := canonicalV19WorktreeCreateFixture(t)
	input := canonicalV19WorktreeCreatePrepareInput(fixture.Home, "operation-uncertain-exact", "binding-uncertain-exact")
	if err := os.MkdirAll(filepath.Dir(input.RequestedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	request, err := PrepareCanonicalV19WorktreeCreate(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID,
		"2026-09-06T09:00:01Z", strings.Repeat("1", 64)); err != nil {
		t.Fatal(err)
	}
	if err := ClassifyCanonicalV19WorktreeCreate(context.Background(), fixture.Home, CanonicalV19WorktreeCreateTransitionInput{
		OperationID: request.OperationID, State: "uncertain", ObservedAt: "2026-09-06T09:00:02Z", EvidenceDigest: strings.Repeat("2", 64),
	}); err != nil {
		t.Fatal(err)
	}
	repository := canonicalV19ObservedPath(fixture.Home, request.RepositoryLocator)
	canonicalV19PlanWriterGit(t, repository, "worktree", "add", "--detach", "--lock", "--reason", request.ExpectedLockReason,
		request.RequestedPath, request.BasisRevision)

	performCalls := 0
	state, err := reconcileCanonicalV19WorktreeCreate(context.Background(), fixture.Home, request.OperationID, canonicalV19WorktreeCreateNativeDeps{
		observe: observeCanonicalV19GitWorktree,
		perform: func(string, CanonicalV19WorktreeCreateRequest) error {
			performCalls++
			return errors.New("must not run")
		},
		now: time.Now,
	})
	if err != nil || state != "succeeded" || performCalls != 0 {
		t.Fatalf("uncertain exact reconcile = %q, %v perform=%d, want succeeded/nil/0", state, err, performCalls)
	}
}

func TestParseCanonicalV19GitWorktreeListPreservesLockReason(t *testing.T) {
	head := strings.Repeat("a", 40)
	output := "worktree /repo\x00HEAD " + head + "\x00detached\x00locked hand:v1:binding-1\x00\x00"
	records, err := parseCanonicalV19GitWorktreeList(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Path != "/repo" || records[0].HeadRevision != head ||
		!records[0].Detached || !records[0].Locked || records[0].LockReason != "hand:v1:binding-1" {
		t.Fatalf("parsed records = %#v", records)
	}
}
