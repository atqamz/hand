package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/spf13/cobra"
)

func newMergeCmd() *cobra.Command {
	var squash, mergeCommit, rebase, local bool

	cmd := &cobra.Command{
		Use:   "merge <id>",
		Short: "Merge a task's completed work",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
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
				return err
			}

			if local {
				return runLocalMerge(cmd, home, t)
			}

			method, err := resolveMergeMethod(squash, mergeCommit, rebase)
			if err != nil {
				return err
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
		return "", fmt.Errorf("only one of --squash, --merge, --rebase may be specified")
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

	green, err := prChecksGreen(t.PR)
	if err != nil {
		return err
	}
	if !green {
		return &ExitError{Err: fmt.Errorf("PR checks for %s are not green", t.ID), Code: 3}
	}

	out, err := exec.Command("gh", "pr", "merge", t.PR, "--"+method).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh pr merge failed: %s", strings.TrimSpace(string(out)))
	}

	t.Merged = true
	t.MergedAt = time.Now().UTC().Format(time.RFC3339)
	if err := state.Write(home, t); err != nil {
		return fmt.Errorf("write task state: %w", err)
	}

	if proj, exists, err := project.Find(home, t.Project); err != nil {
		return err
	} else if exists {
		releaseProject, err := state.Lock(home, "project:"+proj.Name)
		if err != nil {
			return fmt.Errorf("lock project %q: %w", proj.Name, err)
		}
		_, advanced, syncErr := syncOneProject(home, proj)
		releaseProject()
		if syncErr != nil {
			if _, printErr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: project sync failed: %v\n", syncErr); printErr != nil {
				return printErr
			}
		} else if advanced {
			if err := updateDashboardProjects(home); err != nil {
				return err
			}
		}
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "merged %s: %s\n", t.ID, t.PR); err != nil {
		return err
	}
	return nil
}

func runLocalMerge(cmd *cobra.Command, home string, t state.Task) error {
	dirty, err := hasUncommittedChanges(t.Worktree)
	if err != nil {
		return err
	}
	if dirty {
		return &ExitError{Err: fmt.Errorf("uncommitted changes in worktree %s", t.Worktree), Code: 3}
	}

	branch, err := currentBranch(t.Worktree)
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

	checkout := exec.Command("git", "checkout", defaultBr)
	checkout.Dir = clonePath
	if out, err := checkout.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout %s failed: %s", defaultBr, strings.TrimSpace(string(out)))
	}

	merge := exec.Command("git", "merge", "--ff-only", branch)
	merge.Dir = clonePath
	if out, err := merge.CombinedOutput(); err != nil {
		return &ExitError{Err: fmt.Errorf("fast-forward not possible: %s", strings.TrimSpace(string(out))), Code: 3}
	}

	t.Merged = true
	t.MergedAt = time.Now().UTC().Format(time.RFC3339)
	if err := state.Write(home, t); err != nil {
		return fmt.Errorf("write task state: %w", err)
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "merged %s: local fast-forward into %s\n", t.ID, defaultBr); err != nil {
		return err
	}
	return nil
}

// prChecksGreen parses `gh pr checks --json bucket` rather than trusting the
// process exit code, since gh's exit codes (0 pass, 8 pending, 1 fail) are
// harder to distinguish reliably across gh versions than the JSON payload.
func prChecksGreen(pr string) (bool, error) {
	var stdout, stderr bytes.Buffer
	c := exec.Command("gh", "pr", "checks", pr, "--json", "bucket")
	c.Stdout = &stdout
	c.Stderr = &stderr
	runErr := c.Run()

	var checks []struct {
		Bucket string `json:"bucket"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &checks); err != nil {
		if runErr != nil {
			return false, fmt.Errorf("gh pr checks failed: %s", strings.TrimSpace(stderr.String()))
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
