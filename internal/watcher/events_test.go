package watcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/orientation"
	"github.com/atqamz/hand/internal/state"
)

func TestEventWakeCarriesFleetAndOpaqueExactTarget(t *testing.T) {
	provider := orientation.NewProvider(func(context.Context) (orientation.Evidence, error) {
		return orientation.Evidence{
			FleetID: "f_one",
			Targets: []orientation.TargetEvidence{{ID: "task-1", Kind: "task", Generation: []string{"attempt-1"}}},
		}, nil
	})
	oriented, err := provider.Orientation(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	event := Event{TaskID: "task-1", Kind: KindBlocked, Reason: strings.Repeat("reason ", 100)}
	event.FleetID = oriented.FleetID
	event.Target = oriented.Monitors[0]
	wake := event.Wake()
	if wake.FleetID != "f_one" || wake.TargetID != oriented.Monitors[0].ID || wake.Currentness != oriented.Monitors[0].Currentness {
		t.Fatalf("wake = %#v, want exact Fleet target and token", wake)
	}
	if len([]rune(wake.Reason)) > MaxWakeReason {
		t.Fatalf("reason length = %d, want <= %d", len([]rune(wake.Reason)), MaxWakeReason)
	}
	if !wake.Current(oriented) {
		t.Fatal("fresh wake rejected by matching orientation")
	}

	changed := orientation.Build(orientation.Evidence{
		FleetID: "f_one",
		Targets: []orientation.TargetEvidence{{ID: "task-1", Kind: "task", Generation: []string{"attempt-2"}}},
	})
	if wake.Current(changed) {
		t.Fatal("stale wake accepted after target generation changed")
	}
}

// Covers herdr's two spellings of "pane stopped being busy" - see herdr.Status's doc for why idle
// and done must be classified identically: hand's headless polling model observes done, essentially
// always, never idle, for this transition against real herdr.
var notBusyStatuses = []herdr.Status{herdr.StatusIdle, herdr.StatusDone}

func TestClassifyStatusWorkingToNotBusyFiresIdleUnreportedWhenNoTerminalReport(t *testing.T) {
	for _, notBusy := range notBusyStatuses {
		now := time.Now()
		ts := NewTaskState(herdr.StatusWorking, now)

		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(time.Second), ""); e == nil || e.Kind != KindIdleUnreported {
			t.Fatalf("status %q: got %+v, want idle-unreported event when nothing explained the stop", notBusy, e)
		}
		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(2*time.Second), ""); e != nil {
			t.Fatalf("status %q: repeated not-busy state fired again: %+v", notBusy, e)
		}
	}
}

func TestClassifyStatusIdleUnreportedRefiresOnlyAfterTheWorkerResumesAndStopsAgain(t *testing.T) {
	for _, notBusy := range notBusyStatuses {
		now := time.Now()
		ts := NewTaskState(herdr.StatusWorking, now)

		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(time.Second), ""); e == nil || e.Kind != KindIdleUnreported {
			t.Fatalf("status %q: got %+v, want idle-unreported on the first stop", notBusy, e)
		}
		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(2*time.Second), ""); e != nil {
			t.Fatalf("status %q: fired again while the worker stayed in the condition: %+v", notBusy, e)
		}
		if e := ClassifyStatus(ts, "task-1", herdr.StatusWorking, nil, now.Add(3*time.Second), ""); e != nil {
			t.Fatalf("status %q: leaving the condition fired an event: %+v", notBusy, e)
		}
		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(4*time.Second), ""); e == nil || e.Kind != KindIdleUnreported {
			t.Fatalf("status %q: got %+v, want idle-unreported again once the worker left and re-entered the condition", notBusy, e)
		}
	}
}

