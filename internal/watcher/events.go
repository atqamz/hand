package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/state"
)

// Kind values classify an Event for dashboard/log routing.
//
// There is deliberately no bare "done" kind. A done announcement only exists once a
// worker's own done report is cross-checked against recorded evidence that the task
// landed - see doneVerified for what counts, which is deliberately not a question
// about the project's mode or the route the work took - so it is a verified
// KindReportDone. ClassifyStatus emits nothing for herdr's own done/idle split, which
// carries no task-outcome signal. "done" survives only as the state column that event
// writes (state.ReportDone), never as a kind.
const (
	KindIdleUnreported = "idle-unreported"
	KindBlocked        = "blocked"
	KindFailed         = "failed"
	KindStale          = "stale"
	KindPRMerged       = "pr-merged"
	// KindPRNotRecorded and KindPRRecordUnknown are two different facts, kept as
	// two greppable tokens: an auto-record that was attempted and did not
	// complete - for any reason, refused validation through unreadable state to a
	// failed dashboard write - whose remedy is `hand pr`, which reconciles all of
	// them rather than no-opping; versus one never attempted because another
	// process held the task lock, where whether the PR got recorded is unknown
	// and the only honest instruction is to check `hand status`. The split is by
	// whether an attempt happened, never by cause, so a new cause needs no new
	// kind.
	KindPRNotRecorded       = "pr-not-recorded"
	KindPRRecordUnknown     = "pr-record-unknown"
	KindReportWorking       = "report-working"
	KindReportPaused        = "report-paused"
	KindReportBlocked       = "report-blocked"
	KindReportNeedsDecision = "report-needs-decision"
	KindReportDone          = "report-done"
	KindReportFailed        = "report-failed"
	KindReportMalformed     = "report-malformed"
	KindParked              = "parked"
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
	// CreatedAt is the tracked task's identity, not just a timestamp: the poll loop
	// compares it against the task on disk so a torn-down and respawned ID is
	// re-seeded from scratch instead of inheriting this state.
	CreatedAt    string
	Status       herdr.Status
	Probed       bool
	ChangedAt    time.Time
	Blocked      bool
	Stale        bool
	PRMerged     bool
	ReportOffset int64
	// The Persisted* fields mirror what the task's durable state already carries,
	// so a write skipped for lock contention is retried on the next tick instead
	// of silently lost.
	PersistedOffset       int64
	PersistedPRMerged     bool
	PersistedDoneVerified bool
	// PersistedChangedAt mirrors ChangedAt the same way: a dwell clock that
	// resumes from "now" on every restart never survives a fleet busy enough to
	// re-arm faster than it elapses, so ChangedAt is seeded from durable
	// evidence on resume (see resumeTaskState) rather than reset. See
	// ClassifyStale.
	PersistedChangedAt time.Time
	LastReportState    string
	// LastReportNote is kept alongside LastReportState so a done report that only
	// gains its completion evidence later can be re-announced with the same text a
	// synchronous verification would have produced.
	LastReportNote string
	// DoneVerified makes the verified-done announcement idempotent across ticks,
	// and is persisted after the announcement so it stays idempotent across a
	// restart too.
	DoneVerified bool
	// ParkedFiredFor is the report evidence mtime a parked event was already
	// announced for, so the episode fires once and only refires once the file
	// genuinely grows past it. Deliberately not seeded to "now" on resume - see
	// ClassifyParked.
	ParkedFiredFor time.Time
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

// ParkedBounds is how long a task may sit silent before ClassifyParked flags it,
// split by what the worker last reported.
type ParkedBounds struct {
	Paused time.Duration
	Other  time.Duration
}

// parkedBound answers which bound applies to lastState, and whether the task is
// exempt entirely. done/failed are terminal - no one is waiting on the pane
// anymore, so silence there is not a park. paused earns the long bound: a worker
// that named what it is waiting on has already explained the quiet. Everything
// else, including working and no report at all, is the failure case: unexplained
// silence. A non-positive bound is unconfigured, not zero-tolerance - a Config
// that never set ParkedBounds must not park every task instantly.
func parkedBound(lastState string, bounds ParkedBounds) (bound time.Duration, exempt bool) {
	switch lastState {
	case state.ReportDone, state.ReportFailed:
		return 0, true
	case state.ReportPaused:
		bound = bounds.Paused
	default:
		bound = bounds.Other
	}
	if bound <= 0 {
		return 0, true
	}
	return bound, false
}

// ClassifyParked catches the park a transition-only watcher structurally cannot
// see: a worker whose pane simply stops, with herdr registering no status change
// at all, so neither ClassifyStatus nor ClassifyStale has anything to fire on.
// mtime is the task's report file mtime - real evidence of the worker's last
// activity - or, when it has never reported, the task's own CreatedAt.
//
// mtime is deliberately never reset to "now" on a watcher resume, unlike
// ts.ChangedAt above. --until-event exits on every delivered event by design, and
// a busy fleet re-arms it constantly; anchoring this bound to anything the
// watcher resets on its own resume would let those unrelated re-arms keep
// erasing the clock before it ever completes once, silencing a genuinely parked
// worker for as long as the rest of the fleet stays busy. Anchoring to durable
// evidence that only changes when the worker itself does something is what
// makes the bound survive a restart intact.
//
// Fires once per silence episode: ts.ParkedFiredFor latches to the mtime it
// fired for, and only a later mtime - the file actually growing - clears that
// latch and lets a fresh episode fire again.
func ClassifyParked(ts *TaskState, id, lastState, lastLine string, mtime, now time.Time, bounds ParkedBounds) *Event {
	bound, exempt := parkedBound(lastState, bounds)
	if exempt {
		ts.ParkedFiredFor = time.Time{}
		return nil
	}
	if mtime.Equal(ts.ParkedFiredFor) {
		return nil
	}
	if now.Sub(mtime) < bound {
		return nil
	}
	ts.ParkedFiredFor = mtime
	age := dashboard.FormatDuration(now.Sub(mtime))
	return &Event{
		TaskID: id,
		Kind:   KindParked,
		Text:   fmt.Sprintf("parked %s: %s (silent %s)", id, lastLine, age),
		Reason: fmt.Sprintf("%s (silent %s)", lastLine, age),
	}
}

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
func ClassifyReportLine(home string, ts *TaskState, t state.Task, line state.ReportLine) *Event {
	id := t.ID
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
		return classifyReportDone(home, ts, t, line)
	}
	return nil
}

