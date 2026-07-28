package watcher

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/state"
)

// notBusyStatuses covers herdr's two spellings of "pane stopped being busy" - see
// herdr.Status's doc comment for why idle and done must be classified identically:
// hand's headless polling model observes done, essentially always, never idle, for
// this transition against real herdr.
var notBusyStatuses = []herdr.Status{herdr.StatusIdle, herdr.StatusDone}

func TestClassifyStatusWorkingToNotBusyFiresIdleUnreportedWhenNoTerminalReport(t *testing.T) {
	for _, notBusy := range notBusyStatuses {
		now := time.Now()
		ts := NewTaskState(herdr.StatusWorking, now)

		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(time.Second)); e == nil || e.Kind != KindIdleUnreported {
			t.Fatalf("status %q: got %+v, want idle-unreported event when nothing explained the stop", notBusy, e)
		}
		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(2*time.Second)); e != nil {
			t.Fatalf("status %q: repeated not-busy state fired again: %+v", notBusy, e)
		}
	}
}

// TestClassifyStatusIdleUnreportedRefiresOnlyAfterTheWorkerResumesAndStopsAgain
// holds idle-unreported to an edge, which is what lets hand watch --until-event
// wake on it: firing every poll while a pane stays quiet is the wake storm that
// kept it out of the earlier grep-based trigger, and firing once ever would lose
// the second stop, which is a separate occurrence and a separate wake.
func TestClassifyStatusIdleUnreportedRefiresOnlyAfterTheWorkerResumesAndStopsAgain(t *testing.T) {
	for _, notBusy := range notBusyStatuses {
		now := time.Now()
		ts := NewTaskState(herdr.StatusWorking, now)

		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(time.Second)); e == nil || e.Kind != KindIdleUnreported {
			t.Fatalf("status %q: got %+v, want idle-unreported on the first stop", notBusy, e)
		}
		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(2*time.Second)); e != nil {
			t.Fatalf("status %q: fired again while the worker stayed in the condition: %+v", notBusy, e)
		}
		if e := ClassifyStatus(ts, "task-1", herdr.StatusWorking, nil, now.Add(3*time.Second)); e != nil {
			t.Fatalf("status %q: leaving the condition fired an event: %+v", notBusy, e)
		}
		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(4*time.Second)); e == nil || e.Kind != KindIdleUnreported {
			t.Fatalf("status %q: got %+v, want idle-unreported again once the worker left and re-entered the condition", notBusy, e)
		}
	}
}

func TestClassifyStatusWorkingToNotBusyFiresIdleUnreportedWhenLastReportWasStillWorking(t *testing.T) {
	for _, notBusy := range notBusyStatuses {
		now := time.Now()
		ts := NewTaskState(herdr.StatusWorking, now)
		ts.LastReportState = state.ReportWorking

		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(time.Second)); e == nil || e.Kind != KindIdleUnreported {
			t.Fatalf("status %q: got %+v, want idle-unreported even though a working report exists, since it doesn't explain a stop", notBusy, e)
		}
	}
}

func TestClassifyStatusWorkingToNotBusyIsAbsorbedWhenTerminalReportExplainsIt(t *testing.T) {
	for _, notBusy := range notBusyStatuses {
		for _, reportState := range []string{
			state.ReportPaused, state.ReportBlocked, state.ReportNeedsDecision, state.ReportDone, state.ReportFailed,
		} {
			now := time.Now()
			ts := NewTaskState(herdr.StatusWorking, now)
			ts.LastReportState = reportState

			if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(time.Second)); e != nil {
				t.Fatalf("status %q report state %q: got %+v, want the transition absorbed silently", notBusy, reportState, e)
			}
		}
	}
}

func TestClassifyStatusNotBusyToWorkingIsBenign(t *testing.T) {
	for _, notBusy := range notBusyStatuses {
		now := time.Now()
		ts := NewTaskState(notBusy, now)

		if e := ClassifyStatus(ts, "task-1", herdr.StatusWorking, nil, now.Add(time.Second)); e != nil {
			t.Fatalf("status %q: got %+v, want no event for resuming work", notBusy, e)
		}
	}
}

func TestClassifyStatusBlockedFiresOnceUntilResolved(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)

	e := ClassifyStatus(ts, "task-1", herdr.StatusBlocked, nil, now.Add(time.Second))
	if e == nil || e.Kind != KindBlocked || e.Text != "blocked task-1: agent needs help" {
		t.Fatalf("got %+v, want blocked event", e)
	}

	if e := ClassifyStatus(ts, "task-1", herdr.StatusBlocked, nil, now.Add(2*time.Second)); e != nil {
		t.Fatalf("repeated blocked state fired again: %+v", e)
	}

	if e := ClassifyStatus(ts, "task-1", herdr.StatusWorking, nil, now.Add(3*time.Second)); e != nil {
		t.Fatalf("leaving blocked fired an event: %+v", e)
	}

	if e := ClassifyStatus(ts, "task-1", herdr.StatusBlocked, nil, now.Add(4*time.Second)); e == nil || e.Kind != KindBlocked {
		t.Fatalf("got %+v, want blocked event to refire after resolving and re-blocking", e)
	}
}

