package store

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestAcquireLegacyV18CutoverGuardRefusesExactV072SourceBeforeMutation(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	before, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadDir(Dir(home))
	if err != nil {
		t.Fatal(err)
	}

	guard, err := AcquireLegacyV18CutoverGuard(t.Context(), home)
	if guard != nil {
		_ = guard.Close()
		t.Fatal("automatic cutover acquired a guard for exact v0.7.2")
	}
	if !errors.Is(err, ErrLegacyV18AutomaticCutoverUnavailable) {
		t.Fatalf("automatic cutover error = %v, want exact-source refusal", err)
	}
	after, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	stateAfter, err := os.ReadDir(Dir(home))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || len(stateBefore) != len(stateAfter) {
		t.Fatal("refused automatic cutover changed source or state entries")
	}
	for index := range stateBefore {
		if stateBefore[index].Name() != stateAfter[index].Name() {
			t.Fatal("refused automatic cutover changed state entries")
		}
	}
}

func TestLegacyV18CutoverGuardObservationPlanIsCopiedAndRequiresHeldGuard(t *testing.T) {
	guard := &LegacyV18CutoverGuard{
		gate:       &legacyV18CutoverGate{},
		locks:      &legacyV18CutoverLocks{},
		sourceHeld: true,
		plan: LegacyV18CutoverObservationPlan{
			FleetID:   "f_test",
			Projects:  []LegacyV18CutoverProjectObservation{{ProjectID: "project-1", Name: "demo"}},
			Worktrees: []LegacyV18CutoverWorktreeObservation{{AttemptID: 1, WorktreePath: "/worktree"}},
			Herdr:     []LegacyV18CutoverHerdrObservation{{AttemptID: 1, WorkspaceID: "workspace"}},
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
