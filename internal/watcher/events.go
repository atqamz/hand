package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/age"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
)

// Kind values classify an Event for stdout/log routing. There is deliberately no bare "done" kind -
// a done announcement is a verified KindReportDone, see classifyReportDone - and "done" survives
// only as the state column that event writes (state.ReportDone).
const (
	KindIdleUnreported = "idle-unreported"
	KindBlocked        = "blocked"
	KindFailed         = "failed"
	KindStale          = "stale"
	KindPRMerged       = "pr-merged"
	// An auto-record attempted and not completed, for any reason from refused validation through
	// unreadable state. The remedy is `hand pr`, which reconciles all of them rather than no-opping.
	KindPRNotRecorded = "pr-not-recorded"
	// Never attempted, because another process held the task lock: whether the PR got recorded is
	// unknown, so the only honest instruction is to check `hand status`. Two greppable tokens because
	// the split is by whether an attempt happened, never by cause - a new cause needs no new kind.
	KindPRRecordUnknown     = "pr-record-unknown"
	KindReportWorking       = "report-working"
	KindReportPaused        = "report-paused"
	KindReportBlocked       = "report-blocked"
	KindReportNeedsDecision = "report-needs-decision"
	KindReportDone          = "report-done"
	KindReportFailed        = "report-failed"
	KindReportMalformed     = "report-malformed"
	KindParked              = "parked"
	// Three kinds for the usage-limit lifecycle, split by what an operator can do about each: the limit
	// and the resume that ends it are bookkeeping a human has no part in, while `usage-limit-stuck` says
	// the mechanism has run out of its own answers and needs someone.
	KindUsageLimit        = "usage-limit"
	KindUsageLimitResumed = "usage-limit-resumed"
	KindUsageLimitStuck   = "usage-limit-stuck"
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
		KindUsageLimit, KindUsageLimitResumed, KindUsageLimitStuck,
	}
}

// NotifyFilter is the fixed subset of events worth sending through the
// watcher's unattended notification channel.
func NotifyFilter() EventFilter {
	// report-blocked is listed even though blocked already is: it is the worker's own report-channel
	// declaration that it is stuck, not the herdr transition, and ClassifyStatus suppresses
	// idle-unreported once LastReportState is set - so reporting then going idle would notify no one.
	return NewEventFilter([]string{
		KindBlocked, KindReportBlocked, KindFailed, KindReportFailed, KindReportNeedsDecision, KindReportDone,
		// Only usage-limit-stuck of the three limit kinds: a limit that clears on its own wakes nobody by
		// design, since the resume needs no human and notifying on every limit would make the fleet's
		// loudest channel the one carrying its most routine event.
		KindUsageLimitStuck,
	})
}

// EventFilter restricts which event kinds count as a wake for RunUntilEvent, expressed directly in
// terms of Kind rather than against a fixed actionable/progress split: report-working is exactly
// what distinguishes a wedged spawn from a slow one, so hardcoding it out defeats the main case.
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
	// A nil or empty filter matches every kind - the only behavior the streaming Run path ever has,
	// and --until-event's default too.
	if len(f) == 0 {
		return true
	}
	return f[kind]
}

// The only detail available: herdr reports agent_status without a free-text cause, so this mirrors
// herdr's own "blocked" state description.
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
	// Names the herdr pane every pane-anchored field below was cached against, so hand promote handing
	// the task a new pane is detectable as such. No timestamp can stand in: RFC3339 is second-granular,
	// so a restamp in the same second as this watcher's own last write looks like no promote at all.
	PersistedPaneID string
	// Mirrors ChangedAt the same way, with PersistedChangedFor the status it was stamped for: a dwell
	// clock resuming from "now" never survives a fleet re-arming faster than it elapses, so ChangedAt
	// is seeded from durable evidence - evidence that holds only while it still describes that status.
	PersistedChangedAt  time.Time
	PersistedChangedFor string
	LastReportState     string
	// Kept alongside LastReportState so a done report that only gains its completion evidence later
	// can be re-announced with the same text a synchronous verification would have produced.
	LastReportNote string
	// DoneVerified makes the verified-done announcement idempotent across ticks,
	// and is persisted after the announcement so it stays idempotent across a
	// restart too.
	DoneVerified bool
	// Persisted, unlike the stale and unreachable latches: their dwell clocks keep moving, so
	// re-deriving costs one duplicate at most. A done or failed task's report file never grows again,
	// so its silence instant is frozen, every restart re-fires, and capped events.log evicts history.
	ParkedFiredFor          time.Time
	PersistedParkedFiredFor time.Time
	// Mirrors the task's durable usage-limit schedule: a non-zero retry instant is what makes the task
	// limited, and the attempt count is what the backoff and the stuck bound are measured in. Persisted
	// for ParkedFiredFor's reason, sharper - a re-derived schedule resumes against a still-limited account.
	LimitRetryAt           time.Time
	LimitAttempts          int
	PersistedLimitRetryAt  time.Time
	PersistedLimitAttempts int
	// Records that this watcher has read the pane looking for a limit message at least once for this
	// task. Deliberately not persisted, and the reason a watcher starting against an already-limited
	// worker still finds it: the stop predates this process, so a first sighting stands in for a transition.
	LimitProbed bool
	// Claims an outage episode the same way Stale claims a silence episode: re-derived, never
	// persisted. A restart mid-outage loses it and ClassifyUnreachable re-evaluates the dwell against
	// durable evidence, so the safe failure mode is one duplicate announcement, never a suppressed one.
	UnreachableFired bool
}

