// Package watcher implements the poll loop behind hand watch: it tracks herdr
// agent states for active tasks, classifies transitions into actionable events,
// and keeps state/events.log current.
package watcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/notify"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
)

const maxEventLogLines = 200

var afterWatchTick = func() {}

type Config struct {
	Home           string
	PollInterval   time.Duration
	StaleThreshold time.Duration
	// Timeout bounds RunUntilEvent only. Zero blocks until an event arrives.
	Timeout      time.Duration
	ParkedBounds ParkedBounds
	// Bounds which kinds reach out, whichever writer that is: handleEvent applies it to every event on
	// the Run path as much as the RunUntilEvent one. Keeping Run unfiltered is cmd/watch.go's doing,
	// not this package's: it rejects --event without --until-event, so Run never gets a CLI filter.
	EventFilter EventFilter
}

var ErrNoEvent = errors.New("no event")

// ErrArmFailed names the one task whose pane could not be probed at arm time, so
// an exit from RunUntilEvent always means the whole fleet was actually watched.
var ErrArmFailed = errors.New("could not arm")

// ErrInterrupted is the typed result for a graceful watcher interruption that
// is not proven to be an explicit Hand takeover - ctrl-C, an externally
// delivered SIGTERM, or parent/command context cancellation. It maps to exit 8.
var ErrInterrupted = errors.New("watch interrupted")

// ErrReplaced is the typed result for an incumbent that received a valid explicit
// Hand takeover request through its generation-bound endpoint. A plain signal
// must never produce it. It maps to exit 9.
var ErrReplaced = errors.New("watch replaced by explicit takeover")

// Classifies a canceled context into the one typed lifecycle result that fits.
// Explicit replacement and the until-event timeout have distinct causes, while
// every other parent or command cancellation is an interruption.
func cancellationError(ctx context.Context) error {
	if errors.Is(context.Cause(ctx), ErrNoEvent) {
		return ErrNoEvent
	}
	if errors.Is(context.Cause(ctx), ErrReplaced) {
		return ErrReplaced
	}
	return ErrInterrupted
}

func contextLifecycleError(ctx context.Context) error {
	if ctx.Err() == nil {
		return nil
	}
	return cancellationError(ctx)
}

// Run blocks, polling herdr agent states at cfg.PollInterval until ctx is canceled, returning a
// typed lifecycle result for cancellation or an error if herdr is unreachable at startup. out
// receives the actionable event stream, while errOut receives internal diagnostics.
func Run(ctx context.Context, cfg Config, out, errOut io.Writer) error {
	client, err := connect(ctx)
	if err != nil {
		if lifecycleErr := contextLifecycleError(ctx); lifecycleErr != nil {
			return lifecycleErr
		}
		return err
	}

	states := make(map[string]*TaskState)
	tick(ctx, cfg, client, states, out, errOut)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return contextLifecycleError(ctx)
		case <-ticker.C:
			tick(ctx, cfg, client, states, out, errOut)
		}
	}
}

// RunUntilEvent blocks until a tick produces events, writes them to out and returns nil - the exit is
// the delivery, since it is the one signal a supervisory agent's background-task runner already
// honors.
func RunUntilEvent(ctx context.Context, cfg Config, out, errOut io.Writer) error {
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = withWatchTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	client, err := connect(ctx)
	if err != nil {
		if errors.Is(err, ErrNoEvent) {
			return fmt.Errorf("%w within %s: %w", ErrNoEvent, cfg.Timeout, err)
		}
		return err
	}

	if err := probeAllTasks(ctx, cfg.Home, client); err != nil {
		return err
	}

	states := make(map[string]*TaskState)
	// The startup state is never delivered: two ticks take a silent baseline first, so an already-done
	// worker is not mistaken for a fresh transition. Baseline events still reach events.log, just not
	// stdout, which is what io.Discard as out means here.
	tick(ctx, cfg, client, states, io.Discard, errOut)
	tick(ctx, cfg, client, states, io.Discard, errOut)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return runUntilEventContextError(ctx, cfg.Timeout)
		case <-ticker.C:
			var events bytes.Buffer
			tick(ctx, cfg, client, states, &events, errOut)
			afterWatchTick()
			if ctxErr := contextLifecycleError(ctx); ctxErr != nil {
				return runUntilEventContextError(ctx, cfg.Timeout)
			}
			if events.Len() == 0 {
				continue
			}
			_, err := out.Write(events.Bytes())
			return err
		}
	}
}

