package watcher

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
)

// The bounds on resuming a usage-limited worker. The failure mode designed against is a retry storm
// against an account that is still limited, so every attempt is spent deliberately.
const (
	// How long the first attempt waits when the harness named no reset instant at all, and the base of
	// the backoff between later attempts.
	limitFloor = 10 * time.Minute
	// Keeps a long limit from being probed more than hourly.
	limitBackoffCap = time.Hour
	// Caps how far a named reset instant can push an attempt out, so a misparsed or absurd prediction
	// cannot strand the worker. A week-long limit is still not probed hourly for a week: each attempt
	// re-reads the harness's own fresh refusal and reschedules from it, so roughly one attempt a day.
	limitMaxWait = 24 * time.Hour
	// Puts the attempt just past the named instant rather than exactly on it, since a prediction landing
	// on the boundary would otherwise burn an attempt a few seconds early every time.
	limitSkew = time.Minute
	// Where the mechanism admits it is out of answers and says so once on the notify channel. It keeps
	// trying afterwards - a weekly limit is real and does eventually lift - but no longer quietly.
	limitStuckAfter = 6
)

// How much scrollback a limit check reads. The refusal is the last thing a limited harness prints, so
// this only has to outlast whatever the harness draws under it.
const limitReadLines = 60

// The steer that ends a limit, deliberately a plain instruction rather than a bare "continue": a worker
// whose limit has lifted needs to know why it is being poked, and one whose limit has not answers with a
// fresh refusal - the observation the next attempt is scheduled from.
const limitResumeMessage = "Your previous turn stopped on a usage limit. The limit may have lifted now. Resume the task from where it stopped."

// The herdr surface the usage-limit machinery needs, narrowed so a test can drive the whole
// detect-attempt-resume cycle without a herdr daemon.
type limitPane interface {
	PaneRead(paneID string, lines int) (string, error)
	PaneSendText(paneID, text string) error
	PaneSendKeys(paneID string, keys ...string) error
}

// The whole usage-limit lifecycle for one task on one tick: detect a harness that stopped on a limit,
// resume it once the limit plausibly lifted, and let go the moment the worker is running again.
func classifyUsageLimit(cfg Config, client limitPane, ts *TaskState, t state.Task, pane herdr.Pane, status herdr.Status, probeErr error, justStopped bool, now time.Time, errOut io.Writer) *Event {
	// A pane that will not answer PaneGet cannot be read or steered either, and
	// ClassifyUnreachable already owns that fact.
	if probeErr != nil {
		return nil
	}
	// Whether a limit is even detectable is a harness capability, so a harness that declines it - every
	// harness but claude today - costs a map lookup and returns having read no pane and sent nothing.
	// Adding another harness is an entry in that catalogue, not a branch here.
	if !harness.SupportsUsageLimit(pane.Agent) {
		return nil
	}
	if !ts.LimitRetryAt.IsZero() {
		return continueUsageLimit(cfg, client, ts, t, pane, status, now, errOut)
	}
	// justStopped is computed before ClassifyStatus consumes the transition: it is what makes detection
	// edge-triggered rather than a per-tick pane read. ts.LimitProbed is the other edge, covering what no
	// transition can - a watcher starting against a worker already stranded before this process existed.
	if !status.NotBusy() || (!justStopped && ts.LimitProbed) {
		return nil
	}
	return detectUsageLimit(cfg, client, ts, t, pane, now, errOut)
}

