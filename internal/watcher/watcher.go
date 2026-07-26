// Package watcher implements the poll loop behind hand watch: it tracks herdr
// agent states for active tasks, classifies transitions into actionable events,
// and keeps state/events.log and data/dashboard.md current.
package watcher

import (
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
}

// Run blocks, polling herdr agent states at cfg.PollInterval until ctx is
// canceled. It returns nil on clean cancellation, or an error if herdr is
// unreachable at startup. out receives the actionable event stream documented
// in SPECS.md; errOut receives internal diagnostics (list/log/dashboard
// failures), keeping the two streams separable per the stdout/stderr contract.
func Run(ctx context.Context, cfg Config, out, errOut io.Writer) error {
	client := herdr.NewClient()
	if _, err := client.WorkspaceList(); err != nil {
		return fmt.Errorf("herdr unreachable: %w", err)
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

		ts, tracked := states[t.ID]
		if !tracked {
			if probeErr != nil {
				continue
			}
			states[t.ID] = resumeTaskState(cfg.Home, t, status, now, errOut)
			continue
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
		syncTaskState(cfg.Home, t.ID, ts, errOut)
	}

	for id := range states {
		if !seen[id] {
			delete(states, id)
		}
	}
}

// resumeTaskState picks up where a previous hand watch left off rather than
// starting cold: the report offset and any already-announced merge come from the
// task's durable state so nothing is replayed, and the last reported state is
// re-read so a pane found not-busy after a restart isn't mistaken for an
// unexplained stop. An unreadable report is reported as such, never quietly
// treated as "this worker never reported".
func resumeTaskState(home string, t state.Task, status herdr.Status, now time.Time, errOut io.Writer) *TaskState {
	ts := NewTaskState(status, now)
	ts.ReportOffset = t.ReportOffset
	ts.PersistedOffset = t.ReportOffset
	ts.PRMerged = t.PRMergedObserved
	ts.PersistedPRMerged = t.PRMergedObserved

	last, ok, err := state.LastReport(home, t.ID)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: read report for %s failed: %v\n", t.ID, err)
		return ts
	}
	if ok && !last.Malformed {
		ts.LastReportState = last.State
		ts.LastReportNote = last.Note
		ts.DoneVerified = last.State == state.ReportDone && doneVerified(home, ts, t)
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
		if e := ClassifyReportLine(cfg.Home, ts, t.ID, t, line); e != nil {
			handleEvent(cfg, e, t, out, errOut)
		}
		if t.PR == "" {
			if urls := state.FindPRURLs(line.Raw); len(urls) == 1 {
				if err := autoRecordPR(ctx, cfg.Home, t, urls[0]); err != nil {
					_, _ = fmt.Fprintf(errOut, "watch: auto-record PR for %s failed: %v\n", t.ID, err)
					handleEvent(cfg, prNotRecordedEvent(t.ID, urls[0], err), t, out, errOut)
				} else {
					t.PR = urls[0]
				}
			}
		}
	}

	ts.ReportOffset = offset
	return t
}

// prNotRecordedEvent makes a refused or failed auto-record a durable lifecycle
// fact on the event stream and in events.log, alongside the stderr diagnostic:
// the line is consumed and the offset moves on either way, so an unrecorded URL
// that only reached a long-running watcher's stderr would be lost. It is
// deliberately not a Pending Decision - that slot holds the worker's own
// question, and upserting by task ID there would erase one.
func prNotRecordedEvent(id, url string, err error) *Event {
	return &Event{
		TaskID: id,
		Kind:   KindPRNotRecorded,
		Text:   fmt.Sprintf("pr-not-recorded %s: %s (%v)", id, url, err),
		Reason: url,
	}
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
	return dashboard.Update(dashPath, dashboard.UpdateOpts{SetPR: &dashboard.PRUpdate{ID: id, PR: url}})
}

// recordedByLockHolder decides what a lost race to the task lock means. If the
// URL is already on record the holder did the work, so there is nothing to say.
// Otherwise the outcome is genuinely unknown - the holder may be mid-write - and
// the report line is consumed either way, so it stays loud and says only that,
// rather than claiming a failure or naming a remedy that may be a no-op.
func recordedByLockHolder(home, id, url string) error {
	if t, err := state.Read(home, id); err == nil && t.PR == url {
		return nil
	}
	return fmt.Errorf("skipped, task locked by another process that may be recording it - confirm with: hand status %s", id)
}

// syncTaskState writes back the bookkeeping hand watch owns on a task - how far
// its report file is consumed, and whether this watcher's own gh poll already
// announced the PR merged - so a restart neither replays report lines nor
// re-announces a merge.
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
	if ts.ReportOffset == ts.PersistedOffset && ts.PRMerged == ts.PersistedPRMerged {
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
	t.PRMergedObserved = t.PRMergedObserved || ts.PRMerged
	if err := state.Write(home, t); err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: persist task %s failed: %v\n", id, err)
		return
	}
	ts.PersistedOffset = ts.ReportOffset
	ts.PersistedPRMerged = ts.PRMerged
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

func updateDashboardForEvent(home string, e *Event, t state.Task) error {
	dashPath := filepath.Join(home, "data", "dashboard.md")
	age := dashboard.FormatAge(t.CreatedAt)

	opts := dashboard.UpdateOpts{AddEvent: e.Text}
	switch e.Kind {
	case KindIdleUnreported:
		opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: KindIdleUnreported, Age: age}
		opts.SetPendingDecision = &dashboard.PendingDecision{ID: t.ID, Text: fmt.Sprintf("stopped, reason unknown (idle %s)", age)}
	case KindBlocked:
		opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: KindBlocked, Age: age}
		opts.SetPendingDecision = &dashboard.PendingDecision{ID: t.ID, Text: fmt.Sprintf("%s (blocked %s)", e.Reason, age)}
	case KindFailed:
		opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: KindFailed, Age: age}
	// The Pending Decisions slot is upserted by task ID, so a second writer for
	// the same task erases the first. It belongs to whatever the supervisor has
	// to answer about that task - the worker's own question, or an unexplained
	// stop that leaves no one to ask. Operator notices go to the event stream.
	case KindReportBlocked, KindReportNeedsDecision:
		opts.SetPendingDecision = &dashboard.PendingDecision{ID: t.ID, Text: e.Reason}
	case KindReportFailed:
		opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: KindReportFailed, Age: age}
	case KindReportDone:
		// A reported done is never trusted alone. Only once doneVerified finds
		// recorded evidence that the task actually landed (Verified) does it get
		// to change agent state or clear a pending decision, so an unverified
		// self-report shows up in Recent Events without silently overriding what's
		// still pending.
		if e.Verified {
			opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: KindHerdrDone, Age: age}
			opts.ClearPendingDecision = t.ID
		}
	}

	return dashboard.Update(dashPath, opts)
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
