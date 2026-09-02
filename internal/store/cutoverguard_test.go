package store

import (
	"errors"
	"testing"
)

func TestLegacyV18CutoverGuardObservationPlanIsCopiedAndRequiresHeldGuard(t *testing.T) {
	guard := &LegacyV18CutoverGuard{
		gate:  &legacyV18CutoverGate{},
		locks: &legacyV18CutoverLocks{},
		plan: LegacyV18CutoverObservationPlan{
			FleetID: "f_test",
			Projects: []LegacyV18CutoverProjectObservation{{ProjectID: "project-1", Name: "demo"}},
			Worktrees: []LegacyV18CutoverWorktreeObservation{{AttemptID: 1, WorktreePath: "/worktree"}},
			Herdr: []LegacyV18CutoverHerdrObservation{{AttemptID: 1, WorkspaceID: "workspace"}},
		},
	}

	first, err := guard.ObservationPlan()
	if err != nil {
		t.Fatal(err)
	}
	first.Projects[0].Name = "mutated"
	first.Worktrees[0].WorktreePath = "/mutated"
	first.Herdr[0].WorkspaceID = "mutated"

	second, err := guard.ObservationPlan()
	if err != nil {
		t.Fatal(err)
	}
	if second.Projects[0].Name != "demo" || second.Worktrees[0].WorktreePath != "/worktree" || second.Herdr[0].WorkspaceID != "workspace" {
		t.Fatalf("guard plan was mutated through returned copy: %#v", second)
	}

	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.ObservationPlan(); !errors.Is(err, ErrLegacyV18CutoverGuardClosed) {
		t.Fatalf("ObservationPlan after Close = %v, want %v", err, ErrLegacyV18CutoverGuardClosed)
	}
	if err := guard.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
}

func TestLegacyV18CutoverGuardObservationPlanRejectsNilGuard(t *testing.T) {
	var guard *LegacyV18CutoverGuard
	if _, err := guard.ObservationPlan(); !errors.Is(err, ErrLegacyV18CutoverGuardClosed) {
		t.Fatalf("nil guard ObservationPlan = %v, want %v", err, ErrLegacyV18CutoverGuardClosed)
	}
}