// Reads the stopped pane once and, if the harness stopped on a limit, records the schedule that will
// resume it. A worker whose report channel already explains the stop is left alone: a done or failed
// worker is not waiting on quota, and steering one would restart work that is over.
func detectUsageLimit(cfg Config, client limitPane, ts *TaskState, t state.Task, pane herdr.Pane, now time.Time, errOut io.Writer) *Event {
	if reportEndsTask(ts.LastReportState) {
		return nil
	}
	// Set before the read, not after: a read that keeps failing while PaneGet keeps
	// succeeding would otherwise be retried every tick forever, one diagnostic per
	// poll. One attempt per stop edge is the same budget the successful path gets.
	ts.LimitProbed = true

	text, err := client.PaneRead(t.Herdr.PaneID, limitReadLines)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: read pane for %s failed: %v\n", t.ID, err)
		return nil
	}
	reset, limited := harness.DetectUsageLimit(pane.Agent, text, now)
	if !limited {
		return nil
	}

	ts.LimitAttempts = 0
	ts.LimitRetryAt = nextLimitRetry(reset, 0, now)
	writeLimitHold(cfg.Home, t.ID, ts, errOut)
	return &Event{
		TaskID: t.ID,
		Kind:   KindUsageLimit,
		Text:   fmt.Sprintf("usage-limit %s: %s", t.ID, limitReason(ts)),
		Reason: limitReason(ts),
	}
}

// Runs for a task already known to be limited. The clear check comes first and runs every tick, not only
// when an attempt is due: the limit can end for reasons this package had no part in - an operator
// `hand send`, a human typing in the pane - and a visibly running worker must not keep collecting attempts.
func continueUsageLimit(cfg Config, client limitPane, ts *TaskState, t state.Task, pane herdr.Pane, status herdr.Status, now time.Time, errOut io.Writer) *Event {
	if status == herdr.StatusWorking || status == herdr.StatusBlocked {
		return clearUsageLimit(cfg, ts, t, errOut)
	}
	// A limited worker that has since reported done or failed said its own last word
	// on the task. Whatever quota it is owed, nobody is waiting for it.
	if reportEndsTask(ts.LastReportState) {
		return clearUsageLimit(cfg, ts, t, errOut)
	}
	if now.Before(ts.LimitRetryAt) {
		return nil
	}
	return attemptUsageLimitResume(cfg, client, ts, t, pane, now, errOut)
}

// Steers the pane and schedules the next attempt. The attempt itself is the observation: nothing here
// decides whether the limit is over - the next tick's clear check does that, by seeing whether the pane
// started working.
func attemptUsageLimitResume(cfg Config, client limitPane, ts *TaskState, t state.Task, pane herdr.Pane, now time.Time, errOut io.Writer) *Event {
	// The same `send:<id>` lock hand send holds, because two writers racing one composer is the lost
	// steer atqamz/hand#102 traced. TryLock, never Lock: a tick must not block behind an operator's
	// whole --wait, and an operator send landing right now is itself the thing that ends the limit.
	release, err := state.TryLock(cfg.Home, "send:"+t.ID)
	if err != nil {
		if !errors.Is(err, state.ErrLockBusy) {
			_, _ = fmt.Fprintf(errOut, "watch: lock send %s failed: %v\n", t.ID, err)
		}
		// A busy lock spends no attempt: the schedule is left untouched, so the next tick finds it due.
		return nil
	}
	defer release()

	// Read first: the freshest refusal on screen is the harness's own latest prediction of when its quota
	// returns, and scheduling from it keeps a genuinely long limit off the backoff's much shorter clock.
	reset := time.Time{}
	if text, err := client.PaneRead(t.Herdr.PaneID, limitReadLines); err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: read pane for %s failed: %v\n", t.ID, err)
	} else if at, limited := harness.DetectUsageLimit(pane.Agent, text, now); limited {
		reset = at
	}

	ts.LimitAttempts++
	ts.LimitRetryAt = nextLimitRetry(reset, ts.LimitAttempts, now)

	if err := steerPane(client, t.Herdr.PaneID, limitResumeMessage); err != nil {
		// The schedule above is kept deliberately: a steer that did not land is one
		// lost attempt, and reverting the stamp would put this task back in the due
		// state every tick, which is the storm.
		_, _ = fmt.Fprintf(errOut, "watch: resume %s after usage limit failed: %v\n", t.ID, err)
	}
	writeLimitHold(cfg.Home, t.ID, ts, errOut)

	if ts.LimitAttempts != limitStuckAfter {
		return nil
	}
	return &Event{
		TaskID: t.ID,
		Kind:   KindUsageLimitStuck,
		Text:   fmt.Sprintf("usage-limit-stuck %s: %s", t.ID, limitReason(ts)),
		Reason: limitReason(ts),
	}
}

