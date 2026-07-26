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

// TestWatchEventStream drives `hand watch` as a background process against
// two seeded tasks and asserts on its actual contract: a task's status at
// watch startup never retroactively fires an event (only a later transition
// does), events for distinct tasks appear on stdout in the order they
// occurred, and state/events.log durably records them so a consumer that
// starts reading only after the fact - the "late-starting consumer" case -
// still sees everything a live stdout reader saw.
func TestWatchEventStream(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	now := time.Now().UTC().Format(time.RFC3339)
	for _, task := range []state.Task{
		{ID: "task-1", Project: "demo", Kind: state.KindShip, Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"}, CreatedAt: now},
		{ID: "task-2", Project: "demo", Kind: state.KindShip, Worktree: filepath.Join(home, "wt-2"), Herdr: state.Herdr{PaneID: "pane-2"}, CreatedAt: now},
	} {
		if err := state.Write(home, task); err != nil {
			t.Fatal(err)
		}
	}

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")
	setPaneStatus(t, statusDir, "pane-2", "blocked")

	dir := binDir(t)
	herdrLog := filepath.Join(t.TempDir(), "herdr-invocations.log")
	writeFakeHerdrWatch(t, dir, statusDir, herdrLog)

	watch := startHandBackground(t, home, "watch", "--poll", "30ms")

	// Both panes appearing in the invocation log proves the seeding tick -
	// which per watcher.go only records status, never emits - has covered both
	// tasks. Only after that can a status change be a genuine transition
	// rather than a different seed value.
	waitForInvocation(t, herdrLog, "herdr pane get pane-1", 5*time.Second)
	waitForInvocation(t, herdrLog, "herdr pane get pane-2", 5*time.Second)

	// task-1's working -> not-busy transition is the liveness signal: seeing
	// its event proves the watcher has run a post-seed tick over both tasks,
	// so the absence of any task-2 output is a real suppression of task-2's
	// pre-existing "blocked" status rather than a watcher that hasn't polled.
	// Real herdr renders this transition as "done" - see herdr.Status's doc
	// comment - and with no report on file it's unexplained, so it fires
	// idle-unreported, not done.
	setPaneStatus(t, statusDir, "pane-1", "done")
	watch.waitForStdout(t, "idle-unreported task-1", 5*time.Second)
	if strings.Contains(watch.stdout.String(), "task-2") {
		t.Fatalf("watch fired an event for task-2's pre-existing status before any transition: stdout=%q", watch.stdout.String())
	}

	// Drive task-2 out of its seeded "blocked" state and wait for the
	// resulting event, so the later blocked event is a genuine
	// not-busy -> blocked transition rather than a repeat the classifier drops.
	setPaneStatus(t, statusDir, "pane-2", "done")
	watch.waitForStdout(t, "idle-unreported task-2", 5*time.Second)
	setPaneStatus(t, statusDir, "pane-2", "blocked")
	watch.waitForStdout(t, "blocked task-2: agent needs help", 5*time.Second)

	stdout := watch.stdout.String()
	idleUnreportedAt := strings.Index(stdout, "idle-unreported task-1")
	blockedAt := strings.Index(stdout, "blocked task-2")
	if idleUnreportedAt < 0 || blockedAt < 0 || idleUnreportedAt > blockedAt {
		t.Fatalf("events out of order in stdout: %q", stdout)
	}

	result := watch.stop(t, 3*time.Second)
	if result.code != 0 {
		t.Fatalf("hand watch exit = %d after SIGTERM, want 0 (stderr %q)", result.code, result.stderr)
	}

	logData, err := os.ReadFile(filepath.Join(state.Dir(home), "events.log"))
	if err != nil {
		t.Fatalf("read events.log: %v", err)
	}
	log := string(logData)
	if !strings.Contains(log, "idle-unreported task-1") || !strings.Contains(log, "blocked task-2: agent needs help") {
		t.Fatalf("events.log = %q, want both events durably recorded for a late-starting consumer to find", log)
	}
}
