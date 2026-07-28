// Package watcher implements the poll loop behind hand watch: it tracks herdr
// agent states for active tasks, classifies transitions into actionable events,
// and keeps state/events.log and data/dashboard.md current.
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

	"github.com/atqamz/secondhand/internal/atomicfile"
	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/ghutil"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
)

const maxEventLogLines = 200

type Config struct {
	Home           string
	PollInterval   time.Duration
	StaleThreshold time.Duration
	// Timeout bounds RunUntilEvent only. Zero blocks until an event arrives.
	Timeout time.Duration
	// ParkedBounds bounds how long a task may sit silent, split by its last
	// report, before ClassifyParked flags it. See events.go.
	ParkedBounds ParkedBounds
}

// ErrNoEvent reports a RunUntilEvent watcher that stopped without delivering an
// event: its timeout elapsed, or it was signaled. A caller has to tell that
// apart from both a delivered event and a watcher that failed, or a re-arm loop
// silently stalls on one and treats the other as fleet news.
var ErrNoEvent = errors.New("no event")

// ErrArmFailed reports a RunUntilEvent that refused to start because it could
// not probe every active task's pane. A watcher must never look armed for a
// worker it cannot actually see: the ordinary per-tick "!tracked" path in tick
// tolerates a probe failure by silently skipping that task for a cycle, which is
// the right call for the streaming watcher's documented graceful-degradation
// philosophy, but wrong for a mode whose whole contract is "the exit means the
// fleet was watched." Full per-tick probe-failure tracking and any retry policy
// for the life of the watch is out of scope here - see the arm-time check in
// RunUntilEvent.
var ErrArmFailed = errors.New("could not arm")

// Run blocks, polling herdr agent states at cfg.PollInterval until ctx is
// canceled. It returns nil on clean cancellation, or an error if herdr is
// unreachable at startup. out receives the actionable event stream documented
// in SPECS.md; errOut receives internal diagnostics (list/log/dashboard
// failures), keeping the two streams separable per the stdout/stderr contract.
func Run(ctx context.Context, cfg Config, out, errOut io.Writer) error {
	client, err := connect()
	if err != nil {
		return err
	}

	states := make(map[string]*TaskState)
	tick(ctx, cfg, client, states, out, errOut)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			tick(ctx, cfg, client, states, out, errOut)
		}
	}
}

// RunUntilEvent blocks until the poll loop produces its first events, writes
// that tick's output to out, and returns nil. The process exit is the delivery:
// it is the one signal a supervisory agent's background-task runner already
// honors, so waking it needs no pipeline the caller can get wrong.
//
// The fleet state at startup is never delivered. Two ticks take the baseline
// with stdout discarded: the first seeds tracking for every task, the second
// consumes whatever a previous watcher left unconsumed on the report channels,
// which resumeTaskState deliberately does not fast-forward past. Only a change
// from that baseline can exit. A grep-for-the-first-line wrapper is what this
// replaces, and it failed exactly here - a worker that was already done printed
// a matching line on startup, the grep took it for a transition, and the two
// real events that followed reached nobody.
//
// Baseline events still reach state/events.log and the dashboard, since the
// report lines behind them are consumed either way and dropping them silently
// would lose the state change. So an agent arming a watcher reads
// state/events.log or hand status for current truth, and takes this exit as the
// answer to what changed since it armed.
//
// It returns ErrNoEvent when cfg.Timeout elapses or ctx is canceled first, and
// an error only when the watcher itself failed.
func RunUntilEvent(ctx context.Context, cfg Config, out, errOut io.Writer) error {
	client, err := connect()
	if err != nil {
		return err
	}

	if err := probeAllTasks(cfg.Home, client); err != nil {
		return err
	}

	states := make(map[string]*TaskState)
	tick(ctx, cfg, client, states, io.Discard, errOut)
	tick(ctx, cfg, client, states, io.Discard, errOut)

	var timeout <-chan time.Time
	if cfg.Timeout > 0 {
		timer := time.NewTimer(cfg.Timeout)
		defer timer.Stop()
		timeout = timer.C
	}

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: interrupted", ErrNoEvent)
		case <-timeout:
			return fmt.Errorf("%w within %s", ErrNoEvent, cfg.Timeout)
		case <-ticker.C:
			var events bytes.Buffer
			tick(ctx, cfg, client, states, &events, errOut)
			if events.Len() == 0 {
				continue
			}
			_, err := out.Write(events.Bytes())
			return err
		}
	}
}

