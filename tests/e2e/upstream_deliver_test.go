//go:build e2e

package e2e

import (
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/state"
)

// Drives the whole case atqamz/secondhand#78 describes through the built binary: work pushed to a fork, its
// PR opened on an upstream repo the fleet does not control, and a maintainer who has not merged it.
func TestForkContributionDeliveredNotLanded(t *testing.T) {
	// The upstream repo here is a fixture, never the live one: the real contribution is offered to a project
	// Atqa only has read access to, so the case is constructed rather than reproduced.
	remote := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remote)
	redirectGitRemote(t, "https://github.com/atqamz/no-mistakes.git", remote)

	dir := binDir(t)
	worktree := filepath.Join(t.TempDir(), "wt-task-1")
	writeFakeTreehouse(t, dir, worktree)
	writeFakeHerdrStatic(t, dir, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: "no-mistakes"})

	home := newHome(t)
	added := runHand(t, home, "project", "add", "https://github.com/atqamz/no-mistakes.git", "--mode", "direct-pr")
	if added.code != 0 {
		t.Fatalf("project add: exit %d, stderr %q", added.code, added.stderr)
	}

	clonePath := filepath.Join(home, "projects", "no-mistakes")
	runGitIn(t, clonePath, "worktree", "add", "-q", "-b", "task-1-branch", worktree)

	// The upstream maintainer has not merged it and may never: OPEN is the state
	// this whole feature has to be able to tear down under.
	writeFakeDispatch(t, dir, "gh", "", "$1 $2", `  "pr view") echo '{"state":"OPEN"}' ;;`)

	writeBrief(t, home, "task-1")
	spawned := runHand(t, home, "spawn", "task-1", "no-mistakes")
	if spawned.code != 0 {
		t.Fatalf("spawn: exit %d, stderr %q", spawned.code, spawned.stderr)
	}
	runGitIn(t, worktree, "commit", "--allow-empty", "-q", "-m", "fix the flake")

	// The two refusals below are the point: hand pr rejects a repo nobody declared, and teardown rejects the
	// still-open PR until the delivery is recorded. Reverting either half of the change turns one into a pass.
	upstreamPR := "https://github.com/kunchenguid/no-mistakes/pull/597"
	undeclared := runHand(t, home, "pr", "task-1", upstreamPR)
	assertInvocation(t, undeclared, 3, "no upstream is declared for it")

	declared := runHand(t, home, "project", "upstream", "no-mistakes", "kunchenguid/no-mistakes")
	if declared.code != 0 {
		t.Fatalf("project upstream: exit %d, stderr %q", declared.code, declared.stderr)
	}

	unrelated := runHand(t, home, "pr", "task-1", "https://github.com/someone/else/pull/1")
	assertInvocation(t, unrelated, 3, "declared upstream (kunchenguid/no-mistakes)")

	recorded := runHand(t, home, "pr", "task-1", upstreamPR)
	if recorded.code != 0 {
		t.Fatalf("pr record on the declared upstream: exit %d, stderr %q", recorded.code, recorded.stderr)
	}

	refused := runHand(t, home, "teardown", "task-1")
	assertInvocation(t, refused, 3, "is not merged")

	delivered := runHand(t, home, "deliver", "task-1", "--reason", "offered to kunchenguid/no-mistakes, maintainer decides")
	if delivered.code != 0 {
		t.Fatalf("deliver: exit %d, stderr %q", delivered.code, delivered.stderr)
	}

	tornDown := runHand(t, home, "teardown", "task-1")
	if tornDown.code != 0 {
		t.Fatalf("teardown of delivered work: exit %d, stderr %q", tornDown.code, tornDown.stderr)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state.Exists after teardown = %v, %v, want the task removed", exists, err)
	}

	records, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("completions = %+v, want exactly one", records)
	}
	if records[0].Outcome != "delivered" {
		t.Fatalf("outcome = %q, want delivered - the upstream PR was never merged", records[0].Outcome)
	}
}
