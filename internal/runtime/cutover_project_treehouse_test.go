package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
	"github.com/atqamz/hand/internal/worktree"
)

func TestObserveLegacyV18CutoverProjectTreehousePlanAllowsQuiescentProjectAndPool(t *testing.T) {
	home, _, plan, deps := legacyV18CutoverProjectTreehouseFixture(t)

	evidence, err := observeLegacyV18CutoverProjectTreehousePlan(nil, home, plan, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Projects) != 1 || evidence.Projects[0].ProjectID != "project-1" || evidence.Projects[0].Revision != strings.Repeat("a", 40) {
		t.Fatalf("evidence = %#v", evidence)
	}
}

func TestObserveLegacyV18CutoverProjectTreehousePlanAllowsForeignFleetLease(t *testing.T) {
	home, _, plan, deps := legacyV18CutoverProjectTreehouseFixture(t)
	slot := filepath.Join(home, "pool", "slot-1")
	deps.poolStatus = func(string) ([]worktree.PoolEntry, error) {
		return []worktree.PoolEntry{{Path: slot, Status: "leased", LeaseID: "lease-2", LeaseHolder: "hand:f_other:task-2"}}, nil
	}

	if _, err := observeLegacyV18CutoverProjectTreehousePlan(nil, home, plan, deps); err != nil {
		t.Fatalf("foreign Fleet lease blocked cutover: %v", err)
	}
}

func TestObserveLegacyV18CutoverProjectTreehousePlanBlocksForeignHolderWithoutLeaseIdentity(t *testing.T) {
	home, _, plan, deps := legacyV18CutoverProjectTreehouseFixture(t)
	slot := filepath.Join(home, "pool", "slot-1")
	deps.poolStatus = func(string) ([]worktree.PoolEntry, error) {
		return []worktree.PoolEntry{{Path: slot, Status: "leased", LeaseHolder: "hand:f_other:task-2"}}, nil
	}

	requireLegacyV18CutoverProjectTreehouseBlocker(t, home, plan, deps, "treehouse-live-or-unknown-lease")
}

func TestObserveLegacyV18CutoverProjectTreehousePlanBlocksSameFleetLease(t *testing.T) {
	home, _, plan, deps := legacyV18CutoverProjectTreehouseFixture(t)
	slot := filepath.Join(home, "pool", "slot-1")
	deps.poolStatus = func(string) ([]worktree.PoolEntry, error) {
		return []worktree.PoolEntry{{Path: slot, Status: "leased", LeaseID: "lease-1", LeaseHolder: "hand:f_self:task-1"}}, nil
	}

	requireLegacyV18CutoverProjectTreehouseBlocker(t, home, plan, deps, "treehouse-live-or-unknown-lease")
}

func TestObserveLegacyV18CutoverProjectTreehousePlanBlocksAvailableSlotWithLeaseMetadata(t *testing.T) {
	home, _, plan, deps := legacyV18CutoverProjectTreehouseFixture(t)
	slot := filepath.Join(home, "pool", "slot-1")
	deps.poolStatus = func(string) ([]worktree.PoolEntry, error) {
		return []worktree.PoolEntry{{Path: slot, Status: "available", LeaseID: "stale", LeaseHolder: "hand:f_other:task-2"}}, nil
	}

	requireLegacyV18CutoverProjectTreehouseBlocker(t, home, plan, deps, "treehouse-available-slot-has-lease-metadata")
}

func TestObserveLegacyV18CutoverProjectTreehousePlanAllowsRecordedLeaseReusedByForeignFleet(t *testing.T) {
	home, clone, plan, deps := legacyV18CutoverProjectTreehouseFixture(t)
	slot := filepath.Join(home, "pool", "slot-1")
	plan.Worktrees = []store.LegacyV18CutoverWorktreeObservation{{
		TaskID: "task-1", AttemptID: 7, ProjectID: "project-1", ProjectName: "demo", ClonePath: clone,
		WorktreePath: slot, LeaseID: "old-lease", TeardownState: "released",
	}}
	deps.poolStatus = func(string) ([]worktree.PoolEntry, error) {
		return []worktree.PoolEntry{{Path: slot, Status: "leased", LeaseID: "new-lease", LeaseHolder: "hand:f_other:task-2"}}, nil
	}
	deps.observeLease = func(string, string, string) worktree.LeaseObservation {
		return worktree.LeaseObservation{State: worktree.LeaseMismatch, LeaseID: "new-lease"}
	}

	if _, err := observeLegacyV18CutoverProjectTreehousePlan(nil, home, plan, deps); err != nil {
		t.Fatalf("positively foreign lease reuse blocked cutover: %v", err)
	}
}

func TestObserveLegacyV18CutoverProjectTreehousePlanBlocksRecordedLeaseWithoutForeignProof(t *testing.T) {
	home, clone, plan, deps := legacyV18CutoverProjectTreehouseFixture(t)
	slot := filepath.Join(home, "pool", "slot-1")
	plan.Worktrees = []store.LegacyV18CutoverWorktreeObservation{{
		TaskID: "task-1", AttemptID: 7, ProjectID: "project-1", ProjectName: "demo", ClonePath: clone,
		WorktreePath: slot, LeaseID: "old-lease", TeardownState: "abandoned",
	}}
	deps.poolStatus = func(string) ([]worktree.PoolEntry, error) {
		return []worktree.PoolEntry{{Path: slot, Status: "leased", LeaseID: "new-lease", LeaseHolder: "unknown-owner"}}, nil
	}
	deps.observeLease = func(string, string, string) worktree.LeaseObservation {
		return worktree.LeaseObservation{State: worktree.LeaseMismatch, LeaseID: "new-lease"}
	}

	requireLegacyV18CutoverProjectTreehouseBlocker(t, home, plan, deps, "treehouse-recorded-lease-unresolved")
}

