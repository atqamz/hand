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

// Proves the bug atqamz/hand#30 and atqamz/hand#32 set out to kill: herdr's idle and done are
// the same "pane stopped being busy" signal (see herdr.Status's doc comment for why), so a working -> idle
// transition right after a "needs-decision" report must never be classified as done.
func TestWatchIdleAfterReportedNeedsDecisionIsNotDone(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	now := time.Now().UTC().Format(time.RFC3339)
	writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, CreatedAt: now},
		state.Attempt{Lifecycle: state.AttemptRunning, Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"}})

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
	// Driven to "done", not "idle": real herdr renders a working-or-blocked -> idle transition as "done" for a
	// headless poller like hand, never "idle" (hand never focuses a client on a worker's pane, and only a
	// focused client's active tab keeps herdr from collapsing that transition into "done").
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

// The inverse of the case above: a not-busy pane carrying no terminal report
// is actionable, but not a Pending Decision, because inventing "stopped, reason
// unknown" from an idle pane is what crowded out the genuine questions.
func TestWatchIdleWithNoReportIsSupervisorActionable(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	now := time.Now().UTC().Format(time.RFC3339)
	writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, CreatedAt: now},
		state.Attempt{Lifecycle: state.AttemptRunning, Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"}})

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

	eventsLog, err := os.ReadFile(filepath.Join(state.Dir(home), "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(eventsLog), "idle-unreported task-1") {
		t.Fatalf("events.log = %q, want the unreported idle on the log an operator reads", eventsLog)
	}
	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.LastReportState != "" {
		t.Fatalf("LastReportState = %q, want nothing recorded for a worker that reported nothing", attempt.LastReportState)
	}
}

// Workers report with a truncating redirect, so every report after the first rewrites the file in place
// over a line hand watch has already consumed.
func TestWatchReportRewrittenInPlaceIsNotMalformed(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	now := time.Now().UTC().Format(time.RFC3339)
	writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, CreatedAt: now},
		state.Attempt{Lifecycle: state.AttemptRunning, Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"}})

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")

	dir := binDir(t)
	herdrLog := filepath.Join(t.TempDir(), "herdr-invocations.log")
	writeFakeHerdrWatch(t, dir, statusDir, herdrLog)

	watch := startHandBackground(t, home, "watch", "--poll", "30ms")
	waitForInvocation(t, herdrLog, "herdr pane get pane-1", 5*time.Second)

	// Two live samples from atqamz/hand#140, verbatim: both were announced as "malformed report"
	// carrying a mid-word fragment of themselves, and neither contains anything a parser could object to.
	notes := []string{
		"reading the ghutil call sites for --head",
		"gh confirmed --head takes plain branch name (qualified owner:branch returns nothing); implementing multi-repo search in ghutil",
		"adding durable pane_started_at and parked_fired_for columns to internal/store",
	}
	for _, note := range notes {
		if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("working: "+note+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		watch.waitForStdout(t, "working task-1: "+note, 5*time.Second)
	}

	result := watch.stop(t, 3*time.Second)
	if result.code != 0 {
		t.Fatalf("hand watch exit = %d after SIGTERM, want 0 (stderr %q)", result.code, result.stderr)
	}
	if stdout := watch.stdout.String(); strings.Contains(stdout, "malformed report") {
		t.Fatalf("stdout = %q, want no malformed report for a report rewritten in place", stdout)
	}

	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.LastReportNote != notes[len(notes)-1] {
		t.Fatalf("LastReportNote = %q, want %q", attempt.LastReportNote, notes[len(notes)-1])
	}
}

// Reports are one line of house-style prose, so a rewrite landing on exactly the byte count of the report
// it replaces is a matter of time rather than a contrived input - and it was skipped silently, because an
// offset at the end of the file with a newline behind it is what "nothing new" looks like too.
func TestWatchDoneRewrittenToTheSameLengthReachesVerifiedDone(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	now := time.Now().UTC().Format(time.RFC3339)
	writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindScout, CreatedAt: now},
		state.Attempt{Lifecycle: state.AttemptRunning, Worktree: filepath.Join(home, "wt-1"), Herdr: state.Herdr{PaneID: "pane-1"}})

	statusDir := t.TempDir()
	setPaneStatus(t, statusDir, "pane-1", "working")

	dir := binDir(t)
	herdrLog := filepath.Join(t.TempDir(), "herdr-invocations.log")
	writeFakeHerdrWatch(t, dir, statusDir, herdrLog)

	watch := startHandBackground(t, home, "watch", "--poll", "30ms")
	waitForInvocation(t, herdrLog, "herdr pane get pane-1", 5*time.Second)

	working := "working: finishing the findings section\n"
	done := "done: report.md written, findings in it\n"
	if len(working) != len(done) {
		t.Fatalf("working report is %d bytes and done report %d, want the collision this test exists for", len(working), len(done))
	}

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(working), 0o644); err != nil {
		t.Fatal(err)
	}
	watch.waitForStdout(t, "working task-1: finishing the findings section", 5*time.Second)

	// The `done:` variant is the one that costs a completion: the deferred verification is gated on the last
	// recorded report state, so a skipped done means a scout that finished is never announced as finished
	// (atqamz/hand#149).
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(done), 0o644); err != nil {
		t.Fatal(err)
	}
	watch.waitForStdout(t, "reported-done task-1: report.md written, findings in it", 5*time.Second)

	// A scout's completion evidence is the deliverable itself, landing after the
	// done line was consumed - so the verified announcement can only come from the
	// recorded report state, which is what a skipped rewrite leaves stale.
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("# report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The unverified line above ends in this same text, so the leading newline is
	// what tells "done task-1" apart from "reported-done task-1".
	watch.waitForStdout(t, "\ndone task-1: report.md written, findings in it", 5*time.Second)

	result := watch.stop(t, 3*time.Second)
	if result.code != 0 {
		t.Fatalf("hand watch exit = %d after SIGTERM, want 0 (stderr %q)", result.code, result.stderr)
	}
}
