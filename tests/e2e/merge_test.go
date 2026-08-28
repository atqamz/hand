//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/state"
)

// Drives `hand merge` through a faked gh, no real remote: all-green checks merge cleanly, a failing
// bucket is refused before gh pr merge is ever invoked, and a rerun of the merge that succeeded
// converges on the observed merge rather than merging a second time (atqamz/hand#422).
func TestMergePR(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	now := time.Now().UTC().Format(time.RFC3339)
	writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, PR: "1", CreatedAt: now}, state.Attempt{Lifecycle: state.AttemptRunning})
	writeTaskAttempt(t, home, state.Task{ID: "task-2", Project: "demo", Kind: state.KindShip, PR: "2", CreatedAt: now}, state.Attempt{Lifecycle: state.AttemptRunning})

	dir := binDir(t)
	invocationLog := filepath.Join(t.TempDir(), "gh-invocations.log")
	// prChecksGreen (cmd/merge.go) never consults gh's exit code once the "pr checks" JSON parses, so the
	// always-exit-0 fake below exercises the same path a real fail-bucket exit 1 would. Each merge through
	// it moves that PR to MERGED, so the rerun sees the state the first merge left, not the initial one.
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

	// The row is written only after gh has merged, so a fault between the two leaves the PR merged and the
	// row saying otherwise. The rerun recovers by converging on the observed merge, because a repeated
	// `gh pr merge` is exit 0 with a warning (internal/faketool/FIDELITY.md) and nothing downstream would notice.
	task1.MergeExecuted = false
	task1.MergeExecutedAt = ""
	if err := state.Write(home, task1); err != nil {
		t.Fatal(err)
	}
	rerun := runHand(t, home, "merge", "task-1")
	if rerun.code != 0 {
		t.Fatalf("rerun merge task-1: exit %d, stderr %q", rerun.code, rerun.stderr)
	}
	if !strings.Contains(rerun.stdout, "result: converged") {
		t.Fatalf("rerun stdout = %q, want the rerun to converge on the observed merge", rerun.stdout)
	}
	converged, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if converged.MergeExecuted || !converged.MergeAnnounced {
		t.Fatalf("task-1 state = %+v, want MergeExecuted=false and MergeAnnounced=true: hand observed this merge, it did not perform it", converged)
	}

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
