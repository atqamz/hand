package runtime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

func TestTeardownFailureAfterWorktreeReturnPreservesOwnershipEvidence(t *testing.T) {
	home, worktree := teardownFixture(t, true)
	phaseErr := errors.New("stop after worktree return")
	returned := false
	deps := defaultDependencies()
	deps.worktree.returnWorktree = func(path string, force bool) error {
		if path != worktree || force {
			t.Fatalf("returnWorktree(%q, %t), want (%q, false)", path, force, worktree)
		}
		returned = true
		return nil
	}
	deps.phase = func(phase lifecyclePhase) error {
		if phase == phaseWorktreeReturned {
			return phaseErr
		}
		return nil
	}

	_, err := (&Runtime{deps: deps}).Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"})
	if !errors.Is(err, phaseErr) {
		t.Fatalf("Teardown() = %v, want %v", err, phaseErr)
	}
	if !returned {
		t.Fatal("Teardown() did not return the worktree")
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Worktree != worktree {
		t.Fatalf("history after returned-worktree failure = %+v, want active ownership evidence", history)
	}
	if records, err := completion.List(home); err != nil {
		t.Fatal(err)
	} else if len(records) != 0 {
		t.Fatalf("completions after returned-worktree failure = %+v, want none", records)
	}
}

func TestTeardownCompletionAppendFailureLeavesStateRetryable(t *testing.T) {
	home, _ := teardownFixture(t, true)
	appendErr := errors.New("completion store unavailable")
	deps := defaultDependencies()
	deps.worktree.returnWorktree = func(string, bool) error { return nil }
	deps.appendCompletion = func(string, completion.Record) error { return appendErr }

	_, err := (&Runtime{deps: deps}).Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"})
	if !errors.Is(err, appendErr) {
		t.Fatalf("Teardown() = %v, want %v", err, appendErr)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.Task.Lifecycle != state.TaskOpen {
		t.Fatalf("history after completion failure = %+v, want open active task", history)
	}
	if records, err := completion.List(home); err != nil {
		t.Fatal(err)
	} else if len(records) != 0 {
		t.Fatalf("completions after append failure = %+v, want none", records)
	}
}

func TestTeardownFailureAfterCompletionAppendLeavesBoundaryObservable(t *testing.T) {
	home, _ := teardownFixture(t, true)
	phaseErr := errors.New("stop after completion append")
	deps := defaultDependencies()
	deps.worktree.returnWorktree = func(string, bool) error { return nil }
	deps.phase = func(phase lifecyclePhase) error {
		if phase == phaseCompletionAppended {
			return phaseErr
		}
		return nil
	}

	_, err := (&Runtime{deps: deps}).Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"})
	if !errors.Is(err, phaseErr) {
		t.Fatalf("Teardown() = %v, want %v", err, phaseErr)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.Task.Lifecycle != state.TaskOpen {
		t.Fatalf("history after post-append failure = %+v, want open active task", history)
	}
	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "task-1" {
		t.Fatalf("completions after post-append failure = %+v, want one task record", records)
	}
}

func TestTeardownRetryAfterCompletionAppendDoesNotRepeatCleanup(t *testing.T) {
	home, worktree := teardownFixture(t, true)
	returned := 0
	phaseFailure := true
	phaseErr := errors.New("stop after completion append")
	deps := defaultDependencies()
	deps.worktree.returnWorktree = func(path string, force bool) error {
		if path != worktree || force {
			t.Fatalf("returnWorktree(%q, %t), want (%q, false)", path, force, worktree)
		}
		returned++
		return nil
	}
	deps.phase = func(phase lifecyclePhase) error {
		if phase == phaseCompletionAppended && phaseFailure {
			phaseFailure = false
			return phaseErr
		}
		return nil
	}
	runtime := &Runtime{deps: deps}
	request := TeardownRequest{Home: home, ID: "task-1"}
	if _, err := runtime.Teardown(context.Background(), request); !errors.Is(err, phaseErr) {
		t.Fatalf("first Teardown() = %v, want %v", err, phaseErr)
	}
	if _, err := runtime.Teardown(context.Background(), request); err != nil {
		t.Fatalf("retry Teardown() = %v", err)
	}
	if returned != 1 {
		t.Fatalf("worktree return count = %d, want one release", returned)
	}
	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("completions after retry = %+v, want one record", records)
	}
}

func TestTeardownReportsIncompleteHerdrOwnership(t *testing.T) {
	home, _ := teardownFixture(t, false)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	history.ActiveAttempt.Herdr.PaneID = "pane-only"
	if err := state.UpdateAttempt(home, *history.ActiveAttempt); err != nil {
		t.Fatal(err)
	}

	result, err := New().Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatalf("Teardown() = %v", err)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "Herdr ownership incomplete") {
		t.Fatalf("warnings = %v, want incomplete ownership warning", result.Warnings)
	}
}

func TestTeardownSecondInvocationRefusesWithoutRepeatingCompletion(t *testing.T) {
	home, _ := teardownFixture(t, false)
	runtime := New()
	request := TeardownRequest{Home: home, ID: "task-1"}
	if _, err := runtime.Teardown(context.Background(), request); err != nil {
		t.Fatalf("first Teardown() = %v", err)
	}
	if _, err := runtime.Teardown(context.Background(), request); err == nil {
		t.Fatal("second Teardown() succeeded, want no active attempt refusal")
	} else {
		var classified *Error
		if !errors.As(err, &classified) || classified.Kind != ErrorPrecondition {
			t.Fatalf("second Teardown() = %v, want precondition refusal", err)
		}
	}
	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("completions after repeated teardown = %+v, want one record", records)
	}
}

