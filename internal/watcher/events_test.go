package watcher

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
)

// Covers herdr's two spellings of "pane stopped being busy" - see herdr.Status's doc for why idle
// and done must be classified identically: hand's headless polling model observes done, essentially
// always, never idle, for this transition against real herdr.
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

// Covers the case ClassifyStatus's immediate branch cannot: a task whose very first sighting has
// ts.Probed already false, so there is no "was probed" edge to fire on. This is the shape
// resumeTaskState leaves an unreachable-at-first-sighting task in.
func TestClassifyUnreachableFiresOnceAfterTheDwellForATaskFirstSeenDown(t *testing.T) {
	now := time.Now()
	threshold := 5 * time.Minute
	ts := NewTaskState(herdr.StatusUnknown, now)
	ts.Probed = false

	if e := ClassifyUnreachable(ts, "task-1", now.Add(time.Minute), threshold); e != nil {
		t.Fatalf("got %+v, want no event before the dwell matures: this is what makes a blink silent", e)
	}
	if e := ClassifyUnreachable(ts, "task-1", now.Add(6*time.Minute), threshold); e == nil || e.Kind != KindFailed {
		t.Fatalf("got %+v, want a failed event once the outage outlasts the dwell", e)
	}
	if e := ClassifyUnreachable(ts, "task-1", now.Add(10*time.Minute), threshold); e != nil {
		t.Fatalf("failed event fired again for the same outage: %+v", e)
	}
}

func TestClassifyUnreachableStaysSilentOnABlink(t *testing.T) {
	now := time.Now()
	threshold := 5 * time.Minute
	ts := NewTaskState(herdr.StatusUnknown, now)
	ts.Probed = false

	if e := ClassifyUnreachable(ts, "task-1", now.Add(time.Minute), threshold); e != nil {
		t.Fatalf("got %+v, want no event before the dwell matures", e)
	}
	if e := ClassifyStatus(ts, "task-1", herdr.StatusWorking, nil, now.Add(2*time.Minute)); e != nil {
		t.Fatalf("recovery from a first-sighting outage fired an event: %+v", e)
	}
	if e := ClassifyUnreachable(ts, "task-1", now.Add(10*time.Minute), threshold); e != nil {
		t.Fatalf("got %+v, want no failed event: the pane recovered before the dwell matured", e)
	}
}

func TestClassifyUnreachableRefiresOnANewOutageAfterRecovery(t *testing.T) {
	now := time.Now()
	threshold := 5 * time.Minute
	ts := NewTaskState(herdr.StatusUnknown, now)
	ts.Probed = false

	if e := ClassifyUnreachable(ts, "task-1", now.Add(6*time.Minute), threshold); e == nil || e.Kind != KindFailed {
		t.Fatalf("got %+v, want a failed event for the first outage", e)
	}
	if e := ClassifyStatus(ts, "task-1", herdr.StatusWorking, nil, now.Add(7*time.Minute)); e != nil {
		t.Fatalf("recovery fired an event: %+v", e)
	}

	probeErr := errors.New("pane not found")
	if e := ClassifyStatus(ts, "task-1", "", probeErr, now.Add(8*time.Minute)); e == nil || e.Kind != KindFailed {
		t.Fatalf("got %+v, want ClassifyStatus's immediate branch to fire for a known-good task going dark", e)
	}
	if e := ClassifyUnreachable(ts, "task-1", now.Add(20*time.Minute), threshold); e != nil {
		t.Fatalf("got %+v, want no duplicate: ClassifyStatus's immediate branch already claimed this outage", e)
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

func TestClassifyParkedBoundsDoneAndFailedInsteadOfExemptingThem(t *testing.T) {
	now := time.Now()
	bounds := ParkedBounds{Paused: time.Hour, Done: 90 * time.Minute, Other: 20 * time.Minute}

	withinBound := now.Add(-time.Hour)
	doneTs := NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(doneTs, "task-1", state.ReportDone, "done: shipped", withinBound, now, bounds); e != nil {
		t.Fatalf("got %+v, want no parked event while a done worker's silence is still under the done bound", e)
	}
	failedTs := NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(failedTs, "task-1", state.ReportFailed, "failed: build broke", withinBound, now, bounds); e != nil {
		t.Fatalf("got %+v, want no parked event while a failed worker's silence is still under the done bound", e)
	}

	beyondBound := now.Add(-2 * time.Hour)
	doneTs = NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(doneTs, "task-1", state.ReportDone, "done: shipped", beyondBound, now, bounds); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want a parked event once a done worker's silence exceeds the done bound: it may still be attached to a pane and steerable", e)
	}
	failedTs = NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(failedTs, "task-1", state.ReportFailed, "failed: build broke", beyondBound, now, bounds); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want a parked event once a failed worker's silence exceeds the done bound", e)
	}
}

