//go:build e2e

package e2e

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

func TestCanonicalFleetSnapshotCoreCLI(t *testing.T) {
	parent := t.TempDir()
	fleet := filepath.Join(parent, "fleet")
	if got := runHand(t, parent, "init", "--canonical", fleet); got.code != 0 {
		t.Fatal(got)
	}
	initGitRepo(t, filepath.Join(fleet, "projects", "sample"))
	ok := func(args ...string) invocation {
		t.Helper()
		got := runHand(t, fleet, args...)
		if got.code != 0 {
			t.Fatalf("%v: %+v", args, got)
		}
		return got
	}
	registered := ok("project", "register", "sample")
	projectID := canonicalOutputField(t, registered, "project_id")
	workspaceID := canonicalOutputField(t, registered, "workspace_binding_id")
	ok("task", "create", "task-1", "--project-id", projectID, "--goal", "inspect canonical source")
	ok("task", "create", "task-2", "--project-id", projectID, "--goal", "another task")
	ok("project", "policy", "policy-1", "--project-id", projectID,
		"--worker-profile-ref", "", "--qualification-policy-ref", "", "--integration-policy-ref", "",
		"--production-policy-ref", "", "--publication-policy-ref", "")
	ok("plan", "create", "plan-1", "--task-id", "task-1", "--workspace-binding-id", workspaceID,
		"--policy-revision-id", "policy-1", "--intent", "explore", "--judgment", "bounded",
		"--basis", "registered repository", "--brief", "inspect exact state")
	before := snapshotTree(t, fleet)
	shown := ok("fleet", "snapshot")
	if canonicalOutputField(t, shown, "snapshot_schema") != "hand.fleet.core.v1" ||
		canonicalOutputField(t, shown, "completeness") != "partial" ||
		canonicalOutputField(t, shown, "attention") != "unknown" {
		t.Fatalf("snapshot claimed complete authority: %+v", shown)
	}
	if !strings.Contains(shown.stdout, projectID) || !strings.Contains(shown.stdout, "plan-1") ||
		strings.Index(shown.stdout, "task-1") >= strings.Index(shown.stdout, "task-2") {
		t.Fatalf("snapshot lost exact ordered lineage: %+v", shown)
	}
	if !strings.Contains(shown.stdout, "unacknowledged_inputs[0]") || !strings.Contains(shown.stdout, "unresolved_operations[0]") {
		t.Fatalf("snapshot omitted independent input and effect families: %+v", shown)
	}
	if !strings.Contains(shown.stdout, "report_state") {
		t.Fatalf("snapshot omitted typed WorkerReport Attention state: %+v", shown)
	}
	assertTreeUnchanged(t, fleet, before)
}