// Races the reachability probe against ctx because unbounded, a wedged herdr daemon blocks
// RunUntilEvent's --timeout from ever starting to count.
func connect(ctx context.Context) (*herdr.Client, error) {
	client := herdr.NewClient()
	done := make(chan error, 1)
	go func() { _, err := client.WorkspaceListContext(ctx); done <- err }()
	select {
	// Recheck lifecycle cause after the operation: a configured until-event timeout is ErrNoEvent,
	// while generic cancellation and proven takeover retain their own typed results.
	case <-ctx.Done():
		return nil, connectContextError(ctx)
	case err := <-done:
		if ctxErr := contextLifecycleError(ctx); ctxErr != nil {
			return nil, connectContextError(ctx)
		}
		if err != nil {
			return nil, fmt.Errorf("herdr unreachable: %w", err)
		}
		return client, nil
	}
}

// Confirms every running task's pane answers before RunUntilEvent arms; provisioning has no pane
// contract yet and is intentionally left for a later tick after launch confirmation.
func probeAllTasks(ctx context.Context, home string, client *herdr.Client) error {
	histories, err := state.ListOpenHistories(home)
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		for _, history := range histories {
			if history.ActiveAttempt == nil {
				done <- fmt.Errorf("%w: %s has no active attempt", ErrArmFailed, history.Task.ID)
				return
			}
			if history.ActiveAttempt.Lifecycle == state.AttemptProvisioning {
				continue
			}
			if _, err := client.PaneGetContext(ctx, history.ActiveAttempt.Herdr.PaneID); err != nil {
				done <- fmt.Errorf("%w: %s: %v", ErrArmFailed, history.Task.ID, err)
				return
			}
		}
		done <- nil
	}()
	select {
	// Recheck lifecycle cause after the operation so cancellation cannot be relabeled as an arm failure.
	case <-ctx.Done():
		return probeContextError(ctx)
	case err := <-done:
		if ctxErr := contextLifecycleError(ctx); ctxErr != nil {
			return probeContextError(ctx)
		}
		return err
	}
}

func connectContextError(ctx context.Context) error {
	if errors.Is(contextLifecycleError(ctx), ErrNoEvent) {
		return fmt.Errorf("%w: timed out reaching herdr", ErrNoEvent)
	}
	return fmt.Errorf("while reaching herdr: %w", cancellationError(ctx))
}

func probeContextError(ctx context.Context) error {
	if errors.Is(contextLifecycleError(ctx), ErrNoEvent) {
		return fmt.Errorf("%w: timed out probing tasks before arming", ErrNoEvent)
	}
	return fmt.Errorf("while probing tasks before arming: %w", cancellationError(ctx))
}

func withWatchTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	timer := time.AfterFunc(timeout, func() { cancel(ErrNoEvent) })
	return ctx, func() {
		timer.Stop()
		cancel(nil)
	}
}

func runUntilEventContextError(ctx context.Context, timeout time.Duration) error {
	lifecycleErr := contextLifecycleError(ctx)
	if errors.Is(lifecycleErr, ErrNoEvent) {
		return fmt.Errorf("%w within %s", ErrNoEvent, timeout)
	}
	return fmt.Errorf("%w", lifecycleErr)
}