func TestClassifyStatusWorkingToNotBusyFiresIdleUnreportedWhenLastReportWasStillWorking(t *testing.T) {
	for _, notBusy := range notBusyStatuses {
		now := time.Now()
		ts := NewTaskState(herdr.StatusWorking, now)
		ts.LastReportState = state.ReportWorking

		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(time.Second), ""); e == nil || e.Kind != KindIdleUnreported {
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

			if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(time.Second), ""); e != nil {
				t.Fatalf("status %q report state %q: got %+v, want the transition absorbed silently", notBusy, reportState, e)
			}
		}
	}
}

func TestClassifyStatusNotBusyToWorkingIsBenign(t *testing.T) {
	for _, notBusy := range notBusyStatuses {
		now := time.Now()
		ts := NewTaskState(notBusy, now)

		if e := ClassifyStatus(ts, "task-1", herdr.StatusWorking, nil, now.Add(time.Second), ""); e != nil {
			t.Fatalf("status %q: got %+v, want no event for resuming work", notBusy, e)
		}
	}
}

func TestClassifyStatusBlockedFiresOnceUntilResolved(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)

	e := ClassifyStatus(ts, "task-1", herdr.StatusBlocked, nil, now.Add(time.Second), "")
	if e == nil || e.Kind != KindBlocked || e.Text != "blocked task-1: agent needs help" {
		t.Fatalf("got %+v, want blocked event", e)
	}

	if e := ClassifyStatus(ts, "task-1", herdr.StatusBlocked, nil, now.Add(2*time.Second), ""); e != nil {
		t.Fatalf("repeated blocked state fired again: %+v", e)
	}

	if e := ClassifyStatus(ts, "task-1", herdr.StatusWorking, nil, now.Add(3*time.Second), ""); e != nil {
		t.Fatalf("leaving blocked fired an event: %+v", e)
	}

	if e := ClassifyStatus(ts, "task-1", herdr.StatusBlocked, nil, now.Add(4*time.Second), ""); e == nil || e.Kind != KindBlocked {
		t.Fatalf("got %+v, want blocked event to refire after resolving and re-blocking", e)
	}
}

func TestClassifyStatusProbeFailureFiresFailedOnce(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)
	probeErr := errors.New("pane not found")

	if e := ClassifyStatus(ts, "task-1", "", probeErr, now.Add(time.Second), ""); e == nil || e.Kind != KindFailed {
		t.Fatalf("got %+v, want failed event", e)
	}
	if e := ClassifyStatus(ts, "task-1", "", probeErr, now.Add(2*time.Second), ""); e != nil {
		t.Fatalf("repeated probe failure fired again: %+v", e)
	}
}

func TestClassifyStatusSuppressesFailedWhenTeardownClaimedTheHerdrResource(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)
	probeErr := errors.New("pane not found")

	if e := ClassifyStatus(ts, "task-1", "", probeErr, now.Add(time.Second), state.TeardownResourceReleasing); e != nil {
		t.Fatalf("got %+v, want no event: teardown's own release explains the pane going unreachable", e)
	}
	if e := ClassifyStatus(ts, "task-1", "", probeErr, now.Add(2*time.Second), state.TeardownResourceReleased); e != nil {
		t.Fatalf("got %+v, want no event on a later tick either, once teardown has finished releasing", e)
	}
}

func TestClassifyStatusRecoveryAfterFailureCanFireIdleUnreported(t *testing.T) {
	for _, notBusy := range notBusyStatuses {
		now := time.Now()
		ts := NewTaskState(herdr.StatusWorking, now)
		probeErr := errors.New("pane not found")

		if e := ClassifyStatus(ts, "task-1", "", probeErr, now.Add(time.Second), ""); e == nil || e.Kind != KindFailed {
			t.Fatalf("status %q: got %+v, want failed event", notBusy, e)
		}
		if e := ClassifyStatus(ts, "task-1", notBusy, nil, now.Add(2*time.Second), ""); e == nil || e.Kind != KindIdleUnreported {
			t.Fatalf("status %q: got %+v, want idle-unreported event on recovery", notBusy, e)
		}
	}
}

