package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/home"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/spf13/cobra"
)

func newPRCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr <id> <url>",
		Short: "Record a task's pull request URL",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, url := args[0], args[1]
			if !state.ValidatePRURL(url) {
				return &ExitError{Err: fmt.Errorf("invalid PR URL %q: must match https://github.com/<owner>/<repo>/pull/<number>", url), Code: 2}
			}

			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			release, err := state.Lock(home, "task:"+id)
			if err != nil {
				return fmt.Errorf("lock task %q: %w", id, err)
			}
			defer release()

			t, err := state.Read(home, id)
			if err != nil {
				return asPrecondition(err)
			}

			t, reconcile, err := recordPR(cmd.Context(), home, t, url)
			if err != nil {
				return err
			}

			dashPath := filepath.Join(home, "data", "dashboard.md")
			// Exiting 0 with no row updated would report this command as done while
			// the dashboard's PR column stays exactly as empty as before, on either
			// path: the URL is on the task, and only the dashboard is left stale.
			if err := dashboard.Update(dashPath, dashboard.UpdateOpts{SetPR: &dashboard.PRUpdate{ID: t.ID, PR: url}}); err != nil {
				if errors.Is(err, dashboard.ErrPRRowNotFound) {
					return &ExitError{Err: fmt.Errorf("pr recorded for %s: %s, but the dashboard has no active row for it - nothing reconciled", t.ID, url), Code: 3}
				}
				return fmt.Errorf("update dashboard: %w", err)
			}

			if reconcile {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "pr already recorded for %s: %s (dashboard reconciled)\n", t.ID, url)
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "recorded PR for %s: %s\n", t.ID, url)
			return err
		},
	}
	return cmd
}

// recordPR is hand pr's own recording logic, factored out so detectPR (cmd/prdetect.go)
// can route a forge-discovered PR through the same conflict guard and reconciliation
// rather than a second, divergent copy of it. reconcile reports whether url matched
// what was already on t.PR (a no-op on t, since it was already correct).
func recordPR(ctx context.Context, home string, t state.Task, url string) (state.Task, bool, error) {
	if t.PR != "" && t.PR != url {
		return t, false, &ExitError{Err: fmt.Errorf("task %s already has a different PR recorded: %s", t.ID, t.PR), Code: 3}
	}

	// Reconcile rather than no-op: this command is the documented remedy for
	// a pr-not-recorded event, and the recording it has to repair may have
	// failed only at the dashboard, with the URL already in task state.
	reconcile := t.PR == url
	if reconcile {
		return t, true, nil
	}

	proj, exists, err := project.Find(home, t.Project)
	if err != nil {
		return t, false, err
	}
	if !exists {
		return t, false, &ExitError{Err: fmt.Errorf("project %q not registered", t.Project), Code: 3}
	}

	ghCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := project.ValidatePR(ghCtx, home, proj, url); err != nil {
		return t, false, &ExitError{Err: err, Code: 3}
	}

	t.PR = url
	if err := state.Write(home, t); err != nil {
		return t, false, fmt.Errorf("write task state: %w", err)
	}
	return t, false, nil
}