func tick(ctx context.Context, cfg Config, client *herdr.Client, states map[string]*TaskState, out, errOut io.Writer) {
	histories, err := state.ListOpenHistories(cfg.Home)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: list tasks failed: %v\n", err)
		return
	}

	seen := make(map[string]bool, len(histories))
	now := time.Now()
	for _, history := range histories {
		if ctx.Err() != nil {
			return
		}
		t := history.Task
		if history.ActiveAttempt == nil {
			_, _ = fmt.Fprintf(errOut, "watch: task %s has no active attempt\n", t.ID)
			continue
		}
		attempt := *history.ActiveAttempt
		seen[t.ID] = true
		if attempt.Lifecycle == state.AttemptProvisioning {
			continue
		}
		pane, probeErr := client.PaneGetContext(ctx, attempt.Herdr.PaneID)
		if ctx.Err() != nil {
			return
		}
		status := pane.AgentStatus

		// Tracking is keyed by task identity, not by ID: a reopen or a promote gives the same ID a new
		// attempt, and a new attempt is a new execution identity. Same hazard as a surviving report
		// channel, one layer in.
		ts, tracked := states[t.ID]
		// Inheriting the previous run's TaskState would suppress the new task's verified done forever -
		// syncTaskState writes that inherited done_verified onto the fresh row, making the suppression
		// durable - and absorb its first unexplained stop.
		if tracked && (ts.CreatedAt != t.CreatedAt || ts.AttemptID != t.ActiveAttemptID || ts.AttemptLifecycle != attempt.Lifecycle) {
			tracked = false
		}
		if !tracked {
			// herdr.StatusUnknown stands in for "no real status observed yet", so the eventual recovery
			// reads as an ordinary transition rather than inventing a prior status.
			if probeErr != nil {
				status = herdr.StatusUnknown
			}
			ts = resumeTaskState(t, attempt, status, now)
			if err := restoreLimitResumeState(cfg.Home, ts, attempt, errOut); err != nil {
				_, _ = fmt.Fprintf(errOut, "watch: restore usage-limit resume state for %s failed: %v\n", t.ID, err)
			}
			// False starts ClassifyUnreachable's dwell clock immediately, instead of waiting for a second
			// failed probe to notice this task at all.
			ts.Probed = probeErr == nil
			states[t.ID] = ts
			continue
		}

		forgetPaneScopedCache(ts, t, attempt, now)

		t = tailReport(ctx, cfg, ts, t, out, errOut)

		// Read before ClassifyStatus consumes the transition, which is the edge the
		// usage-limit check reads a pane on.
		justStopped := status.NotBusy() && status != ts.Status

		if e := ClassifyStatus(ts, t.ID, status, probeErr, now); e != nil {
			handleEvent(cfg, e, out, errOut)
		}
		if e := ClassifyUnreachable(ts, t.ID, now, cfg.StaleThreshold); e != nil {
			handleEvent(cfg, e, out, errOut)
		}
		if e := ClassifyStale(ts, t.ID, now, cfg.StaleThreshold); e != nil {
			handleEvent(cfg, e, out, errOut)
		}
		if t.PR != "" && !ts.PRMerged {
			ghCtx, ghCancel := context.WithTimeout(ctx, 30*time.Second)
			merged, err := ghutil.PRIsMerged(ghCtx, t.PR)
			ghCancel()
			if err == nil {
				if e := ClassifyPRMerged(ts, t.ID, merged); e != nil {
					handleEvent(cfg, e, out, errOut)
				}
			}
		}
		if e := ClassifyDeferredDone(cfg.Home, ts, t); e != nil {
			handleEvent(cfg, e, out, errOut)
		}
		if mtime, err := reportEvidenceTime(cfg.Home, t, attempt); err != nil {
			_, _ = fmt.Fprintf(errOut, "watch: stat report %s failed: %v\n", t.ID, err)
		} else if e := ClassifyParked(ts, t.ID, ts.LastReportState, lastReportLine(ts), mtime, now, cfg.ParkedBounds); e != nil {
			handleEvent(cfg, e, out, errOut)
		}
		if e := classifyUsageLimit(cfg, client, ts, t, attempt, pane, status, probeErr, justStopped, now, errOut); e != nil {
			handleEvent(cfg, e, out, errOut)
		}
		// Last, after every event this tick produced has been announced: a marker persisted before its
		// line is emitted would, if the process died in between, suppress an announcement nothing can
		// re-derive. A duplicate line is a far cheaper failure than a silently dropped one.
		syncTaskState(cfg.Home, t.ID, ts, now, errOut)
	}

	for id := range states {
		if !seen[id] {
			delete(states, id)
		}
	}
}

func restoreLimitResumeState(home string, ts *TaskState, attempt state.Attempt, errOut io.Writer) error {
	send, found, err := state.LatestSendMetadata(home, attempt.TaskID, attempt.ID, state.SendOriginUsageLimitResume)
	if err != nil || !found {
		return err
	}
	if !resumeSendBelongsToActiveEpisode(send, attempt) {
		return nil
	}
	if state.SendRetrySafe(send) {
		return nil
	}
	ts.LimitResumeBlocked = true
	ts.LimitRetryAt = time.Time{}
	if send.State == state.SendPending {
		_, _ = fmt.Fprintf(errOut, "watch: usage-limit resume send %d remains pending; automatic resend disabled\n", send.ID)
		if err := normalizePendingResume(home, attempt, errOut); err != nil {
			return err
		}
	}
	return nil
}