func TestClassifyCatchUpFiresIdleUnreportedForAStopThatPredatesTheWatcher(t *testing.T) {
	for _, notBusy := range notBusyStatuses {
		for _, lastReport := range []string{"", state.ReportWorking} {
			ts := NewTaskState(notBusy, time.Now())
			ts.LastReportState = lastReport

			e := ClassifyCatchUp(ts, "task-1", notBusy, "working", "")
			if e == nil || e.Kind != KindIdleUnreported || e.Text != "idle-unreported task-1" {
				t.Fatalf("status %q, last report %q: got %+v, want idle-unreported", notBusy, lastReport, e)
			}
		}
	}
}

func TestClassifyCatchUpStaysSilentOnAnEpisodeDurableStateAlreadyNames(t *testing.T) {
	for _, notBusy := range notBusyStatuses {
		ts := NewTaskState(notBusy, time.Now())

		if e := ClassifyCatchUp(ts, "task-1", notBusy, string(notBusy), ""); e != nil {
			t.Fatalf("status %q: got %+v, want silence: some watcher observed and announced this episode", notBusy, e)
		}
	}
}

// A stop the worker itself explained needs no wake here, exactly as ClassifyStatus's own idle edge
// absorbs it: the report line was the news, and it has its own event.
func TestClassifyCatchUpAbsorbsAStopAReportExplains(t *testing.T) {
	for _, explained := range []string{state.ReportDone, state.ReportFailed, state.ReportBlocked, state.ReportNeedsDecision, state.ReportPaused} {
		ts := NewTaskState(herdr.StatusDone, time.Now())
		ts.LastReportState = explained

		if e := ClassifyCatchUp(ts, "task-1", herdr.StatusDone, "working", ""); e != nil {
			t.Fatalf("last report %q: got %+v, want the stop absorbed", explained, e)
		}
	}
}

func TestClassifyCatchUpFiresBlockedForAPaneAlreadyBlockedAtArm(t *testing.T) {
	ts := NewTaskState(herdr.StatusBlocked, time.Now())

	e := ClassifyCatchUp(ts, "task-1", herdr.StatusBlocked, "working", "")
	if e == nil || e.Kind != KindBlocked || e.Text != "blocked task-1: agent needs help" {
		t.Fatalf("got %+v, want blocked event", e)
	}
	if !ts.Blocked {
		t.Fatal("ts.Blocked = false, want the episode claimed so the next tick does not announce it again")
	}
	if e := ClassifyStatus(ts, "task-1", herdr.StatusBlocked, nil, time.Now(), ""); e != nil {
		t.Fatalf("got %+v, want ClassifyStatus to find the caught-up blocked episode already claimed", e)
	}
}

// atqamz/hand#252 must not reopen atqamz/hand#235: a pane hand teardown is releasing is nobody's
// condition to act on, whichever classifier meets it first.
func TestClassifyCatchUpSuppressesEveryConditionWhileTeardownHoldsTheHerdrResource(t *testing.T) {
	for _, releasing := range []string{state.TeardownResourceReleasing, state.TeardownResourceReleased} {
		for _, status := range []herdr.Status{herdr.StatusDone, herdr.StatusIdle, herdr.StatusBlocked} {
			ts := NewTaskState(status, time.Now())

			if e := ClassifyCatchUp(ts, "task-1", status, "working", releasing); e != nil {
				t.Fatalf("status %q, teardown %q: got %+v, want no wake", status, releasing, e)
			}
		}
	}
}