func connect() (*herdr.Client, error) {
	client := herdr.NewClient()
	if _, err := client.WorkspaceList(); err != nil {
		return nil, fmt.Errorf("herdr unreachable: %w", err)
	}
	return client, nil
}

// probeAllTasks confirms every active task's pane answers before RunUntilEvent
// arms, so it never sits waiting on a worker it can never actually observe. The
// streaming Run tolerates a probe failure per tick and moves on; RunUntilEvent
// cannot, because its whole contract is that the exit reports what the fleet
// did, and a task it never managed to probe would otherwise wait silently until
// the timeout (or forever) with no distinguishing signal from a real timeout.
func probeAllTasks(home string, client *herdr.Client) error {
	tasks, err := state.List(home)
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}
	for _, t := range tasks {
		if _, err := client.PaneGet(t.Herdr.PaneID); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrArmFailed, t.ID, err)
		}
	}
	return nil
}

func tick(ctx context.Context, cfg Config, client *herdr.Client, states map[string]*TaskState, out, errOut io.Writer) {
	tasks, err := state.List(cfg.Home)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: list tasks failed: %v\n", err)
		return
	}

	seen := make(map[string]bool, len(tasks))
	now := time.Now()
	for _, t := range tasks {
		if ctx.Err() != nil {
			return
		}
		seen[t.ID] = true
		pane, probeErr := client.PaneGet(t.Herdr.PaneID)
		status := pane.AgentStatus

		// Tracking is keyed by task identity, not by ID: an ID torn down and
		// respawned between two ticks is a different task, and inheriting the
		// previous run's TaskState would suppress the new task's verified done
		// forever - syncTaskState writes that inherited done_verified onto the fresh
		// JSON, making the suppression durable - and absorb its first unexplained
		// stop. Same hazard as a surviving report channel, one layer in; see
		// state.Delete for that half.
		ts, tracked := states[t.ID]
		if tracked && ts.CreatedAt != t.CreatedAt {
			tracked = false
		}
		if !tracked {
			if probeErr != nil {
				continue
			}
			states[t.ID] = resumeTaskState(cfg.Home, t, status, now, errOut)
			continue
		}

		// A verified-done marker only ever regresses from true to false on disk
		// through a rewrite outside the watcher - the watcher itself only ever sets
		// it true. hand promote clearing it for a task's new ship run is that
		// rewrite, and CreatedAt is unchanged so the identity check above doesn't
		// catch it. Forget the cached copy too, or syncTaskState's OR would
		// resurrect the marker promote just cleared.
		if !t.DoneVerified && ts.DoneVerified {
			ts.DoneVerified = false
			ts.PersistedDoneVerified = false
		}

		t = tailReport(ctx, cfg, ts, t, out, errOut)

		if e := ClassifyStatus(ts, t.ID, status, probeErr, now); e != nil {
			handleEvent(cfg, e, t, out, errOut)
		}
		if e := ClassifyStale(ts, t.ID, now, cfg.StaleThreshold); e != nil {
			handleEvent(cfg, e, t, out, errOut)
		}
		if t.PR != "" && !ts.PRMerged {
			ghCtx, ghCancel := context.WithTimeout(ctx, 30*time.Second)
			merged, err := ghutil.PRIsMerged(ghCtx, t.PR)
			ghCancel()
			if err == nil {
				if e := ClassifyPRMerged(ts, t.ID, merged); e != nil {
					handleEvent(cfg, e, t, out, errOut)
				}
			}
		}
		if e := ClassifyDeferredDone(cfg.Home, ts, t); e != nil {
			handleEvent(cfg, e, t, out, errOut)
		}
		if mtime, err := reportEvidenceTime(cfg.Home, t); err != nil {
			_, _ = fmt.Fprintf(errOut, "watch: stat report %s failed: %v\n", t.ID, err)
		} else if e := ClassifyParked(ts, t.ID, ts.LastReportState, lastReportLine(ts), mtime, now, cfg.ParkedBounds); e != nil {
			handleEvent(cfg, e, t, out, errOut)
		}
		syncTaskState(cfg.Home, t.ID, ts, errOut)
	}

	for id := range states {
		if !seen[id] {
			delete(states, id)
		}
	}
}