func TestClassifyStatusProbeFailureFiresFailedOnce(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)
	probeErr := errors.New("pane not found")

	if e := ClassifyStatus(ts, "task-1", "", probeErr, now.Add(time.Second)); e == nil || e.Kind != KindFailed {
		t.Fatalf("got %+v, want failed event", e)
	}
	if e := ClassifyStatus(ts, "task-1", "", probeErr, now.Add(2*time.Second)); e != nil {
		t.Fatalf("repeated probe failure fired again: %+v", e)
	}
}

func TestClassifyStatusRecoveryAfterFailureCanFireIdleUnreported(t *testing.T) {
	for _, notBusy := range notBusyStatuses {
		now := time.Now()
		ts := NewTaskState(herdr.StatusWorking, now)
		probeErr := errors.New("pane not found")

		if e := ClassifyStatus(ts, "task-1", "", probeErr, now.Add(time.Second)); e == nil || e.Kind != KindFailed {
			t.Fatalf("status %q: got %+v, want failed event", notBusy, e)
		}
		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(2*time.Second)); e == nil || e.Kind != KindIdleUnreported {
			t.Fatalf("status %q: got %+v, want idle-unreported event on recovery", notBusy, e)
		}
	}
}

func TestClassifyStaleFiresOncePerWindow(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)
	threshold := 5 * time.Minute

	if e := ClassifyStale(ts, "task-1", now.Add(time.Minute), threshold); e != nil {
		t.Fatalf("got %+v, want no stale event before threshold", e)
	}
	if e := ClassifyStale(ts, "task-1", now.Add(6*time.Minute), threshold); e == nil || e.Kind != KindStale {
		t.Fatalf("got %+v, want stale event", e)
	}
	if e := ClassifyStale(ts, "task-1", now.Add(10*time.Minute), threshold); e != nil {
		t.Fatalf("stale event fired again in the same window: %+v", e)
	}

	ClassifyStatus(ts, "task-1", herdr.StatusDone, nil, now.Add(11*time.Minute))
	if e := ClassifyStale(ts, "task-1", now.Add(12*time.Minute), threshold); e != nil {
		t.Fatalf("stale fired right after a status change reset the window: %+v", e)
	}
	if e := ClassifyStale(ts, "task-1", now.Add(17*time.Minute), threshold); e == nil || e.Kind != KindStale {
		t.Fatalf("got %+v, want stale again once the reset window elapsed: it is a wake trigger, so leaving and re-entering the condition is a second occurrence", e)
	}
}

func TestClassifyStaleSkipsUnprobedTasks(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)
	ClassifyStatus(ts, "task-1", "", errors.New("down"), now.Add(time.Second))

	if e := ClassifyStale(ts, "task-1", now.Add(time.Hour), time.Minute); e != nil {
		t.Fatalf("got %+v, want no stale event while probe is failing", e)
	}
}

func TestClassifyPRMergedFiresOnce(t *testing.T) {
	ts := NewTaskState(herdr.StatusWorking, time.Now())

	if e := ClassifyPRMerged(ts, "task-1", false); e != nil {
		t.Fatalf("got %+v, want no event when not merged", e)
	}
	if e := ClassifyPRMerged(ts, "task-1", true); e == nil || e.Kind != KindPRMerged {
		t.Fatalf("got %+v, want pr-merged event", e)
	}
	if e := ClassifyPRMerged(ts, "task-1", true); e != nil {
		t.Fatalf("pr-merged fired again: %+v", e)
	}
}

func TestClassifyReportLineAgainstDogfoodData(t *testing.T) {
	home := t.TempDir()
	ts := NewTaskState(herdr.StatusWorking, time.Now())
	task := state.Task{ID: "task-1", Kind: state.KindShip}

	line := state.ParseReportLine("working: workflow_dispatch added to release.yaml, invoking no-mistakes")
	e := ClassifyReportLine(home, ts, task, line)
	if e == nil || e.Kind != KindReportWorking || ts.LastReportState != state.ReportWorking {
		t.Fatalf("got %+v ts=%+v, want report-working", e, ts)
	}

	line = state.ParseReportLine("needs-decision: review gate on PR for #20 raised 2 ask-user findings - (1) concurrency group release-${{ github.ref }} does not serialize manual dispatch against push-triggered runs on main, risking concurrent release-please runs; (2) dispatch replays same release-please step that already no-op'd on issue #20, may not unblock the conflicted PR without also deleting/recreating the release branch. Run parked at review gate, run id 01KYEVGV26MD8X08MZY2VXXCSR on branch 20-release-workflow-dispatch.")
	e = ClassifyReportLine(home, ts, task, line)
	if e == nil || e.Kind != KindReportNeedsDecision || ts.LastReportState != state.ReportNeedsDecision {
		t.Fatalf("got %+v ts=%+v, want report-needs-decision", e, ts)
	}

	line = state.ParseReportLine("done: PR https://github.com/atqamz/secondhand/pull/31 checks green")
	e = ClassifyReportLine(home, ts, task, line)
	if e == nil || e.Kind != KindReportDone {
		t.Fatalf("got %+v, want report-done", e)
	}
	if e.Verified {
		t.Fatalf("got Verified=true with no PR/merge recorded on the task yet, want an unverified reported-done")
	}
}

