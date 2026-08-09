//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

// Drives `hand watch` as a background process against two seeded tasks and asserts on its actual contract:
// a task's status at watch startup never retroactively fires an event, only a later transition does.
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

	// Both panes appearing in the invocation log proves the seeding tick - which per watcher.go only records
	// status, never emits - has covered both tasks. Only after that can a status change be a genuine
	// transition rather than a different seed value.
	waitForInvocation(t, herdrLog, "herdr pane get pane-1", 5*time.Second)
	waitForInvocation(t, herdrLog, "herdr pane get pane-2", 5*time.Second)

	// Real herdr renders task-1's working -> not-busy transition as "done" - see herdr.Status's doc comment -
	// and with no report on file it is unexplained, so it fires idle-unreported rather than done.
	setPaneStatus(t, statusDir, "pane-1", "done")
	watch.waitForStdout(t, "idle-unreported task-1", 5*time.Second)
	// task-1's event is the liveness signal: it proves the watcher has run a post-seed tick over both tasks,
	// so the absence of any task-2 output is a real suppression of task-2's pre-existing "blocked" status
	// rather than a watcher that has not polled yet.
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

	// Events for distinct tasks have to appear on stdout in the order they occurred.
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

	// The log has to hold them durably too, so a consumer that starts reading only after the fact still sees
	// everything a live stdout reader saw.
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