// resumeTaskState picks up where a previous hand watch left off rather than
// starting cold. Every fact the watcher announces comes back from the task's
// durable state, never re-derived from current evidence: the report offset, the
// merge its own gh poll announced, and the verified done. Re-deriving any of
// them lets evidence that landed while the watcher was down (hand merge writing
// merged, say) look like an announcement that already went out.
//
// What is deliberately re-derived, and why that is safe, is enumerated in
// SPECS.md's "What survives a hand watch restart". The last reported state is
// one of them: it is re-read from the report file - itself durable - so a pane
// found not-busy after a restart isn't mistaken for an unexplained stop. It is the
// last line that classified, not simply the last line, so a trailing malformed one
// doesn't erase what the worker did report, exactly as the live path refuses to. An
// unreadable report is reported as such, never quietly treated as "this worker
// never reported".
func resumeTaskState(home string, t state.Task, status herdr.Status, now time.Time, errOut io.Writer) *TaskState {
	changedAt := statusChangeSeed(t, now)
	ts := NewTaskState(status, changedAt)
	ts.PersistedChangedAt = changedAt
	ts.CreatedAt = t.CreatedAt
	ts.ReportOffset = t.ReportOffset
	ts.PersistedOffset = t.ReportOffset
	ts.PRMerged = t.MergeAnnounced
	ts.PersistedPRMerged = t.MergeAnnounced
	ts.DoneVerified = t.DoneVerified
	ts.PersistedDoneVerified = t.DoneVerified

	lines, err := state.ReadReportLines(home, t.ID)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: read report for %s failed: %v\n", t.ID, err)
		return ts
	}
	if last, ok := state.LastReportedState(lines); ok {
		ts.LastReportState = last.State
		ts.LastReportNote = last.Note
	}
	return ts
}

// tailReport classifies whatever report lines have arrived since ts.ReportOffset,
// before ClassifyStatus runs for this tick, so a report that lands in the same
// poll as a herdr idle transition is already reflected in ts.LastReportState when
// the idle-vs-idle-unreported decision is made. A line carrying exactly one
// embedded PR URL auto-records it, subject to the same validation hand pr
// enforces; more than one URL, or a PR already on record, is left alone.
func tailReport(ctx context.Context, cfg Config, ts *TaskState, t state.Task, out, errOut io.Writer) state.Task {
	path := state.ReportPath(cfg.Home, t.ID)
	lines, offset, err := state.TailReport(path, ts.ReportOffset)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: tail report %s failed: %v\n", t.ID, err)
		return t
	}

	for _, line := range lines {
		if e := ClassifyReportLine(cfg.Home, ts, t, line); e != nil {
			handleEvent(cfg, e, t, out, errOut)
		}
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

	ts.ReportOffset = offset
	return t
}

// statusChangeSeed answers how long a task has already been dwelling in its
// current herdr status when hand watch first tracks it, so a restart does not
// artificially reset a dwell that has been real all along - the same hazard
// ClassifyParked's mtime anchor exists to avoid (see its doc comment).
// StatusChangedAt is durable evidence of the last observed transition; a task
// that has never had one dwells in its first status exactly as long as it has
// existed, so CreatedAt is the honest answer instead.
func statusChangeSeed(t state.Task, now time.Time) time.Time {
	if t.StatusChangedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, t.StatusChangedAt); err == nil {
			return parsed
		}
	}
	if parsed, err := time.Parse(time.RFC3339, t.CreatedAt); err == nil {
		return parsed
	}
	return now
}

// reportEvidenceTime anchors ClassifyParked's silence clock to the last real
// evidence of activity: the report file's own mtime, which a watcher restart
// cannot touch. A task that has never reported has no file to stat yet, so its
// creation time stands in - still durable, still untouched by a resume.
func reportEvidenceTime(home string, t state.Task) (time.Time, error) {
	info, err := os.Stat(state.ReportPath(home, t.ID))
	if err == nil {
		return info.ModTime(), nil
	}
	if !os.IsNotExist(err) {
		return time.Time{}, err
	}
	created, err := time.Parse(time.RFC3339, t.CreatedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse created_at %q: %w", t.CreatedAt, err)
	}
	return created, nil
}