func TestClassifyReportLineMalformedIsSurfacedAndDoesNotOverwriteLastReportState(t *testing.T) {
	home := t.TempDir()
	ts := NewTaskState(herdr.StatusWorking, time.Now())
	ts.LastReportState = state.ReportBlocked
	task := state.Task{ID: "task-1", Kind: state.KindShip}

	e := ClassifyReportLine(home, ts, task, state.ParseReportLine("thinking: about to start"))
	if e == nil || e.Kind != KindReportMalformed {
		t.Fatalf("got %+v, want a malformed report surfaced, not dropped", e)
	}
	if ts.LastReportState != state.ReportBlocked {
		t.Fatalf("got LastReportState=%q, want the prior blocked report preserved", ts.LastReportState)
	}
}

func TestClassifyReportDoneVerifiedOnlyWithCompletionEvidence(t *testing.T) {
	line := state.ParseReportLine("done: all landed")

	cases := []struct {
		name       string
		task       state.Task
		scoutFound bool
		verified   bool
	}{
		{name: "ship with no PR recorded", task: state.Task{ID: "task-1", Kind: state.KindShip}},
		{name: "ship with PR recorded but not merged", task: state.Task{ID: "task-1", Kind: state.KindShip, PR: "https://github.com/a/b/pull/1"}},
		{name: "ship with PR recorded and merged", task: state.Task{ID: "task-1", Kind: state.KindShip, PR: "https://github.com/a/b/pull/1", MergeExecuted: true}, verified: true},
		{name: "ship merged locally, which leaves no PR at all", task: state.Task{ID: "task-1", Kind: state.KindShip, MergeExecuted: true}, verified: true},
		{name: "scout with no report.md", task: state.Task{ID: "task-1", Kind: state.KindScout}},
		{name: "scout with its report.md deliverable", task: state.Task{ID: "task-1", Kind: state.KindScout}, scoutFound: true, verified: true},
	}
	for _, c := range cases {
		home := t.TempDir()
		if c.scoutFound {
			writeScoutReport(t, home, c.task.ID)
		}
		ts := NewTaskState(herdr.StatusWorking, time.Now())
		e := classifyReportDone(home, ts, c.task, line)
		if e.Verified != c.verified {
			t.Errorf("%s: got Verified=%v, want %v", c.name, e.Verified, c.verified)
		}
		if ts.DoneVerified != c.verified {
			t.Errorf("%s: got ts.DoneVerified=%v, want %v", c.name, ts.DoneVerified, c.verified)
		}
	}
}

// TestClassifyDeferredDoneFiresOnceWhenEvidenceArrivesAfterTheReport covers the
// ordinary ordering: the worker reports done, the PR is merged only afterwards,
// and the done line is long consumed by the time the merge is observed.
func TestClassifyDeferredDoneFiresOnceWhenEvidenceArrivesAfterTheReport(t *testing.T) {
	home := t.TempDir()
	task := state.Task{ID: "task-1", Kind: state.KindShip, PR: "https://github.com/a/b/pull/1"}
	ts := NewTaskState(herdr.StatusWorking, time.Now())

	e := ClassifyReportLine(home, ts, task, state.ParseReportLine("done: checks green"))
	if e == nil || e.Verified {
		t.Fatalf("got %+v, want an unverified reported-done while the PR is still open", e)
	}
	if e := ClassifyDeferredDone(home, ts, task); e != nil {
		t.Fatalf("got %+v, want nothing until the merge is observed", e)
	}

	if e := ClassifyPRMerged(ts, task.ID, true); e == nil {
		t.Fatal("want a pr-merged event")
	}
	e = ClassifyDeferredDone(home, ts, task)
	if e == nil || !e.Verified || e.Kind != KindReportDone {
		t.Fatalf("got %+v, want a verified done event once the PR is merged", e)
	}
	if e.Text != "done task-1: checks green" {
		t.Fatalf("got Text=%q, want the note the report carried", e.Text)
	}
	if e := ClassifyDeferredDone(home, ts, task); e != nil {
		t.Fatalf("verified done fired again: %+v", e)
	}
}

func TestClassifyDeferredDoneVerifiesScoutFromItsOwnReport(t *testing.T) {
	home := t.TempDir()
	task := state.Task{ID: "task-1", Kind: state.KindScout}
	ts := NewTaskState(herdr.StatusWorking, time.Now())

	e := ClassifyReportLine(home, ts, task, state.ParseReportLine("done: findings written"))
	if e == nil || e.Verified {
		t.Fatalf("got %+v, want an unverified reported-done with no report.md on disk", e)
	}

	writeScoutReport(t, home, task.ID)
	e = ClassifyDeferredDone(home, ts, task)
	if e == nil || !e.Verified {
		t.Fatalf("got %+v, want a verified done once the scout's report.md exists - no PR is ever involved", e)
	}
}

func writeScoutReport(t *testing.T, home, id string) {
	t.Helper()
	dir := filepath.Join(home, "data", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte("# findings\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
