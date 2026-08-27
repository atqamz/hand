//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

// Builds the two-task, one-pool-slot fixture both tests below spawn into, and returns the fake bin
// directory so each can install the treehouse fake whose lease behavior it is about.
func setupCollisionHome(t *testing.T) (string, string) {
	t.Helper()
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	writeBrief(t, home, "task-1")
	writeBrief(t, home, "task-2")

	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	sharedWorktree := filepath.Join(home, "wt-shared")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "shared", sharedWorktree)

	dir := binDir(t)
	writeFakeHerdrStatic(t, dir, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo"})
	return home, dir
}

// Proves worktree.CheckCollision is actually wired into the built spawn command, not just unit-tested in
// isolation: a second spawn onto a slot a surviving task row still names must be refused before it ever
// reaches herdr, while its provisioning attempt preserves the failed boundary.
func TestSpawnDetectsWorktreeCollision(t *testing.T) {
	home, dir := setupCollisionHome(t)

	sharedWorktree := filepath.Join(home, "wt-shared")
	// With nothing to key on but the path, the fallback this fake drives cannot tell task-1's row from a live
	// holder, so it refuses; that conservatism is the whole point of keeping it.
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

	if exists, err := state.Exists(home, "task-2"); err != nil || !exists {
		t.Fatalf("state.Exists(task-2) = %v, %v, want the provisioning attempt preserved", exists, err)
	}
	task2, attempt2 := readTaskAttempt(t, home, "task-2")
	if task2.Lifecycle != state.TaskOpen || attempt2.Lifecycle != state.AttemptProvisioning || attempt2.Worktree != "" || attempt2.LeaseID != "" {
		t.Fatalf("task-2 state = %+v / %+v, want returned resource ownership cleared on the provisioning attempt", task2, attempt2)
	}
	task1, attempt1 := readTaskAttempt(t, home, "task-1")
	if attempt1.Worktree != sharedWorktree {
		t.Fatalf("task-1 worktree = %q, want %q (collision handling must not mutate the winner; task=%+v)", attempt1.Worktree, sharedWorktree, task1)
	}
}

// The other branch, end to end: a real treehouse recycles a returned pool slot's path under a brand-new
// lease identity, and a task row still naming that path - left behind by a teardown whose state.Delete
// failed - must not abort the spawn that legitimately acquired it.
func TestSpawnAllowsARecycledWorktreePathUnderAFreshLease(t *testing.T) {
	home, dir := setupCollisionHome(t)
	sharedWorktree := filepath.Join(home, "wt-shared")
	writeFakeTreehouse(t, dir, sharedWorktree)

	first := runHand(t, home, "spawn", "task-1", "demo")
	if first.code != 0 {
		t.Fatalf("spawn task-1: exit %d, stderr %q", first.code, first.stderr)
	}
	// Genuinely back in the pool before task-2 asks for it, because that is the only way the real backend
	// hands one path out twice.
	returnFakeWorktree(t, sharedWorktree)

	second := runHand(t, home, "spawn", "task-2", "demo")
	if second.code != 0 {
		t.Fatalf("spawn task-2: exit %d, stderr %q, want the recycled path to be accepted", second.code, second.stderr)
	}

	_, attempt1 := readTaskAttempt(t, home, "task-1")
	_, attempt2 := readTaskAttempt(t, home, "task-2")
	if attempt1.LeaseID == "" || attempt2.LeaseID == "" {
		t.Fatalf("lease identities not recorded: task-1 %q, task-2 %q", attempt1.LeaseID, attempt2.LeaseID)
	}
	if attempt1.LeaseID == attempt2.LeaseID {
		t.Fatalf("both tasks recorded lease %q, so this proved nothing about identity keying", attempt1.LeaseID)
	}
}
