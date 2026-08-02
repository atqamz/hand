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
// lives somewhere else. Deleting the file here, rather than after hand init
// as usual, sidesteps internal/agentsmd's dashboard.md stat for fleet-home
// detection (atqamz/secondhand#46, not this issue's fix) simply by never calling
// hand init or hand update again once the file is gone.
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
	if err := os.Remove(dashPath); err != nil {
		t.Fatal(err)
	}

	// teardown still fails here: its last step renders into data/dashboard.md,
	// and that file's removal belongs to atqamz/secondhand#62, not this issue.
	// The point this test proves is what happens before that last step: the
	// completion record and the task state deletion both come earlier in
	// cmd/teardown.go, so neither is lost when the dashboard step has nothing
	// left to read.
	done := runHand(t, home, "teardown", "task-1")
	assertInvocation(t, done, 1, "update dashboard")

	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state.Exists after teardown = %v, %v, want task removed", exists, err)
	}
	if _, err := os.Stat(dashPath); !os.IsNotExist(err) {
		t.Fatalf("dashboard.md stat = %v, want it to stay gone: teardown must not have recreated it", err)
	}

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "task-1" || records[0].Project != "demo" || records[0].Outcome != "merged" {
		t.Fatalf("completion.List = %+v, want one merged record for task-1", records)
	}
}