func TestCanonicalFleetSnapshotShowsLatestWorkerReportAndExactAcknowledgement(t *testing.T) {
	parent := t.TempDir()
	fleet := filepath.Join(parent, "fleet")
	if got := runHand(t, parent, "init", "--canonical", fleet); got.code != 0 {
		t.Fatal(got)
	}
	initGitRepo(t, filepath.Join(fleet, "projects", "sample"))
	ok := func(args ...string) invocation {
		t.Helper()
		got := runHand(t, fleet, args...)
		if got.code != 0 {
			t.Fatalf("%v: %+v", args, got)
		}
		return got
	}
	registered := ok("project", "register", "sample")
	projectID := canonicalOutputField(t, registered, "project_id")
	workspaceID := canonicalOutputField(t, registered, "workspace_binding_id")
	ok("task", "create", "task-1", "--project-id", projectID, "--goal", "inspect report")
	ok("project", "policy", "policy-1", "--project-id", projectID,
		"--worker-profile-ref", "", "--qualification-policy-ref", "", "--integration-policy-ref", "",
		"--production-policy-ref", "", "--publication-policy-ref", "")
	ok("plan", "create", "plan-1", "--task-id", "task-1", "--workspace-binding-id", workspaceID,
		"--policy-revision-id", "policy-1", "--intent", "explore", "--judgment", "bounded",
		"--basis", "registered repository", "--brief", "inspect exact state")
	if _, err := store.CreateCanonicalV19Attempt(context.Background(), fleet, store.CanonicalV19AttemptCreateInput{
		ID: "attempt-1", PlanID: "plan-1", WorkerHarnessRef: "worker-harness/codex",
		WorkerHarnessVersion: "1.0.0", WorkerProfileRef: "profile/default", ModelRef: "model/example",
		EffortRef: "medium", SessionAdapterRef: "builtin/session", CreatedAt: "2026-09-24T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	execFleetFixtureSQL(t, fleet, `INSERT INTO worker_report(
		id,attempt_id,source_prefix_digest,source_end_offset,report_state,note,created_at
	) VALUES('report-1','attempt-1','prefix-1',17,'blocked','wait for evidence','2026-09-24T00:01:00Z')`)
	before := snapshotTree(t, fleet)
	shown := ok("fleet", "snapshot")
	if !strings.Contains(shown.stdout, "latest_worker_reports[1]{attempt_id,report_id,source_prefix_digest,source_end_offset,report_state,acknowledged}:") ||
		!strings.Contains(shown.stdout, "attempt-1,report-1,prefix-1,17,blocked,false") ||
		!strings.Contains(shown.stdout, "current-latest-worker-report-unacknowledged") ||
		strings.Contains(shown.stdout, "wait for evidence") {
		t.Fatalf("snapshot lost bounded unacknowledged report metadata: %+v", shown)
	}
	assertTreeUnchanged(t, fleet, before)
	if _, err := store.CreateCanonicalV19WorkerReportAcknowledgement(context.Background(), fleet,
		store.CanonicalV19WorkerReportAcknowledgementCreateInput{
			WorkerReportID: "report-1", ActorKind: "supervisor", AcknowledgedAt: "2026-09-24T00:02:00Z",
			EvidenceDigest: "read-report-1",
		}); err != nil {
		t.Fatal(err)
	}
	before = snapshotTree(t, fleet)
	shown = ok("fleet", "snapshot")
	if !strings.Contains(shown.stdout, "attempt-1,report-1,prefix-1,17,blocked,true") ||
		strings.Contains(shown.stdout, "current-latest-worker-report-unacknowledged") {
		t.Fatalf("snapshot lost exact report acknowledgement: %+v", shown)
	}
	assertTreeUnchanged(t, fleet, before)
}

func TestCanonicalFleetSnapshotShowsOnlyOpenResourceIdentities(t *testing.T) {
	parent := t.TempDir()
	fleet := filepath.Join(parent, "fleet")
	if got := runHand(t, parent, "init", "--canonical", fleet); got.code != 0 {
		t.Fatal(got)
	}
	initGitRepo(t, filepath.Join(fleet, "projects", "sample"))
	ok := func(args ...string) invocation {
		t.Helper()
		got := runHand(t, fleet, args...)
		if got.code != 0 {
			t.Fatalf("%v: %+v", args, got)
		}
		return got
	}
	registered := ok("project", "register", "sample")
	projectID := canonicalOutputField(t, registered, "project_id")
	workspaceID := canonicalOutputField(t, registered, "workspace_binding_id")
	ok("task", "create", "task-1", "--project-id", projectID, "--goal", "inspect resources")
	ok("project", "policy", "policy-1", "--project-id", projectID,
		"--worker-profile-ref", "", "--qualification-policy-ref", "", "--integration-policy-ref", "",
		"--production-policy-ref", "", "--publication-policy-ref", "")
	ok("plan", "create", "plan-1", "--task-id", "task-1", "--workspace-binding-id", workspaceID,
		"--policy-revision-id", "policy-1", "--intent", "explore", "--judgment", "bounded",
		"--basis", "registered repository", "--brief", "inspect exact state")
	ctx := context.Background()
	if _, err := store.CreateCanonicalV19Attempt(ctx, fleet, store.CanonicalV19AttemptCreateInput{
		ID: "attempt-1", PlanID: "plan-1", WorkerHarnessRef: "worker-harness/codex",
		WorkerHarnessVersion: "1.0.0", WorkerProfileRef: "profile/default", ModelRef: "model/example",
		EffortRef: "medium", SessionAdapterRef: "builtin/session", CreatedAt: "2026-09-24T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	worktree, err := store.PrepareCanonicalV19WorktreeCreate(ctx, fleet, store.CanonicalV19WorktreeCreatePrepareInput{
		OperationID: "create-1", OperationKey: "create-key-1", AttemptID: "attempt-1", BindingID: "worktree-1",
		RequestedPath: filepath.Join(fleet, "worktrees", "worktree-1"), CreatedAt: "2026-09-24T00:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EstablishCanonicalV19WorktreeBinding(ctx, fleet, store.CanonicalV19WorktreeBindingEvidence{
		OperationID: worktree.OperationID, Path: worktree.RequestedPath, CommonGitDir: worktree.ExpectedCommonGitDir,
		PrivateGitDir: filepath.Join("worktrees", worktree.BindingID, ".git-private"), LockReason: worktree.ExpectedLockReason,
		BasisRevision: worktree.BasisRevision, HeadRevision: worktree.BasisRevision,
		PhysicalIdentityDigest: "physical-1", EstablishedAt: "2026-09-24T00:02:00Z", EvidenceDigest: "worktree-established",
	}); err != nil {
		t.Fatal(err)
	}
	session, err := store.PrepareCanonicalV19SessionAcquire(ctx, fleet, store.CanonicalV19SessionAcquirePrepareInput{
		OperationID: "acquire-1", OperationKey: "acquire-key-1", AttemptID: "attempt-1", BindingID: "session-1",
		RequestedProviderSessionKey: "provider-session-sensitive", CreatedAt: "2026-09-24T00:03:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EstablishCanonicalV19SessionBinding(ctx, fleet, store.CanonicalV19SessionBindingEvidence{
		OperationID: session.OperationID, ProviderSessionKey: "provider-session-sensitive",
		EstablishedAt: "2026-09-24T00:04:00Z", EvidenceDigest: "session-established",
	}); err != nil {
		t.Fatal(err)
	}
	launch, err := store.PrepareCanonicalV19Launch(ctx, fleet, store.CanonicalV19LaunchPrepareInput{
		OperationID: "launch-1", OperationKey: "launch-key-1", AttemptID: "attempt-1",
		SessionBindingID: session.BindingID, BindingID: "executor-1", CreatedAt: "2026-09-24T00:05:00Z",
		Spec: store.CanonicalV19LaunchSpec{Executable: "worker-bin", Cwd: worktree.RequestedPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EstablishCanonicalV19ExecutorBinding(ctx, fleet, store.CanonicalV19ExecutorBindingEvidence{
		OperationID: launch.OperationID, ProviderExecutorKey: "provider-executor-sensitive",
		EstablishedAt: "2026-09-24T00:06:00Z", EvidenceDigest: "executor-established",
	}); err != nil {
		t.Fatal(err)
	}
	show := func(worktrees, sessions, executors int) invocation {
		t.Helper()
		before := snapshotTree(t, fleet)
		got := ok("fleet", "snapshot")
		for _, want := range []string{
			"current_open_worktree_bindings[" + strconv.Itoa(worktrees) + "]",
			"current_open_session_bindings[" + strconv.Itoa(sessions) + "]",
			"current_open_executor_bindings[" + strconv.Itoa(executors) + "]",
		} {
			if !strings.Contains(got.stdout, want) {
				t.Fatalf("resource snapshot missing %q: %+v", want, got)
			}
		}
		for _, forbidden := range []string{"provider-session-sensitive", "provider-executor-sensitive", worktree.RequestedPath} {
			if strings.Contains(got.stdout, forbidden) {
				t.Fatalf("resource snapshot disclosed %q: %+v", forbidden, got)
			}
		}
		assertTreeUnchanged(t, fleet, before)
		return got
	}
	shown := show(1, 1, 1)
	for _, want := range []string{
		"current_open_worktree_bindings[1]{id,attempt_id}:\n  worktree-1,attempt-1",
		"current_open_session_bindings[1]{id,attempt_id,worktree_binding_id,adapter_ref}:\n  session-1,attempt-1,worktree-1,builtin/session",
		"current_open_executor_bindings[1]{id,attempt_id,session_binding_id,adapter_ref}:\n  executor-1,attempt-1,session-1,builtin/session",
	} {
		if !strings.Contains(shown.stdout, want) {
			t.Fatalf("open resource row %q missing: %+v", want, shown)
		}
	}
	execFleetFixtureSQL(t, fleet, `INSERT INTO executor_binding_termination(
		executor_binding_id,terminal_kind,observed_at,evidence_digest
	) VALUES('executor-1','provider-gone','2026-09-24T00:07:00Z','executor-gone')`)
	show(1, 1, 0)
	release, err := store.PrepareCanonicalV19SessionRelease(ctx, fleet, store.CanonicalV19SessionReleasePrepareInput{
		OperationID: "release-1", OperationKey: "release-key-1", SessionBindingID: session.BindingID,
		CreatedAt: "2026-09-24T00:08:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteCanonicalV19SessionRelease(ctx, fleet, store.CanonicalV19SessionReleasedEvidence{
		OperationID: release.OperationID, ReleasedAt: "2026-09-24T00:09:00Z", EvidenceDigest: "session-gone",
	}); err != nil {
		t.Fatal(err)
	}
	show(1, 0, 0)
	remove, err := store.PrepareCanonicalV19WorktreeRemove(ctx, fleet, store.CanonicalV19WorktreeRemovePrepareInput{
		OperationID: "remove-1", OperationKey: "remove-key-1", AttemptID: "attempt-1",
		BindingID: worktree.BindingID, CreatedAt: "2026-09-24T00:10:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteCanonicalV19WorktreeRemove(ctx, fleet, store.CanonicalV19WorktreeRemovedEvidence{
		OperationID: remove.OperationID, RemovedAt: "2026-09-24T00:11:00Z", EvidenceDigest: "worktree-gone",
	}); err != nil {
		t.Fatal(err)
	}
	show(0, 0, 0)
}

func TestCanonicalFleetSnapshotCoreRefusesMissingAndLegacyStores(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		fleet := t.TempDir()
		if legacy {
			createCutoverLegacyFixture(t, fleet)
		}
		seedPrivateRuntime(t, fleet)
		before := snapshotTree(t, fleet)
		if got := runHand(t, fleet, "fleet", "snapshot"); got.code == 0 {
			t.Fatalf("accepted missing/legacy Fleet: %+v", got)
		}
		assertTreeUnchanged(t, fleet, before)
	}
}
