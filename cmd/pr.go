package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/secondhand/internal/dashboard"
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

			home, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
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

			if t.PR != "" && t.PR != url {
				return &ExitError{Err: fmt.Errorf("task %s already has a different PR recorded: %s", t.ID, t.PR), Code: 3}
			}

			// Reconcile rather than no-op: this command is the documented remedy for
			// a pr-not-recorded event, and the recording it has to repair may have
			// failed only at the dashboard, with the URL already in task state.
			reconcile := t.PR == url
			if !reconcile {
				proj, exists, err := project.Find(home, t.Project)
				if err != nil {
					return err
				}
				if !exists {
					return &ExitError{Err: fmt.Errorf("project %q not registered", t.Project), Code: 3}
				}

				ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
				defer cancel()
				if err := project.ValidatePR(ctx, home, proj, url); err != nil {
					return &ExitError{Err: err, Code: 3}
				}

				t.PR = url
				if err := state.Write(home, t); err != nil {
					return fmt.Errorf("write task state: %w", err)
				}
			}

			dashPath := filepath.Join(home, "data", "dashboard.md")
			if err := dashboard.Update(dashPath, dashboard.UpdateOpts{SetPR: &dashboard.PRUpdate{ID: t.ID, PR: url}}); err != nil {
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
