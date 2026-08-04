package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/secondhand/internal/age"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/state"
)

// Kind values classify an Event for stdout/log routing.
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
	// complete - for any reason, refused validation through unreadable state -
	// whose remedy is `hand pr`, which reconciles all of them rather than
	// no-opping; versus one never attempted because another
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

// KnownKinds lists every Kind a caller can name in an EventFilter, so cmd/watch.go
// can reject a typo'd --event value at flag-parsing time instead of it silently
// matching nothing forever.
func KnownKinds() []string {
	return []string{
		KindIdleUnreported, KindBlocked, KindFailed, KindStale, KindPRMerged,
		KindPRNotRecorded, KindPRRecordUnknown, KindReportWorking, KindReportPaused,
		KindReportBlocked, KindReportNeedsDecision, KindReportDone, KindReportFailed,
		KindReportMalformed, KindParked,
	}
}

// NotifyFilter is the EventFilter for the watcher's in-process notify hook -
// see SPECS.md's "Notifying a supervisory agent with no session watching" for
// why its membership differs from --event's. report-blocked has to be listed
// even though blocked already is: it is the worker's own report-channel
// declaration that it is stuck, not the herdr transition, and ClassifyStatus
// suppresses idle-unreported once LastReportState is set - so a worker that
// reports blocked and then goes idle would otherwise notify no one.
func NotifyFilter() EventFilter {
	return NewEventFilter([]string{
		KindBlocked, KindReportBlocked, KindFailed, KindReportFailed, KindReportNeedsDecision, KindReportDone,
	})
}

// EventFilter restricts which event kinds count as a wake for RunUntilEvent. The
// caller expresses it directly in terms of Kind rather than against a fixed
// actionable/progress split: report-working is exactly what distinguishes a
// wedged spawn from a slow one, so hardcoding it out would defeat the one case
// that most needs watching. A nil/empty filter matches every kind - the only
// behavior the streaming Run path ever has, and --until-event's default too.
type EventFilter map[string]bool

// NewEventFilter builds a filter from caller-supplied kind names. An empty list
// returns nil so Matches falls through to "match everything" rather than
// "match nothing".
func NewEventFilter(kinds []string) EventFilter {
	if len(kinds) == 0 {
		return nil
	}
	f := make(EventFilter, len(kinds))
	for _, k := range kinds {
		f[k] = true
	}
	return f
}

func (f EventFilter) Matches(kind string) bool {
	if len(f) == 0 {
		return true
	}
	return f[kind]
}

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
	ReportCursor state.ReportCursor
	// The Persisted* fields mirror what the task's durable state already carries,
	// so a write skipped for lock contention is retried on the next tick instead
	// of silently lost.
	PersistedCursor       state.ReportCursor
	PersistedPRMerged     bool
	PersistedDoneVerified bool
	// PersistedPaneID names the herdr pane every pane-anchored field below was
	// cached against, so hand promote handing the task a new pane is detectable as
	// such. No timestamp can stand in for it: RFC3339 is second-granular, so a
	// restamp landing in the same second as this watcher's own last write is
	// indistinguishable from no promote at all.
	PersistedPaneID string
	// PersistedChangedAt mirrors ChangedAt the same way, and PersistedChangedFor the
	// status it was stamped for: a dwell clock that resumes from "now" never
	// survives a fleet re-arming faster than it elapses, so ChangedAt is seeded from
	// durable evidence on resume rather than reset - evidence that only holds while
	// it still describes the status being dwelt in.
	PersistedChangedAt  time.Time
	PersistedChangedFor string
	LastReportState     string
	// LastReportNote is kept alongside LastReportState so a done report that only
	// gains its completion evidence later can be re-announced with the same text a
	// synchronous verification would have produced.
	LastReportNote string
	// DoneVerified makes the verified-done announcement idempotent across ticks,
	// and is persisted after the announcement so it stays idempotent across a
	// restart too.
	DoneVerified bool
	// ParkedFiredFor is persisted, unlike the stale and unreachable latches: what
	// makes re-deriving those safe is that their dwell clocks keep moving, so a
	// restart costs one duplicate at most. A done or failed task's report file
	// never grows again, so its silence instant is frozen and every restart
	// re-fires against it - and state/events.log is capped, so those duplicates
	// evict real history rather than merely repeating themselves.
	ParkedFiredFor          time.Time
	PersistedParkedFiredFor time.Time
	// UnreachableFired claims an outage episode the same way Stale claims a
	// silence episode: re-derived, never persisted. A restart mid-outage loses it
	// and ClassifyUnreachable simply re-evaluates the dwell against durable
	// evidence, so the safe failure mode is one duplicate announcement, never a
	// suppressed one.
	UnreachableFired bool
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
			ts.UnreachableFired = true
			return &Event{TaskID: id, Kind: KindFailed, Text: fmt.Sprintf("failed %s", id)}
		}
		return nil
	}
	ts.Probed = true
	ts.UnreachableFired = false

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

// ClassifyUnreachable covers the one outage ClassifyStatus's immediate branch
// can't: a task whose very first sighting - or first sighting after a restart -
// finds its pane unreachable, which leaves ts.Probed false with no prior "was
// probed" edge to fire on. Gating on threshold rather than firing on sight is
// what makes a blink produce nothing: a pane that answers again before the dwell
// matures never reaches here, since ClassifyStatus's success path clears
// ts.Probed back to true first. ts.ChangedAt is reused as the outage's dwell
// clock rather than adding a new one, so it rides the same restart-safe seeding
// every other dwell in this package already gets - see statusChangeSeed. Reusing
// KindFailed rather than a new kind keeps this the same fact ClassifyStatus's
// immediate branch already announces: the pane is unreachable, whichever tick
// caught it first.
func ClassifyUnreachable(ts *TaskState, id string, now time.Time, threshold time.Duration) *Event {
	if ts.Probed || ts.UnreachableFired {
		return nil
	}
	if now.Sub(ts.ChangedAt) < threshold {
		return nil
	}
	ts.UnreachableFired = true
	return &Event{TaskID: id, Kind: KindFailed, Text: fmt.Sprintf("failed %s", id)}
}

type ParkedBounds struct {
	Paused time.Duration
	Done   time.Duration
	Other  time.Duration
}

// A non-positive bound means unconfigured rather than zero-tolerance. done/failed
// get their own tier rather than the blanket exemption this used to be: the
// status file being torn down is what actually severs a task from steering, not
// the worker's own last word on the matter, so a done/failed worker still
// attached to a pane is silence like any other and gets bounded the same way.
func parkedBound(lastState string, bounds ParkedBounds) (bound time.Duration, exempt bool) {
	switch lastState {
	case state.ReportDone, state.ReportFailed:
		bound = bounds.Done
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

// mtime is deliberately never reset to "now" on resume: --until-event restarts on
// every delivered event, and a busy fleet would otherwise erase the clock before
// it ever completes once.
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
	silentFor := age.FormatDuration(now.Sub(mtime))
	return &Event{
		TaskID: id,
		Kind:   KindParked,
		Text:   fmt.Sprintf("parked %s: %s (silent %s)", id, lastLine, silentFor),
		Reason: fmt.Sprintf("%s (silent %s)", lastLine, silentFor),
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
// independent completion evidence the event is marked unverified so watch
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
