package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/age"
	"github.com/atqamz/hand/internal/attention"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/orientation"
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
	// The two answers cmd/statusview.go's gateProblem already renders as "gate-absent"/"gate-unknown" -
	// GateKind is the one place both hand status and hand watch turn a gate-run observation into these
	// tokens, so the strings can never drift apart the way atqamz/hand#268 found them.
	KindGateAbsent  = "gate-absent"
	KindGateUnknown = "gate-unknown"
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
		KindGateAbsent, KindGateUnknown,
		KindPRNotRecorded, KindPRRecordUnknown, KindReportWorking, KindReportPaused,
		KindReportBlocked, KindReportNeedsDecision, KindReportDone, KindReportFailed,
		KindReportMalformed, KindParked,
		KindUsageLimit, KindUsageLimitResumed, KindUsageLimitStuck,
	}
}

// CatchUpFilter is the subset of kinds an arm-time observation may deliver: every kind whose
// announcement durable state records, so a re-arm meeting an already-announced condition stays quiet.
// KindStale is the one exclusion, and needsAttention never modeled it either - the same call (atqamz/hand#268).
func CatchUpFilter() EventFilter {
	f := NewEventFilter(KnownKinds())
	// Its latch is re-derived per watcher process, and its dwell is satisfied by any task whose herdr
	// status has simply not changed lately, so delivering it at arm would return from every re-arm at
	// once, forever.
	delete(f, KindStale)
	return f
}

// NotifyFilter is the fixed subset of events worth sending through the watcher's unattended
// notification channel: a curated filter over KnownKinds, not a second attention definition
// (atqamz/hand#268). Neither gate kind is in it - a merge precondition, not an unattended interrupt.
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
	FleetID  string
	Target   orientation.MonitorTarget
}

const MaxWakeReason = 240

type Wake struct {
	FleetID     string                       `json:"fleet_id"`
	Kind        string                       `json:"kind"`
	EventKind   string                       `json:"event_kind"`
	TargetID    string                       `json:"target_id"`
	Currentness orientation.CurrentnessToken `json:"currentness"`
	Reason      string                       `json:"reason"`
}

func (e Event) Wake() Wake {
	reason := e.Reason
	if reason == "" {
		reason = e.Text
	}
	if runes := []rune(reason); len(runes) > MaxWakeReason {
		reason = string(runes[:MaxWakeReason])
	}
	return Wake{FleetID: e.FleetID, Kind: e.Target.Kind, EventKind: e.Kind, TargetID: e.Target.ID, Currentness: e.Target.Currentness, Reason: reason}
}

func (w Wake) Current(current orientation.SupervisorOrientation) bool {
	if w.FleetID == "" || w.FleetID != current.FleetID || w.TargetID == "" || w.Currentness.IsZero() {
		return false
	}
	for _, target := range current.Monitors {
		if target.ID == w.TargetID && target.Kind == w.Kind && target.Currentness == w.Currentness {
			return true
		}
	}
	return false
}

// TaskState tracks what's already been observed and announced for one task, so the
// classifier only fires once per transition instead of once per poll tick.
type TaskState struct {
	// CreatedAt pairs with AttemptID as the tracked execution identity, not just a timestamp:
	// the poll loop compares both against the task on disk so a reopened or promoted ID is
	// re-seeded from scratch instead of inheriting this state.
	CreatedAt        string
	AttemptID        int64
	AttemptLifecycle state.AttemptLifecycle
	Status           herdr.Status
	Probed           bool
	ChangedAt        time.Time
	Blocked          bool
	Stale            bool
	PRMerged         bool
	// Re-derived, never persisted, for the reason UnreachableFired is: a restart re-asking no-mistakes
	// once more is cheap, while a suppressed regression is not.
	GateProblemFired bool
	ReportCursor     state.ReportCursor
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
	PaneSample              string
	PaneSampleObserved      bool
	// Mirrors the task's durable usage-limit schedule: a non-zero retry instant is what makes the task
	// limited, and the attempt count is what the backoff and the stuck bound are measured in. Persisted
	// for ParkedFiredFor's reason, sharper - a re-derived schedule resumes against a still-limited account.
	LimitRetryAt               time.Time
	LimitAttempts              int
	LimitResumeBlocked         bool
	LimitEpisode               int64
	LimitStuckEpisode          int64
	PersistedLimitRetryAt      time.Time
	PersistedLimitAttempts     int
	PersistedLimitEpisode      int64
	PersistedLimitStuckEpisode int64
	// Records that this watcher has read the pane looking for a limit message at least once for this
	// task. Deliberately not persisted, and the reason a watcher starting against an already-limited
	// worker still finds it: the stop predates this process, so a first sighting stands in for a transition.
	LimitProbed bool
	// Claims an outage episode the same way Stale claims a silence episode: re-derived, never
	// persisted. A restart mid-outage loses it and ClassifyUnreachable re-evaluates the dwell against
	// durable evidence, so the safe failure mode is one duplicate announcement, never a suppressed one.
	UnreachableFired bool
	// Records that ClassifyCatchUp has had its one look at this task. Deliberately not persisted: the
	// durable evidence it reads is what dedupes across processes, while this only keeps one watcher
	// from re-asking a question whose answer cannot change until a status transition does.
	CaughtUp bool
}

// NewTaskState seeds tracking for a task first observed at now, without emitting an
// event for whatever state it happens to already be in.
func NewTaskState(status herdr.Status, now time.Time) *TaskState {
	return &TaskState{Status: status, Probed: true, ChangedAt: now}
}

