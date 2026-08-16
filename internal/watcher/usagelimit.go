package watcher

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/steering"
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
	PaneGet(paneID string) (herdr.Pane, error)
	PaneRead(paneID string, lines int) (string, error)
	PaneSendText(paneID, text string) error
	PaneSendKeys(paneID string, keys ...string) error
}

// The whole usage-limit lifecycle for one task on one tick: detect a harness that stopped on a limit,
// resume it once the limit plausibly lifted, and let go the moment the worker is running again.
func classifyUsageLimit(cfg Config, client limitPane, ts *TaskState, t state.Task, a state.Attempt, pane herdr.Pane, status herdr.Status, probeErr error, justStopped bool, now time.Time, errOut io.Writer) *Event {
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
		return continueUsageLimit(cfg, client, ts, t, a, pane, status, now, errOut)
	}
	if ts.LimitResumeBlocked {
		if err := normalizePendingResume(cfg.Home, a, errOut); err != nil {
			_, _ = fmt.Fprintf(errOut, "watch: recover pending resume for %s failed: %v\n", t.ID, err)
		}
		if status == herdr.StatusWorking || status == herdr.StatusBlocked || reportEndsTask(ts.LastReportState) {
			return clearUsageLimit(cfg, ts, t, a, errOut)
		}
		return observeBlockedUsageLimit(cfg, ts, t, a, true, errOut)
	}
	// justStopped is computed before ClassifyStatus consumes the transition: it is what makes detection
	// edge-triggered rather than a per-tick pane read. ts.LimitProbed is the other edge, covering what no
	// transition can - a watcher starting against a worker already stranded before this process existed.
	if !status.NotBusy() || (!justStopped && ts.LimitProbed) {
		return nil
	}
	return detectUsageLimit(cfg, client, ts, t, a, pane, now, errOut)
}

// Reads the stopped pane once and, if the harness stopped on a limit, records the schedule that will
// resume it. A worker whose report channel already explains the stop is left alone: a done or failed
// worker is not waiting on quota, and steering one would restart work that is over.
func detectUsageLimit(cfg Config, client limitPane, ts *TaskState, t state.Task, a state.Attempt, pane herdr.Pane, now time.Time, errOut io.Writer) *Event {
	if reportEndsTask(ts.LastReportState) {
		return nil
	}
	// Set before the read, not after: a read that keeps failing while PaneGet keeps
	// succeeding would otherwise be retried every tick forever, one diagnostic per
	// poll. One attempt per stop edge is the same budget the successful path gets.
	ts.LimitProbed = true

	text, err := client.PaneRead(a.Herdr.PaneID, limitReadLines)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: read pane for %s failed: %v\n", t.ID, err)
		return nil
	}
	reset, limited := harness.DetectUsageLimit(pane.Agent, text, now)
	if !limited {
		return nil
	}

	if ts.LimitEpisode < a.UsageLimitEpisode {
		ts.LimitEpisode = a.UsageLimitEpisode
	}
	ts.LimitEpisode++
	ts.LimitAttempts = 0
	ts.LimitRetryAt = nextLimitRetry(reset, 0, now)
	if writeLimitHoldForAttempt(cfg, t, a, ts, errOut) == limitHoldProjectionFailed {
		return nil
	}
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
func continueUsageLimit(cfg Config, client limitPane, ts *TaskState, t state.Task, a state.Attempt, pane herdr.Pane, status herdr.Status, now time.Time, errOut io.Writer) *Event {
	if status == herdr.StatusWorking || status == herdr.StatusBlocked {
		return clearUsageLimit(cfg, ts, t, a, errOut)
	}
	// A limited worker that has since reported done or failed said its own last word
	// on the task. Whatever quota it is owed, nobody is waiting for it.
	if reportEndsTask(ts.LastReportState) {
		return clearUsageLimit(cfg, ts, t, a, errOut)
	}
	if ts.LimitResumeBlocked {
		return observeBlockedUsageLimit(cfg, ts, t, a, true, errOut)
	}
	if now.Before(ts.LimitRetryAt) {
		return nil
	}
	return attemptUsageLimitResume(cfg, client, ts, t, a, pane, now, errOut)
}

