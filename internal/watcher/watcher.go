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

	"github.com/atqamz/secondhand/internal/atomicfile"
	"github.com/atqamz/secondhand/internal/ghutil"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/notify"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
)

const maxEventLogLines = 200

type Config struct {
	Home           string
	PollInterval   time.Duration
	StaleThreshold time.Duration
	// Timeout bounds RunUntilEvent only. Zero blocks until an event arrives.
	Timeout      time.Duration
	ParkedBounds ParkedBounds
	// EventFilter bounds which kinds reach out, whichever writer that is: handleEvent
	// applies it to every event it writes there, on the Run path as much as the
	// RunUntilEvent one. Keeping the streaming path unfiltered is cmd/watch.go's
	// doing, not this package's - it rejects --event without --until-event, so Run
	// never receives a filter from the CLI.
	EventFilter EventFilter
}

var ErrNoEvent = errors.New("no event")

// ErrArmFailed names the one task whose pane could not be probed at arm time, so
// an exit from RunUntilEvent always means the whole fleet was actually watched.
var ErrArmFailed = errors.New("could not arm")

// Run blocks, polling herdr agent states at cfg.PollInterval until ctx is
// canceled. It returns nil on clean cancellation, or an error if herdr is
// unreachable at startup. out receives the actionable event stream documented
// in SPECS.md; errOut receives internal diagnostics (list/log failures),
// keeping the two streams separable per the stdout/stderr contract.
func Run(ctx context.Context, cfg Config, out, errOut io.Writer) error {
	client, err := connect(ctx)
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

// RunUntilEvent blocks until a tick produces events, writes them to out, and
// returns nil - the exit is the delivery, since it's the one signal a supervisory
// agent's background-task runner already honors. The startup state is never
// delivered: two ticks take a silent baseline first, so an already-done worker
// isn't mistaken for a fresh transition. Baseline events still reach
// events.log, just not stdout.
func RunUntilEvent(ctx context.Context, cfg Config, out, errOut io.Writer) error {
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	client, err := connect(ctx)
	if err != nil {
		return err
	}

	if err := probeAllTasks(ctx, cfg.Home, client); err != nil {
		return err
	}

	states := make(map[string]*TaskState)
	tick(ctx, cfg, client, states, io.Discard, errOut)
	tick(ctx, cfg, client, states, io.Discard, errOut)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%w within %s", ErrNoEvent, cfg.Timeout)
			}
			return fmt.Errorf("%w: interrupted", ErrNoEvent)
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

// connect races the reachability probe against ctx because unbounded, a wedged
// herdr daemon blocks RunUntilEvent's --timeout from ever starting to count.
// Losing that race is ErrNoEvent for the same reason probeAllTasks's is: the
// window closed during arming, which is exit 4 wherever in arming it happens.
func connect(ctx context.Context) (*herdr.Client, error) {
	client := herdr.NewClient()
	done := make(chan error, 1)
	go func() { _, err := client.WorkspaceList(); done <- err }()
	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: timed out reaching herdr", ErrNoEvent)
		}
		return nil, fmt.Errorf("%w: interrupted while reaching herdr", ErrNoEvent)
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("herdr unreachable: %w", err)
		}
		return client, nil
	}
}