// The status this watcher has itself seen change is ClassifyStatus's edge, and announcing both would
// wake the supervisor twice for one stop.
func TestClassifyCatchUpLeavesAnObservedTransitionToClassifyStatus(t *testing.T) {
	ts := NewTaskState(herdr.StatusWorking, time.Now())

	if e := ClassifyCatchUp(ts, "task-1", herdr.StatusDone, "working", ""); e != nil {
		t.Fatalf("got %+v, want silence while ts still holds the status it was seeded with", e)
	}
	if e := ClassifyStatus(ts, "task-1", herdr.StatusDone, nil, time.Now(), ""); e == nil || e.Kind != KindIdleUnreported {
		t.Fatalf("got %+v, want the transition announced exactly once, by ClassifyStatus", e)
	}
}

func TestCatchUpFilterCarriesEveryKindWhoseAnnouncementIsDurable(t *testing.T) {
	f := CatchUpFilter()

	if f.Matches(KindStale) {
		t.Fatal("stale is deliverable at arm, so every re-arm against an unchanged status returns at once")
	}
	for _, kind := range KnownKinds() {
		if kind == KindStale {
			continue
		}
		if !f.Matches(kind) {
			t.Fatalf("kind %q is not deliverable at arm, so a condition that arrived while no watcher was alive is lost", kind)
		}
	}
}

func TestClassifyStaleFiresOncePerWindow(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)
	threshold := 5 * time.Minute

	if e := ClassifyStale(ts, "task-1", "", now.Add(time.Minute), threshold); e != nil {
		t.Fatalf("got %+v, want no stale event before threshold", e)
	}
	if e := ClassifyStale(ts, "task-1", "", now.Add(6*time.Minute), threshold); e == nil || e.Kind != KindStale {
		t.Fatalf("got %+v, want stale event", e)
	}
	if e := ClassifyStale(ts, "task-1", "", now.Add(10*time.Minute), threshold); e != nil {
		t.Fatalf("stale event fired again in the same window: %+v", e)
	}

	ClassifyStatus(ts, "task-1", herdr.StatusDone, nil, now.Add(11*time.Minute), "")
	if e := ClassifyStale(ts, "task-1", "", now.Add(12*time.Minute), threshold); e != nil {
		t.Fatalf("stale fired right after a status change reset the window: %+v", e)
	}
	if e := ClassifyStale(ts, "task-1", "", now.Add(17*time.Minute), threshold); e == nil || e.Kind != KindStale {
		t.Fatalf("got %+v, want stale again once the reset window elapsed: it is a wake trigger, so leaving and re-entering the condition is a second occurrence", e)
	}
}

func TestClassifyStaleSuppressesTerminalWorkerReports(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	threshold := 5 * time.Minute

	for _, reportState := range []string{state.ReportDone, state.ReportFailed} {
		ts := NewTaskState(herdr.StatusWorking, now.Add(-threshold))
		ts.LastReportState = reportState

		if e := ClassifyStale(ts, "task-1", "", now, threshold); e != nil {
			t.Fatalf("report state %q: got %+v, want no stale event", reportState, e)
		}
		if ts.Stale {
			t.Fatalf("report state %q: stale latch changed without an event", reportState)
		}
	}
}

func TestClassifyStaleSuppressesDeliveredTaskWithoutTerminalReport(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	threshold := 5 * time.Minute
	ts := NewTaskState(herdr.StatusWorking, now.Add(-threshold))
	ts.LastReportState = state.ReportWorking

	if e := ClassifyStale(ts, "task-1", "2026-08-17T00:00:00Z", now, threshold); e != nil {
		t.Fatalf("got %+v, want no stale event for delivered work", e)
	}
	if ts.Stale {
		t.Fatal("stale latch changed without an event for delivered work")
	}
}

func TestClassifyStaleKeepsNonTerminalReportsEligible(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	threshold := 5 * time.Minute

	for _, reportState := range []string{"", state.ReportWorking, state.ReportPaused, state.ReportBlocked, state.ReportNeedsDecision} {
		ts := NewTaskState(herdr.StatusWorking, now.Add(-threshold))
		ts.LastReportState = reportState

		if e := ClassifyStale(ts, "task-1", "", now, threshold); e == nil || e.Kind != KindStale {
			t.Fatalf("report state %q: got %+v, want stale event", reportState, e)
		}
	}
}

