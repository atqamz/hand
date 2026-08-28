package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/integration"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/spf13/cobra"
)

func newMergeCmd() *cobra.Command {
	var squash, mergeCommit, rebase, local bool

	cmd := &cobra.Command{
		Use:   "merge <id>",
		Short: "Merge a task's completed work",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if local && (squash || mergeCommit || rebase) {
				return &ExitError{Err: fmt.Errorf("--squash, --merge, --rebase cannot be combined with --local"), Code: 2}
			}
			method, err := resolveMergeMethod(squash, mergeCommit, rebase)
			if err != nil {
				return err
			}

			id := args[0]
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

			if t.MergeExecuted {
				return &ExitError{Err: fmt.Errorf("task %s already merged", t.ID), Code: 3}
			}
			active, err := state.ActiveAttempt(home, id)
			if err != nil {
				if errors.Is(err, state.ErrNoActiveAttempt) {
					return &ExitError{Err: fmt.Errorf("task %q has no active attempt", id), Code: 3}
				}
				return fmt.Errorf("read active attempt for task %q: %w", id, err)
			}

			if local {
				return runLocalMerge(cmd, home, t, active)
			}
			return runPRMerge(cmd, home, t, method)
		},
	}

	cmd.Flags().BoolVar(&squash, "squash", false, "squash merge (default for PR merges)")
	cmd.Flags().BoolVar(&mergeCommit, "merge", false, "merge commit instead of squash")
	cmd.Flags().BoolVar(&rebase, "rebase", false, "rebase merge")
	cmd.Flags().BoolVar(&local, "local", false, "fast-forward merge for local-only tasks")
	return cmd
}

func resolveMergeMethod(squash, mergeCommit, rebase bool) (string, error) {
	count := 0
	for _, v := range []bool{squash, mergeCommit, rebase} {
		if v {
			count++
		}
	}
	if count > 1 {
		return "", &ExitError{Err: fmt.Errorf("only one of --squash, --merge, --rebase may be specified"), Code: 2}
	}
	switch {
	case mergeCommit:
		return "merge", nil
	case rebase:
		return "rebase", nil
	default:
		return "squash", nil
	}
}

func runPRMerge(cmd *cobra.Command, home string, t state.Task, method string) error {
	if t.PR == "" {
		return &ExitError{Err: fmt.Errorf("no PR recorded for %s", t.ID), Code: 3}
	}

	// A gate-opened PR (atqamz/hand#69) can populate t.PR without hand having merged it, so t.PR no
	// longer implies hand hasn't seen it merged yet; check before running CI checks against a PR gh
	// already closed.
	observation := ghutil.ObserveMergeState(cmd.Context(), t.PR)
	// Neither an absent PR nor an unobserved one may fall through to gh pr merge: merging is
	// irreversible, and only a completed observation proves this PR exists and is still open.
	if observation.Absent() {
		return &ExitError{Err: fmt.Errorf("PR %s does not exist", t.PR), Code: 3}
	}
	if observation.Unknown() {
		return &ExitError{Err: fmt.Errorf("state of PR %s could not be observed, so hand refuses to merge it: %s", t.PR, observation.Reason()), Code: 3}
	}
	if observation.Merged {
		return convergeAlreadyMergedPR(cmd, home, t)
	}

	green, err := prChecksGreen(t.PR)
	if err != nil {
		return err
	}
	if !green {
		return &ExitError{Err: fmt.Errorf("PR checks for %s are not green", t.ID), Code: 3}
	}

	out, stderr, err := integration.Run(context.Background(), "github/gh", "", "pr", "merge", t.PR, "--"+method)
	out = append(out, stderr...)
	if err != nil {
		return fmt.Errorf("gh pr merge failed: %s", strings.TrimSpace(string(out)))
	}

	mergedAt := time.Now().UTC().Format(time.RFC3339)
	if err := state.SetTaskMerge(home, t.ID, mergedAt); err != nil {
		return fmt.Errorf("write task state: %w", err)
	}
	t.MergeExecuted = true
	t.MergeExecutedAt = mergedAt

	if err := syncProjectAfterMerge(cmd, home, t); err != nil {
		return err
	}

	var doc axi.Doc
	doc.Field("id", t.ID)
	doc.Field("result", "merged")
	doc.Field("method", method)
	doc.Field("pr", t.PR)
	doc.Field("merged", t.MergeExecutedAt)
	doc.Help("Run `hand teardown " + t.ID + "` to release this task's worktree and pane")
	return doc.Render(cmd.OutOrStdout())
}