// probeAllTasks confirms every active task's pane answers before RunUntilEvent
// arms, since an unprobed task would otherwise wait out the timeout with no
// distinguishing signal. Losing the race against ctx is ErrNoEvent, not
// ErrArmFailed: the window is simply over and no single task can be named as the
// cause the way ErrArmFailed's exit promises.
func probeAllTasks(ctx context.Context, home string, client *herdr.Client) error {
	tasks, err := state.List(home)
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		for _, t := range tasks {
			if _, err := client.PaneGet(t.Herdr.PaneID); err != nil {
				done <- fmt.Errorf("%w: %s: %v", ErrArmFailed, t.ID, err)
				return
			}
		}
		done <- nil
	}()
	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w: timed out probing tasks before arming", ErrNoEvent)
		}
		return fmt.Errorf("%w: interrupted while probing tasks before arming", ErrNoEvent)
	case err := <-done:
		return err
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

		// Tracking is keyed by task identity, not by ID: an ID torn down and
		// respawned between two ticks is a different task, and inheriting the
		// previous run's TaskState would suppress the new task's verified done
		// forever - syncTaskState writes that inherited done_verified onto the fresh
		// row, making the suppression durable - and absorb its first unexplained
		// stop. Same hazard as a surviving report channel, one layer in; see
		// state.Delete for that half.
		ts, tracked := states[t.ID]
		if tracked && ts.CreatedAt != t.CreatedAt {
			tracked = false
		}
		if !tracked {
			// herdr.StatusUnknown stands in for "no real status observed yet", so the
			// eventual recovery reads as an ordinary transition rather than inventing a
			// prior status. Probed=false starts ClassifyUnreachable's dwell clock
			// immediately instead of waiting for a second failed probe to notice this
			// task at all.
			if probeErr != nil {
				status = herdr.StatusUnknown
			}
			ts = resumeTaskState(t, status, now)
			ts.Probed = probeErr == nil
			states[t.ID] = ts
			continue
		}

		forgetPaneScopedCache(ts, t, now)

		t = tailReport(ctx, cfg, ts, t, out, errOut)

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
		if mtime, err := reportEvidenceTime(cfg.Home, t); err != nil {
			_, _ = fmt.Fprintf(errOut, "watch: stat report %s failed: %v\n", t.ID, err)
		} else if e := ClassifyParked(ts, t.ID, ts.LastReportState, lastReportLine(ts), mtime, now, cfg.ParkedBounds); e != nil {
			handleEvent(cfg, e, out, errOut)
		}
		syncTaskState(cfg.Home, t.ID, ts, now, errOut)
	}

	for id := range states {
		if !seen[id] {
			delete(states, id)
		}
	}
}

// Every fact resumeTaskState restores comes from durable state, never re-derived
// from current evidence: evidence that landed while the watcher was down (hand
// merge writing merged, say) would otherwise look like an announcement that
// already went out. SPECS.md's "What survives a hand watch restart" enumerates
// what is deliberately re-derived instead, and why that is safe.
func resumeTaskState(t state.Task, status herdr.Status, now time.Time) *TaskState {
	changedAt := statusChangeSeed(t, status, now)
	ts := NewTaskState(status, changedAt)
	ts.PersistedChangedAt = changedAt
	ts.PersistedChangedFor = t.StatusChangedFor
	ts.PersistedPaneID = t.Herdr.PaneID
	ts.CreatedAt = t.CreatedAt
	ts.ReportOffset = t.ReportOffset
	ts.PersistedOffset = t.ReportOffset
	ts.PRMerged = t.MergeAnnounced
	ts.PersistedPRMerged = t.MergeAnnounced
	ts.DoneVerified = t.DoneVerified
	ts.PersistedDoneVerified = t.DoneVerified
	ts.LastReportState = t.LastReportState
	ts.LastReportNote = t.LastReportNote
	return ts
}