func normalizePendingResume(home string, attempt state.Attempt, errOut io.Writer) error {
	releaseSend, err := state.TryLock(home, "send:"+attempt.TaskID)
	if err != nil {
		if errors.Is(err, state.ErrLockBusy) {
			return nil
		}
		return fmt.Errorf("lock send %s while recovering pending resume: %w", attempt.TaskID, err)
	}
	defer releaseSend()
	releaseTask, err := state.TryLock(home, "task:"+attempt.TaskID)
	if err != nil {
		if errors.Is(err, state.ErrLockBusy) {
			return nil
		}
		return fmt.Errorf("lock task %s while recovering pending resume: %w", attempt.TaskID, err)
	}
	defer releaseTask()
	history, err := state.ReadHistory(home, attempt.TaskID)
	if err != nil {
		return fmt.Errorf("read task %s while recovering pending resume: %w", attempt.TaskID, err)
	}
	current := history.ActiveAttempt
	if history.Task.Lifecycle != state.TaskOpen || current == nil || current.ID != attempt.ID || current.TaskID != attempt.TaskID || current.Lifecycle != state.AttemptRunning || current.Herdr != attempt.Herdr {
		return nil
	}
	pending, found, err := state.PendingSend(home, attempt.TaskID, attempt.ID)
	if err != nil {
		return fmt.Errorf("read pending resume for task %s: %w", attempt.TaskID, err)
	}
	if !found || pending.Origin != state.SendOriginUsageLimitResume {
		return nil
	}
	_, changed, err := state.NormalizePendingSend(home, pending.ID, attempt.TaskID, attempt.ID, "stale-pending-recovered", time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("recover pending resume send %d: %w", pending.ID, err)
	}
	if changed {
		_, _ = fmt.Fprintf(errOut, "watch: usage-limit resume send %d recovered as uncertain; automatic resend disabled\n", pending.ID)
	}
	return nil
}

func resumeSendBelongsToActiveEpisode(send state.SendAttempt, attempt state.Attempt) bool {
	if attempt.UsageLimitRetryAt == "" {
		return false
	}
	if attempt.UsageLimitEpisode == 0 {
		return send.UsageLimitEpisode == 0
	}
	return send.UsageLimitEpisode == attempt.UsageLimitEpisode
}

// Every restored fact comes from durable state: re-deriving what landed while the watcher was down
// would make new evidence look like an announcement that already went out.
func resumeTaskState(t state.Task, a state.Attempt, status herdr.Status, now time.Time) *TaskState {
	changedAt := statusChangeSeed(t, a, status, now)
	ts := NewTaskState(status, changedAt)
	ts.PersistedChangedAt = changedAt
	ts.PersistedChangedFor = a.StatusChangedFor
	ts.PersistedPaneID = a.Herdr.PaneID
	ts.CreatedAt = t.CreatedAt
	ts.AttemptID = t.ActiveAttemptID
	ts.AttemptLifecycle = a.Lifecycle
	ts.ReportCursor = state.ReportCursor{Offset: t.ReportOffset, Digest: t.ReportDigest}
	ts.PersistedCursor = ts.ReportCursor
	ts.PRMerged = t.MergeAnnounced
	ts.PersistedPRMerged = t.MergeAnnounced
	ts.DoneVerified = a.DoneVerified
	ts.PersistedDoneVerified = a.DoneVerified
	ts.LastReportState = a.LastReportState
	ts.LastReportNote = a.LastReportNote
	ts.ParkedFiredFor = parkedFiredSeed(a)
	ts.PersistedParkedFiredFor = ts.ParkedFiredFor
	ts.LimitRetryAt = limitRetrySeed(a)
	ts.PersistedLimitRetryAt = ts.LimitRetryAt
	ts.LimitAttempts = a.UsageLimitAttempts
	ts.PersistedLimitAttempts = ts.LimitAttempts
	ts.LimitEpisode = a.UsageLimitEpisode
	ts.PersistedLimitEpisode = ts.LimitEpisode
	ts.LimitStuckEpisode = a.UsageLimitStuckEpisode
	ts.PersistedLimitStuckEpisode = ts.LimitStuckEpisode
	return ts
}

// Reads back the instant a limited worker may next be tried. An unparseable stamp seeds unlimited,
// losing the schedule rather than inventing one: the next stop edge or first probe re-detects the limit
// from the pane, whereas a stamp guessed at here would drive real steers off a value nothing wrote.
func limitRetrySeed(a state.Attempt) time.Time {
	parsed, err := time.Parse(time.RFC3339, a.UsageLimitRetryAt)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func limitRetryStamp(retryAt time.Time) string {
	if retryAt.IsZero() {
		return ""
	}
	return retryAt.UTC().Format(time.RFC3339)
}

// Reads back the silence instant parked last fired against. An unparseable stamp seeds unfired
// rather than failing the resume: one duplicate event is the same failure direction the whole
// classifier already prefers over a suppressed one.
func parkedFiredSeed(a state.Attempt) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, a.ParkedFiredFor)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// Nanosecond precision because the value is a report file's mtime compared for exact equality, and
// RFC3339's whole seconds would round it down into an instant no later mtime ever matches -
// re-firing on every restart, which is the bug.
func parkedFiredStamp(fired time.Time) string {
	if fired.IsZero() {
		return ""
	}
	return fired.UTC().Format(time.RFC3339Nano)
}

