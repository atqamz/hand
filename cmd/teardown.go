package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/atqamz/secondhand/internal/worktree"
	"github.com/spf13/cobra"
)

var errTaskTabNotFound = errors.New("task tab not found")

func newTeardownCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "teardown <id>",
		Short: "Clean up a completed task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			home, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			t, err := state.Read(home, id)
			if err != nil {
				return err
			}

			if !force {
				if err := checkLandedWork(home, t); err != nil {
					return err
				}
			}

			client := herdr.NewClient()
			if err := closeTaskTab(client, t.Herdr.WorkspaceID, t.Herdr.TabID); err != nil {
				if errors.Is(err, errTaskTabNotFound) {
					return err
				}
				if _, printErr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: herdr tab close failed: %v\n", err); printErr != nil {
					return printErr
				}
			}

			if err := worktree.Return(t.Worktree, force); err != nil {
				return err
			}

			if err := state.Delete(home, id); err != nil {
				return err
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

func checkLandedWork(home string, t state.Task) error {
	if t.Kind == state.KindScout {
		reportPath := filepath.Join("data", t.ID, "report.md")
		if _, err := os.Stat(filepath.Join(home, reportPath)); err != nil {
			return fmt.Errorf("report not found at %s", reportPath)
		}
		return nil
	}

	dirty, err := hasUncommittedChanges(t.Worktree)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("uncommitted changes in worktree %s", t.Worktree)
	}

	if t.PR != "" {
		merged, err := prIsMerged(t.PR)
		if err != nil {
			return err
		}
		if !merged {
			return fmt.Errorf("PR %s is not merged", t.PR)
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
			return fmt.Errorf("branch for %s is not merged into the default branch", t.ID)
		}
		return nil
	}

	return fmt.Errorf("no PR recorded for %s and project is not local-only: work may not be landed", t.ID)
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

func prIsMerged(pr string) (bool, error) {
	out, err := exec.Command("gh", "pr", "view", pr, "--json", "state").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("gh pr view failed: %s", strings.TrimSpace(string(out)))
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return false, fmt.Errorf("parse gh pr view output: %w", err)
	}
	return body.State == "MERGED", nil
}

func branchIsMerged(clonePath, worktreePath string) (bool, error) {
	branch, err := currentBranch(worktreePath)
	if err != nil {
		return false, err
	}

	c := exec.Command("git", "branch", "--merged")
	c.Dir = clonePath
	out, err := c.Output()
	if err != nil {
		return false, fmt.Errorf("git branch --merged failed: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*")) == branch {
			return true, nil
		}
	}
	return false, nil
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
		return fmt.Errorf("%w: herdr tab %s not found in workspace %s", errTaskTabNotFound, tabID, workspaceID)
	}
	if len(tabs) == 1 {
		return client.WorkspaceClose(workspaceID)
	}
	return client.TabClose(tabID)
}