// NewTaskState seeds tracking for a task first observed at now, without emitting an
// event for whatever state it happens to already be in.
func NewTaskState(status herdr.Status, now time.Time) *TaskState {
	return &TaskState{Status: status, Probed: true, ChangedAt: now}
}

// ClassifyStatus compares a fresh probe with tracked state. Actionable transitions return an event;
// working and repeated not-busy or blocked states update ts and return nil.
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
	// herdr's idle and done are one signal here: the pane stopped being busy, neither saying the task
	// finished. See herdr.Status's doc for why hand only ever observes done, not idle, for this
	// transition - and nothing at all is emitted for that done/idle split, which carries no outcome.
	case status.NotBusy():
		if prevStatus == herdr.StatusWorking || prevStatus == herdr.StatusBlocked {
			ts.Blocked = false
			// Only when nothing has explained the stop: no report at all, or a last report still
			// "working". Any other reported state - paused, blocked, needs-decision, done, failed -
			// already explains the pane going quiet, so the transition is absorbed instead of alarming.
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

// ClassifyUnreachable covers the one outage ClassifyStatus's immediate branch cannot: a task whose
// very first sighting - or first sighting after a restart - finds its pane unreachable, which leaves
// ts.Probed false with no prior "was probed" edge to fire on.
func ClassifyUnreachable(ts *TaskState, id string, now time.Time, threshold time.Duration) *Event {
	// ClassifyStatus's success path clears ts.Probed back to true, so a pane that answers again before
	// the dwell matures never reaches it.
	if ts.Probed || ts.UnreachableFired {
		return nil
	}
	// Gating on the threshold rather than firing on sight is what makes a blink produce nothing.
	// ts.ChangedAt is reused as the outage's dwell clock rather than adding a new one, so it rides the
	// same restart-safe seeding every other dwell in this package gets - see statusChangeSeed.
	if now.Sub(ts.ChangedAt) < threshold {
		return nil
	}
	ts.UnreachableFired = true
	// KindFailed rather than a new kind: this is the same fact ClassifyStatus's immediate branch
	// announces - the pane is unreachable, whichever tick caught it first.
	return &Event{TaskID: id, Kind: KindFailed, Text: fmt.Sprintf("failed %s", id)}
}

type ParkedBounds struct {
	Paused time.Duration
	Done   time.Duration
	Other  time.Duration
}

// done and failed get their own tier rather than the blanket exemption this used to be: the status
// file being torn down is what actually severs a task from steering, not the worker's own last word,
// so a done or failed worker still attached to a pane is silence like any other and is bounded too.
func parkedBound(lastState string, bounds ParkedBounds) (bound time.Duration, exempt bool) {
	switch lastState {
	case state.ReportDone, state.ReportFailed:
		bound = bounds.Done
	case state.ReportPaused:
		bound = bounds.Paused
	default:
		bound = bounds.Other
	}
	// A non-positive bound means unconfigured, not zero-tolerance.
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

// ClassifyReportLine turns one classified report line into an event and records it as
// ts.LastReportState so a subsequent idle transition can consult it.
func ClassifyReportLine(home string, ts *TaskState, t state.Task, line state.ReportLine) *Event {
	id := t.ID
	// A malformed line is surfaced rather than dropped, but does not overwrite the last known report
	// state, since free text alone explains nothing.
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

// Never trusts a worker's own belief that it is finished: without independent completion evidence
// the event is marked unverified, so watch consumers surface "worker says done" without treating it
// as confirmed fact.
func classifyReportDone(home string, ts *TaskState, t state.Task, line state.ReportLine) *Event {
	verified := doneVerified(home, ts, t)
	if verified {
		ts.DoneVerified = true
	}
	return &Event{TaskID: t.ID, Kind: KindReportDone, Text: doneText(t.ID, line.Note, verified), Reason: line.Note, Verified: verified}
}

// ClassifyDeferredDone covers the ordinary ordering, where a worker reports done before the evidence
// exists: the done line was already consumed and cannot be re-read, so once evidence shows up on a
// later tick the verified event fires from the state the report left behind.
func ClassifyDeferredDone(home string, ts *TaskState, t state.Task) *Event {
	// Idempotent: at most one deferred announcement per task.
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

// Asks one question: is there recorded evidence, not authored by the worker, that this task landed?
// What counts is a property of the deliverable, not of the route or the project's mode. Narrowing it
// to one route is what made this check silently always-false for a whole class of task twice.
func doneVerified(home string, ts *TaskState, t state.Task) bool {
	switch t.Kind {
	case state.KindShip:
		// A ship task lands by merging, however the merge happened: t.MergeExecuted is only ever
		// written after one actually did, through a PR or a local fast-forward leaving no PR at all. A
		// recorded PR the watcher's own poll saw merged is that same evidence arriving the other way.
		return t.MergeExecuted || (t.PR != "" && ts.PRMerged)
	case state.KindScout:
		// A scout task lands by producing the data/<id>/report.md hand promote itself requires.
		_, err := os.Stat(filepath.Join(home, "data", t.ID, "report.md"))
		return err == nil
	}
	return false
}
