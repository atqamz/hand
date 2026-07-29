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

// task-2 sits in a state the grep-on-first-line wrapper this mode replaces would
// have matched immediately (see TestRunUntilEventTakesTheStartupStateAsBaseline),
// so nothing about it may appear on stdout.
func TestWatchUntilEventExitsOnTheFirstTransition(t *testing.T) {
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
	// A terminal report line nobody consumed: report_offset is still 0, so the
	// poll loop classifies it as new. The baseline has to absorb it silently.
	if err := os.WriteFile(state.ReportPath(home, "task-2"), []byte("done: PR merged already\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")
	setPaneStatus(t, statusDir, "pane-2", "done")

	dir := binDir(t)
	herdrLog := filepath.Join(t.TempDir(), "herdr-invocations.log")
	writeFakeHerdrWatch(t, dir, statusDir, herdrLog)

	watch := startHandBackground(t, home, "watch", "--until-event", "--poll", "30ms", "--timeout", "60s")

	// Three polls each - arm probe, seeding tick, report-backlog tick - and only
	// then is the watcher armed: a status published earlier becomes the baseline
	// and the transition this test waits on never happens.
	waitForInvocations(t, herdrLog, "herdr pane get pane-1", 3, 5*time.Second)
	waitForInvocations(t, herdrLog, "herdr pane get pane-2", 3, 5*time.Second)

	setPaneStatus(t, statusDir, "pane-1", "done")

	result := watch.waitForExit(t, 10*time.Second, "a delivered event")
	if result.code != 0 {
		t.Fatalf("exit = %d, want 0 for a delivered event (stdout %q, stderr %q)", result.code, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stdout, "idle-unreported task-1") {
		t.Fatalf("stdout = %q, want idle-unreported task-1", result.stdout)
	}
	if strings.Contains(result.stdout, "task-2") {
		t.Fatalf("stdout = %q, want nothing about task-2: its state and its unconsumed report line are both baseline, not transitions", result.stdout)
	}

	logData, err := os.ReadFile(filepath.Join(state.Dir(home), "events.log"))
	if err != nil {
		t.Fatalf("read events.log: %v", err)
	}
	if !strings.Contains(string(logData), "reported-done task-2") {
		t.Fatalf("events.log = %q, want task-2's baseline report line recorded", string(logData))
	}
}

func TestWatchUntilEventTimesOutWithADistinctCode(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	if err := state.Write(home, state.Task{
		ID: "task-1", Project: "demo", Kind: state.KindShip,
		Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")

	dir := binDir(t)
	writeFakeHerdrWatch(t, dir, statusDir, filepath.Join(t.TempDir(), "herdr-invocations.log"))

	got := runHand(t, home, "watch", "--until-event", "--poll", "30ms", "--timeout", "300ms")
	if got.code != 4 {
		t.Fatalf("exit = %d, want 4 for a window that produced no event (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "no event") {
		t.Fatalf("stderr = %q, want it to say no event occurred", got.stderr)
	}
	if strings.TrimSpace(got.stdout) != "" {
		t.Fatalf("stdout = %q, want events only, never the timeout notice", got.stdout)
	}
}

// The generous --timeout is the assertion: an unprobeable worker would otherwise
// burn the whole window and come back as a quiet fleet, so exit 5 has to arrive
// well before it.
func TestWatchUntilEventFailsToArmWithADistinctCode(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	if err := state.Write(home, state.Task{
		ID: "task-1", Project: "demo", Kind: state.KindShip,
		Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	writeFakeHerdrUnprobeablePanes(t, binDir(t))

	got := runHand(t, home, "watch", "--until-event", "--poll", "30ms", "--timeout", "10s")
	if got.code != 5 {
		t.Fatalf("exit = %d, want 5 for a task that could not be probed at arm time (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "task-1") {
		t.Fatalf("stderr = %q, want the unreachable worker named: the caller has nothing on stdout to go on", got.stderr)
	}
	if strings.TrimSpace(got.stdout) != "" {
		t.Fatalf("stdout = %q, want nothing: arming never completed, so no fleet news was delivered", got.stdout)
	}
}

// The pane stays "working" for the whole run: no herdr transition can produce
// this event, so a watcher that only classified status changes would sit here
// until its timeout while the worker was already gone.
func TestWatchUntilEventDeliversParkedWhenTheReportChannelGoesSilent(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	writeConfig(t, home, "parked-other-bound", "2")

	if err := state.Write(home, state.Task{
		ID: "slow-migration", Project: "demo", Kind: state.KindShip,
		Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	// Written now so the bound is crossed while the poll loop runs, not before it.
	if err := os.WriteFile(state.ReportPath(home, "slow-migration"), []byte("working: still on the migration\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")
	writeFakeHerdrWatch(t, binDir(t), statusDir, filepath.Join(t.TempDir(), "herdr-invocations.log"))

	got := runHand(t, home, "watch", "--until-event", "--poll", "30ms", "--timeout", "30s")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0 for a delivered parked event (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "parked slow-migration: working: still on the migration") {
		t.Fatalf("stdout = %q, want the parked line carrying the worker's last report", got.stdout)
	}
}
