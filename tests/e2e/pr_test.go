//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/state"
)

// TestPRCommand drives `hand pr` through the built binary against a real
// local clone (redirected to a GitHub URL via git's insteadOf mechanism, no
// network) and a faked gh: recording succeeds only once the URL's repo
// matches the project's own origin remote and gh confirms the PR exists, and
// refuses a different URL once one is already recorded. A repeated identical
// URL reconciles rather than no-ops, repairing the dashboard row; with no row
// to write to, both paths exit non-zero rather than claim a recording the
// dashboard never received.
func TestPRCommand(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remote)
	redirectGitRemote(t, "https://github.com/owner/e2e-fixture.git", remote)

	dir := binDir(t)
	writeFakeTreehouse(t, dir, filepath.Join(t.TempDir(), "unused-worktree"))

	home := newHome(t)
	added := runHand(t, home, "project", "add", "https://github.com/owner/e2e-fixture.git", "--mode", "direct-pr")
	if added.code != 0 {
		t.Fatalf("project add: exit %d, stderr %q", added.code, added.stderr)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "e2e-fixture", Kind: state.KindShip, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	invocationLog := filepath.Join(t.TempDir(), "gh-invocations.log")
	writeFakeDispatch(t, dir, "gh", invocationLog, "$1 $2", `  "pr view") echo '{"state":"OPEN"}' ;;`)

	mismatch := runHand(t, home, "pr", "task-1", "https://github.com/other/repo/pull/1")
	assertInvocation(t, mismatch, 3, "not project")

	// This task was written directly rather than spawned, so it has no active
	// dashboard row yet: the record lands on the task but has nothing to write
	// to the dashboard, and must say so instead of reporting a clean record.
	url := "https://github.com/owner/e2e-fixture/pull/7"
	noRow := runHand(t, home, "pr", "task-1", url)
	assertInvocation(t, noRow, 3, "no active row")

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != url {
		t.Fatalf("task.PR = %q, want %q", task.PR, url)
	}

	dashPath := filepath.Join(home, "data", "dashboard.md")
	if err := dashboard.Update(dashPath, dashboard.UpdateOpts{AddActiveTask: &dashboard.ActiveTask{
		ID: "task-1", Project: "e2e-fixture", Kind: state.KindShip, State: "working", Age: "just now",
	}}); err != nil {
		t.Fatal(err)
	}

	again := runHand(t, home, "pr", "task-1", url)
	if again.code != 0 {
		t.Fatalf("repeated identical pr record: exit %d, stderr %q", again.code, again.stderr)
	}
	if !strings.Contains(again.stdout, "already recorded") || !strings.Contains(again.stdout, "reconciled") {
		t.Fatalf("repeat stdout = %q, want the reconciling repeat reported", again.stdout)
	}
	dash, err := os.ReadFile(dashPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dash), url) {
		t.Fatalf("dashboard.md = %q, want the PR column repaired by the repeat", string(dash))
	}

	different := runHand(t, home, "pr", "task-1", "https://github.com/owner/e2e-fixture/pull/8")
	assertInvocation(t, different, 3, "different PR recorded")

	task, err = state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != url {
		t.Fatalf("task.PR = %q after refused overwrite, want the original %q untouched", task.PR, url)
	}
}
