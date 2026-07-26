package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/state"
)

// Kind values classify an Event for dashboard/log routing.
const (
	// KindHerdrDone is only ever emitted by classifyReportDone once a worker's own
	// done report is cross-checked against a merged PR - ClassifyStatus never emits
	// it directly, since herdr's own done/idle split carries no task-outcome signal.
	KindHerdrDone           = "done"
	KindIdleUnreported      = "idle-unreported"
	KindBlocked             = "blocked"
	KindFailed              = "failed"
	KindStale               = "stale"
	KindPRMerged            = "pr-merged"
	KindPRNotRecorded       = "pr-not-recorded"
	KindReportWorking       = "report-working"
	KindReportPaused        = "report-paused"
	KindReportBlocked       = "report-blocked"
	KindReportNeedsDecision = "report-needs-decision"
	KindReportDone          = "report-done"
	KindReportFailed        = "report-failed"
	KindReportMalformed     = "report-malformed"
)

// blockedReason is the only detail available: herdr reports agent_status without a
// free-text cause, so this mirrors herdr's own "blocked" state description.
const blockedReason = "agent needs help"

type Event struct {
	TaskID   string
	Kind     string
	Text     string
	Reason   string
	Verified bool
}

// TaskState tracks what's already been observed and announced for one task, so the
// classifier only fires once per transition instead of once per poll tick.
type TaskState struct {
	Status       herdr.Status
	Probed       bool
	ChangedAt    time.Time
	Blocked      bool
	Stale        bool
	PRMerged     bool
	ReportOffset int64
	// PersistedOffset and PersistedPRMerged mirror what the task's durable state
	// already carries, so a write skipped for lock contention is retried on the
	// next tick instead of silently lost.
	PersistedOffset   int64
	PersistedPRMerged bool
	LastReportState   string
	// LastReportNote is kept alongside LastReportState so a done report that only
	// gains its completion evidence later can be re-announced with the same text a
	// synchronous verification would have produced.
	LastReportNote string
	// DoneVerified makes the verified-done announcement idempotent across ticks.
	DoneVerified bool
}

// NewTaskState seeds tracking for a task first observed at now, without emitting an
// event for whatever state it happens to already be in.
func NewTaskState(status herdr.Status, now time.Time) *TaskState {
	return &TaskState{Status: status, Probed: true, ChangedAt: now}
}

// ClassifyStatus compares a freshly probed status against ts and returns an
// actionable event for the transitions SPECS.md calls out (idle-unreported, blocked,
// failed). Benign transitions (into working, repeated not-busy/blocked) update ts in
// place and return nil.
//
// herdr's idle and done are the same signal for hand's purposes: the pane stopped
// being busy. Neither says anything about whether the task actually finished - see
// herdr.Status's doc comment for why hand only ever observes done, not idle, for this
// transition in practice. A transition out of working/blocked into not-busy only fires
// KindIdleUnreported when nothing has explained the stop: no report at all, or the
// last report was still "working". Any other reported state (paused, blocked,
// needs-decision, done, failed) already explains the pane going quiet, so the
// transition is absorbed silently instead of raising a false alarm.
func ClassifyStatus(ts *TaskState, id string, status herdr.Status, probeErr error, now time.Time) *Event {
	if probeErr != nil {
		wasProbed := ts.Probed
		ts.Probed = false
		if wasProbed {
			return &Event{TaskID: id, Kind: KindFailed, Text: fmt.Sprintf("failed %s", id)}
		}
		return nil
	}
	ts.Probed = true

	if status == ts.Status {
		return nil
	}
	prevStatus := ts.Status
	ts.Status = status
	ts.ChangedAt = now
	ts.Stale = false

	switch {
	case status.NotBusy():
		if prevStatus == herdr.StatusWorking || prevStatus == herdr.StatusBlocked {
			ts.Blocked = false
			if ts.LastReportState == "" || ts.LastReportState == state.ReportWorking {
				return &Event{TaskID: id, Kind: KindIdleUnreported, Text: fmt.Sprintf("idle-unreported %s", id)}
			}
		}
	case status == herdr.StatusBlocked:
		if !ts.Blocked {
			ts.Blocked = true
			return &Event{TaskID: id, Kind: KindBlocked, Text: fmt.Sprintf("blocked %s: %s", id, blockedReason), Reason: blockedReason}
		}
	default:
		ts.Blocked = false
	}
	return nil
}

// ClassifyStale fires once per stale window: when a task's status hasn't changed
// for at least threshold, and it hasn't already been flagged since the last change.
func ClassifyStale(ts *TaskState, id string, now time.Time, threshold time.Duration) *Event {
	if !ts.Probed || ts.Stale {
		return nil
	}
	if now.Sub(ts.ChangedAt) < threshold {
		return nil
	}
	ts.Stale = true
	return &Event{TaskID: id, Kind: KindStale, Text: fmt.Sprintf("stale %s", id)}
}

