//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/state"
)

// TestSpawnDetectsWorktreeCollision proves worktree.CheckCollision is actually
// wired into the built spawn command, not just unit-tested in isolation: a
// second spawn that leases the same worktree path treehouse already handed
// task-1 (the firstmate #947 stale-lease-after-crash scenario) must be
// refused before it ever reaches herdr, and must leave no trace of task-2.
func TestSpawnDetectsWorktreeCollision(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	writeBrief(t, home, "task-1")
	writeBrief(t, home, "task-2")

	clonePath := filepath.Join(home, "projects", "demo")
	if err := os.MkdirAll(clonePath, 0o755); err != nil {
		t.Fatal(err)
	}

	sharedWorktree := filepath.Join(home, "wt-shared")
	dir := binDir(t)
	writeFakeTreehouse(t, dir, sharedWorktree)
	writeFakeHerdrStatic(t, dir, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo"})

	first := runHand(t, home, "spawn", "task-1", "demo")
	if first.code != 0 {
		t.Fatalf("spawn task-1: exit %d, stderr %q", first.code, first.stderr)
	}

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
