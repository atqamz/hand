package runtime

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
)

func TestCleanupScoutStopsAfterPaneReleasePhase(t *testing.T) {
	fake := &provisionHerdr{tabs: []herdr.Tab{{TabID: "old-tab"}, {TabID: "other-tab"}}}
	phaseErr := errors.New("stop after scout pane release")
	returned := false
	runtime := &Runtime{deps: dependencies{
		herdr: func() herdrClient { return fake },
		worktree: worktreeDependencies{returnWorktree: func(string, bool) error {
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

	warnings, err := runtime.cleanupScout("/tmp/home", "task-1", scoutAttempt("/tmp/old", "old-workspace", "old-tab"))
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
		worktree: worktreeDependencies{returnWorktree: func(string, bool) error {
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

	warnings, err := runtime.cleanupScout("/tmp/home", "task-1", scoutAttempt("/tmp/old", "old-workspace", "old-tab"))
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
		worktree: worktreeDependencies{returnWorktree: func(string, bool) error { return nil }},
		phase:    func(lifecyclePhase) error { return nil },
	}}

	warnings, err := runtime.cleanupScout("/tmp/home", "task-1", state.Attempt{
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

func scoutAttempt(worktreePath, workspaceID, tabID string) state.Attempt {
	return state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptCompleted, Worktree: worktreePath,
		Herdr: state.Herdr{WorkspaceID: workspaceID, TabID: tabID},
	}
}