// Drops the cached facts hand promote invalidated: it gives the task a new herdr pane while keeping
// created_at, so tick's identity check never fires, yet every pane-anchored fact cached here
// describes a pane the task no longer has.
func forgetPaneScopedCache(ts *TaskState, t state.Task, a state.Attempt, now time.Time) {
	// Compared against the persisted mirror, not the live flag: the watcher only ever sets the flag
	// true, so a disk value gone false since this watcher wrote true is such a rewrite - while a flag
	// set true earlier in this very tick is simply not persisted yet, which syncTaskState's OR fixes.
	if !a.DoneVerified && ts.PersistedDoneVerified {
		ts.DoneVerified = false
		ts.PersistedDoneVerified = false
	}
	// The trigger is the pane changing, not the newly observed status differing - a ship whose first
	// probe reads the status the scout last held raises no transition at all - and not a restamped
	// timestamp, too eager (a resume reseeds the dwell) and too blunt (same-second restamps vanish).
	if a.Herdr.PaneID == ts.PersistedPaneID {
		return
	}
	ts.PersistedPaneID = a.Herdr.PaneID
	seed := statusChangeSeed(t, a, ts.Status, now)
	ts.ChangedAt = seed
	ts.PersistedChangedAt = seed
	ts.PersistedChangedFor = a.StatusChangedFor
	// Reset after the seed above, which asks what status the cached dwell describes. StatusUnknown
	// matches neither the working nor the blocked branch, so a ship's first probe reads as the
	// baseline a first sighting always is rather than as a transition out of the scout's status.
	ts.Status = herdr.StatusUnknown
	ts.Stale = false
	ts.Blocked = false
	// False, exactly as tick seeds a task first sighted with an unreachable pane: an unreachable first
	// probe of the new pane dwells under ClassifyUnreachable's threshold instead of firing `failed` on
	// sight, which the old pane's true would do off a blink, on a probe describing the scout's pane.
	ts.Probed = false
	// A latch claiming the old pane's outage has nothing to say about the new one. Left true it would
	// sit inert until the next probe failure reset Probed anyway, but a fresh pane deserves a fresh
	// episode on purpose, not by accident of that ordering.
	ts.UnreachableFired = false
	// A usage limit is the harness's, and the new pane runs a new harness process with its own quota
	// state. Carrying the schedule over would steer the fresh pane on a clock the scout's refusal set.
	ts.LimitRetryAt = time.Time{}
	ts.LimitAttempts = 0
	ts.LimitResumeBlocked = false
	ts.LimitEpisode = 0
	ts.LimitStuckEpisode = 0
	ts.LimitProbed = false
	// Re-read from the promoted row rather than zeroed alongside the rest, so the columns get written
	// clear here in the one case hand promote did not already clear them itself.
	ts.PersistedLimitRetryAt = limitRetrySeed(a)
	ts.PersistedLimitAttempts = a.UsageLimitAttempts
	ts.PersistedLimitEpisode = a.UsageLimitEpisode
	ts.PersistedLimitStuckEpisode = a.UsageLimitStuckEpisode
	ts.LastReportState = a.LastReportState
	ts.LastReportNote = a.LastReportNote
}

// Classifies whatever report lines have arrived since ts.ReportCursor, before ClassifyStatus runs
// for this tick, so a report landing in the same poll as a herdr idle transition is already
// reflected in ts.LastReportState when the idle-vs-idle-unreported decision is made.
func tailReport(ctx context.Context, cfg Config, ts *TaskState, t state.Task, out, errOut io.Writer) state.Task {
	path := state.ReportPath(cfg.Home, t.ID)
	lines, cursor, err := state.TailReport(path, ts.ReportCursor)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: tail report %s failed: %v\n", t.ID, err)
		return t
	}

	for _, line := range lines {
		if e := ClassifyReportLine(cfg.Home, ts, t, line); e != nil {
			handleEvent(cfg, e, out, errOut)
		}
		// A line carrying exactly one embedded PR URL auto-records it, subject to the same validation
		// hand pr enforces; more than one URL, or a PR already on record, is left alone.
		if t.PR == "" {
			if urls := state.FindPRURLs(line.Raw); len(urls) == 1 {
				if err := autoRecordPR(ctx, cfg.Home, t, urls[0]); err != nil {
					announceAutoRecordFailure(cfg, t, urls[0], err, out, errOut)
				} else {
					t.PR = urls[0]
				}
			}
		}
	}

	ts.ReportCursor = cursor
	return t
}