// lastReportLine reconstructs the last report line ClassifyParked names in its
// event, from the same state+note ts already tracks for the idle classifier -
// consistent with how the rest of the watcher treats "the last report" (see
// ClassifyReportLine), rather than re-reading the file for its raw text.
func lastReportLine(ts *TaskState) string {
	if ts.LastReportState == "" {
		return "no report"
	}
	return fmt.Sprintf("%s: %s", ts.LastReportState, ts.LastReportNote)
}

// announceAutoRecordFailure makes an auto-record that didn't happen a durable
// lifecycle fact on the event stream and in events.log, alongside the stderr
// diagnostic: the line is consumed and the offset moves on either way, so an
// unrecorded URL that only reached a long-running watcher's stderr would be lost.
// It is deliberately not a Pending Decision - that slot holds the worker's own
// question, and upserting by task ID there would erase one.
//
// The event kind is the outcome, since that token is what an operator greps
// events.log for: an attempt that did not complete is pr-not-recorded whatever
// stopped it, while losing the task lock leaves the outcome unknown and must not
// claim otherwise. The kind says only that much; the appended error text is what
// says why, so neither has to stand in for the other.
func announceAutoRecordFailure(cfg Config, t state.Task, url string, err error, out, errOut io.Writer) {
	kind, outcome := KindPRNotRecorded, "failed"
	if errors.Is(err, errLockContended) {
		kind, outcome = KindPRRecordUnknown, "skipped"
	}
	_, _ = fmt.Fprintf(errOut, "watch: auto-record PR for %s %s: %v\n", t.ID, outcome, err)
	handleEvent(cfg, &Event{
		TaskID: t.ID,
		Kind:   kind,
		Text:   fmt.Sprintf("%s %s: %s (%s)", kind, t.ID, url, flattenError(err)),
		Reason: url,
	}, t, out, errOut)
}

// flattenError renders err's whole cause on one line. An event is one line on
// stdout, one entry in events.log, and one dashboard bullet; the errors reaching
// here wrap gh's stderr verbatim, which is routinely multi-line for auth and
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

// autoRecordPR routes a worker-supplied URL through hand pr's own validation
// before it can reach task state: a URL that survives here is what `hand merge`
// later hands to `gh pr merge`, so a PR belonging to some other repo the worker
// merely mentioned must be refused, not recorded.
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

