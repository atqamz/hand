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
	deps.worktree.returnWorktree = func(_, path string, force bool) error {
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

func TestTeardownMigratesLegacyCompletionsBeforeAppending(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("findings\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(completion.Path(home), []byte(`{"id":"legacy","project":"demo","kind":"ship","outcome":"merged","detail":"","torndown_at":"2026-08-01T00:00:00Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.CreateTask(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindScout, Lifecycle: state.TaskOpen}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.CreateAttempt(home, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptRunning, CreatedAt: "2026-08-14T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	if _, err := New().Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1", Force: true}); err != nil {
		t.Fatal(err)
	}
	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Version != completion.RecordVersion || records[0].ProjectID != completion.ProjectIDUnknown {
		t.Fatalf("records = %+v, want the legacy line migrated before teardown appended", records)
	}
}

func TestTeardownCompletionAppendFailureLeavesStateRetryable(t *testing.T) {
	home, _ := teardownFixture(t, true)
	appendErr := errors.New("completion store unavailable")
	deps := defaultDependencies()
	deps.worktree.returnWorktree = func(string, string, bool) error { return nil }
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
	deps.worktree.returnWorktree = func(string, string, bool) error { return nil }
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
	deps.worktree.returnWorktree = func(_, path string, force bool) error {
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

func TestTeardownReleasesResourcesRecordedOnTerminalTask(t *testing.T) {
	home, worktreePath, _ := terminalResourceFixture(t)
	clonePath := filepath.Join(home, "projects", "demo")
	client := &teardownHerdr{}
	returns := 0
	deps := defaultDependencies()
	deps.herdr = func() herdrClient { return client }
	deps.worktree.observeLease = func(gotClonePath, path, leaseID string) worktree.LeaseObservation {
		if gotClonePath != clonePath {
			t.Fatalf("observeLease clone path = %q, want %q", gotClonePath, clonePath)
		}
		if path != worktreePath || leaseID != "lease-1" {
			t.Fatalf("observeLease(%q, %q), want (%q, lease-1)", path, leaseID, worktreePath)
		}
		return worktree.LeaseObservation{State: worktree.LeaseExact, LeaseID: leaseID}
	}
	deps.worktree.observeCommits = func(string) worktree.CommitSafetyObservation {
		return worktree.CommitSafetyObservation{State: worktree.CommitSafetyRemoteObserved}
	}
	deps.worktree.returnWithID = func(gotClonePath, path, leaseID string, force bool) error {
		if gotClonePath != clonePath {
			t.Fatalf("returnWithID clone path = %q, want %q", gotClonePath, clonePath)
		}
		if path != worktreePath || leaseID != "lease-1" || force {
			t.Fatalf("returnWithID(%q, %q, %t), want the proven terminal lease returned", path, leaseID, force)
		}
		returns++
		return nil
	}

	if _, err := (&Runtime{deps: deps}).Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if returns != 1 || client.closes != 1 || history.Task.Lifecycle != state.TaskTerminal || history.Attempts[0].TeardownWorktreeState != state.TeardownResourceReleased || history.Attempts[0].TeardownHerdrState != state.TeardownResourceReleased {
		t.Fatalf("returns=%d closes=%d history=%+v, want terminal resources released", returns, client.closes, history)
	}
}

func TestTeardownRefusesTerminalTaskWhenCommitSafetyIsUnprovable(t *testing.T) {
	home, worktreePath, _ := terminalResourceFixture(t)
	returns := 0
	deps := defaultDependencies()
	deps.worktree.observeLease = func(string, string, string) worktree.LeaseObservation {
		return worktree.LeaseObservation{State: worktree.LeaseExact, LeaseID: "lease-1"}
	}
	deps.worktree.observeCommits = func(string) worktree.CommitSafetyObservation {
		return worktree.CommitSafetyObservation{State: worktree.CommitSafetyUnknown, Probe: worktree.CommitSafetyProbe{WorkingDir: worktreePath}}
	}
	deps.worktree.returnWithID = func(string, string, string, bool) error { returns++; return nil }

	_, err := (&Runtime{deps: deps}).Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"})
	if err == nil || !strings.Contains(err.Error(), "commit safety") {
		t.Fatalf("Teardown() = %v, want commit-safety refusal", err)
	}
	if returns != 0 {
		t.Fatalf("worktree returns = %d, want no return", returns)
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

// Covers atqamz/hand#235: the worker's own self-reported failure, not the teardown act, is what a
// completion record must attribute a failed outcome to.
func TestTeardownReportsGenuineWorkerFailureAsFailedOutcome(t *testing.T) {
	home, _ := teardownFixture(t, false)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	history.ActiveAttempt.LastReportState = state.ReportFailed
	history.ActiveAttempt.LastReportNote = "tests would not pass"
	if err := state.UpdateAttempt(home, *history.ActiveAttempt); err != nil {
		t.Fatal(err)
	}

	result, err := New().Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatalf("Teardown() = %v", err)
	}
	if result.Outcome != "failed" || result.Detail != "tests would not pass" {
		t.Fatalf("result = %+v, want the worker's own failure report carried through, not a teardown detail", result)
	}

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Outcome != "failed" {
		t.Fatalf("completions = %+v, want the failure durable in the ledger, not only in rendered output", records)
	}
}

// Covers atqamz/hand#235: --force skips the landed-work checks, but it must not turn a task nobody
// reported failed into a recorded failure.
func TestTeardownForceOnASuccessfulTaskIsNotRecordedAsFailed(t *testing.T) {
	home, _ := teardownFixture(t, false)

	result, err := New().Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1", Force: true})
	if err != nil {
		t.Fatalf("Teardown() = %v", err)
	}
	if result.Outcome != "torn-down" || result.Detail != "forced (landed-work checks skipped)" {
		t.Fatalf("result = %+v, want the forced disposition outcome, not an invented failure", result)
	}

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Outcome == "failed" {
		t.Fatalf("completions = %+v, want --force on a successful task to leave the record's truth alone", records)
	}
}

// Covers atqamz/hand#235: forcing both teardowns proves --force does not decide the outcome either
// way - a genuine self-reported failure still reads as failed, a task nobody reported failed still
// does not, and the two stay distinct in the same durable ledger shape.
func TestTeardownDistinguishesGenuineFailureFromForcedSuccessInDurableState(t *testing.T) {
	failedHome, _ := teardownFixture(t, false)
	history, err := state.ReadHistory(failedHome, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	history.ActiveAttempt.LastReportState = state.ReportFailed
	if err := state.UpdateAttempt(failedHome, *history.ActiveAttempt); err != nil {
		t.Fatal(err)
	}
	if _, err := New().Teardown(context.Background(), TeardownRequest{Home: failedHome, ID: "task-1", Force: true}); err != nil {
		t.Fatalf("Teardown() = %v", err)
	}

	successHome, _ := teardownFixture(t, false)
	if _, err := New().Teardown(context.Background(), TeardownRequest{Home: successHome, ID: "task-1", Force: true}); err != nil {
		t.Fatalf("Teardown() = %v", err)
	}

	failedRecords, err := completion.List(failedHome)
	if err != nil || len(failedRecords) != 1 {
		t.Fatalf("failed completions = %+v, err=%v", failedRecords, err)
	}
	successRecords, err := completion.List(successHome)
	if err != nil || len(successRecords) != 1 {
		t.Fatalf("success completions = %+v, err=%v", successRecords, err)
	}
	if failedRecords[0].Outcome != "failed" {
		t.Fatalf("failed record outcome = %q, want failed even under --force", failedRecords[0].Outcome)
	}
	if successRecords[0].Outcome == "failed" {
		t.Fatalf("success record outcome = %q, want --force on success to stay distinct from a genuine failure", successRecords[0].Outcome)
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
	record := completionFor(state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, PR: "https://example.com/pr/1"}, disposition, true, "", "")
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
	deps.worktree.returnWorktree = func(_, path string, force bool) error {
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
	deps.worktree.returnWorktree = func(_, path string, force bool) error {
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
	home, worktreePath, _ := leasedTeardownFixture(t)
	clonePath := filepath.Join(home, "projects", "demo")
	returns := 0
	observed := 0
	deps := defaultDependencies()
	deps.worktree.observeLease = func(gotClonePath, path, leaseID string) worktree.LeaseObservation {
		if gotClonePath != clonePath {
			t.Fatalf("observeLease clone path = %q, want %q", gotClonePath, clonePath)
		}
		if path != worktreePath || leaseID != "lease-1" {
			t.Fatalf("observeLease(%q, %q), want (%q, %q)", path, leaseID, worktreePath, "lease-1")
		}
		observed++
		return worktree.LeaseObservation{State: worktree.LeaseExact, LeaseID: "lease-1"}
	}
	deps.worktree.returnWithID = func(gotClonePath, path, leaseID string, force bool) error {
		if gotClonePath != clonePath {
			t.Fatalf("returnWithID clone path = %q, want %q", gotClonePath, clonePath)
		}
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
	history, err := state.ReadHistory(home, "task-1")
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
	if returns != 2 || observed != 2 || result.Detail != "report "+filepath.Join("data", "task-1", "report.md") {
		t.Fatalf("retry returns=%d observed=%d result=%+v, want a proven forced retry and unchanged detail", returns, observed, result)
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
	home, worktreePath, attempt := leasedTeardownFixture(t)
	clonePath := filepath.Join(home, "projects", "demo")
	called := false
	deps := defaultDependencies()
	deps.worktree.observeLease = func(gotClonePath, path, leaseID string) worktree.LeaseObservation {
		if gotClonePath != clonePath {
			t.Fatalf("observeLease clone path = %q, want %q", gotClonePath, clonePath)
		}
		if path != worktreePath || leaseID != "lease-1" {
			t.Fatalf("observeLease(%q, %q), want (%q, %q)", path, leaseID, worktreePath, "lease-1")
		}
		return worktree.LeaseObservation{State: worktree.LeaseExact, LeaseID: "lease-1"}
	}
	deps.worktree.returnWorktree = func(string, string, bool) error {
		t.Fatal("teardown used path-only return for a known lease")
		return nil
	}
	deps.worktree.returnWithID = func(gotClonePath, path, leaseID string, force bool) error {
		if gotClonePath != clonePath {
			t.Fatalf("returnWithID clone path = %q, want %q", gotClonePath, clonePath)
		}
		if path != worktreePath || leaseID != "lease-1" || force {
			t.Fatalf("returnWithID(%q, %q, %t), want (%q, %q, false)", path, leaseID, force, worktreePath, "lease-1")
		}
		called = true
		return nil
	}

	if err := (&Runtime{deps: deps}).releaseWorktree(clonePath, home, "task-1", attempt, false); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("releaseWorktree did not use the identity-targeted return")
	}
}

func TestTeardownRefusesARecycledWorktreeLeaseOnFirstReturn(t *testing.T) {
	home, worktreePath, _ := leasedTeardownFixture(t)
	observed := 0
	returns := 0
	deps := defaultDependencies()
	deps.worktree.observeLease = func(_, path, leaseID string) worktree.LeaseObservation {
		if path != worktreePath || leaseID != "lease-1" {
			t.Fatalf("observeLease(%q, %q), want (%q, %q)", path, leaseID, worktreePath, "lease-1")
		}
		observed++
		return worktree.LeaseObservation{State: worktree.LeaseMismatch, LeaseID: "lease-2"}
	}
	deps.worktree.returnWorktree = func(string, string, bool) error {
		returns++
		return nil
	}

	_, err := (&Runtime{deps: deps}).Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"})
	if err == nil || !strings.Contains(err.Error(), "prove worktree ownership") {
		t.Fatalf("Teardown() = %v, want an unproven-ownership refusal", err)
	}
	if !strings.Contains(err.Error(), "belongs to another owner") {
		t.Fatalf("Teardown() = %v, want the mismatch named as another owner's lease", err)
	}
	if observed != 1 {
		t.Fatalf("lease observation count = %d, want one", observed)
	}
	if returns != 0 {
		t.Fatalf("worktree return count = %d, want no destructive return", returns)
	}
	history, err := state.ReadHistory(home, "task-1")
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

func leasedTeardownFixture(t *testing.T) (string, string, state.Attempt) {
	t.Helper()
	home, worktreePath := teardownFixture(t, true)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	history.ActiveAttempt.LeaseID = "lease-1"
	if err := state.UpdateAttempt(home, *history.ActiveAttempt); err != nil {
		t.Fatal(err)
	}
	return home, worktreePath, *history.ActiveAttempt
}

func terminalResourceFixture(t *testing.T) (string, string, state.Attempt) {
	t.Helper()
	home, worktreePath := teardownFixture(t, true)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	attempt := *history.ActiveAttempt
	attempt.LeaseID = "lease-1"
	attempt.Herdr = state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}
	if err := state.UpdateAttempt(home, attempt); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownDecision(home, "task-1", attempt.ID, state.AttemptCompleted, state.TeardownDispositionCompleted); err != nil {
		t.Fatal(err)
	}
	record := completion.Record{ID: "task-1", Project: "demo", Kind: state.KindScout, Outcome: "done", Detail: "already recorded", AttemptID: attempt.ID, AttemptLifecycle: string(state.AttemptCompleted)}
	if err := state.SetAttemptTeardownCompletionState(home, "task-1", attempt.ID, state.AttemptRunning, state.TeardownCompletionPending); err != nil {
		t.Fatal(err)
	}
	if err := completion.Append(home, record); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownCompletionState(home, "task-1", attempt.ID, state.AttemptRunning, state.TeardownCompletionAppended); err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptRunning, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	return home, worktreePath, attempt
}

type teardownHerdr struct {
	healthyReconcileHerdr
	closes int
}

func (f *teardownHerdr) TabClose(string) error {
	f.closes++
	return nil
}

func (f *teardownHerdr) WorkspaceClose(string) error {
	f.closes++
	return nil
}

func unobservablePool(worktreePath string) worktree.LeaseObservation {
	return worktree.LeaseObservation{State: worktree.LeaseUnknown, Probe: worktree.LeaseProbe{
		Command: "treehouse status --json", WorkingDir: worktreePath, Reason: "treehouse reported no pool entries",
	}}
}

// The defect of atqamz/hand#245: one pool that could not be observed used to latch teardown
// permanently. It must record nothing durable and leave a later observation free to prove ownership.
func TestTeardownDoesNotLatchOnAnUnobservablePool(t *testing.T) {
	home, worktreePath, _ := leasedTeardownFixture(t)
	returns := 0
	observation := unobservablePool(worktreePath)
	deps := defaultDependencies()
	deps.worktree.observeLease = func(string, string, string) worktree.LeaseObservation { return observation }
	deps.worktree.returnWithID = func(string, string, string, bool) error { returns++; return nil }
	runtime := &Runtime{deps: deps}

	_, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"})
	if err == nil {
		t.Fatal("Teardown() released a worktree whose ownership could not be observed")
	}
	for _, want := range []string{"could not be observed", "treehouse status --json", worktreePath, "could not be proven, not because a lease mismatched"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Teardown() = %v, want the diagnostic to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "does not match") || strings.Contains(err.Error(), "belongs to another owner") {
		t.Fatalf("Teardown() = %v, want no mismatch claimed about a byte-identical lease", err)
	}
	if returns != 0 {
		t.Fatalf("worktree return count = %d, want no destructive return", returns)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.TeardownWorktreeState != "" {
		t.Fatalf("worktree state after an unobservable pool = %+v, want no durable conclusion", history.ActiveAttempt)
	}

	observation = worktree.LeaseObservation{State: worktree.LeaseExact, LeaseID: "lease-1"}
	if _, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("retry after the pool became observable = %v, want release", err)
	}
	if returns != 1 {
		t.Fatalf("worktree return count = %d, want exactly one proven return", returns)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Attempts[0].TeardownWorktreeState != state.TeardownResourceReleased {
		t.Fatalf("worktree state after a proven return = %q, want released", history.Attempts[0].TeardownWorktreeState)
	}
}

// --force retries a return that aborted; it never substitutes for proof of ownership, and the
// retryable knowledge the earlier abort produced survives the refusal.
func TestTeardownForceCannotDestroyAnUnobservableWorktree(t *testing.T) {
	home, worktreePath, _ := leasedTeardownFixture(t)
	returns := 0
	observation := worktree.LeaseObservation{State: worktree.LeaseExact, LeaseID: "lease-1"}
	deps := defaultDependencies()
	deps.worktree.observeLease = func(string, string, string) worktree.LeaseObservation { return observation }
	deps.worktree.returnWithID = func(_, _, _ string, force bool) error {
		returns++
		if !force {
			return worktree.ErrReturnAborted
		}
		return nil
	}
	runtime := &Runtime{deps: deps}
	if _, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"}); !errors.Is(err, worktree.ErrReturnAborted) {
		t.Fatalf("first Teardown() = %v, want the known abort", err)
	}

	observation = unobservablePool(worktreePath)
	_, err := runtime.Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1", Force: true})
	if err == nil || !strings.Contains(err.Error(), "could not be observed") {
		t.Fatalf("forced retry = %v, want a refusal naming the unobservable pool", err)
	}
	if returns != 1 {
		t.Fatalf("worktree return count = %d, want no forced destructive retry", returns)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.TeardownWorktreeState != state.TeardownResourceRetryable {
		t.Fatalf("worktree state after a refused forced retry = %+v, want the retryable evidence kept", history.ActiveAttempt)
	}
}

// The observation that would have refused the release is the one allowed to resume it, so a latch an
// earlier failure left behind converges instead of standing forever.
func TestTeardownConvergesALatchedWorktreeOnceOwnershipIsProven(t *testing.T) {
	home, _, attempt := leasedTeardownFixture(t)
	if err := state.SetAttemptTeardownResourceState(home, "task-1", attempt.ID, attempt.Lifecycle, "worktree", state.TeardownResourceAmbiguous); err != nil {
		t.Fatal(err)
	}
	returns := 0
	deps := defaultDependencies()
	deps.worktree.observeLease = func(string, string, string) worktree.LeaseObservation {
		return worktree.LeaseObservation{State: worktree.LeaseExact, LeaseID: "lease-1"}
	}
	deps.worktree.returnWithID = func(string, string, string, bool) error { returns++; return nil }

	if _, err := (&Runtime{deps: deps}).Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Teardown() on a latched attempt = %v, want release once ownership is proven", err)
	}
	if returns != 1 {
		t.Fatalf("worktree return count = %d, want one proven return", returns)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Attempts[0].TeardownWorktreeState != state.TeardownResourceReleased {
		t.Fatalf("worktree state after clearing the latch = %q, want released", history.Attempts[0].TeardownWorktreeState)
	}
}

// A latch is not a bypass: proof is still required to clear it, so an unobservable pool leaves the
// latch exactly where it was rather than being released or reclassified.
func TestTeardownKeepsALatchedWorktreeWhenOwnershipStaysUnobservable(t *testing.T) {
	home, worktreePath, attempt := leasedTeardownFixture(t)
	if err := state.SetAttemptTeardownResourceState(home, "task-1", attempt.ID, attempt.Lifecycle, "worktree", state.TeardownResourceAmbiguous); err != nil {
		t.Fatal(err)
	}
	returns := 0
	deps := defaultDependencies()
	deps.worktree.observeLease = func(string, string, string) worktree.LeaseObservation { return unobservablePool(worktreePath) }
	deps.worktree.returnWithID = func(string, string, string, bool) error { returns++; return nil }

	_, err := (&Runtime{deps: deps}).Teardown(context.Background(), TeardownRequest{Home: home, ID: "task-1", Force: true})
	if err == nil || !strings.Contains(err.Error(), "could not be observed") {
		t.Fatalf("Teardown() = %v, want a refusal naming the unobservable pool", err)
	}
	if returns != 0 {
		t.Fatalf("worktree return count = %d, want no destructive return", returns)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.TeardownWorktreeState != state.TeardownResourceAmbiguous {
		t.Fatalf("worktree state = %+v, want the latch unchanged", history.ActiveAttempt)
	}
}

func TestTeardownRetryRefusesWorktreeWithoutLeaseIdentity(t *testing.T) {
	home, worktreePath := teardownFixture(t, true)
	returns := 0
	observed := 0
	deps := defaultDependencies()
	deps.worktree.observeLease = func(_, path, leaseID string) worktree.LeaseObservation {
		if path != worktreePath || leaseID != "" {
			t.Fatalf("observeLease(%q, %q), want (%q, empty)", path, leaseID, worktreePath)
		}
		observed++
		return worktree.LeaseObservation{State: worktree.LeaseUnprovable}
	}
	deps.worktree.returnWorktree = func(string, string, bool) error {
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
	if returns != 1 || observed != 1 {
		t.Fatalf("retry returns=%d observed=%d, want one initial return and one unproven observation", returns, observed)
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