func TestClassifyParkedExemptsDoneAndFailedWhenTheDoneBoundIsUnconfigured(t *testing.T) {
	now := time.Now()
	bounds := ParkedBounds{Paused: time.Hour, Other: 20 * time.Minute}
	old := now.Add(-24 * time.Hour)

	ts := NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(ts, "task-1", state.ReportDone, "done: shipped", old, now, bounds); e != nil {
		t.Fatalf("got %+v, want no parked event: a non-positive bound means unconfigured, not zero-tolerance", e)
	}
}

func TestClassifyParkedSelectsBoundByLastReport(t *testing.T) {
	now := time.Now()
	bounds := ParkedBounds{Paused: time.Hour, Other: 20 * time.Minute}

	pausedTs := NewTaskState(herdr.StatusIdle, now)
	silentFor30m := now.Add(-30 * time.Minute)
	if e := ClassifyParked(pausedTs, "task-1", state.ReportPaused, "paused: waiting on review", silentFor30m, now, bounds); e != nil {
		t.Fatalf("got %+v, want no parked event: 30m silence is still under the paused bound", e)
	}

	workingTs := NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(workingTs, "task-1", state.ReportWorking, "working: on it", silentFor30m, now, bounds); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want parked event: 30m silence exceeds the shorter default bound", e)
	}

	unreportedTs := NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(unreportedTs, "task-1", "", "no report", silentFor30m, now, bounds); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want parked event: a task that never reported gets the short bound too", e)
	}
}

func TestClassifyParkedFiresOncePerEpisodeAndResetsOnGrowth(t *testing.T) {
	now := time.Now()
	bounds := ParkedBounds{Paused: time.Hour, Other: 20 * time.Minute}
	ts := NewTaskState(herdr.StatusIdle, now)
	mtime := now.Add(-30 * time.Minute)

	if e := ClassifyParked(ts, "task-1", state.ReportWorking, "working: on it", mtime, now, bounds); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want parked event on first crossing", e)
	}
	if e := ClassifyParked(ts, "task-1", state.ReportWorking, "working: on it", mtime, now.Add(10*time.Minute), bounds); e != nil {
		t.Fatalf("parked fired again for the same episode: %+v", e)
	}

	grown := now.Add(-time.Minute)
	if e := ClassifyParked(ts, "task-1", state.ReportWorking, "working: on it", grown, now.Add(11*time.Minute), bounds); e != nil {
		t.Fatalf("got %+v, want no parked event right after the report file grows", e)
	}
	if e := ClassifyParked(ts, "task-1", state.ReportWorking, "working: on it", grown, now.Add(45*time.Minute), bounds); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want a second parked event once the new episode crosses the bound", e)
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

	line = state.ParseReportLine("done: PR https://github.com/atqamz/hand/pull/31 checks green")
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

// Covers the ordinary ordering: the worker reports done, the PR is merged only afterwards, and the
// done line is long consumed by the time the merge is observed.
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