// Steers the pane and records the outcome. The next tick's clear check decides whether the limit is over
// by seeing whether the pane started working.
func attemptUsageLimitResume(cfg Config, client limitPane, ts *TaskState, t state.Task, a state.Attempt, pane herdr.Pane, now time.Time, errOut io.Writer) *Event {
	result, err := steering.Execute(steering.Request{
		Home: cfg.Home, TaskID: t.ID, Message: limitResumeMessage, Origin: state.SendOriginUsageLimitResume,
		Client: client, TryLock: true, Expected: &a, UsageLimitEpisode: ts.LimitEpisode, Now: func() time.Time { return now },
	})
	if err != nil {
		var sendErr *steering.Error
		if !errors.As(err, &sendErr) {
			_, _ = fmt.Fprintf(errOut, "watch: resume %s after usage limit failed: %v\n", t.ID, err)
			return nil
		}
		if sendErr.Precondition {
			if isSteeringLockBusy(err) {
				return nil
			}
			if errors.Is(err, steering.ErrOwnershipConflict) || errors.Is(err, steering.ErrPaneOwnershipMismatch) {
				_, _ = fmt.Fprintf(errOut, "watch: resume %s refused for stale ownership: %v\n", t.ID, err)
			} else {
				_, _ = fmt.Fprintf(errOut, "watch: resume %s precondition failed: %v\n", t.ID, err)
			}
			return nil
		}
		if sendErr.State == "" {
			ts.LimitAttempts++
			ts.LimitRetryAt = nextLimitRetry(time.Time{}, ts.LimitAttempts, now)
			_, _ = fmt.Fprintf(errOut, "watch: resume %s did not begin submission; retry scheduled: %v\n", t.ID, err)
		} else if sendErr.State == state.SendNotSubmitted && sendErr.RetrySafe {
			ts.LimitAttempts++
			ts.LimitRetryAt = nextLimitRetry(limitReset(client, a, pane, t, now, errOut), ts.LimitAttempts, now)
		} else {
			ts.LimitResumeBlocked = true
			ts.LimitAttempts++
			ts.LimitRetryAt = nextLimitRetry(time.Time{}, ts.LimitAttempts, now)
			_, _ = fmt.Fprintf(errOut, "watch: resume %s is %s; no automatic resend: %v\n", t.ID, sendErr.State, err)
		}
	} else {
		_ = result
		ts.LimitResumeBlocked = true
		ts.LimitAttempts++
		ts.LimitRetryAt = nextLimitRetry(time.Time{}, ts.LimitAttempts, now)
		_, _ = fmt.Fprintf(errOut, "watch: resume %s submitted to the terminal pane; waiting for worker observation\n", t.ID)
	}
	if writeLimitHoldForAttempt(cfg, t, a, ts, errOut) == limitHoldProjectionFailed {
		return nil
	}
	return observeBlockedUsageLimit(cfg, ts, t, a, false, errOut)
}

func isSteeringLockBusy(err error) bool {
	return errors.Is(err, state.ErrLockBusy)
}

func limitReset(client limitPane, attempt state.Attempt, pane herdr.Pane, task state.Task, now time.Time, errOut io.Writer) time.Time {
	text, err := client.PaneRead(attempt.Herdr.PaneID, limitReadLines)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: read pane for %s failed: %v\n", task.ID, err)
		return time.Time{}
	}
	reset, limited := harness.DetectUsageLimit(pane.Agent, text, now)
	if !limited {
		return time.Time{}
	}
	return reset
}

// The Task and Attempt a tick carries were read before the locks above, so a teardown or promotion that
// landed in between describes execution this fleet has moved past: steering it pokes a pane the run no
// longer owns, and the hold write would put a limit hold back on an id nothing is waiting for.
func ownsAttempt(home string, t state.Task, a state.Attempt, errOut io.Writer) bool {
	history, err := state.ReadHistory(home, t.ID)
	if err != nil {
		if !errors.Is(err, state.ErrTaskNotFound) {
			_, _ = fmt.Fprintf(errOut, "watch: re-read task %s failed: %v\n", t.ID, err)
		}
		return false
	}
	current := history.ActiveAttempt
	return history.Task.ID == t.ID && history.Task.Lifecycle == state.TaskOpen &&
		current != nil && current.ID == a.ID && current.TaskID == t.ID &&
		current.Lifecycle == state.AttemptRunning && current.Herdr == a.Herdr
}

// Forgets the schedule and the operator-visible hold together, so the two cannot disagree about whether
// a task is still waiting on quota.
func clearUsageLimit(cfg Config, ts *TaskState, t state.Task, a state.Attempt, errOut io.Writer) *Event {
	if !clearLimitHoldForAttempt(cfg, t, a, errOut) {
		return nil
	}
	attempts := ts.LimitAttempts
	ts.LimitRetryAt = time.Time{}
	ts.LimitAttempts = 0
	ts.LimitResumeBlocked = false
	// The stop edge that stranded this worker is long consumed, so a limit hitting the
	// same worker again needs a fresh probe to be found on.
	ts.LimitProbed = false
	return &Event{
		TaskID: t.ID,
		Kind:   KindUsageLimitResumed,
		Text:   fmt.Sprintf("usage-limit-resumed %s: running again after %s", t.ID, attemptCount(attempts)),
		Reason: fmt.Sprintf("running again after %s", attemptCount(attempts)),
	}
}