func TestClassifyStaleTreatsHerdrDoneAsNotBusyOnly(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	threshold := 5 * time.Minute
	ts := NewTaskState(herdr.StatusDone, now.Add(-threshold))

	if e := ClassifyStale(ts, "task-1", "", now, threshold); e == nil || e.Kind != KindStale {
		t.Fatalf("got %+v, want stale event for unexplained herdr done", e)
	}
}

func TestClassifyStaleSkipsUnprobedTasks(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)
	ClassifyStatus(ts, "task-1", "", errors.New("down"), now.Add(time.Second), "")

	if e := ClassifyStale(ts, "task-1", "", now.Add(time.Hour), time.Minute); e != nil {
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

	if e := ClassifyUnreachable(ts, "task-1", now.Add(time.Minute), threshold, ""); e != nil {
		t.Fatalf("got %+v, want no event before the dwell matures: this is what makes a blink silent", e)
	}
	if e := ClassifyUnreachable(ts, "task-1", now.Add(6*time.Minute), threshold, ""); e == nil || e.Kind != KindFailed {
		t.Fatalf("got %+v, want a failed event once the outage outlasts the dwell", e)
	}
	if e := ClassifyUnreachable(ts, "task-1", now.Add(10*time.Minute), threshold, ""); e != nil {
		t.Fatalf("failed event fired again for the same outage: %+v", e)
	}
}

func TestClassifyUnreachableSuppressesFailedWhenTeardownClaimedTheHerdrResource(t *testing.T) {
	now := time.Now()
	threshold := 5 * time.Minute
	ts := NewTaskState(herdr.StatusUnknown, now)
	ts.Probed = false

	if e := ClassifyUnreachable(ts, "task-1", now.Add(6*time.Minute), threshold, state.TeardownResourceReleasing); e != nil {
		t.Fatalf("got %+v, want no event past the dwell either: teardown's own release explains the outage", e)
	}
}

func TestClassifyUnreachableStaysSilentOnABlink(t *testing.T) {
	now := time.Now()
	threshold := 5 * time.Minute
	ts := NewTaskState(herdr.StatusUnknown, now)
	ts.Probed = false

	if e := ClassifyUnreachable(ts, "task-1", now.Add(time.Minute), threshold, ""); e != nil {
		t.Fatalf("got %+v, want no event before the dwell matures", e)
	}
	if e := ClassifyStatus(ts, "task-1", herdr.StatusWorking, nil, now.Add(2*time.Minute), ""); e != nil {
		t.Fatalf("recovery from a first-sighting outage fired an event: %+v", e)
	}
	if e := ClassifyUnreachable(ts, "task-1", now.Add(10*time.Minute), threshold, ""); e != nil {
		t.Fatalf("got %+v, want no failed event: the pane recovered before the dwell matured", e)
	}
}