// recordAutoPR is race-safe against a concurrent explicit `hand pr` call or
// another tick's auto-record. The task lock is non-blocking for the same reason
// syncTaskState's is: this runs in the poll loop, which owes every other task a
// timely tick and its own ctx a prompt exit that flock cannot honor, while hand
// merge and hand promote hold the same task lock across gh and git round-trips.
//
// So the "already recorded" no-op has two halves. Holding the lock, it re-reads
// and no-ops if the PR arrived first. Denied the lock, it re-reads anyway: the
// likeliest holder is the `hand pr` recording this very URL, and treating that
// as a failed auto-record would announce a problem that fixed itself.
func recordAutoPR(home, id, url string) error {
	unlock, err := state.TryLock(home, "task:"+id)
	if err != nil {
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
	if t.PR != "" {
		return nil
	}
	t.PR = url
	if err := state.Write(home, t); err != nil {
		return fmt.Errorf("write task %s: %w", id, err)
	}

	// Task lock before dashboard lock, never the reverse - the one ordering
	// every site that takes both follows.
	dashPath := filepath.Join(home, "data", "dashboard.md")
	if err := dashboard.Update(dashPath, dashboard.UpdateOpts{SetPR: &dashboard.PRUpdate{ID: id, PR: url}}); err != nil {
		if errors.Is(err, dashboard.ErrPRRowNotFound) {
			// The write above succeeded, so the PR is genuinely on record - only the
			// dashboard has no active row to carry it, the same silent no-op cmd/pr.go
			// exits non-zero to refuse. The watcher cannot exit non-zero per task, so
			// pr-not-recorded is how an operator learns the dashboard column is stale
			// despite the state write having landed.
			return fmt.Errorf("%w - the PR is recorded on the task but nothing was reconciled", err)
		}
		return fmt.Errorf("recorded PR for %s in task state but dashboard update failed: %w", id, err)
	}
	return nil
}

// errLockContended marks an auto-record the watcher declined rather than
// attempted, so callers can tell "refused this URL" from "never got to try".
var errLockContended = errors.New("task locked by another process")

// recordedByLockHolder decides what a lost race to the task lock means. If the
// URL is already on record the holder did the work, so there is nothing to say.
// Otherwise the outcome is genuinely unknown - the holder may be mid-write - and
// the report line is consumed either way, so it stays loud and says only that,
// rather than claiming a failure or naming a remedy that may be a no-op. A task
// whose state won't read says exactly that instead of sending the operator to
// hand status, which reads the same unreadable file.
func recordedByLockHolder(home, id, url string) error {
	t, err := state.Read(home, id)
	if err != nil {
		return fmt.Errorf("%w, and its state could not be read: %v", errLockContended, err)
	}
	if t.PR == url {
		return nil
	}
	return fmt.Errorf("%w that may be recording it - confirm with: hand status %s", errLockContended, id)
}

// syncTaskState writes back the bookkeeping hand watch owns on a task - how far
// its report file is consumed, whether this watcher's own gh poll already
// announced the PR merged, and whether the verified done already went out - so a
// restart neither replays report lines nor re-announces, nor silently skips, an
// announcement it can no longer re-derive.
//
// It runs last, after every event this tick produced has been announced: a marker
// persisted before its line is emitted would, if the process died in between,
// suppress an announcement nothing can re-derive. A duplicate line is a far
// cheaper failure than a silently dropped one.
//
// The lock is non-blocking on purpose. hand merge holds the same task lock across
// gh round-trips, and the poll loop - which owes every other task a timely tick,
// and its own ctx a prompt exit that flock can't honor - must not queue behind it.
// Everything written here is re-derivable, so a skipped write just retries.
func syncTaskState(home, id string, ts *TaskState, errOut io.Writer) {
	if ts.ReportOffset == ts.PersistedOffset && ts.PRMerged == ts.PersistedPRMerged &&
		ts.DoneVerified == ts.PersistedDoneVerified && ts.ChangedAt.Equal(ts.PersistedChangedAt) {
		return
	}

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
	t.ReportOffset = ts.ReportOffset
	t.MergeAnnounced = t.MergeAnnounced || ts.PRMerged
	t.DoneVerified = t.DoneVerified || ts.DoneVerified
	t.StatusChangedAt = ts.ChangedAt.UTC().Format(time.RFC3339)
	if err := state.Write(home, t); err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: persist task %s failed: %v\n", id, err)
		return
	}
	ts.PersistedOffset = ts.ReportOffset
	ts.PersistedPRMerged = ts.PRMerged
	ts.PersistedDoneVerified = ts.DoneVerified
	ts.PersistedChangedAt = ts.ChangedAt
}

func handleEvent(cfg Config, e *Event, t state.Task, out, errOut io.Writer) {
	_, _ = fmt.Fprintln(out, e.Text)

	logPath := filepath.Join(state.Dir(cfg.Home), "events.log")
	if err := appendEventLog(logPath, time.Now().UTC().Format(time.RFC3339)+" "+e.Text); err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: append events.log failed: %v\n", err)
	}

	if err := updateDashboardForEvent(cfg.Home, e, t); err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: update dashboard failed: %v\n", err)
	}
}

// dashboardRow is one event kind's complete disposition of a task's row. The row has
// two event-driven fields and a kind must answer both: a kind that answered only the
// state column is how a stopped worker's row kept reading "working", then how a steered
// worker's row kept reading "needs-decision", and answering only that column again is
// how the Pending Decisions slot kept a question the worker had already resolved. Three
// misses of the same shape, so the disposition is a value each kind returns rather than
// a set of assignments each kind is trusted to remember.
//
// Answering is not the same as choosing between set and clear. "Leave it" is a real
// answer and the safe one, so each field is a tri-state; what closes the family is that
// no kind may be silently absent, which dashboardRowFor enforces on its own.
type dashboardRow struct {
	// Written false is the third answer, not a default: the kind says nothing about
	// the worker and only appends to Recent Events.
	Written bool
	State   string
	// Pending is the Pending Decisions text this kind writes; ClearPending retires
	// whatever is there. Leaving both zero is the third answer for that slot, and it
	// is the default on purpose: clearing destroys a supervisor question nothing can
	// restore - the report line is already past report_offset - so it takes positive
	// evidence that the question is retired, not merely a kind with nothing of its
	// own to say. A stale question next to a state column that already reads "failed"
	// is visible and actionable; an erased one is gone.
	Pending      string
	ClearPending bool
}

