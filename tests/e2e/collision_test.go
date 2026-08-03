//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/state"
)

// setupCollisionHome builds the two-task, one-pool-slot fixture both tests below
// spawn into, and returns the fake bin directory so each can install the
// treehouse fake whose lease behavior it is about.
func setupCollisionHome(t *testing.T) (string, string) {
	t.Helper()
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	writeBrief(t, home, "task-1")
	writeBrief(t, home, "task-2")

	clonePath := filepath.Join(home, "projects", "demo")
	if err := os.MkdirAll(clonePath, 0o755); err != nil {
		t.Fatal(err)
	}

	dir := binDir(t)
	writeFakeHerdrStatic(t, dir, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo"})
	return home, dir
}

// TestSpawnDetectsWorktreeCollision proves worktree.CheckCollision is actually
// wired into the built spawn command, not just unit-tested in isolation: a
// second spawn onto a slot a surviving task row still names must be refused
// before it ever reaches herdr, and must leave no trace of task-2.
//
// The fake here is a treehouse older than v2.1.0, reporting no lease identity at
// all, which is what drives the guard down its path-comparison fallback - the
// same branch a task row written before the lease_id column existed takes. With
// nothing to key on but the path, the fallback cannot tell task-1's row from a
// live holder, so it refuses; that conservatism is the whole point of keeping it.
func TestSpawnDetectsWorktreeCollision(t *testing.T) {
	home, dir := setupCollisionHome(t)

	sharedWorktree := filepath.Join(home, "wt-shared")
	writeFakeTreehouseWithoutLeaseIdentity(t, dir, sharedWorktree)

	first := runHand(t, home, "spawn", "task-1", "demo")
	if first.code != 0 {
		t.Fatalf("spawn task-1: exit %d, stderr %q", first.code, first.stderr)
	}
	returnFakeWorktree(t, sharedWorktree)

	second := runHand(t, home, "spawn", "task-2", "demo")
	assertInvocation(t, second, 3, "collision")
	if !strings.Contains(second.stderr, "task-1") {
		t.Fatalf("collision stderr = %q, want it to name the conflicting task task-1", second.stderr)
	}

	if exists, err := state.Exists(home, "task-2"); err != nil || exists {
		t.Fatalf("state.Exists(task-2) = %v, %v, want the collision to leave no task-2 state", exists, err)
	}
	task1, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatalf("task-1 state should be untouched by task-2's collision: %v", err)
	}
	if task1.Worktree != sharedWorktree {
		t.Fatalf("task-1 worktree = %q, want %q (collision handling must not mutate the winner)", task1.Worktree, sharedWorktree)
	}
}

// The other branch, end to end: a real treehouse recycles a returned pool slot's
// path under a brand-new lease identity, and a task row still naming that path -
// left behind by a teardown whose state.Delete failed - must not abort the spawn
// that legitimately acquired it. The slot is genuinely back in the pool before
// task-2 asks for it, because that is the only way the real backend hands one
// path out twice.
func TestSpawnAllowsARecycledWorktreePathUnderAFreshLease(t *testing.T) {
	home, dir := setupCollisionHome(t)
	sharedWorktree := filepath.Join(home, "wt-shared")
	writeFakeTreehouse(t, dir, sharedWorktree)

	first := runHand(t, home, "spawn", "task-1", "demo")
	if first.code != 0 {
		t.Fatalf("spawn task-1: exit %d, stderr %q", first.code, first.stderr)
	}
	returnFakeWorktree(t, sharedWorktree)

	second := runHand(t, home, "spawn", "task-2", "demo")
	if second.code != 0 {
		t.Fatalf("spawn task-2: exit %d, stderr %q, want the recycled path to be accepted", second.code, second.stderr)
	}

	task1, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	task2, err := state.Read(home, "task-2")
	if err != nil {
		t.Fatal(err)
	}
	if task1.LeaseID == "" || task2.LeaseID == "" {
		t.Fatalf("lease identities not recorded: task-1 %q, task-2 %q", task1.LeaseID, task2.LeaseID)
	}
	if task1.LeaseID == task2.LeaseID {
		t.Fatalf("both tasks recorded lease %q, so this proved nothing about identity keying", task1.LeaseID)
	}
}