func TestTeardownDoesNotUseAnOlderSameSecondCompletion(t *testing.T) {
	home, _ := teardownFixture(t, false)
	first, err := state.ActiveAttempt(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", first.ID, state.AttemptRunning, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	old := completion.Record{ID: "task-1", Project: "demo", Kind: state.KindScout, Outcome: "done", Detail: "old-without-attempt-id", TornDownAt: first.CreatedAt}
	if err := completion.Append(home, old); err != nil {
		t.Fatal(err)
	}
	identifiedOld := old
	identifiedOld.Detail = "old-attempt-1"
	identifiedOld.AttemptID = first.ID
	if err := completion.Append(home, identifiedOld); err != nil {
		t.Fatal(err)
	}
	second, err := state.ReopenTask(home, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptProvisioning, CreatedAt: first.CreatedAt})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatalf("reopened attempt ID = %d, want a fresh attempt", second.ID)
	}
	deps := defaultDependencies()
	deps.appendCompletion = completion.Append
	result, err := (&Runtime{deps: deps}).Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Detail != "attempt never launched" {
		t.Fatalf("result detail = %q, want fresh attempt teardown detail", result.Detail)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Attempts) != 2 || history.Attempts[1].ID != second.ID || history.Attempts[1].Lifecycle != state.AttemptInterrupted {
		t.Fatalf("attempt history = %+v, want second attempt interrupted", history.Attempts)
	}
}

func TestTeardownDecisionKeepsLaunchedProvisioningDetail(t *testing.T) {
	terminal, disposition := teardownDecision(false, true, state.AttemptProvisioning, false)
	if terminal != state.AttemptInterrupted {
		t.Fatalf("terminal lifecycle = %s, want interrupted", terminal)
	}
	if disposition != state.TeardownDispositionLaunchedProvisioning {
		t.Fatalf("disposition = %q, want launched-provisioning", disposition)
	}
	record := completionFor(state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, PR: "https://example.com/pr/1"}, disposition, true)
	if record.Detail == "attempt never launched" {
		t.Fatalf("completion = %+v, launched provisioning must keep landed-work detail", record)
	}
}

