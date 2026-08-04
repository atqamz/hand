//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/faketool"
	"github.com/atqamz/secondhand/internal/state"
)

// TestMergePR drives `hand merge` through a faked gh, no real remote: a task
// whose PR checks are all green merges cleanly, one whose checks include a
// failing bucket is refused before gh pr merge is ever invoked, and a rerun of
// the merge that succeeded is refused rather than merging a second time.
func TestMergePR(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	now := time.Now().UTC().Format(time.RFC3339)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, PR: "1", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-2", Project: "demo", Kind: state.KindShip, PR: "2", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	dir := binDir(t)
	invocationLog := filepath.Join(t.TempDir(), "gh-invocations.log")
	// prChecksGreen (cmd/merge.go) never consults gh's exit code once the "pr
	// checks" JSON parses, so the always-exit-0 fake below exercises the same
	// path a real fail-bucket exit 1 would. Each merge through it moves that PR
	// to MERGED, which is what makes the rerun below see the state the first
	// merge left rather than the state it started in.
	faketool.GH{Log: invocationLog, PRs: []faketool.GHPR{
		{Number: 1, Branch: "task-1-branch", Repo: "org/demo", Checks: []string{"pass"}},
		{Number: 2, Branch: "task-2-branch", Repo: "org/demo", Checks: []string{"fail"}},
	}}.Install(t, dir)

	clean := runHand(t, home, "merge", "task-1")
	if clean.code != 0 {
		t.Fatalf("merge task-1: exit %d, stderr %q", clean.code, clean.stderr)
	}
	task1, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !task1.MergeExecuted || task1.MergeExecutedAt == "" {
		t.Fatalf("task-1 state = %+v, want MergeExecuted=true and MergeExecutedAt set", task1)
	}

	// The row is written only after gh has merged, so a fault between the two leaves
	// the PR merged and the row saying otherwise. The pre-check is then all that stops
	// a rerun re-merging it, and a repeated `gh pr merge` is exit 0 with a warning
	// (internal/faketool/FIDELITY.md), so nothing downstream would notice.
	task1.MergeExecuted = false
	task1.MergeExecutedAt = ""
	if err := state.Write(home, task1); err != nil {
		t.Fatal(err)
	}
	rerun := runHand(t, home, "merge", "task-1")
	assertInvocation(t, rerun, 3, "already merged")

	refused := runHand(t, home, "merge", "task-2")
	assertInvocation(t, refused, 3, "not green")
	task2, err := state.Read(home, "task-2")
	if err != nil {
		t.Fatal(err)
	}
	if task2.MergeExecuted {
		t.Fatalf("task-2 state = %+v, want MergeExecuted=false after refused merge (red checks must never reach gh pr merge)", task2)
	}

	logData, err := os.ReadFile(invocationLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), "pr merge 2") {
		t.Fatalf("gh invocation log = %q, want red checks to short-circuit before gh pr merge is ever called for task-2", logData)
	}
	if n := strings.Count(string(logData), "pr merge 1"); n != 1 {
		t.Fatalf("gh pr merge ran %d times for task-1, want 1: the rerun must not merge again\n%s", n, logData)
	}
}
