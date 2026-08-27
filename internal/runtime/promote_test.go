package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

func TestCleanupScoutStopsAfterPaneReleasePhase(t *testing.T) {
	fake := &provisionHerdr{tabs: []herdr.Tab{{TabID: "old-tab"}, {TabID: "other-tab"}}}
	phaseErr := errors.New("stop after scout pane release")
	returned := false
	runtime := &Runtime{deps: dependencies{
		herdr: func() herdrClient { return fake },
		worktree: worktreeDependencies{returnWorktree: func(string, string, bool) error {
			returned = true
			return nil
		}},
		phase: func(phase lifecyclePhase) error {
			if phase == phaseScoutPaneReleased {
				return phaseErr
			}
			return nil
		},
	}}

	warnings, err := runtime.cleanupScout("/tmp/home", "/tmp/home/projects/demo", "task-1", scoutAttempt("/tmp/old", "old-workspace", "old-tab"))
	if !errors.Is(err, phaseErr) {
		t.Fatalf("cleanupScout() = %v, want %v", err, phaseErr)
	}
	if len(warnings) != 0 || fake.closedTab != "old-tab" || returned {
		t.Fatalf("cleanupScout() warnings=%v closedTab=%q returned=%t, want pane released only", warnings, fake.closedTab, returned)
	}
}

func TestCleanupScoutReportsPartialReleaseOutcomes(t *testing.T) {
	closeErr := errors.New("herdr unavailable")
	returnErr := errors.New("treehouse unavailable")
	phaseErr := errors.New("stop after scout worktree return")
	fake := &provisionHerdr{
		tabs:        []herdr.Tab{{TabID: "old-tab"}, {TabID: "other-tab"}},
		tabCloseErr: closeErr,
	}
	returned := false
	runtime := &Runtime{deps: dependencies{
		herdr: func() herdrClient { return fake },
		worktree: worktreeDependencies{returnWorktree: func(string, string, bool) error {
			returned = true
			return returnErr
		}},
		phase: func(phase lifecyclePhase) error {
			if phase == phaseScoutWorktreeReturned {
				return phaseErr
			}
			return nil
		},
	}}

	warnings, err := runtime.cleanupScout("/tmp/home", "/tmp/home/projects/demo", "task-1", scoutAttempt("/tmp/old", "old-workspace", "old-tab"))
	if !errors.Is(err, phaseErr) {
		t.Fatalf("cleanupScout() = %v, want %v", err, phaseErr)
	}
	if !returned || fake.closedTab != "old-tab" {
		t.Fatalf("cleanupScout() returned=%t closedTab=%q, want both cleanup attempts", returned, fake.closedTab)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, closeErr.Error()) || !strings.Contains(joined, returnErr.Error()) {
		t.Fatalf("cleanup warnings = %v, want both partial cleanup errors", warnings)
	}
}

func TestCleanupScoutReportsIncompleteHerdrOwnership(t *testing.T) {
	runtime := &Runtime{deps: dependencies{
		herdr:    func() herdrClient { return &provisionHerdr{} },
		worktree: worktreeDependencies{returnWorktree: func(string, string, bool) error { return nil }},
		phase:    func(lifecyclePhase) error { return nil },
	}}

	warnings, err := runtime.cleanupScout("/tmp/home", "/tmp/home/projects/demo", "task-1", state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptCompleted,
		Herdr: state.Herdr{PaneID: "pane-only"},
	})
	if err != nil {
		t.Fatalf("cleanupScout() = %v, want a reported partial cleanup", err)
	}
	if !slices.ContainsFunc(warnings, func(warning string) bool { return strings.Contains(warning, "ownership incomplete") }) {
		t.Fatalf("cleanup warnings = %v, want incomplete ownership warning", warnings)
	}
}

func TestPromotePersistsPartialOldScoutCleanupWithoutMovingOwnership(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{"brief.md": "brief\n", "report.md": "findings\n"} {
		if err := os.WriteFile(filepath.Join(home, "data", "task-1", name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://example.com/demo.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(t.TempDir(), "old")
	oldAttempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindScout, Lifecycle: state.TaskOpen}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Worktree: oldPath, LeaseID: "L1", Herdr: state.Herdr{WorkspaceID: "old-ws", TabID: "old-tab", PaneID: "old-pane"}, Harness: "claude", LaunchSubmittedAt: "2026-08-14T00:00:00Z", LaunchConfirmedAt: "2026-08-14T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", oldAttempt.ID); err != nil {
		t.Fatal(err)
	}
	oldReturnErr := errors.New("old scout worktree return failed")
	fake := &provisionHerdr{tabs: []herdr.Tab{{TabID: "old-tab"}, {TabID: "other-tab"}}}
	deps := testProvisionRuntime(fake, func(lifecyclePhase) error { return nil }).deps
	deps.worktree.get = func(string, string) (worktree.Lease, error) {
		return worktree.Lease{Path: "/new/worktree", ID: "L2"}, nil
	}
	deps.worktree.returnWorktree = func(_, path string, force bool) error {
		if path == oldPath && !force {
			t.Fatalf("old scout return force = %t, want true", force)
		}
		if path == oldPath {
			return oldReturnErr
		}
		return nil
	}
	result, err := (&Runtime{deps: deps}).Promote(context.Background(), PromoteRequest{Home: home, ID: "task-1", Harness: "claude", HarnessFromFlag: true})
	if err != nil {
		t.Fatalf("Promote() = %v, want partial cleanup warning and ship launch", err)
	}
	if !slices.ContainsFunc(result.Warnings, func(warning string) bool { return strings.Contains(warning, oldReturnErr.Error()) }) {
		t.Fatalf("warnings = %v, want old worktree failure", result.Warnings)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Attempts) != 2 || history.Attempts[0].Lifecycle != state.AttemptCompleted || history.Attempts[1].Lifecycle != state.AttemptRunning {
		t.Fatalf("attempt history = %+v, want terminal scout and running ship", history.Attempts)
	}
	old := history.Attempts[0]
	ship := history.Attempts[1]
	if old.ID == ship.ID || old.TeardownHerdrState != state.TeardownResourceReleased || old.TeardownWorktreeState != state.TeardownResourceAmbiguous {
		t.Fatalf("old attempt = %+v, ship attempt = %+v, want distinct IDs and partial cleanup evidence", old, ship)
	}
	if ship.Worktree != "/new/worktree" || ship.LeaseID != "L2" || ship.Worktree == old.Worktree {
		t.Fatalf("ship ownership = %+v, want only new lease ownership", ship)
	}
}

func scoutAttempt(worktreePath, workspaceID, tabID string) state.Attempt {
	return state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptCompleted, Worktree: worktreePath,
		Herdr: state.Herdr{WorkspaceID: workspaceID, TabID: tabID, PaneID: "old-pane"},
	}
}