func TestTeardownRetryPreservesForcedDisposition(t *testing.T) {
	home, _ := teardownFixture(t, false)
	phaseFailure := true
	deps := defaultDependencies()
	deps.phase = func(phase lifecyclePhase) error {
		if phase == phaseCompletionAppended && phaseFailure {
			phaseFailure = false
			return errors.New("crash after forced completion")
		}
		return nil
	}
	runtime := &Runtime{deps: deps}
	if _, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1", Force: true}); err == nil {
		t.Fatal("forced teardown succeeded across injected crash")
	}
	if _, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Attempts[0].Lifecycle != state.AttemptInterrupted {
		t.Fatalf("attempt lifecycle = %s, want interrupted", history.Attempts[0].Lifecycle)
	}
	records, err := completion.List(home)
	if err != nil || len(records) != 1 || records[0].Detail != "forced (landed-work checks skipped)" {
		t.Fatalf("completion records = %+v, err=%v", records, err)
	}
}

func TestTeardownForcedRetryKeepsForcedWorktreeReturn(t *testing.T) {
	home, worktree := teardownFixture(t, true)
	phaseFailure := true
	returned := 0
	deps := defaultDependencies()
	deps.worktree.returnWorktree = func(path string, force bool) error {
		if path != worktree || !force {
			t.Fatalf("returnWorktree(%q, %t), want (%q, true)", path, force, worktree)
		}
		returned++
		return nil
	}
	deps.phase = func(phase lifecyclePhase) error {
		if phase == phaseHerdrReleased && phaseFailure {
			phaseFailure = false
			return errors.New("stop before worktree return")
		}
		return nil
	}
	runtime := &Runtime{deps: deps}
	if _, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1", Force: true}); err == nil {
		t.Fatal("forced teardown succeeded across injected pre-return failure")
	}
	result, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if returned != 1 || result.Detail != "forced (landed-work checks skipped)" {
		t.Fatalf("retry returned=%d result=%+v, want forced return and detail", returned, result)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Attempts[0].Lifecycle != state.AttemptInterrupted {
		t.Fatalf("attempt lifecycle = %s, want interrupted", history.Attempts[0].Lifecycle)
	}
}

func TestTeardownRetryPreservesCompletedDisposition(t *testing.T) {
	home, _ := teardownFixture(t, false)
	phaseFailure := true
	deps := defaultDependencies()
	deps.phase = func(phase lifecyclePhase) error {
		if phase == phaseCompletionAppended && phaseFailure {
			phaseFailure = false
			return errors.New("crash after completed completion")
		}
		return nil
	}
	runtime := &Runtime{deps: deps}
	if _, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"}); err == nil {
		t.Fatal("teardown succeeded across injected crash")
	}
	if _, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1", Force: true}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Attempts[0].Lifecycle != state.AttemptCompleted {
		t.Fatalf("attempt lifecycle = %s, want completed", history.Attempts[0].Lifecycle)
	}
}

func TestTeardownRetrySkipsReleasedWorktreeAfterLeaseReused(t *testing.T) {
	home, worktree := teardownFixture(t, true)
	returns := 0
	reacquired := false
	phaseFailure := true
	deps := defaultDependencies()
	deps.worktree.returnWorktree = func(path string, force bool) error {
		if path != worktree || force {
			t.Fatalf("returnWorktree(%q, %t), want (%q, false)", path, force, worktree)
		}
		if reacquired {
			t.Fatal("stale teardown attempted to return the recycled L2 lease")
		}
		returns++
		return nil
	}
	deps.phase = func(phase lifecyclePhase) error {
		if phase == phaseWorktreeReturned && phaseFailure {
			phaseFailure = false
			return errors.New("crash after lease L1 returned")
		}
		return nil
	}
	runtime := &Runtime{deps: deps}
	if _, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"}); err == nil {
		t.Fatal("teardown succeeded across injected lease-reuse crash")
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.TeardownWorktreeState != state.TeardownResourceReleased {
		t.Fatalf("worktree teardown state = %+v, want released evidence", history.ActiveAttempt)
	}
	// The fake Treehouse can now assign this path to another holder. The stale retry must not call
	// its path-addressed return operation again.
	reacquired = true
	if _, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	if returns != 1 {
		t.Fatalf("worktree return count = %d, want one release", returns)
	}
}