func observeBlockedUsageLimit(cfg Config, ts *TaskState, t state.Task, a state.Attempt, advance bool, errOut io.Writer) *Event {
	if writeLimitHoldForAttempt(cfg, t, a, ts, errOut) == limitHoldProjectionFailed {
		return nil
	}
	if advance && ts.LimitAttempts < limitStuckAfter {
		ts.LimitAttempts++
	}
	if ts.LimitAttempts < limitStuckAfter {
		return nil
	}
	if ts.LimitEpisode == 0 {
		if ts.LimitStuckEpisode == -1 {
			return nil
		}
		ts.LimitStuckEpisode = -1
	} else if ts.LimitStuckEpisode == ts.LimitEpisode {
		return nil
	} else {
		ts.LimitStuckEpisode = ts.LimitEpisode
	}
	return &Event{
		TaskID: t.ID,
		Kind:   KindUsageLimitStuck,
		Text:   fmt.Sprintf("usage-limit-stuck %s: %s", t.ID, limitReason(ts)),
		Reason: limitReason(ts),
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

// What an operator sees in hand status's held block, refreshed on every attempt: which attempt the
// mechanism is on and when it next tries. The hold is the projection of the schedule, so it carries no
// fact the schedule does not.
func limitReason(ts *TaskState) string {
	if ts.LimitResumeBlocked {
		return "automatic usage-limit resume is unresolved or submitted; no automatic resend will be attempted"
	}
	return fmt.Sprintf("harness stopped on a usage limit; %s made, next try %s",
		attemptCount(ts.LimitAttempts), ts.LimitRetryAt.UTC().Format(time.RFC3339))
}

// Makes the limit visible where an operator already looks, and blocks `hand spawn` from reusing the id
// out from under a worker that is only waiting on quota. A failure is loud but never fatal: the schedule
// in task state is what resumes the worker, and losing its projection must not cost the resume.
type limitHoldProjection uint8

const (
	limitHoldProjectionFailed limitHoldProjection = iota
	limitHoldProjectionWritten
	limitHoldProjectionBlocked
)

func writeLimitHold(home, id string, ts *TaskState, errOut io.Writer) limitHoldProjection {
	h := state.Hold{
		ID:     id,
		Kind:   state.HoldKindLimit,
		Reason: limitReason(ts),
		SetAt:  time.Now().UTC().Format(time.RFC3339),
	}
	written, err := state.SetHoldIfNotOtherKind(home, h)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: set usage-limit hold on %s failed: %v\n", id, err)
		return limitHoldProjectionFailed
	}
	if !written {
		_, _ = fmt.Fprintf(errOut, "watch: hold on %s is not of kind limit; usage-limit wait left unprojected: %s\n", id, limitReason(ts))
		return limitHoldProjectionBlocked
	}
	return limitHoldProjectionWritten
}

func writeLimitHoldForAttempt(cfg Config, t state.Task, a state.Attempt, ts *TaskState, errOut io.Writer) limitHoldProjection {
	releaseTask, err := state.TryLock(cfg.Home, "task:"+t.ID)
	if err != nil {
		if !errors.Is(err, state.ErrLockBusy) {
			_, _ = fmt.Fprintf(errOut, "watch: lock task %s failed: %v\n", t.ID, err)
		}
		return limitHoldProjectionFailed
	}
	defer releaseTask()
	return writeLimitHoldForOwnedAttempt(cfg, t, a, ts, errOut)
}

func writeLimitHoldForOwnedAttempt(cfg Config, t state.Task, a state.Attempt, ts *TaskState, errOut io.Writer) limitHoldProjection {
	if !ownsAttempt(cfg.Home, t, a, errOut) {
		return limitHoldProjectionFailed
	}
	return writeLimitHold(cfg.Home, t.ID, ts, errOut)
}

func clearLimitHoldForAttempt(cfg Config, t state.Task, a state.Attempt, errOut io.Writer) bool {
	releaseTask, err := state.TryLock(cfg.Home, "task:"+t.ID)
	if err != nil {
		if !errors.Is(err, state.ErrLockBusy) {
			_, _ = fmt.Fprintf(errOut, "watch: lock task %s failed: %v\n", t.ID, err)
		}
		return false
	}
	defer releaseTask()
	return clearLimitHoldForOwnedAttempt(cfg, t, a, errOut)
}

func clearLimitHoldForOwnedAttempt(cfg Config, t state.Task, a state.Attempt, errOut io.Writer) bool {
	if !ownsAttempt(cfg.Home, t, a, errOut) {
		return false
	}
	if err := state.ClearHoldIfKind(cfg.Home, t.ID, state.HoldKindLimit); err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: clear usage-limit hold on %s failed: %v\n", t.ID, err)
		return false
	}
	return true
}