// Forgets the schedule and the operator-visible hold together, so the two cannot disagree about whether
// a task is still waiting on quota.
func clearUsageLimit(cfg Config, ts *TaskState, t state.Task, errOut io.Writer) *Event {
	attempts := ts.LimitAttempts
	ts.LimitRetryAt = time.Time{}
	ts.LimitAttempts = 0
	// The stop edge that stranded this worker is long consumed, so a limit hitting the
	// same worker again needs a fresh probe to be found on.
	ts.LimitProbed = false
	if err := state.ClearHoldIfKind(cfg.Home, t.ID, state.HoldKindLimit); err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: clear usage-limit hold on %s failed: %v\n", t.ID, err)
	}
	return &Event{
		TaskID: t.ID,
		Kind:   KindUsageLimitResumed,
		Text:   fmt.Sprintf("usage-limit-resumed %s: running again after %s", t.ID, attemptCount(attempts)),
		Reason: fmt.Sprintf("running again after %s", attemptCount(attempts)),
	}
}

func attemptCount(attempts int) string {
	switch attempts {
	case 0:
		return "no attempts"
	case 1:
		return "1 attempt"
	}
	return fmt.Sprintf("%d attempts", attempts)
}

// Picks when to try next: never earlier than the backoff allows, never earlier than the instant the
// harness itself named, and never further out than limitMaxWait. A named reset is only ever a
// prediction, so it moves the attempt - it never decides that the limit is over.
func nextLimitRetry(reset time.Time, attempts int, now time.Time) time.Time {
	wait := limitBackoff(attempts)
	if !reset.IsZero() {
		if until := reset.Add(limitSkew).Sub(now); until > wait {
			wait = until
		}
	}
	if wait > limitMaxWait {
		wait = limitMaxWait
	}
	return now.Add(wait)
}

// Doubles from limitFloor up to limitBackoffCap. The shift is bounded before it happens rather than
// after, since a large attempt count would otherwise overflow the duration into a negative wait - a
// task retried on every tick.
func limitBackoff(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	wait := limitFloor
	for range attempts {
		if wait >= limitBackoffCap {
			break
		}
		wait *= 2
	}
	if wait > limitBackoffCap {
		return limitBackoffCap
	}
	return wait
}

func reportEndsTask(lastState string) bool {
	return lastState == state.ReportDone || lastState == state.ReportFailed
}

// The same two-call steer hand send performs: text into the composer, then Enter to submit it. Split
// the same way, because text that arrived but was never submitted is a distinct failure from text that
// never arrived.
func steerPane(client limitPane, paneID, message string) error {
	if err := client.PaneSendText(paneID, message); err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	if err := client.PaneSendKeys(paneID, "Enter"); err != nil {
		return fmt.Errorf("submit message: %w", err)
	}
	return nil
}

// What an operator sees in hand status's held block, refreshed on every attempt: which attempt the
// mechanism is on and when it next tries. The hold is the projection of the schedule, so it carries no
// fact the schedule does not.
func limitReason(ts *TaskState) string {
	return fmt.Sprintf("harness stopped on a usage limit; %s made, next try %s",
		attemptCount(ts.LimitAttempts), ts.LimitRetryAt.UTC().Format(time.RFC3339))
}

// Makes the limit visible where an operator already looks, and blocks `hand spawn` from reusing the id
// out from under a worker that is only waiting on quota. A failure is loud but never fatal: the schedule
// in task state is what resumes the worker, and losing its projection must not cost the resume.
func writeLimitHold(home, id string, ts *TaskState, errOut io.Writer) {
	h := state.Hold{
		ID:     id,
		Kind:   state.HoldKindLimit,
		Reason: limitReason(ts),
		SetAt:  time.Now().UTC().Format(time.RFC3339),
	}
	written, err := state.SetHoldIfNotOtherKind(home, h)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: set usage-limit hold on %s failed: %v\n", id, err)
		return
	}
	if !written {
		_, _ = fmt.Fprintf(errOut, "watch: hold on %s is not of kind limit; usage-limit wait left unprojected: %s\n", id, limitReason(ts))
	}
}