// forgetPaneScopedCache drops the cached facts hand promote invalidated: it gives
// the task a new herdr pane while keeping created_at, so tick's identity check
// never fires, yet every pane-anchored fact cached here describes a pane the task
// no longer has. The trigger is the pane itself changing, not the newly observed
// status differing - a ship whose first probe reads the status the scout last held
// raises no transition at all - and not any restamped timestamp either, which is
// both too eager (a resume reseeds the dwell to now for reasons of its own) and too
// blunt (a restamp inside one RFC3339 second of this watcher's own write is
// invisible).
func forgetPaneScopedCache(ts *TaskState, t state.Task, now time.Time) {
	// Compared against the persisted mirror, not the live flag: the watcher only ever
	// sets the flag true, so a disk value that has gone false since this watcher last
	// wrote true is such a rewrite - whereas a flag set true earlier in this very tick
	// is simply not persisted yet, and syncTaskState's OR is how it gets there.
	if !t.DoneVerified && ts.PersistedDoneVerified {
		ts.DoneVerified = false
		ts.PersistedDoneVerified = false
	}
	if t.Herdr.PaneID == ts.PersistedPaneID {
		return
	}
	ts.PersistedPaneID = t.Herdr.PaneID
	seed := statusChangeSeed(t, ts.Status, now)
	ts.ChangedAt = seed
	ts.PersistedChangedAt = seed
	ts.PersistedChangedFor = t.StatusChangedFor
	// Reset after the seed above, which asks what status the cached dwell describes.
	// StatusUnknown is the sentinel a ship's first probe is diffed against, matching
	// neither the working nor the blocked branch, so that probe reads as the baseline
	// a first sighting always is rather than as a transition out of the scout's status.
	ts.Status = herdr.StatusUnknown
	ts.Stale = false
	ts.Blocked = false
	// False, exactly as tick seeds a task first sighted with an unreachable pane: the
	// ship's first probe of its new pane is a first sighting, so an unreachable one
	// dwells under ClassifyUnreachable's threshold instead of firing `failed` on
	// sight. Carrying the old pane's true would fire that no-dwell `failed` off a
	// blink, on the strength of a probe that only ever described the scout's pane.
	ts.Probed = false
	// A latch claiming the old pane's outage has nothing to say about the new one;
	// left true it would sit inert until the next probe failure resets Probed to
	// false anyway, but a fresh pane deserves a fresh episode on purpose, not by
	// accident of that ordering.
	ts.UnreachableFired = false
	ts.LastReportState = t.LastReportState
	ts.LastReportNote = t.LastReportNote
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
			handleEvent(cfg, e, out, errOut)
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

// statusChangeSeed answers how long a task has already been dwelling in status, so
// a restart does not reset a dwell that has been real all along. StatusChangedAt
// is only evidence about the status it was stamped for; a task with no observed
// transition at all has been dwelling since CreatedAt.
func statusChangeSeed(t state.Task, status herdr.Status, now time.Time) time.Time {
	if t.StatusChangedAt != "" {
		if t.StatusChangedFor != string(status) {
			return now
		}
		if parsed, err := time.Parse(time.RFC3339, t.StatusChangedAt); err == nil {
			return parsed
		}
	}
	if parsed, err := time.Parse(time.RFC3339, t.CreatedAt); err == nil {
		return parsed
	}
	return now
}

// reportEvidenceTime floors the report file's mtime at the instant the task's
// current pane started, because hand promote leaves the scout's report file - and
// so its mtime - untouched while clearing the last-report state that had the scout's
// silence under the long done/failed bound. Unfloored, a ship seconds old inherits
// the scout's whole silence, now measured against the short bound, and fires parked
// immediately.
func reportEvidenceTime(home string, t state.Task) (time.Time, error) {
	started, err := paneStartTime(t)
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
	if mtime := info.ModTime(); mtime.After(started) {
		return mtime, nil
	}
	return started, nil
}

// paneStartTime is when the task's current pane started, as spawn and hand promote
// each recorded it. It deliberately no longer reads StatusChangedAt, which the
// outage-dwell clock restamps for a pane it could not even reach and which would
// slide this floor forward by up to a full bound of real report silence. A row
// written before the column existed falls back to CreatedAt, the instant its one
// and only pane started.
func paneStartTime(t state.Task) (time.Time, error) {
	stamp, field := t.PaneStartedAt, "pane_started_at"
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
	}, out, errOut)
}

// flattenError renders err's whole cause on one line. An event is one line on
// stdout and one entry in events.log; the errors reaching here wrap gh's
// stderr verbatim, which is routinely multi-line for auth and network
// failures. The stderr diagnostic above keeps the original formatting.
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
func syncTaskState(home, id string, ts *TaskState, now time.Time, errOut io.Writer) {
	if ts.ReportOffset == ts.PersistedOffset && ts.PRMerged == ts.PersistedPRMerged &&
		ts.DoneVerified == ts.PersistedDoneVerified && ts.ChangedAt.Equal(ts.PersistedChangedAt) &&
		ts.PersistedChangedFor == string(ts.Status) {
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
	// A promote may have landed since this tick's state.List. Writing the cached
	// values back would erase its restamp and leave the disk value matching what
	// this watcher persisted, so no later tick would find anything to forget either.
	forgetPaneScopedCache(ts, t, now)

	t.ReportOffset = ts.ReportOffset
	t.MergeAnnounced = t.MergeAnnounced || ts.PRMerged
	t.DoneVerified = t.DoneVerified || ts.DoneVerified
	t.StatusChangedAt = ts.ChangedAt.UTC().Format(time.RFC3339)
	t.StatusChangedFor = string(ts.Status)
	t.LastReportState = ts.LastReportState
	t.LastReportNote = ts.LastReportNote
	if err := state.Write(home, t); err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: persist task %s failed: %v\n", id, err)
		return
	}
	ts.PersistedOffset = ts.ReportOffset
	ts.PersistedPRMerged = ts.PRMerged
	ts.PersistedDoneVerified = ts.DoneVerified
	ts.PersistedChangedAt = ts.ChangedAt
	ts.PersistedChangedFor = string(ts.Status)
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

// notifyEvent is NotifyFilter's own consumer of the classified event stream -
// see SPECS.md's "Notifying a supervisory agent with no session watching" for
// why an unconfigured config/notify stays silent while a failed send is loud.
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