// ClassifyStatus compares a fresh probe with tracked state. Actionable transitions return an event;
// working and repeated not-busy or blocked states update ts and return nil. A non-empty
// teardownHerdrState means hand teardown, not a crash, explains the pane going unreachable.
func ClassifyStatus(ts *TaskState, id string, status herdr.Status, probeErr error, now time.Time, teardownHerdrState string) *Event {
	if probeErr != nil {
		wasProbed := ts.Probed
		ts.Probed = false
		if teardownHerdrState != "" {
			return nil
		}
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
			if attention.UnreportedRuntime(string(status), ts.LastReportState) {
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

// ClassifyCatchUp reports the condition a task is already in the first time this watcher classifies
// it, which ClassifyStatus can only ever see as a transition. observedFor is the attempt's durable
// status_changed_for: naming the observed status is what proves some watcher announced this episode.
func ClassifyCatchUp(ts *TaskState, id string, status herdr.Status, observedFor, teardownHerdrState string) *Event {
	// A pane released by hand teardown is nobody's condition to act on - atqamz/hand#235's rule, one
	// classifier further on.
	if teardownHerdrState != "" {
		return nil
	}
	// A status this watcher has itself seen change belongs to ClassifyStatus's edge, and an episode
	// durable state already names was announced when it was observed. Catching up on either says it twice.
	if status != ts.Status || observedFor == string(status) {
		return nil
	}
	switch {
	case status.NotBusy():
		// The same question ClassifyStatus asks of its own idle edge: has anything explained the stop.
		if attention.UnreportedRuntime(string(status), ts.LastReportState) {
			return &Event{TaskID: id, Kind: KindIdleUnreported, Text: fmt.Sprintf("idle-unreported %s", id)}
		}
	case status == herdr.StatusBlocked:
		if !ts.Blocked {
			ts.Blocked = true
			return &Event{TaskID: id, Kind: KindBlocked, Text: fmt.Sprintf("blocked %s: %s", id, blockedReason), Reason: blockedReason}
		}
	}
	return nil
}

func ClassifyStale(ts *TaskState, id, deliveredAt string, now time.Time, threshold time.Duration) *Event {
	if !ts.Probed || ts.Stale {
		return nil
	}
	// Silence is no longer an unattended condition after a terminal worker report or durable task
	// delivery, so neither fact may consume the stale latch.
	if state.TerminalReport(ts.LastReportState) || deliveredAt != "" {
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
// ts.Probed false with no prior "was probed" edge to fire on. teardownHerdrState is as in ClassifyStatus.
func ClassifyUnreachable(ts *TaskState, id string, now time.Time, threshold time.Duration, teardownHerdrState string) *Event {
	if teardownHerdrState != "" {
		return nil
	}
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

// Parked is the level check behind ClassifyParked's latch, exported so cmd/statusview.go can ask the
// same question for atqamz/hand#32's own case (atqamz/hand#268's disagreement 2). silentSince is the
// evidence instant ReportEvidenceTime floors, the same one ClassifyParked calls mtime.
func Parked(lastReportState string, silentSince, now time.Time, bounds ParkedBounds) bool {
	bound, exempt := parkedBound(lastReportState, bounds)
	if exempt {
		return false
	}
	return now.Sub(silentSince) >= bound
}

// mtime is deliberately never reset to "now" on resume: --until-event restarts on
// every delivered event, and a busy fleet would otherwise erase the clock before
// it ever completes once.
func ClassifyParked(ts *TaskState, id, lastState, lastLine string, mtime, now time.Time, bounds ParkedBounds, client PaneReader, paneID string) *Event {
	if _, exempt := parkedBound(lastState, bounds); exempt {
		ts.ParkedFiredFor = time.Time{}
		return nil
	}
	if mtime.Equal(ts.ParkedFiredFor) {
		return nil
	}
	naive := Parked(lastState, mtime, now, bounds)
	// Read once each tick so the next tick has a bounded activity baseline without blocking the poll loop.
	confirmed, sample, observed := ConfirmParked(naive, ts.PaneSample, ts.PaneSampleObserved, client, paneID)
	if observed {
		ts.PaneSample = sample
		ts.PaneSampleObserved = true
	}
	// Cross-checked before the latch: a live pane leaves this silence episode announceable.
	if !confirmed {
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

// GateApplies is cmd/statusview.go's gateRunApplies, exported: the gate-run check has anything to say
// about a task only when it is a done ship task carrying a recorded PR, and hand status and hand
// watch must agree on that before either of them shells out to ask no-mistakes anything.
func GateApplies(taskKind, pr string, reportedDone bool) bool {
	return taskKind == state.KindShip && pr != "" && reportedDone
}

// GateKind maps a gate-run observation to the attention condition it represents, the token
// cmd/statusview.go's taskFlags already renders as "gate-absent"/"gate-unknown". ok is false for a
// found run or an observation the check never reached, neither of which is a problem to report.
func GateKind(observed ghutil.ObservationState) (kind string, ok bool) {
	switch observed {
	case ghutil.ObservationAbsent:
		return KindGateAbsent, true
	case ghutil.ObservationUnknown:
		return KindGateUnknown, true
	}
	return "", false
}

// ClassifyGateProblem closes atqamz/hand#268's disagreement 1: a PR behind no completed gate run used
// to be attention in hand status and silence here. Fires once per process like ClassifyPRMerged, and
// resets as soon as the check stops applying so a later attempt gets its own announcement.
func ClassifyGateProblem(ts *TaskState, id string, applies bool, observed ghutil.ObservationState) *Event {
	if !applies {
		ts.GateProblemFired = false
		return nil
	}
	kind, ok := GateKind(observed)
	if !ok {
		ts.GateProblemFired = false
		return nil
	}
	if ts.GateProblemFired {
		return nil
	}
	ts.GateProblemFired = true
	return &Event{TaskID: id, Kind: kind, Text: fmt.Sprintf("%s %s", kind, id)}
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