func TestTeardownRetriesKnownAbortedReturnWithForce(t *testing.T) {
	home, worktreePath := teardownFixture(t, true)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	history.ActiveAttempt.LeaseID = "lease-1"
	if err := state.UpdateAttempt(home, *history.ActiveAttempt); err != nil {
		t.Fatal(err)
	}
	returns := 0
	verified := 0
	deps := defaultDependencies()
	deps.worktree.verifyLease = func(path, leaseID string) error {
		if path != worktreePath || leaseID != "lease-1" {
			t.Fatalf("verifyLease(%q, %q), want (%q, %q)", path, leaseID, worktreePath, "lease-1")
		}
		verified++
		return nil
	}
	deps.worktree.returnWithID = func(path, leaseID string, force bool) error {
		if path != worktreePath {
			t.Fatalf("returnWithID path = %q, want %q", path, worktreePath)
		}
		if leaseID != "lease-1" {
			t.Fatalf("returnWithID lease ID = %q, want lease-1", leaseID)
		}
		returns++
		if !force {
			return worktree.ErrReturnAborted
		}
		return nil
	}
	runtime := &Runtime{deps: deps}
	if _, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"}); !errors.Is(err, worktree.ErrReturnAborted) {
		t.Fatalf("first Teardown() = %v, want aborted-return error", err)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.TeardownWorktreeState != state.TeardownResourceRetryable {
		t.Fatalf("worktree state after aborted return = %+v, want retryable evidence", history.ActiveAttempt)
	}
	result, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if returns != 2 || verified != 2 || result.Detail != "report "+filepath.Join("data", "task-1", "report.md") {
		t.Fatalf("retry returns=%d verified=%d result=%+v, want verified forced retry and unchanged detail", returns, verified, result)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Attempts[0].Lifecycle != state.AttemptCompleted {
		t.Fatalf("attempt lifecycle = %s, want completed", history.Attempts[0].Lifecycle)
	}
}

func TestTeardownUsesConditionalReturnForKnownLease(t *testing.T) {
	home, worktreePath := teardownFixture(t, true)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	history.ActiveAttempt.LeaseID = "lease-1"
	if err := state.UpdateAttempt(home, *history.ActiveAttempt); err != nil {
		t.Fatal(err)
	}
	called := false
	deps := defaultDependencies()
	deps.worktree.verifyLease = func(path, leaseID string) error {
		if path != worktreePath || leaseID != "lease-1" {
			t.Fatalf("verifyLease(%q, %q), want (%q, %q)", path, leaseID, worktreePath, "lease-1")
		}
		return nil
	}
	deps.worktree.returnWorktree = func(string, bool) error {
		t.Fatal("teardown used path-only return for a known lease")
		return nil
	}
	deps.worktree.returnWithID = func(path, leaseID string, force bool) error {
		if path != worktreePath || leaseID != "lease-1" || force {
			t.Fatalf("returnWithID(%q, %q, %t), want (%q, %q, false)", path, leaseID, force, worktreePath, "lease-1")
		}
		called = true
		return nil
	}

	if err := (&Runtime{deps: deps}).releaseWorktree(home, "task-1", *history.ActiveAttempt, false); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("releaseWorktree did not use the identity-targeted return")
	}
}

