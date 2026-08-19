//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/state"
)

// The clone is redirected to a GitHub URL via git's insteadOf mechanism, so
// the URL-to-origin check runs against a real remote with no network.
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
	writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "e2e-fixture", Kind: state.KindShip, CreatedAt: now}, state.Attempt{Lifecycle: state.AttemptRunning})

	url := "https://github.com/owner/e2e-fixture/pull/7"
	invocationLog := filepath.Join(t.TempDir(), "gh-invocations.log")
	faketool.GH{Log: invocationLog, PRs: []faketool.GHPR{{URL: url, State: "OPEN"}}}.Install(t, dir)

	mismatch := runHand(t, home, "pr", "task-1", "https://github.com/other/repo/pull/1")
	assertInvocation(t, mismatch, 3, "not project")

	recorded := runHand(t, home, "pr", "task-1", url)
	if recorded.code != 0 {
		t.Fatalf("pr record: exit %d, stderr %q", recorded.code, recorded.stderr)
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != url {
		t.Fatalf("task.PR = %q, want %q", task.PR, url)
	}

	again := runHand(t, home, "pr", "task-1", url)
	if again.code != 0 {
		t.Fatalf("repeated identical pr record: exit %d, stderr %q", again.code, again.stderr)
	}
	if !strings.Contains(again.stdout, "result: already-recorded\n") {
		t.Fatalf("repeat stdout = %q, want the repeat reported", again.stdout)
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
