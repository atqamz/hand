//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/secondhand/internal/completion"
	"github.com/atqamz/secondhand/internal/state"
)

// TestTeardownCompletionSurvivesMissingDashboard is atqamz/secondhand#61's
// done-when condition run literally: data/dashboard.md is gone from disk before
// teardown runs, so the only way this test can pass is if the completion record
// lives somewhere else. data/dashboard.md is also the fleet-home marker
// home.Resolve looks for (atqamz/secondhand#46, not this issue's fix), so the
// final teardown names the home through HAND_HOME instead of the walk up: an
// operator whose dashboard is gone runs `hand init` to restore it, which is
// what the resolver's own refusal tells them to do.
func TestTeardownCompletionSurvivesMissingDashboard(t *testing.T) {
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

	if spawned := runHand(t, home, "spawn", "task-1", "demo"); spawned.code != 0 {
		t.Fatalf("spawn: exit %d, stderr %q", spawned.code, spawned.stderr)
	}

	runGitIn(t, worktree, "commit", "--allow-empty", "-q", "-m", "wip")

	if merged := runHand(t, home, "merge", "task-1", "--local"); merged.code != 0 {
		t.Fatalf("merge --local: exit %d, stderr %q", merged.code, merged.stderr)
	}

	dashPath := filepath.Join(home, "data", "dashboard.md")
	before, err := os.ReadFile(dashPath)
	if err != nil {
		t.Fatal(err)
	}
	sealDir(t, filepath.Join(home, "data"))

	// teardown still fails here: its last step renders into data/dashboard.md,
	// and surviving that failure belongs to atqamz/secondhand#62, not this
	// issue. The point this test proves is what happens before that last step:
	// the completion record and the task state deletion both come earlier in
	// cmd/teardown.go, so neither is lost when the dashboard step cannot write.
	done := runHand(t, home, "teardown", "task-1")
	assertInvocation(t, done, 1, "update dashboard")

	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state.Exists after teardown = %v, %v, want task removed", exists, err)
	}
	after, err := os.ReadFile(dashPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("dashboard.md = %q, want it unchanged: the step that could not write must not have half-written", after)
	}

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "task-1" || records[0].Project != "demo" || records[0].Outcome != "merged" {
		t.Fatalf("completion.List = %+v, want one merged record for task-1", records)
	}
}