// A done worker still attached to its pane is silence like any other: what severs a task from steering is
// the status file being torn down, not the worker's own last word, so done/failed are bounded under their
// own tier rather than exempt.
func TestWatchParksADoneWorkerUnderItsOwnBound(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	writeConfig(t, home, "parked-done-bound", "2")

	if err := state.Write(home, state.Task{
		ID: "shipped-task", Project: "demo", Kind: state.KindShip,
		Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	// Written now so the bound is crossed while the poll loop runs, not before it.
	if err := os.WriteFile(state.ReportPath(home, "shipped-task"), []byte("done: shipped the migration\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	statusDir := t.TempDir()
	// The pane stays "working" throughout, so no herdr transition can produce the parked line below - only
	// the done-tier bound can.
	setPaneStatus(t, statusDir, "pane-1", "working")
	writeFakeHerdrWatch(t, binDir(t), statusDir, filepath.Join(t.TempDir(), "herdr-invocations.log"))

	watch := startHandBackground(t, home, "watch", "--poll", "30ms")

	// The report line landing first is what makes the parked line below a
	// done-tier decision: the classifier has to have recorded "done" as the last
	// report state before it can pick that tier.
	watch.waitForStdout(t, "reported-done shipped-task: shipped the migration", 5*time.Second)
	watch.waitForStdout(t, "parked shipped-task: done: shipped the migration (silent", 15*time.Second)

	result := watch.stop(t, 3*time.Second)
	if result.code != 0 {
		t.Fatalf("hand watch exit = %d after SIGTERM, want 0 (stderr %q)", result.code, result.stderr)
	}
}

// The bug atqamz/secondhand#127 tracks, exercised through a real restart rather than through the latch
// alone: a done task's report file never grows again, so the silence parked fired against stays frozen, and
// a re-derived latch re-announces it on every re-arm.
func TestWatchDoesNotRefireParkedForADoneTaskAcrossARestart(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	// Wider than the streaming sibling's bound: --until-event arms with a probe and two stdout-discarding
	// baseline ticks, and the bound has to outlast all of them under -race, or the parked line lands in the
	// log while stdout is still discarded and the first run exits 4 with nothing delivered.
	writeConfig(t, home, "parked-done-bound", "6")

	spawnedAt := time.Now().UTC().Format(time.RFC3339)
	if err := state.Write(home, state.Task{
		ID: "shipped-task", Project: "demo", Kind: state.KindShip,
		Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"},
		CreatedAt: spawnedAt, PaneStartedAt: spawnedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, "shipped-task"), []byte("done: shipped the migration\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")
	writeFakeHerdrWatch(t, binDir(t), statusDir, filepath.Join(t.TempDir(), "herdr-invocations.log"))

	first := runHand(t, home, "watch", "--until-event", "--event", "parked", "--poll", "30ms", "--timeout", "30s")
	if first.code != 0 {
		t.Fatalf("first watch exit = %d, want 0 for a delivered parked event (stdout %q, stderr %q)", first.code, first.stdout, first.stderr)
	}
	// Counted in the log rather than only on stdout because state/events.log is capped at 200 lines, so a
	// duplicate evicts real history; on stdout the second run's duplicate would never have appeared anyway.
	if got := countEventLogLines(t, home, "parked shipped-task"); got != 1 {
		t.Fatalf("events.log holds %d parked lines after the first run, want 1", got)
	}

	second := runHand(t, home, "watch", "--until-event", "--event", "parked", "--poll", "30ms", "--timeout", "3s")
	if second.code != 4 {
		t.Fatalf("second watch exit = %d, want 4: the only silence it could wake on was already announced (stdout %q, stderr %q)", second.code, second.stdout, second.stderr)
	}
	if got := countEventLogLines(t, home, "parked shipped-task"); got != 1 {
		t.Fatalf("events.log holds %d parked lines after the restart, want 1: the report file never grew, so this is the same silence", got)
	}
}

func countEventLogLines(t *testing.T, home, substr string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state.Dir(home), "events.log"))
	if err != nil {
		t.Fatalf("read events.log: %v", err)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, substr) {
			count++
		}
	}
	return count
}

// The report channel produces a wake (`report-done`) long before the parked bound matures, so exiting on
// `parked` at all is the filter's doing: an unfiltered --until-event would have delivered that earlier wake
// and exited on it.
func TestWatchUntilEventWakesOnlyOnTheFilteredKind(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	writeConfig(t, home, "parked-done-bound", "2")

	if err := state.Write(home, state.Task{
		ID: "shipped-task", Project: "demo", Kind: state.KindShip,
		Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, "shipped-task"), []byte("done: shipped the migration\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")
	writeFakeHerdrWatch(t, binDir(t), statusDir, filepath.Join(t.TempDir(), "herdr-invocations.log"))

	got := runHand(t, home, "watch", "--until-event", "--event", "parked", "--poll", "30ms", "--timeout", "30s")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0 for a delivered parked event (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "parked shipped-task: done: shipped the migration (silent") {
		t.Fatalf("stdout = %q, want the parked line the caller asked to wake on", got.stdout)
	}
	if strings.Contains(got.stdout, "reported-done") {
		t.Fatalf("stdout = %q, want the report-done wake filtered out: the caller named parked only", got.stdout)
	}

	// The filter is stdout-only, so events.log still has to carry the wake it suppressed.
	logData, err := os.ReadFile(filepath.Join(state.Dir(home), "events.log"))
	if err != nil {
		t.Fatalf("read events.log: %v", err)
	}
	if !strings.Contains(string(logData), "reported-done shipped-task: shipped the migration") {
		t.Fatalf("events.log = %q, want the filtered wake still durably recorded: the filter is stdout-only", logData)
	}
}

// The notify template writes $HAND_MESSAGE straight to a marker file with no wrapper script of any kind, so
// a marker that ends up holding the exact event text is only possible if hand watch invoked config/notify
// in-process itself - the two hard requirements the wiring exists to satisfy.
func TestWatchNotifiesInProcessForABlockedEvent(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	marker := filepath.Join(home, "notify-marker.txt")
	writeConfig(t, home, "notify", "printf '%s' \"$HAND_MESSAGE\" > "+marker)

	if err := state.Write(home, state.Task{
		ID: "task-1", Project: "demo", Kind: state.KindShip,
		Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")
	herdrLog := filepath.Join(t.TempDir(), "herdr-invocations.log")
	writeFakeHerdrWatch(t, binDir(t), statusDir, herdrLog)

	watch := startHandBackground(t, home, "watch", "--poll", "30ms")
	waitForInvocation(t, herdrLog, "herdr pane get pane-1", 5*time.Second)

	setPaneStatus(t, statusDir, "pane-1", "blocked")
	watch.waitForStdout(t, "blocked task-1: agent needs help", 5*time.Second)

	deadline := time.Now().Add(5 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		var err error
		got, err = os.ReadFile(marker)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if string(got) != "blocked task-1: agent needs help" {
		t.Fatalf("notify marker = %q, want the event text delivered by config/notify in-process", got)
	}

	result := watch.stop(t, 3*time.Second)
	if result.code != 0 {
		t.Fatalf("hand watch exit = %d after SIGTERM, want 0 (stderr %q)", result.code, result.stderr)
	}
}

// A task first sighted with its pane already unreachable - a re-scan picking up a fresh spawn - has no
// probed-to-unprobed edge to fire `failed` on, and used to be dropped from tracking entirely. The blink
// task is the other half: it answers again before the dwell matures and must produce nothing at all.
func TestWatchTracksATaskFirstSightedUnreachable(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")
	writeConfig(t, home, "stale-threshold", "2")

	statusDir := t.TempDir()
	herdrLog := filepath.Join(t.TempDir(), "herdr-invocations.log")
	writeFakeHerdrWatch(t, binDir(t), statusDir, herdrLog)

	now := time.Now().UTC().Format(time.RFC3339)
	setPaneStatus(t, statusDir, "pane-anchor", "working")
	if err := state.Write(home, state.Task{
		ID: "anchor-task", Project: "demo", Kind: state.KindShip,
		Worktree: filepath.Join(home, "wt-anchor"), Herdr: state.Herdr{PaneID: "pane-anchor"},
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	watch := startHandBackground(t, home, "watch", "--poll", "30ms")
	waitForInvocation(t, herdrLog, "herdr pane get pane-anchor", 5*time.Second)

	// Published only once the poll loop is already running, so these are sightings
	// no arm-time probe sweep could have made: this is the after-arming case.
	setPaneStatus(t, statusDir, "pane-dark", "unreachable")
	setPaneStatus(t, statusDir, "pane-blink", "unreachable")
	sighted := time.Now().UTC().Format(time.RFC3339)
	for _, task := range []state.Task{
		{ID: "dark-task", Project: "demo", Kind: state.KindShip, Worktree: filepath.Join(home, "wt-dark"), Herdr: state.Herdr{PaneID: "pane-dark"}, CreatedAt: sighted},
		{ID: "blink-task", Project: "demo", Kind: state.KindShip, Worktree: filepath.Join(home, "wt-blink"), Herdr: state.Herdr{PaneID: "pane-blink"}, CreatedAt: sighted},
	} {
		if err := state.Write(home, task); err != nil {
			t.Fatal(err)
		}
	}

	// Three failing probes at a 30ms poll is well inside the 2s dwell, so silence
	// here is the dwell holding rather than a watcher that hasn't looked yet.
	waitForInvocations(t, herdrLog, "herdr pane get pane-dark", 3, 5*time.Second)
	if strings.Contains(watch.stdout.String(), "failed dark-task") {
		t.Fatalf("failed fired on sight, before the dwell matured: stdout=%q", watch.stdout.String())
	}
	setPaneStatus(t, statusDir, "pane-blink", "working")

	watch.waitForStdout(t, "failed dark-task", 15*time.Second)
	if strings.Contains(watch.stdout.String(), "failed blink-task") {
		t.Fatalf("a pane that blinked and came back raised failed: stdout=%q", watch.stdout.String())
	}

	result := watch.stop(t, 3*time.Second)
	if result.code != 0 {
		t.Fatalf("hand watch exit = %d after SIGTERM, want 0 (stderr %q)", result.code, result.stderr)
	}
}

// The full-stack half of internal/watcher's usage-limit tests: a real `hand watch` process, a real sqlite
// state file, a real hold, and a fake herdr whose panes it reads and types into. The resume is split
// across two watch runs because the first attempt is never due within a test's lifetime.
func TestWatchResumesAUsageLimitedWorkerAndLeavesOthersAlone(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	now := time.Now().UTC().Format(time.RFC3339)
	for _, task := range []state.Task{
		{ID: "limited", Project: "demo", Kind: state.KindShip, Worktree: filepath.Join(home, "wt-limited"), Herdr: state.Herdr{PaneID: "pane-limited"}, CreatedAt: now},
		{ID: "plain", Project: "demo", Kind: state.KindShip, Worktree: filepath.Join(home, "wt-plain"), Herdr: state.Herdr{PaneID: "pane-plain"}, CreatedAt: now},
	} {
		if err := state.Write(home, task); err != nil {
			t.Fatal(err)
		}
	}

	statusDir := t.TempDir()
	for _, pane := range []string{"pane-limited", "pane-plain"} {
		setPaneAgent(t, statusDir, pane, "claude")
		setPaneStatus(t, statusDir, pane, "working")
	}
	// Both tasks stop the same way and only one has a limit refusal on screen, so the untouched task is the
	// control: it proves the resume is driven by what the harness printed rather than by the stop itself.
	setPaneText(t, statusDir, "pane-limited", "> resume\n\nClaude usage limit reached. Your limit will reset at 3pm (UTC).\n")
	setPaneText(t, statusDir, "pane-plain", "> resume\n\nI have finished the refactor.\n")

	dir := binDir(t)
	detectLog := filepath.Join(t.TempDir(), "detect.log")
	writeFakeHerdrWatch(t, dir, statusDir, detectLog)

	watch := startHandBackground(t, home, "watch", "--poll", "30ms")
	waitForInvocation(t, detectLog, "herdr pane get pane-limited", 5*time.Second)
	waitForInvocation(t, detectLog, "herdr pane get pane-plain", 5*time.Second)

	setPaneStatus(t, statusDir, "pane-limited", "done")
	setPaneStatus(t, statusDir, "pane-plain", "done")

	watch.waitForStdout(t, "usage-limit limited:", 5*time.Second)
	// The plain task's own stop event is the liveness signal: once it has fired, the
	// watcher has read that pane and decided against it, so the absence of a steer
	// below is a real decision rather than a poll that has not happened yet.
	watch.waitForStdout(t, "idle-unreported plain", 5*time.Second)

	hold, found, err := state.ReadHold(home, "limited")
	if err != nil || !found {
		t.Fatalf("ReadHold(limited) = %v, %v, want a hold recording the limit", found, err)
	}
	if hold.Kind != state.HoldKindLimit {
		t.Fatalf("hold kind = %q, want %q", hold.Kind, state.HoldKindLimit)
	}
	if _, found, err := state.ReadHold(home, "plain"); found || err != nil {
		t.Fatalf("ReadHold(plain) = %v, %v, want no hold for a worker that stopped for another reason", found, err)
	}

	limited, err := state.Read(home, "limited")
	if err != nil {
		t.Fatal(err)
	}
	if limited.UsageLimitRetryAt == "" {
		t.Fatalf("limited task = %+v, want a durable retry stamp", limited)
	}
	plain, err := state.Read(home, "plain")
	if err != nil {
		t.Fatal(err)
	}
	if plain.UsageLimitRetryAt != "" || plain.UsageLimitAttempts != 0 {
		t.Fatalf("plain task = %+v, want no usage-limit schedule", plain)
	}

	if result := watch.stop(t, 3*time.Second); result.code != 0 {
		t.Fatalf("hand watch exit = %d after SIGTERM, want 0 (stderr %q)", result.code, result.stderr)
	}
	if data, err := os.ReadFile(detectLog); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(data), "send-text") {
		t.Fatalf("herdr log = %q, want no pane steered before any attempt was due", data)
	}

	// The restart: the stamp the first run wrote moved into the past, which is the one thing that makes an
	// attempt due without waiting out the ten-minute floor. It covers the durability too - a watcher that
	// came up fresh still knows this worker is limited and when it may be poked.
	limited.UsageLimitRetryAt = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if err := state.Write(home, limited); err != nil {
		t.Fatal(err)
	}

	resumeLog := filepath.Join(t.TempDir(), "resume.log")
	writeFakeHerdrWatch(t, dir, statusDir, resumeLog)
	resumed := startHandBackground(t, home, "watch", "--poll", "30ms")

	waitForInvocation(t, resumeLog, "herdr pane send-text pane-limited", 10*time.Second)
	waitForInvocation(t, resumeLog, "herdr pane send-keys pane-limited Enter", 10*time.Second)

	// The steer is only an attempt; the pane running again is what ends the limit.
	setPaneStatus(t, statusDir, "pane-limited", "working")
	resumed.waitForStdout(t, "usage-limit-resumed limited: running again after 1 attempt", 10*time.Second)

	if _, found, err := state.ReadHold(home, "limited"); found || err != nil {
		t.Fatalf("ReadHold(limited) after resume = %v, %v, want the hold released", found, err)
	}
	after, err := state.Read(home, "limited")
	if err != nil {
		t.Fatal(err)
	}
	if after.UsageLimitRetryAt != "" || after.UsageLimitAttempts != 0 {
		t.Fatalf("limited task after resume = %+v, want the schedule cleared", after)
	}

	if result := resumed.stop(t, 3*time.Second); result.code != 0 {
		t.Fatalf("hand watch exit = %d after SIGTERM, want 0 (stderr %q)", result.code, result.stderr)
	}
	if data, err := os.ReadFile(resumeLog); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(data), "send-text pane-plain") {
		t.Fatalf("herdr log = %q, want the unlimited worker never steered", data)
	}
}