// dashboardRowFor gives every event kind a disposition, or fails. An unknown kind is
// a programming error rather than a silent no-op, which is what makes the rule
// checkable: TestEveryEventKindHasADashboardRowDisposition reads the kind constants
// straight out of events.go, so a new kind that reaches here without a decision fails
// the build's tests instead of quietly leaving a field stale.
func dashboardRowFor(e *Event, age string) (dashboardRow, error) {
	switch e.Kind {
	case KindIdleUnreported:
		return dashboardRow{Written: true, State: KindIdleUnreported, Pending: fmt.Sprintf("stopped, reason unknown (idle %s)", age)}, nil
	case KindBlocked:
		return dashboardRow{Written: true, State: KindBlocked, Pending: fmt.Sprintf("%s (blocked %s)", e.Reason, age)}, nil
	// The slot is upserted by task ID, so a second writer for the same task erases
	// the first. It belongs to whatever the supervisor has to answer about that task
	// - the worker's own question, or an unexplained stop that leaves no one to ask.
	// Operator notices go to the event stream.
	case KindReportBlocked, KindReportNeedsDecision:
		return dashboardRow{Written: true, State: e.Kind, Pending: e.Reason}, nil
	// A pane hand cannot probe, and a worker parked or stopped on its own, all say
	// nothing about a question already asked. KindFailed is the sharpest: ClassifyStatus
	// fires it on any probe error, so clearing here would let one herdr daemon restart
	// wipe every tracked task's slot in a single tick.
	case KindFailed:
		return dashboardRow{Written: true, State: KindFailed}, nil
	case KindReportPaused:
		return dashboardRow{Written: true, State: KindReportPaused}, nil
	// A park is a live pane sitting quiet, same shape as idle-unreported and
	// blocked: something is waiting on a human, so it earns a row and a Pending
	// Decisions slot naming the last report and how long it has been silent.
	case KindParked:
		return dashboardRow{Written: true, State: KindParked, Pending: e.Reason}, nil
	case KindReportFailed:
		return dashboardRow{Written: true, State: KindReportFailed}, nil
	// The only two events that are evidence a question is retired: the worker is
	// working again, or the task verifiably landed.
	case KindReportWorking:
		return dashboardRow{Written: true, State: state.ReportWorking, ClearPending: true}, nil
	case KindReportDone:
		// A reported done is never trusted alone. Only once doneVerified finds
		// recorded evidence that the task actually landed (Verified) does it get to
		// touch the row at all, so an unverified self-report shows up in Recent
		// Events without silently overriding what's still pending.
		if !e.Verified {
			return dashboardRow{}, nil
		}
		return dashboardRow{Written: true, State: state.ReportDone, ClearPending: true}, nil
	case KindStale, KindPRMerged, KindPRNotRecorded, KindPRRecordUnknown, KindReportMalformed:
		// Nothing about what the worker is doing: elapsed time in a state rather than
		// a new one, facts about a PR record, and free text that classifies to
		// nothing.
		return dashboardRow{}, nil
	}
	return dashboardRow{}, fmt.Errorf("no dashboard row disposition for event kind %q", e.Kind)
}

func updateDashboardForEvent(home string, e *Event, t state.Task) error {
	age := dashboard.FormatAge(t.CreatedAt)
	row, err := dashboardRowFor(e, age)
	if err != nil {
		return err
	}

	opts := dashboard.UpdateOpts{AddEvent: e.Text}
	if row.Written {
		opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: row.State, Age: age}
		switch {
		case row.Pending != "":
			opts.SetPendingDecision = &dashboard.PendingDecision{ID: t.ID, Text: row.Pending}
		case row.ClearPending:
			opts.ClearPendingDecision = t.ID
		}
	}

	return dashboard.Update(filepath.Join(home, "data", "dashboard.md"), opts)
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
