//go:build e2e

package e2e

import (
	"path/filepath"
	"testing"

	"github.com/atqamz/secondhand/internal/state"
)

// TestSpawnTeardownCycle drives a full spawn -> refused teardown -> local
// merge -> successful teardown cycle through the built binary, using a
// local-only project so the landed-work check exercises real git plumbing
// (a linked worktree merged into the clone's default branch) instead of a
// faked gh.
func TestSpawnTeardownCycle(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "local-only")
	writeBrief(t, home, "task-1")

	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)

	worktree := filepath.Join(home, "wt-task-1")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "task-1-branch", worktree)

	dir := binDir(t)
	writeFakeTreehouse(t, dir, worktree)
	writeFakeHerdrStatic(t, dir, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "demo"})

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

	dash := readDashboard(t, home)
	if at, ok := findActiveTask(dash, "task-1"); !ok || at.Project != "demo" || at.Kind != string(state.KindShip) {
		t.Fatalf("dashboard active tasks = %+v, want an entry for task-1", dash.ActiveTasks)
	}

	runGitIn(t, worktree, "commit", "--allow-empty", "-q", "-m", "wip")

	refused := runHand(t, home, "teardown", "task-1")
	assertInvocation(t, refused, 3, "not merged into the default branch")
	if exists, err := state.Exists(home, "task-1"); err != nil || !exists {
		t.Fatalf("state.Exists after refused teardown = %v, %v, want task to still exist", exists, err)
	}
	if _, ok := findActiveTask(readDashboard(t, home), "task-1"); !ok {
		t.Fatal("dashboard lost task-1's active row after a refused teardown")
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
	finalDash := readDashboard(t, home)
	if _, ok := findActiveTask(finalDash, "task-1"); ok {
		t.Fatal("teardown left task-1 in the dashboard's active tasks")
	}
	if len(finalDash.RecentCompletions) == 0 {
		t.Fatal("teardown did not record a recent completion")
	}
}