// ClassifyPRMerged fires once, the first time merged is observed true for a task.
func ClassifyPRMerged(ts *TaskState, id string, merged bool) *Event {
	if !merged || ts.PRMerged {
		return nil
	}
	ts.PRMerged = true
	return &Event{TaskID: id, Kind: KindPRMerged, Text: fmt.Sprintf("pr-merged %s", id)}
}

// ClassifyReportLine turns one classified report line into an event and records
// it as ts.LastReportState so a subsequent idle transition can consult it. A
// malformed line is surfaced rather than dropped, but doesn't overwrite the last
// known report state since free text alone explains nothing.
func ClassifyReportLine(home string, ts *TaskState, id string, t state.Task, line state.ReportLine) *Event {
	if line.Malformed {
		return &Event{TaskID: id, Kind: KindReportMalformed, Text: fmt.Sprintf("malformed report %s: %s", id, line.Raw), Reason: line.Raw}
	}
	ts.LastReportState = line.State
	ts.LastReportNote = line.Note

	switch line.State {
	case state.ReportWorking:
		return &Event{TaskID: id, Kind: KindReportWorking, Text: fmt.Sprintf("working %s: %s", id, line.Note), Reason: line.Note}
	case state.ReportPaused:
		return &Event{TaskID: id, Kind: KindReportPaused, Text: fmt.Sprintf("paused %s: %s", id, line.Note), Reason: line.Note}
	case state.ReportBlocked:
		return &Event{TaskID: id, Kind: KindReportBlocked, Text: fmt.Sprintf("report-blocked %s: %s", id, line.Note), Reason: line.Note}
	case state.ReportNeedsDecision:
		return &Event{TaskID: id, Kind: KindReportNeedsDecision, Text: fmt.Sprintf("needs-decision %s: %s", id, line.Note), Reason: line.Note}
	case state.ReportFailed:
		return &Event{TaskID: id, Kind: KindReportFailed, Text: fmt.Sprintf("report-failed %s: %s", id, line.Note), Reason: line.Note}
	case state.ReportDone:
		return classifyReportDone(home, ts, id, t, line)
	}
	return nil
}

// classifyReportDone never trusts a worker's own belief that it's finished: without
// independent completion evidence the event is marked unverified so dashboard/watch
// consumers surface "worker says done" without treating it as confirmed fact.
func classifyReportDone(home string, ts *TaskState, id string, t state.Task, line state.ReportLine) *Event {
	verified := doneVerified(home, ts, t)
	if verified {
		ts.DoneVerified = true
	}
	return &Event{TaskID: id, Kind: KindReportDone, Text: doneText(id, line.Note, verified), Reason: line.Note, Verified: verified}
}

// ClassifyDeferredDone covers the ordinary ordering, where a worker reports done
// before the evidence exists: the done line was already consumed and can't be
// re-read, so once evidence shows up on a later tick the verified event fires from
// the state the report left behind. Idempotent - it fires at most once per task.
func ClassifyDeferredDone(home string, ts *TaskState, t state.Task) *Event {
	if ts.DoneVerified || ts.LastReportState != state.ReportDone {
		return nil
	}
	if !doneVerified(home, ts, t) {
		return nil
	}
	ts.DoneVerified = true
	return &Event{TaskID: t.ID, Kind: KindReportDone, Text: doneText(t.ID, ts.LastReportNote, true), Reason: ts.LastReportNote, Verified: true}
}

func doneText(id, note string, verified bool) string {
	text := "reported-done"
	if verified {
		text = "done"
	}
	return fmt.Sprintf("%s %s: %s", text, id, note)
}

// doneVerified reports whether completion evidence exists that doesn't come from
// the worker itself. Each task kind has its own: a ship task's merge, and a scout
// task's data/<id>/report.md - the deliverable hand promote itself requires.
//
// The ship check deliberately asks nothing about the project's mode. t.Merged is
// only ever written after a merge actually happened, whichever route got there
// (a PR merge or a local fast-forward, which leaves no PR at all); a recorded PR
// the watcher's own poll saw merged is the same evidence arriving the other way.
func doneVerified(home string, ts *TaskState, t state.Task) bool {
	switch t.Kind {
	case state.KindShip:
		return t.Merged || (t.PR != "" && ts.PRMerged)
	case state.KindScout:
		_, err := os.Stat(filepath.Join(home, "data", t.ID, "report.md"))
		return err == nil
	}
	return false
}