func TestObserveLegacyV18CutoverProjectTreehousePlanBlocksOrphanManagedProjectPath(t *testing.T) {
	home, _, plan, deps := legacyV18CutoverProjectTreehouseFixture(t)
	if err := os.Mkdir(filepath.Join(home, "projects", "orphan"), 0o755); err != nil {
		t.Fatal(err)
	}

	requireLegacyV18CutoverProjectTreehouseBlocker(t, home, plan, deps, "project-orphan-path")
}

func TestObserveLegacyV18CutoverProjectTreehousePlanBlocksUnresolvedDiscoveredSlot(t *testing.T) {
	home, _, plan, deps := legacyV18CutoverProjectTreehouseFixture(t)
	orphan := filepath.Join(home, "historical-pool", "slot-9")
	deps.discoverPoolSlots = func(string, ...string) ([]worktree.PoolSlot, error) {
		return []worktree.PoolSlot{{Path: orphan}}, nil
	}
	deps.observeLease = func(string, string, string) worktree.LeaseObservation {
		return worktree.LeaseObservation{State: worktree.LeaseUnknown, Probe: worktree.LeaseProbe{Reason: "historical pool cannot be observed"}}
	}

	requireLegacyV18CutoverProjectTreehouseBlocker(t, home, plan, deps, "treehouse-orphan-slot-unresolved")
}

func TestObserveLegacyV18CutoverProjectTreehousePlanBlocksSlotCollision(t *testing.T) {
	home, _, plan, deps := legacyV18CutoverProjectTreehouseFixture(t)
	left := worktree.PoolSlot{Path: filepath.Join(home, "pool-a", "slot")}
	right := worktree.PoolSlot{Path: filepath.Join(home, "pool-b", "slot")}
	deps.discoverPoolSlots = func(string, ...string) ([]worktree.PoolSlot, error) {
		return []worktree.PoolSlot{left, right}, nil
	}
	deps.poolSlotCollisions = func([]worktree.PoolSlot) [][]worktree.PoolSlot {
		return [][]worktree.PoolSlot{{left, right}}
	}
	deps.observeLease = func(string, string, string) worktree.LeaseObservation {
		return worktree.LeaseObservation{State: worktree.LeaseAbsent}
	}

	requireLegacyV18CutoverProjectTreehouseBlocker(t, home, plan, deps, "treehouse-slot-collision")
}

func legacyV18CutoverProjectTreehouseFixture(t *testing.T) (string, string, store.LegacyV18CutoverObservationPlan, legacyV18CutoverProjectTreehouseDeps) {
	t.Helper()
	home := t.TempDir()
	clone := filepath.Join(home, "projects", "demo")
	common := filepath.Join(clone, ".git")
	if err := os.MkdirAll(common, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := store.LegacyV18CutoverObservationPlan{
		FleetID: "f_self",
		Projects: []store.LegacyV18CutoverProjectObservation{{
			ProjectID: "project-1", Name: "demo", ClonePath: clone,
		}},
	}
	deps := legacyV18CutoverProjectTreehouseDeps{
		resolveRoot: func(string) (string, error) { return clone, nil },
		commonDir:   func(string) (string, error) { return common, nil },
		isBare:      func(string) (bool, error) { return false, nil },
		headCommit:  func(string) (string, error) { return strings.Repeat("a", 40), nil },
		poolSearchRoots: func(string, string) []string {
			return nil
		},
		discoverPoolSlots: func(string, ...string) ([]worktree.PoolSlot, error) {
			return nil, nil
		},
		poolSlotCollisions: func([]worktree.PoolSlot) [][]worktree.PoolSlot { return nil },
		poolStatus:         func(string) ([]worktree.PoolEntry, error) { return nil, nil },
		observeLease: func(string, string, string) worktree.LeaseObservation {
			return worktree.LeaseObservation{State: worktree.LeaseAbsent}
		},
	}
	return home, clone, plan, deps
}

func requireLegacyV18CutoverProjectTreehouseBlocker(t *testing.T, home string, plan store.LegacyV18CutoverObservationPlan, deps legacyV18CutoverProjectTreehouseDeps, code string) {
	t.Helper()
	_, err := observeLegacyV18CutoverProjectTreehousePlan(nil, home, plan, deps)
	if err == nil {
		t.Fatalf("want blocker %q, got nil", code)
	}
	if !errors.Is(err, errLegacyV18CutoverProjectTreehouseUnsafe) {
		t.Fatalf("error = %v, want %v", err, errLegacyV18CutoverProjectTreehouseUnsafe)
	}
	var blocked *legacyV18CutoverProviderBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("error = %T %v, want *legacyV18CutoverProviderBlockedError", err, err)
	}
	for _, blocker := range blocked.Blockers {
		if blocker.Code == code {
			return
		}
	}
	t.Fatalf("blockers = %#v, want code %q", blocked.Blockers, code)
}