// Answers how long a task has already been dwelling in status, so a restart does not reset a dwell
// that has been real all along.

func statusChangeSeed(t state.Task, a state.Attempt, status herdr.Status, now time.Time) time.Time {
	// StatusChangedAt is only evidence about the status it was stamped for.
	if a.StatusChangedAt != "" {
		if a.StatusChangedFor != string(status) {
			return now
		}
		if parsed, err := time.Parse(time.RFC3339, a.StatusChangedAt); err == nil {
			return parsed
		}
	}
	// A task with no observed transition at all has been dwelling since CreatedAt.
	if parsed, err := time.Parse(time.RFC3339, t.CreatedAt); err == nil {
		return parsed
	}
	return now
}

// Floors the report file's mtime at the instant the task's current pane started, because hand
// promote leaves the scout's report file - and so its mtime - untouched while clearing the
// last-report state that had the scout's silence under the long done/failed bound.
func reportEvidenceTime(home string, t state.Task, a state.Attempt) (time.Time, error) {
	started, err := paneStartTime(t, a)
	if err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(state.ReportPath(home, t.ID))
	if err != nil {
		if !os.IsNotExist(err) {
			return time.Time{}, err
		}
		return started, nil
	}
	// Unfloored, a ship seconds old inherits the scout's whole silence, now measured against the short
	// bound, and fires parked immediately.
	if mtime := info.ModTime(); mtime.After(started) {
		return mtime, nil
	}
	return started, nil
}

// When the task's current pane started, as spawn and hand promote each recorded it. Deliberately no
// longer StatusChangedAt, which the outage-dwell clock restamps for a pane it could not even reach,
// sliding this floor forward by up to a full bound of real report silence.
func paneStartTime(t state.Task, a state.Attempt) (time.Time, error) {
	stamp, field := a.PaneStartedAt, "pane_started_at"
	// The schema migration and the legacy import both backfill the column, so an empty stamp means a
	// row nothing in hand wrote, and CreatedAt is the honest floor for it.
	if stamp == "" {
		stamp, field = t.CreatedAt, "created_at"
	}
	parsed, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s %q: %w", field, stamp, err)
	}
	return parsed, nil
}

func lastReportLine(ts *TaskState) string {
	if ts.LastReportState == "" {
		return "no report"
	}
	return fmt.Sprintf("%s: %s", ts.LastReportState, ts.LastReportNote)
}

// Makes an auto-record that did not happen a durable lifecycle fact on the event stream and in
// events.log, alongside the stderr diagnostic: the line is consumed and the offset moves on either
// way, so an unrecorded URL that only reached a long-running watcher's stderr would be lost.
func announceAutoRecordFailure(cfg Config, t state.Task, url string, err error, out, errOut io.Writer) {
	// Deliberately not a Pending Decision: that slot holds the worker's own question, and upserting by
	// task ID there would erase one.
	kind, outcome := KindPRNotRecorded, "failed"
	// The kind is the outcome, the token an operator greps events.log for: an attempt that did not
	// complete is pr-not-recorded whatever stopped it, while losing the task lock leaves the outcome
	// unknown and must not claim otherwise.
	if errors.Is(err, errLockContended) {
		kind, outcome = KindPRRecordUnknown, "skipped"
	}
	_, _ = fmt.Fprintf(errOut, "watch: auto-record PR for %s %s: %v\n", t.ID, outcome, err)
	// The kind says only that much; the appended error text is what says why, so neither has to stand
	// in for the other.
	handleEvent(cfg, &Event{
		TaskID: t.ID,
		Kind:   kind,
		Text:   fmt.Sprintf("%s %s: %s (%s)", kind, t.ID, url, flattenError(err)),
		Reason: url,
	}, out, errOut)
}

// Renders err's whole cause on one line: an event is one line on stdout and one entry in events.log,
// while the errors reaching here wrap gh's stderr verbatim, routinely multi-line for auth and
// network failures. The stderr diagnostic above keeps the original formatting.
func flattenError(err error) string {
	var parts []string
	for _, line := range strings.FieldsFunc(err.Error(), func(r rune) bool { return r == '\n' || r == '\r' }) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, "; ")
}