func TestTeardownRefusesARecycledWorktreeLeaseOnFirstReturn(t *testing.T) {
	home, worktreePath := teardownFixture(t, true)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	history.ActiveAttempt.LeaseID = "lease-1"
	if err := state.UpdateAttempt(home, *history.ActiveAttempt); err != nil {
		t.Fatal(err)
	}
	verified := 0
	returns := 0
	deps := defaultDependencies()
	deps.worktree.verifyLease = func(path, leaseID string) error {
		if path != worktreePath || leaseID != "lease-1" {
			t.Fatalf("verifyLease(%q, %q), want (%q, %q)", path, leaseID, worktreePath, "lease-1")
		}
		verified++
		return errors.New("treehouse lease is lease-2")
	}
	deps.worktree.returnWorktree = func(string, bool) error {
		returns++
		return nil
	}

	_, err = (&Runtime{deps: deps}).Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"})
	if err == nil || !strings.Contains(err.Error(), "verify worktree ownership") {
		t.Fatalf("Teardown() = %v, want lease verification refusal", err)
	}
	if verified != 1 {
		t.Fatalf("lease verification count = %d, want one", verified)
	}
	if returns != 0 {
		t.Fatalf("worktree return count = %d, want no destructive return", returns)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != state.TaskOpen || history.ActiveAttempt == nil {
		t.Fatalf("history after lease mismatch = %+v, want open task and active attempt", history)
	}
	if history.ActiveAttempt.Lifecycle != state.AttemptRunning || history.ActiveAttempt.TeardownWorktreeState != state.TeardownResourceAmbiguous {
		t.Fatalf("attempt after lease mismatch = %+v, want running and ambiguous worktree state", history.ActiveAttempt)
	}
	if records, err := completion.List(home); err != nil {
		t.Fatal(err)
	} else if len(records) != 0 {
		t.Fatalf("completions after lease mismatch = %+v, want none", records)
	}
}

func TestTeardownRetryRefusesWorktreeWithoutLeaseIdentity(t *testing.T) {
	home, worktreePath := teardownFixture(t, true)
	returns := 0
	verified := 0
	deps := defaultDependencies()
	deps.worktree.verifyLease = func(path, leaseID string) error {
		if path != worktreePath || leaseID != "" {
			t.Fatalf("verifyLease(%q, %q), want (%q, empty)", path, leaseID, worktreePath)
		}
		verified++
		return errors.New("missing lease identity")
	}
	deps.worktree.returnWorktree = func(string, bool) error {
		returns++
		return worktree.ErrReturnAborted
	}
	runtime := &Runtime{deps: deps}
	if _, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"}); !errors.Is(err, worktree.ErrReturnAborted) {
		t.Fatalf("first Teardown() = %v, want known abort", err)
	}
	if _, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1", Force: true}); err == nil {
		t.Fatal("forced retry succeeded without a lease identity")
	}
	if returns != 1 || verified != 1 {
		t.Fatalf("retry returns=%d verified=%d, want one initial return and one failed verification", returns, verified)
	}
}

func TestBranchIsMergedUsesOriginDefaultBranch(t *testing.T) {
	clonePath := filepath.Join(t.TempDir(), "clone")
	initRuntimeGitRepo(t, clonePath)
	runRuntimeGit(t, clonePath, "branch", "release")
	runRuntimeGit(t, clonePath, "branch", "task-1")
	runRuntimeGit(t, clonePath, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	runRuntimeGit(t, clonePath, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	runRuntimeGit(t, clonePath, "worktree", "add", "-q", worktreePath, "task-1")
	if err := os.WriteFile(filepath.Join(worktreePath, "feature.txt"), []byte("feature"), 0o644); err != nil {
		t.Fatal(err)
	}
	runRuntimeGit(t, worktreePath, "add", "feature.txt")
	runRuntimeGit(t, worktreePath, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "feature")
	runRuntimeGit(t, clonePath, "-c", "user.name=test", "-c", "user.email=test@example.com", "merge", "--no-ff", "-q", "task-1", "-m", "merge task")
	runRuntimeGit(t, clonePath, "checkout", "-q", "release")
	merged, err := branchIsMerged(clonePath, worktreePath)
	if err != nil || !merged {
		t.Fatalf("branchIsMerged() = %t, %v, want true", merged, err)
	}
}

func initRuntimeGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runRuntimeGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	runRuntimeGit(t, dir, "add", "README.md")
	runRuntimeGit(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "initial commit")
}

func runRuntimeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, output)
	}
}

func teardownFixture(t *testing.T, withWorktree bool) (string, string) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("findings\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://example.com/demo.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "worktree")
	path := ""
	if withWorktree {
		path = worktree
	}
	if err := state.CreateTask(home, state.Task{
		ID: "task-1", Project: "demo", Kind: state.KindScout, Lifecycle: state.TaskOpen,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.CreateAttempt(home, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptRunning, Worktree: path, CreatedAt: "2026-08-14T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	return home, worktree
}
