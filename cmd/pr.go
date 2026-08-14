package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
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

			result := "recorded"
			if reconcile {
				result = "already-recorded"
			}
			var doc axi.Doc
			doc.Field("id", t.ID)
			doc.Field("result", result)
			doc.Field("pr", url)
			doc.Help("Run `hand merge " + t.ID + "` once this PR's checks are green")
			return doc.Render(cmd.OutOrStdout())
		},
	}
	return cmd
}

// hand pr's own recording logic, factored out so detectPR (cmd/prdetect.go) can route a forge-discovered
// PR through the same conflict guard and reconciliation rather than a second, divergent copy of it. The
// bool reports whether url matched what was already on t.PR (a no-op on t, since it was already correct).
func recordPR(ctx context.Context, home string, t state.Task, url string) (state.Task, bool, error) {
	if t.PR != "" && t.PR != url {
		return t, false, &ExitError{Err: fmt.Errorf("task %s already has a different PR recorded: %s", t.ID, t.PR), Code: 3}
	}

	// This command is the documented remedy for a pr-not-recorded event, and an
	// operator retrying it after the URL already made it into task state should
	// get a friendly no-op rather than an error.
	reconcile := t.PR == url
	if reconcile {
		return t, true, nil
	}

	// After the reconcile no-op above, so a retry that changes nothing still reads as one: what a
	// torn-down task refuses is a PR that would land on a completion record already written from
	// the state it had at teardown.
	if t.Lifecycle == state.TaskTerminal {
		return t, false, &ExitError{Err: fmt.Errorf("task %s is torn down; run hand reopen %s before recording a PR on it", t.ID, t.ID), Code: 3}
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

	if err := state.SetTaskPR(home, t.ID, url); err != nil {
		return t, false, fmt.Errorf("write task state: %w", err)
	}
	t.PR = url
	return t, false, nil
}