func TestClassifyUnreachableRefiresOnANewOutageAfterRecovery(t *testing.T) {
	now := time.Now()
	threshold := 5 * time.Minute
	ts := NewTaskState(herdr.StatusUnknown, now)
	ts.Probed = false

	if e := ClassifyUnreachable(ts, "task-1", now.Add(6*time.Minute), threshold, ""); e == nil || e.Kind != KindFailed {
		t.Fatalf("got %+v, want a failed event for the first outage", e)
	}
	if e := ClassifyStatus(ts, "task-1", herdr.StatusWorking, nil, now.Add(7*time.Minute), ""); e != nil {
		t.Fatalf("recovery fired an event: %+v", e)
	}

	probeErr := errors.New("pane not found")
	if e := ClassifyStatus(ts, "task-1", "", probeErr, now.Add(8*time.Minute), ""); e == nil || e.Kind != KindFailed {
		t.Fatalf("got %+v, want ClassifyStatus's immediate branch to fire for a known-good task going dark", e)
	}
	if e := ClassifyUnreachable(ts, "task-1", now.Add(20*time.Minute), threshold, ""); e != nil {
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

// atqamz/hand#268's disagreement 1: a PR behind no completed gate run was attention in hand status
// and silence in hand watch, because nothing here ever asked the question. GateKind is the same
// mapping cmd/statusview.go's taskFlags renders, so the two can never drift on what the tokens mean.
func TestGateKindMatchesTheTokensStatusRenders(t *testing.T) {
	if kind, ok := GateKind(ghutil.ObservationAbsent); !ok || kind != KindGateAbsent {
		t.Fatalf("GateKind(absent) = %q, %t, want %q, true", kind, ok, KindGateAbsent)
	}
	if kind, ok := GateKind(ghutil.ObservationUnknown); !ok || kind != KindGateUnknown {
		t.Fatalf("GateKind(unknown) = %q, %t, want %q, true", kind, ok, KindGateUnknown)
	}
	if _, ok := GateKind(ghutil.ObservationFound); ok {
		t.Fatal("GateKind(found) reported a problem, want none: a found run is not attention")
	}
	if _, ok := GateKind(""); ok {
		t.Fatal("GateKind(\"\") reported a problem, want none: an empty observation means the check never applied")
	}
}

func TestClassifyGateProblemFiresOnceThenResetsWhenApplicabilityLapses(t *testing.T) {
	ts := NewTaskState(herdr.StatusWorking, time.Now())

	if e := ClassifyGateProblem(ts, "task-1", false, ghutil.ObservationAbsent); e != nil {
		t.Fatalf("got %+v, want no event while the check does not apply", e)
	}
	if e := ClassifyGateProblem(ts, "task-1", true, ghutil.ObservationFound); e != nil {
		t.Fatalf("got %+v, want no event for a found run", e)
	}
	if e := ClassifyGateProblem(ts, "task-1", true, ghutil.ObservationAbsent); e == nil || e.Kind != KindGateAbsent {
		t.Fatalf("got %+v, want a gate-absent event", e)
	}
	if e := ClassifyGateProblem(ts, "task-1", true, ghutil.ObservationAbsent); e != nil {
		t.Fatalf("gate-absent fired again in the same episode: %+v", e)
	}
	if e := ClassifyGateProblem(ts, "task-1", false, ""); e != nil {
		t.Fatalf("got %+v, want no event once the check stops applying", e)
	}
	if e := ClassifyGateProblem(ts, "task-1", true, ghutil.ObservationUnknown); e == nil || e.Kind != KindGateUnknown {
		t.Fatalf("got %+v, want gate-unknown to fire once more: the earlier episode ended when applicability lapsed", e)
	}
}

func TestClassifyParkedBoundsDoneAndFailedInsteadOfExemptingThem(t *testing.T) {
	now := time.Now()
	bounds := ParkedBounds{Paused: time.Hour, Done: 90 * time.Minute, Other: 20 * time.Minute}

	withinBound := now.Add(-time.Hour)
	doneTs := NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(doneTs, "task-1", state.ReportDone, "done: shipped", "", withinBound, now, bounds, nil, ""); e != nil {
		t.Fatalf("got %+v, want no parked event while a done worker's silence is still under the done bound", e)
	}
	failedTs := NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(failedTs, "task-1", state.ReportFailed, "failed: build broke", "", withinBound, now, bounds, nil, ""); e != nil {
		t.Fatalf("got %+v, want no parked event while a failed worker's silence is still under the done bound", e)
	}

	beyondBound := now.Add(-2 * time.Hour)
	doneTs = NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(doneTs, "task-1", state.ReportDone, "done: shipped", "", beyondBound, now, bounds, nil, ""); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want a parked event once a done worker's silence exceeds the done bound: it may still be attached to a pane and steerable", e)
	}
	failedTs = NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(failedTs, "task-1", state.ReportFailed, "failed: build broke", "", beyondBound, now, bounds, nil, ""); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want a parked event once a failed worker's silence exceeds the done bound", e)
	}
}