// classifyReportDone never trusts a worker's own belief that it's finished: without
// independent completion evidence the event is marked unverified so dashboard/watch
// consumers surface "worker says done" without treating it as confirmed fact.
func classifyReportDone(home string, ts *TaskState, t state.Task, line state.ReportLine) *Event {
	verified := doneVerified(home, ts, t)
	if verified {
		ts.DoneVerified = true
	}
	return &Event{TaskID: t.ID, Kind: KindReportDone, Text: doneText(t.ID, line.Note, verified), Reason: line.Note, Verified: verified}
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

// doneVerified asks one question: is there recorded evidence, not authored by the
// worker, that this task landed? What counts is a property of the deliverable, not
// of the route or the project's mode - a ship task lands by merging, however that
// merge happened, and a scout task lands by producing the data/<id>/report.md that
// hand promote itself requires.
//
// So the ship check reads t.MergeExecuted first and asks nothing further: it is
// only ever written after a merge actually happened, whether through a PR or a
// local fast-forward that leaves no PR at all. A recorded PR the watcher's own
// poll saw merged is that same evidence arriving the other way. Narrowing this to
// one route is what made the check silently always-false for a whole class of
// task twice.
func doneVerified(home string, ts *TaskState, t state.Task) bool {
	switch t.Kind {
	case state.KindShip:
		return t.MergeExecuted || (t.PR != "" && ts.PRMerged)
	case state.KindScout:
		_, err := os.Stat(filepath.Join(home, "data", t.ID, "report.md"))
		return err == nil
	}
	return false
}
