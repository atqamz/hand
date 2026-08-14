package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
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
