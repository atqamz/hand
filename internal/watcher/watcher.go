// Package watcher implements the poll loop behind hand watch: it tracks herdr
// agent states for active tasks, classifies transitions into actionable events,
// and keeps state/events.log and data/dashboard.md current.
package watcher

import (
	"context"
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
			states[t.ID] = NewTaskState(status, now)
			continue
		}

		t = tailReport(cfg, ts, t, out, errOut)

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
	}

	for id := range states {
		if !seen[id] {
			delete(states, id)
		}
	}
}

// tailReport classifies whatever report lines have arrived since ts.ReportOffset,
// before ClassifyStatus runs for this tick, so a report that lands in the same
// poll as a herdr idle transition is already reflected in ts.LastReportState when
// the idle-vs-idle-unreported decision is made. A line carrying exactly one
// embedded PR URL auto-records it (shape validation only - hand pr's own network
// check is what actually confirms the PR); more than one URL, or a PR already on
// record, is left alone.
func tailReport(cfg Config, ts *TaskState, t state.Task, out, errOut io.Writer) state.Task {
	path := state.ReportPath(cfg.Home, t.ID)
	lines, offset, err := state.TailReport(path, ts.ReportOffset)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "watch: tail report %s failed: %v\n", t.ID, err)
		return t
	}
	ts.ReportOffset = offset

	for _, line := range lines {
		if e := ClassifyReportLine(ts, t.ID, t, line); e != nil {
			handleEvent(cfg, e, t, out, errOut)
		}
		if t.PR == "" {
			if urls := state.FindPRURLs(line.Raw); len(urls) == 1 {
				if err := recordAutoPR(cfg.Home, t.ID, urls[0]); err != nil {
					_, _ = fmt.Fprintf(errOut, "watch: auto-record PR for %s failed: %v\n", t.ID, err)
				} else {
					t.PR = urls[0]
				}
			}
		}
	}
	return t
}

// recordAutoPR is race-safe against a concurrent explicit `hand pr` call or
// another tick's auto-record: it locks, re-reads, and no-ops if the PR is
// already set by the time it gets the lock.
func recordAutoPR(home, id, url string) error {
	unlock, err := state.Lock(home, "task:"+id)
	if err != nil {
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

	dashPath := filepath.Join(home, "data", "dashboard.md")
	return dashboard.Update(dashPath, dashboard.UpdateOpts{SetPR: &dashboard.PRUpdate{ID: id, PR: url}})
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
	case KindHerdrDone:
		opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: KindHerdrDone, Age: age}
		opts.ClearPendingDecision = t.ID
	case KindIdleUnreported:
		opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: KindIdleUnreported, Age: age}
		opts.SetPendingDecision = &dashboard.PendingDecision{ID: t.ID, Text: fmt.Sprintf("stopped, reason unknown (idle %s)", age)}
	case KindBlocked:
		opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: KindBlocked, Age: age}
		opts.SetPendingDecision = &dashboard.PendingDecision{ID: t.ID, Text: fmt.Sprintf("%s (blocked %s)", e.Reason, age)}
	case KindFailed:
		opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: KindFailed, Age: age}
	case KindReportBlocked, KindReportNeedsDecision:
		opts.SetPendingDecision = &dashboard.PendingDecision{ID: t.ID, Text: e.Reason}
	case KindReportFailed:
		opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: KindReportFailed, Age: age}
	case KindReportDone:
		// A reported done is never trusted alone: only once it's cross-checked
		// against a merged PR (Verified) does it get to change agent state or
		// clear a pending decision, so an unverified self-report shows up in
		// Recent Events without silently overriding what's still pending.
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
