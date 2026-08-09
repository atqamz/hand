//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/state"
)

// Drives a full spawn -> refused teardown -> local merge -> successful teardown cycle through the built
// binary, using a local-only project so the landed-work check exercises real git plumbing (a linked
// worktree merged into the clone's default branch) instead of a faked gh.
func TestSpawnTeardownCycle(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "local-only")
	writeBrief(t, home, "task-1")

	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)

	worktree := filepath.Join(home, "wt-task-1")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "task-1-branch", worktree)

	dir := binDir(t)
	invocationLog := filepath.Join(t.TempDir(), "invocations.log")
	writeFakeTreehouse(t, dir, worktree)
	writeFakeHerdrStaticLogged(t, dir, invocationLog, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo"})

	spawned := runHand(t, home, "spawn", "task-1", "demo")
	if spawned.code != 0 {
		t.Fatalf("spawn: exit %d, stderr %q", spawned.code, spawned.stderr)
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Project != "demo" || task.Kind != state.KindShip || task.Worktree != worktree ||
		task.Harness == "" || task.Herdr.WorkspaceID != "ws-1" || task.Herdr.TabID != "tab-1" || task.Herdr.PaneID != "pane-1" {
		t.Fatalf("spawned task state = %+v, want project=demo kind=ship worktree=%s herdr ids populated", task, worktree)
	}
	if task.PaneStartedAt != task.CreatedAt || task.PaneStartedAt == "" {
		t.Fatalf("spawned task PaneStartedAt = %q, CreatedAt = %q, want the spawn instant recorded as both: a task's first pane starts when it is created", task.PaneStartedAt, task.CreatedAt)
	}

	// The regression this cycle exists to catch: a first spawn into a fresh workspace must reuse
	// the workspace's own root tab (renamed to the task id) rather than creating a second one,
	// which is what used to leave an orphan shell parked in the workspace at the clone's cwd.
	spawnLog, err := os.ReadFile(invocationLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(spawnLog), "herdr tab rename tab-1 task-1") {
		t.Fatalf("invocation log = %q, want spawn to rename the fresh workspace's root tab to the task id", spawnLog)
	}
	if strings.Contains(string(spawnLog), "herdr tab create") {
		t.Fatalf("invocation log = %q, want spawn to reuse the root tab instead of creating a second one", spawnLog)
	}

	runGitIn(t, worktree, "commit", "--allow-empty", "-q", "-m", "wip")

	refused := runHand(t, home, "teardown", "task-1")
	assertInvocation(t, refused, 3, "not merged into the default branch")
	if exists, err := state.Exists(home, "task-1"); err != nil || !exists {
		t.Fatalf("state.Exists after refused teardown = %v, %v, want task to still exist", exists, err)
	}

	merged := runHand(t, home, "merge", "task-1", "--local")
	if merged.code != 0 {
		t.Fatalf("merge --local: exit %d, stderr %q", merged.code, merged.stderr)
	}

	done := runHand(t, home, "teardown", "task-1")
	if done.code != 0 {
		t.Fatalf("teardown: exit %d, stderr %q", done.code, done.stderr)
	}

	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state.Exists after teardown = %v, %v, want task removed", exists, err)
	}
	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("teardown did not record a recent completion")
	}

	// The task's tab was the workspace's only tab, so teardown must close the whole workspace
	// (closeTaskTab's sole-tab shortcut) rather than leaving it behind with nothing pointing at it -
	// the second half of the orphan-tab regression: an unreachable last-tab check never fires.
	finalLog, err := os.ReadFile(invocationLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(finalLog), "herdr workspace close ws-1") {
		t.Fatalf("invocation log = %q, want teardown to close the now-empty workspace", finalLog)
	}
	if strings.Contains(string(finalLog), "herdr tab close tab-1") {
		t.Fatalf("invocation log = %q, want teardown to close the workspace, not just the sole tab in it", finalLog)
	}
}