func TestClassifyParkedExemptsDoneAndFailedWhenTheDoneBoundIsUnconfigured(t *testing.T) {
	now := time.Now()
	bounds := ParkedBounds{Paused: time.Hour, Other: 20 * time.Minute}
	old := now.Add(-24 * time.Hour)

	ts := NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(ts, "task-1", state.ReportDone, "done: shipped", "", old, now, bounds, nil, ""); e != nil {
		t.Fatalf("got %+v, want no parked event: a non-positive bound means unconfigured, not zero-tolerance", e)
	}
}

func TestClassifyParkedSelectsBoundByLastReport(t *testing.T) {
	now := time.Now()
	bounds := ParkedBounds{Paused: time.Hour, Other: 20 * time.Minute}

	pausedTs := NewTaskState(herdr.StatusIdle, now)
	silentFor30m := now.Add(-30 * time.Minute)
	if e := ClassifyParked(pausedTs, "task-1", state.ReportPaused, "paused: waiting on review", "", silentFor30m, now, bounds, nil, ""); e != nil {
		t.Fatalf("got %+v, want no parked event: 30m silence is still under the paused bound", e)
	}

	workingTs := NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(workingTs, "task-1", state.ReportWorking, "working: on it", "", silentFor30m, now, bounds, nil, ""); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want parked event: 30m silence exceeds the shorter default bound", e)
	}

	unreportedTs := NewTaskState(herdr.StatusIdle, now)
	if e := ClassifyParked(unreportedTs, "task-1", "", "no report", "", silentFor30m, now, bounds, nil, ""); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want parked event: a task that never reported gets the short bound too", e)
	}
}

func TestClassifyParkedFiresOncePerEpisodeAndResetsOnGrowth(t *testing.T) {
	now := time.Now()
	bounds := ParkedBounds{Paused: time.Hour, Other: 20 * time.Minute}
	ts := NewTaskState(herdr.StatusIdle, now)
	mtime := now.Add(-30 * time.Minute)

	if e := ClassifyParked(ts, "task-1", state.ReportWorking, "working: on it", "", mtime, now, bounds, nil, ""); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want parked event on first crossing", e)
	}
	if e := ClassifyParked(ts, "task-1", state.ReportWorking, "working: on it", "", mtime, now.Add(10*time.Minute), bounds, nil, ""); e != nil {
		t.Fatalf("parked fired again for the same episode: %+v", e)
	}

	grown := now.Add(-time.Minute)
	if e := ClassifyParked(ts, "task-1", state.ReportWorking, "working: on it", "", grown, now.Add(11*time.Minute), bounds, nil, ""); e != nil {
		t.Fatalf("got %+v, want no parked event right after the report file grows", e)
	}
	if e := ClassifyParked(ts, "task-1", state.ReportWorking, "working: on it", "", grown, now.Add(45*time.Minute), bounds, nil, ""); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want a second parked event once the new episode crosses the bound", e)
	}
}

