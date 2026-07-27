package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/ghutil"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/atqamz/secondhand/internal/worktree"
	"github.com/spf13/cobra"
)

func newTeardownCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "teardown <id>",
		Short: "Clean up a completed task",
		Args:  usageArgs(cobra.ExactArgs(1)),
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
				return asPrecondition(err)
			}

			if !force {
				if err := checkLandedWork(cmd.Context(), home, t); err != nil {
					return err
				}
			}
			releaseProject, err := state.Lock(home, "project:"+t.Project)
			if err != nil {
				return fmt.Errorf("lock project %q: %w", t.Project, err)
			}
			defer releaseProject()

			client := herdr.NewClient()
			if err := closeTaskTab(client, t.Herdr.WorkspaceID, t.Herdr.TabID); err != nil {
				if _, printErr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: herdr tab close failed: %v\n", err); printErr != nil {
					return printErr
				}
			}

			if err := worktree.Return(t.Worktree, force); err != nil {
				return err
			}

			if err := state.Delete(home, id); err != nil {
				return asPrecondition(err)
			}

			completion := completionFor(t, force)
			dashPath := filepath.Join(home, "data", "dashboard.md")
			if err := dashboard.Update(dashPath, dashboard.UpdateOpts{Complete: &completion}); err != nil {
				return fmt.Errorf("update dashboard: %w", err)
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "teardown %s complete\n", id); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip landed-work checks")
	return cmd
}

func completionFor(t state.Task, forced bool) dashboard.Completion {
	c := dashboard.Completion{ID: t.ID, Project: t.Project, Kind: t.Kind}
	switch {
	case forced:
		c.Outcome = "torn-down"
		c.Detail = "forced (landed-work checks skipped)"
	case t.Kind == state.KindScout:
		c.Outcome = "done"
		c.Detail = "report " + filepath.Join("data", t.ID, "report.md")
	case t.PR != "":
		c.Outcome = "merged"
		c.Detail = "PR " + t.PR
	default:
		c.Outcome = "merged"
		c.Detail = "branch merged"
	}
	return c
}

func checkLandedWork(ctx context.Context, home string, t state.Task) error {
	if t.Kind == state.KindScout {
		reportPath := filepath.Join("data", t.ID, "report.md")
		if _, err := os.Stat(filepath.Join(home, reportPath)); err != nil {
			return &ExitError{Err: fmt.Errorf("report not found at %s", reportPath), Code: 3}
		}
		return nil
	}

	dirty, err := hasUncommittedChanges(t.Worktree)
	if err != nil {
		return err
	}
	if dirty {
		return &ExitError{Err: fmt.Errorf("uncommitted changes in worktree %s", t.Worktree), Code: 3}
	}

	if t.PR != "" {
		merged, err := ghutil.PRIsMerged(ctx, t.PR)
		if err != nil {
			return err
		}
		if !merged {
			return &ExitError{Err: fmt.Errorf("PR %s is not merged", t.PR), Code: 3}
		}
		return nil
	}

	proj, exists, err := project.Find(home, t.Project)
	if err != nil {
		return err
	}
	if exists && proj.Mode == project.ModeLocalOnly {
		merged, err := branchIsMerged(filepath.Join(home, "projects", t.Project), t.Worktree)
		if err != nil {
			return err
		}
		if !merged {
			return &ExitError{Err: fmt.Errorf("branch for %s is not merged into the default branch", t.ID), Code: 3}
		}
		return nil
	}

	return &ExitError{Err: fmt.Errorf("no PR recorded for %s and project is not local-only: work may not be landed", t.ID), Code: 3}
}

func hasUncommittedChanges(worktreePath string) (bool, error) {
	c := exec.Command("git", "status", "--porcelain")
	c.Dir = worktreePath
	out, err := c.Output()
	if err != nil {
		return false, fmt.Errorf("git status failed: %w", err)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

func branchIsMerged(clonePath, worktreePath string) (bool, error) {
	branch, err := currentBranch(worktreePath)
	if err != nil {
		return false, err
	}

	defaultBranch, err := defaultBranch(clonePath)
	if err != nil {
		return false, err
	}
	c := exec.Command("git", "branch", "--merged", defaultBranch)
	c.Dir = clonePath
	out, err := c.Output()
	if err != nil {
		return false, fmt.Errorf("git branch --merged %s failed: %w", defaultBranch, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "*+"))
		if line == branch {
			return true, nil
		}
	}
	return false, nil
}

func defaultBranch(clonePath string) (string, error) {
	c := exec.Command("git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	c.Dir = clonePath
	out, err := c.Output()
	if err == nil {
		branch := strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/")
		if branch != "" {
			return branch, nil
		}
	}

	c = exec.Command("git", "remote", "show", "origin")
	c.Dir = clonePath
	out, err = c.Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "HEAD" && fields[1] == "branch:" && fields[2] != "" {
				return fields[2], nil
			}
		}
	}

	for _, branch := range []string{"main", "master"} {
		c = exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
		c.Dir = clonePath
		if err := c.Run(); err == nil {
			return branch, nil
		}
	}
	return "", fmt.Errorf("resolve default branch failed")
}

func currentBranch(worktreePath string) (string, error) {
	c := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	c.Dir = worktreePath
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// closeTaskTab closes the task's tab, or the whole workspace if this was its last tab
// (herdr refuses to close a workspace's only tab directly).
//
// A tab that is no longer listed is already closed, which is this step's goal, so
// it is success and not an error: teardown removes several resources in sequence
// and any of the later steps can fail, so the whole command has to be runnable a
// second time without tripping over the work the first run already did.
func closeTaskTab(client *herdr.Client, workspaceID, tabID string) error {
	tabs, err := client.TabList(workspaceID)
	if err != nil {
		return err
	}
	found := false
	for _, tab := range tabs {
		if tab.TabID == tabID {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	if len(tabs) == 1 {
		return client.WorkspaceClose(workspaceID)
	}
	return client.TabClose(tabID)
}
