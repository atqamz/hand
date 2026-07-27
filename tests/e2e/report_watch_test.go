//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/state"
)

// TestWatchIdleAfterReportedNeedsDecisionIsNotDone proves the bug #30/#32 set
// out to kill: herdr's idle and done are the same "pane stopped being busy"
// signal (see herdr.Status's doc comment for why), and a worker's last
// reported state before that is what actually explains why. A working -> idle
// transition right after a "needs-decision" report must never be classified
// as done. This drives the fake to "done", not "idle": real herdr renders a
// working-or-blocked -> idle transition as "done" for a headless poller like
// hand, never "idle" (hand never focuses a client on a worker's pane, and only
// a focused client's active tab keeps herdr's own notification bookkeeping
// from collapsing that transition into "done").
func TestWatchIdleAfterReportedNeedsDecisionIsNotDone(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	now := time.Now().UTC().Format(time.RFC3339)
	if err := state.Write(home, state.Task{
		ID: "task-1", Project: "demo", Kind: state.KindShip,
		Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"}, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")

	dir := binDir(t)
	herdrLog := filepath.Join(t.TempDir(), "herdr-invocations.log")
	writeFakeHerdrWatch(t, dir, statusDir, herdrLog)

	watch := startHandBackground(t, home, "watch", "--poll", "30ms")
	waitForInvocation(t, herdrLog, "herdr pane get pane-1", 5*time.Second)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("needs-decision: waiting on review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setPaneStatus(t, statusDir, "pane-1", "done")
	watch.waitForStdout(t, "needs-decision task-1: waiting on review", 5*time.Second)

	result := watch.stop(t, 3*time.Second)
	if result.code != 0 {
		t.Fatalf("hand watch exit = %d after SIGTERM, want 0 (stderr %q)", result.code, result.stderr)
	}

	stdout := watch.stdout.String()
	if strings.Contains(stdout, "done task-1") {
		t.Fatalf("stdout = %q, want idle after a needs-decision report to never be classified as done", stdout)
	}
	if strings.Contains(stdout, "idle-unreported task-1") {
		t.Fatalf("stdout = %q, want the idle transition absorbed since the needs-decision report explains the stop", stdout)
	}
}

// TestWatchIdleWithNoReportIsSupervisorActionable proves the inverse: a
// not-busy pane (real herdr's "done", per the comment above) that carries no
// terminal report at all must surface as actionable ("stopped, reason
// unknown"), not be silently dropped the way it used to be treated as done.
func TestWatchIdleWithNoReportIsSupervisorActionable(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	now := time.Now().UTC().Format(time.RFC3339)
	if err := state.Write(home, state.Task{
		ID: "task-1", Project: "demo", Kind: state.KindShip,
		Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"}, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")

	dir := binDir(t)
	herdrLog := filepath.Join(t.TempDir(), "herdr-invocations.log")
	writeFakeHerdrWatch(t, dir, statusDir, herdrLog)

	watch := startHandBackground(t, home, "watch", "--poll", "30ms")
	waitForInvocation(t, herdrLog, "herdr pane get pane-1", 5*time.Second)

	setPaneStatus(t, statusDir, "pane-1", "done")
	watch.waitForStdout(t, "idle-unreported task-1", 5*time.Second)

	result := watch.stop(t, 3*time.Second)
	if result.code != 0 {
		t.Fatalf("hand watch exit = %d after SIGTERM, want 0 (stderr %q)", result.code, result.stderr)
	}

	dashData, err := os.ReadFile(filepath.Join(home, "data", "dashboard.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dashData), "stopped, reason unknown") {
		t.Fatalf("dashboard.md = %q, want the unreported idle flagged as an actionable pending decision", string(dashData))
	}
}