// Reached whether hand merged the PR itself or it merged out of band (a merge queue, release-please,
// the web UI, or a stacked PR routed through pulls/N/merge-async). SetTaskMergeAnnounced, not
// SetTaskMerge, keeps what hand observed distinguishable from what hand did.
func convergeAlreadyMergedPR(cmd *cobra.Command, home string, t state.Task) error {
	if err := state.SetTaskMergeAnnounced(home, t.ID); err != nil {
		return fmt.Errorf("write task state: %w", err)
	}

	if err := syncProjectAfterMerge(cmd, home, t); err != nil {
		return err
	}

	var doc axi.Doc
	doc.Field("id", t.ID)
	doc.Field("result", "converged")
	doc.Field("pr", t.PR)
	doc.Help("hand observed PR " + t.PR + " already merged on GitHub and recorded it; run `hand teardown " + t.ID + "` to release this task's worktree and pane")
	return doc.Render(cmd.OutOrStdout())
}

func syncProjectAfterMerge(cmd *cobra.Command, home string, t state.Task) error {
	proj, exists, err := project.Find(home, t.Project)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	releaseProject, err := state.Lock(home, "project:"+proj.Name)
	if err != nil {
		return fmt.Errorf("lock project %q: %w", proj.Name, err)
	}
	_, syncErr := syncOneProject(home, proj)
	releaseProject()
	if syncErr != nil {
		if _, printErr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: project sync failed: %v\n", syncErr); printErr != nil {
			return printErr
		}
	}
	return nil
}

func runLocalMerge(cmd *cobra.Command, home string, t state.Task, active state.Attempt) error {
	worktree := active.Worktree
	dirty, err := hasUncommittedChanges(worktree)
	if err != nil {
		return err
	}
	if dirty {
		return &ExitError{Err: fmt.Errorf("uncommitted changes in worktree %s", worktree), Code: 3}
	}

	branch, err := currentBranch(worktree)
	if err != nil {
		return err
	}

	releaseProject, err := state.Lock(home, "project:"+t.Project)
	if err != nil {
		return fmt.Errorf("lock project %q: %w", t.Project, err)
	}
	defer releaseProject()

	clonePath := filepath.Join(home, "projects", t.Project)
	defaultBr, err := defaultBranch(clonePath)
	if err != nil {
		return err
	}

	if out, err := runManagedCore(context.Background(), "git", clonePath, "checkout", defaultBr); err != nil {
		return fmt.Errorf("git checkout %s failed: %s", defaultBr, managedCommandFailure(out, err))
	}

	if out, err := runManagedCore(context.Background(), "git", clonePath, "merge", "--ff-only", branch); err != nil {
		return &ExitError{Err: fmt.Errorf("fast-forward not possible: %s", managedCommandFailure(out, err)), Code: 3}
	}

	mergedAt := time.Now().UTC().Format(time.RFC3339)
	if err := state.SetTaskMerge(home, t.ID, mergedAt); err != nil {
		return fmt.Errorf("write task state: %w", err)
	}
	t.MergeExecuted = true
	t.MergeExecutedAt = mergedAt

	var doc axi.Doc
	doc.Field("id", t.ID)
	doc.Field("result", "merged")
	doc.Field("method", "local-fast-forward")
	doc.Field("branch", branch)
	doc.Field("into", defaultBr)
	doc.Field("merged", t.MergeExecutedAt)
	doc.Help("Run `hand teardown " + t.ID + "` to release this task's worktree and pane")
	return doc.Render(cmd.OutOrStdout())
}

// Parses `gh pr checks --json bucket` rather than trusting the process exit code, since gh's exit codes
// (0 pass, 8 pending, 1 fail) are harder to distinguish reliably across gh versions than the JSON payload.
func prChecksGreen(pr string) (bool, error) {
	stdout, stderr, runErr := integration.Run(context.Background(), "github/gh", "", "pr", "checks", pr, "--json", "bucket")

	var checks []struct {
		Bucket string `json:"bucket"`
	}
	if err := json.Unmarshal(stdout, &checks); err != nil {
		if runErr != nil {
			return false, fmt.Errorf("gh pr checks failed: %s", strings.TrimSpace(string(stderr)))
		}
		return false, fmt.Errorf("parse gh pr checks output: %w", err)
	}
	if len(checks) == 0 {
		return false, fmt.Errorf("gh pr checks reported no checks for %s", pr)
	}
	for _, c := range checks {
		if c.Bucket != "pass" && c.Bucket != "skipping" {
			return false, nil
		}
	}
	return true, nil
}