// Routes a worker-supplied URL through hand pr's own validation before it can reach task state: a
// URL that survives here is what `hand merge` later hands to `gh pr merge`, so a PR belonging to
// some other repo the worker merely mentioned must be refused, not recorded.
func autoRecordPR(ctx context.Context, home string, t state.Task, url string) error {
	proj, exists, err := project.Find(home, t.Project)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("project %q not registered", t.Project)
	}

	ghCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := project.ValidatePR(ghCtx, home, proj, url); err != nil {
		return err
	}
	return recordAutoPR(home, t.ID, url)
}

// Race-safe against a concurrent explicit `hand pr` call or another tick's auto-record.
func recordAutoPR(home, id, url string) error {
	// The task lock is non-blocking for the same reason syncTaskState's is: this runs in the poll loop,
	// which owes every other task a timely tick and its own ctx a prompt exit flock cannot honor,
	// while hand merge and hand promote hold the same task lock across gh and git round-trips.
	unlock, err := state.TryLock(home, "task:"+id)
	if err != nil {
		// Denied the lock, it re-reads anyway: the likeliest holder is the `hand pr` recording this very
		// URL, and treating that as a failed auto-record would announce a problem that fixed itself.
		if errors.Is(err, state.ErrLockBusy) {
			return recordedByLockHolder(home, id, url)
		}
		return fmt.Errorf("lock task %s: %w", id, err)
	}
	defer unlock()

	t, err := state.Read(home, id)
	if err != nil {
		return fmt.Errorf("read task %s: %w", id, err)
	}
	// Holding the lock, the "already recorded" no-op re-reads and no-ops if the PR arrived first.
	if t.PR != "" {
		return nil
	}
	if err := state.SetTaskPR(home, id, url); err != nil {
		return fmt.Errorf("write task %s: %w", id, err)
	}
	return nil
}

// Marks an auto-record the watcher declined rather than attempted, so callers can tell "refused this
// URL" from "never got to try".
var errLockContended = errors.New("task locked by another process")

// Decides what a lost race to the task lock means.
func recordedByLockHolder(home, id, url string) error {
	t, err := state.Read(home, id)
	if err != nil {
		// A task whose state will not read says exactly that, instead of sending the operator to hand
		// status, which reads the same unreadable file.
		return fmt.Errorf("%w, and its state could not be read: %v", errLockContended, err)
	}
	// The URL already on record means the holder did the work, so there is nothing to say.
	if t.PR == url {
		return nil
	}
	// Otherwise the outcome is genuinely unknown - the holder may be mid-write - and the report line is
	// consumed either way, so this stays loud and says only that, rather than claiming a failure or
	// naming a remedy that may be a no-op.
	return fmt.Errorf("%w that may be recording it - confirm with: hand status %s", errLockContended, id)
}

