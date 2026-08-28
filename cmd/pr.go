package cmd

import (
	"context"
	"fmt"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/runtime"
	"github.com/atqamz/hand/internal/state"
	"github.com/spf13/cobra"
)

func newPRCmd() *cobra.Command {
	var crossRepo bool
	var reason string

	cmd := &cobra.Command{
		Use:   "pr <id> <url>",
		Short: "Record a task's pull request URL",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, url := args[0], args[1]
			if !state.ValidatePRURL(url) {
				return &ExitError{Err: fmt.Errorf("invalid PR URL %q: must match https://github.com/<owner>/<repo>/pull/<number>", url), Code: 2}
			}
			if crossRepo && reason == "" {
				return &ExitError{Err: fmt.Errorf("--cross-repo requires --reason describing the deliberate delivery elsewhere"), Code: 2}
			}
			if !crossRepo && reason != "" {
				return &ExitError{Err: fmt.Errorf("--reason applies only to a --cross-repo record"), Code: 2}
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			release, err := state.Lock(fleetHome, "task:"+id)
			if err != nil {
				return fmt.Errorf("lock task %q: %w", id, err)
			}
			defer release()
			task, err := state.Read(fleetHome, id)
			if err != nil {
				return asPrecondition(err)
			}
			task, reconcile, err := recordPR(cmd.Context(), fleetHome, task, url, crossRepo, reason)
			if err != nil {
				return err
			}
			if err := project.ReassertPRMetadata(cmd.Context(), fleetHome, url); err != nil {
				return fmt.Errorf("reassert operator-owned PR metadata: %w", err)
			}
			result := "recorded"
			if reconcile {
				result = "already-recorded"
			}
			var doc axi.Doc
			doc.Field("id", task.ID)
			doc.Field("result", result)
			doc.Field("pr", url)
			if task.PRCrossRepoReason != "" {
				doc.Field("cross_repo_reason", task.PRCrossRepoReason)
			}
			// A torn-down task has no active attempt for hand merge to act on (atqamz/hand#424): naming
			// it here would suggest the task is live again, which recording a PR on it must never do.
			if task.Lifecycle != state.TaskTerminal {
				doc.Help("Run `hand merge " + task.ID + "` once this PR's checks are green")
			}
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&crossRepo, "cross-repo", false, "record a PR in a repository other than the project's own or its declared upstream, deliberately")
	cmd.Flags().StringVar(&reason, "reason", "", "why this PR landed in a different repository, required with --cross-repo")
	return cmd
}

func recordPR(ctx context.Context, homeDir string, task state.Task, url string, crossRepo bool, reason string) (state.Task, bool, error) {
	updated, reconcile, err := runtime.RecordPR(ctx, homeDir, task, url, crossRepo, reason)
	if err != nil {
		return task, false, asPrecondition(err)
	}
	return updated, reconcile, nil
}
