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
	case KindDone:
		opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: KindDone, Age: age}
		opts.ClearPendingDecision = t.ID
	case KindBlocked:
		opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: KindBlocked, Age: age}
		opts.SetPendingDecision = &dashboard.PendingDecision{ID: t.ID, Text: fmt.Sprintf("%s (blocked %s)", e.Reason, age)}
	case KindFailed:
		opts.UpdateAgentState = &dashboard.AgentStateUpdate{ID: t.ID, State: KindFailed, Age: age}
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