// Writes back the bookkeeping hand watch owns on a task - how far its report file is consumed,
// whether this watcher's own gh poll announced the PR merged, whether the verified done went out - so
// a restart neither replays report lines nor re-announces, nor skips, what it cannot re-derive.
func syncTaskState(home, id string, ts *TaskState, now time.Time, errOut io.Writer) {
	if ts.ReportCursor == ts.PersistedCursor && ts.PRMerged == ts.PersistedPRMerged &&
		ts.DoneVerified == ts.PersistedDoneVerified && ts.ChangedAt.Equal(ts.PersistedChangedAt) &&
		ts.PersistedChangedFor == string(ts.Status) && ts.ParkedFiredFor.Equal(ts.PersistedParkedFiredFor) &&
		ts.LimitRetryAt.Equal(ts.PersistedLimitRetryAt) && ts.LimitAttempts == ts.PersistedLimitAttempts &&
		ts.LimitEpisode == ts.PersistedLimitEpisode && ts.LimitStuckEpisode == ts.PersistedLimitStuckEpisode {
		return
	}

	// Non-blocking on purpose: hand merge holds the same task lock across gh round-trips, and the poll
	// loop - which owes every other task a timely tick, and its own ctx a prompt exit flock cannot
	// honor - must not queue behind it. Everything written here is re-derivable, so a skip retries.
	unlock, err := state.TryLock(home, "task:"+id)
	if err != nil {
		if !errors.Is(err, state.ErrLockBusy) {
			_, _ = fmt.Fprintf(errOut, "watch: lock task %s failed: %v\n", id, err)
		}
		return
	}
	defer unlock()

	t, err := state.Read(home, id)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: read task %s failed: %v\n", id, err)
		return
	}
	// The report cursor is task-owned and lands before the attempt is resolved: dropping it because a
	// task terminalized mid-tick would replay lines this watcher already consumed, which re-raises
	// resolved decisions and can auto-record a stale PR URL.
	if err := state.SetTaskReportState(home, id, ts.ReportCursor.Offset, ts.ReportCursor.Digest, ts.PRMerged); err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: persist task %s failed: %v\n", id, err)
		return
	}
	ts.PersistedCursor = ts.ReportCursor
	ts.PersistedPRMerged = ts.PRMerged

	active, found, err := state.ReadAttempt(home, ts.AttemptID)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: read active attempt %s failed: %v\n", id, err)
		return
	}
	if !found || active.TaskID != id || t.ActiveAttemptID != ts.AttemptID || active.Lifecycle != ts.AttemptLifecycle {
		_, _ = fmt.Fprintf(errOut, "watch: read active attempt %s failed: attempt ownership changed\n", id)
		return
	}
	if !ts.LimitRetryAt.IsZero() || ts.LimitResumeBlocked {
		_ = writeLimitHoldForOwnedAttempt(Config{Home: home}, t, active, ts, errOut)
	} else if !ts.PersistedLimitRetryAt.IsZero() || ts.PersistedLimitAttempts != 0 {
		_ = clearLimitHoldForOwnedAttempt(Config{Home: home}, t, active, errOut)
	}
	// A promote may have landed since this tick's state.List. Writing the cached values back would
	// erase its restamp and leave the disk value matching what this watcher persisted, so no later
	// tick would find anything to forget either.
	forgetPaneScopedCache(ts, t, active, now)
	if err := state.UpdateAttemptObservation(home, id, ts.AttemptID, ts.AttemptLifecycle,
		ts.ChangedAt.UTC().Format(time.RFC3339Nano), string(ts.Status), ts.DoneVerified,
		ts.LastReportState, ts.LastReportNote, parkedFiredStamp(ts.ParkedFiredFor),
		limitRetryStamp(ts.LimitRetryAt), ts.LimitAttempts, ts.LimitEpisode, ts.LimitStuckEpisode); err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: persist attempt %s failed: %v\n", id, err)
		return
	}
	ts.PersistedDoneVerified = ts.DoneVerified
	ts.PersistedChangedAt = ts.ChangedAt
	ts.PersistedChangedFor = string(ts.Status)
	ts.PersistedParkedFiredFor = ts.ParkedFiredFor
	ts.PersistedLimitRetryAt = ts.LimitRetryAt
	ts.PersistedLimitAttempts = ts.LimitAttempts
	ts.PersistedLimitEpisode = ts.LimitEpisode
	ts.PersistedLimitStuckEpisode = ts.LimitStuckEpisode
}

// EventFilter gates only the out write - events.log and the notify hook both
// run unconditionally, so narrowing --event never narrows what reaches
// config/notify.
func handleEvent(cfg Config, e *Event, out, errOut io.Writer) {
	if cfg.EventFilter.Matches(e.Kind) {
		_, _ = fmt.Fprintln(out, e.Text)
	}

	logPath := filepath.Join(state.Dir(cfg.Home), "events.log")
	if err := appendEventLog(logPath, time.Now().UTC().Format(time.RFC3339)+" "+e.Text); err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: append events.log failed: %v\n", err)
	}

	notifyEvent(cfg.Home, e, errOut)
}

// The unattended hook treats an absent config as disabled, while a configured
// channel that fails is a diagnostic worth surfacing.
func notifyEvent(home string, e *Event, errOut io.Writer) {
	if !NotifyFilter().Matches(e.Kind) {
		return
	}
	if err := notify.Send(home, e.Text); err != nil && !errors.Is(err, notify.ErrNotConfigured) {
		_, _ = fmt.Fprintf(errOut, "watch: notify failed: %v\n", err)
	}
}

func appendEventLog(path, line string) error {
	lines, err := readLines(path)
	if err != nil {
		return err
	}
	lines = append(lines, line)
	if len(lines) > maxEventLogLines {
		lines = lines[len(lines)-maxEventLogLines:]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create events log directory: %w", err)
	}
	if err := atomicfile.Write(path, ".events.log-", []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write events log: %w", err)
	}
	return nil
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read events log: %w", err)
	}
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}