// atqamz/hand#492: the durable read-back of the same latch
// TestClassifyParkedFiresOncePerEpisodeAndResetsOnGrowth exercises from the in-memory side.
func TestAlreadyAnnounced(t *testing.T) {
	silentSince := time.Now().Add(-30 * time.Minute)
	if AlreadyAnnounced(state.Attempt{}, silentSince) {
		t.Fatal("an attempt with no recorded latch is already announced")
	}
	announced := state.Attempt{ParkedFiredFor: silentSince.UTC().Format(time.RFC3339Nano)}
	if !AlreadyAnnounced(announced, silentSince) {
		t.Fatal("an attempt latched for this exact silence instant is not already announced")
	}
	stale := state.Attempt{ParkedFiredFor: silentSince.Add(-time.Hour).UTC().Format(time.RFC3339Nano)}
	if AlreadyAnnounced(stale, silentSince) {
		t.Fatal("a latch recorded for a different, earlier episode reads as already announced")
	}
	if AlreadyAnnounced(state.Attempt{ParkedFiredFor: "not-a-time"}, silentSince) {
		t.Fatal("an unparseable latch reads as already announced")
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

func TestClassifyParkedWithdrawsAndStaysAnnounceableWhileThePaneIsLive(t *testing.T) {
	now := time.Now()
	bounds := ParkedBounds{Paused: time.Hour, Other: 20 * time.Minute}
	first := NewTaskState(herdr.StatusWorking, now)
	firstReader := &fakePaneReader{reads: []string{"waiting for input"}}
	if e := ClassifyParked(first, "task-1", state.ReportWorking, "working: on it", "", now.Add(-30*time.Minute), now, bounds, firstReader, "wA:pB"); e != nil || !first.PaneSampleObserved || !first.ParkedFiredFor.IsZero() {
		t.Fatalf("first observation got event=%+v sample=%v latch=%v, want sample only", e, first.PaneSampleObserved, first.ParkedFiredFor)
	}
	ts := NewTaskState(herdr.StatusWorking, now)
	mtime := now.Add(-30 * time.Minute)

	ts.PaneSample = "esc to interrupt (12s)"
	ts.PaneSampleObserved = true
	live := &fakePaneReader{reads: []string{"esc to interrupt (15s)"}}
	if e := ClassifyParked(ts, "task-1", state.ReportWorking, "working: on it", "", mtime, now, bounds, live, "wA:pB"); e != nil {
		t.Fatalf("got %+v, want no parked event while the pane is still printing", e)
	}
	if !ts.ParkedFiredFor.IsZero() {
		t.Fatal("a withdrawn verdict consumed the episode's latch: the same silence outlasting the activity would then never be announced")
	}

	ts.PaneSample = "waiting for input"
	still := &fakePaneReader{reads: []string{"waiting for input"}}
	if e := ClassifyParked(ts, "task-1", state.ReportWorking, "working: on it", "", mtime, now.Add(time.Minute), bounds, still, "wA:pB"); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want a parked event once the pane goes quiet inside the same silence episode", e)
	}
}

func TestClassifyParkedExemptsADeliveredTask(t *testing.T) {
	now := time.Now()
	bounds := ParkedBounds{Paused: time.Hour, Other: 20 * time.Minute}
	ts := NewTaskState(herdr.StatusIdle, now)
	silentFor30m := now.Add(-30 * time.Minute)

	if e := ClassifyParked(ts, "task-1", state.ReportWorking, "working: on it", "2026-08-17T00:00:00Z", silentFor30m, now, bounds, nil, ""); e != nil {
		t.Fatalf("got %+v, want no parked event: delivery is a stronger nothing-more-to-do signal than the worker's own silence", e)
	}
	if !ts.ParkedFiredFor.IsZero() {
		t.Fatal("an exempt task consumed the episode latch")
	}
	if e := ClassifyParked(ts, "task-1", state.ReportWorking, "working: on it", "", silentFor30m, now, bounds, nil, ""); e == nil || e.Kind != KindParked {
		t.Fatalf("got %+v, want the same silence still parked while the task is undelivered", e)
	}
}

func TestGateAppliesStopsOnceTheTaskIsDelivered(t *testing.T) {
	pr := "https://github.com/atqamz/hand/pull/120"

	if !GateApplies(state.KindShip, pr, "", true) {
		t.Fatal("a done ship task with a recorded PR was skipped, want the gate check to apply")
	}
	if GateApplies(state.KindShip, pr, "2026-08-17T00:00:00Z", true) {
		t.Fatal("the gate check still applied after delivery, want it silent: it has nothing further to say once the supervisor has handed the task off")
	}
}
