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
	writeFakeHerdrWatch(t, dir, statusDir)

	watch := startHandBackground(t, home, "watch", "--poll", "30ms")
	defer func() {
		if watch.cmd.ProcessState == nil {
			_ = watch.cmd.Process.Kill()
		}
	}()

	// Give the first tick (which only seeds state, per watcher.go's tick)
	// several poll intervals to run, then confirm task-2's pre-existing
	// "blocked" status never produced an event: only a later transition
	// should.
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(watch.stdout.String(), "task-2") {
		t.Fatalf("watch fired an event for task-2's pre-existing status before any transition: stdout=%q", watch.stdout.String())
	}

	setPaneStatus(t, statusDir, "pane-1", "done")
	watch.waitForStdout(t, "done task-1", 2*time.Second)

	setPaneStatus(t, statusDir, "pane-2", "working")
	time.Sleep(100 * time.Millisecond)
	setPaneStatus(t, statusDir, "pane-2", "blocked")
	watch.waitForStdout(t, "blocked task-2: agent needs help", 2*time.Second)

	stdout := watch.stdout.String()
	doneAt := strings.Index(stdout, "done task-1")
	blockedAt := strings.Index(stdout, "blocked task-2")
	if doneAt < 0 || blockedAt < 0 || doneAt > blockedAt {
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
	if !strings.Contains(log, "done task-1") || !strings.Contains(log, "blocked task-2: agent needs help") {
		t.Fatalf("events.log = %q, want both events durably recorded for a late-starting consumer to find", log)
	}
}
