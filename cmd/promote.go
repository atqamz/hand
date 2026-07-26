package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/atqamz/secondhand/internal/harness"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/atqamz/secondhand/internal/worktree"
	"github.com/spf13/cobra"
)

func newPromoteCmd() *cobra.Command {
	var harnessName string
	var model string
	var effort string

	cmd := &cobra.Command{
		Use:   "promote <id>",
		Short: "Promote a completed scout task into a ship task",
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
			if t.Kind != state.KindScout {
				return &ExitError{Err: fmt.Errorf("task %q is not a scout", id), Code: 3}
			}

			reportRel := filepath.Join("data", id, "report.md")
			if _, err := os.Stat(filepath.Join(home, reportRel)); err != nil {
				return &ExitError{Err: fmt.Errorf("scout report not found at %s", reportRel), Code: 3}
			}

			client := herdr.NewClient()
			if status := paneAgentStatus(client, t.Herdr.PaneID); status != string(herdr.StatusDone) && status != string(herdr.StatusUnknown) {
				return &ExitError{Err: fmt.Errorf("task %q is not a completed scout (agent state: %s)", id, status), Code: 3}
			}

			briefRel := filepath.Join("data", id, "brief.md")
			briefAbs := filepath.Join(home, briefRel)
			if _, err := os.Stat(briefAbs); err != nil {
				return &ExitError{Err: fmt.Errorf("brief not found at %s", briefRel), Code: 3}
			}

			harnessFromFlag := harnessName != ""
			if !harnessFromFlag {
				harnessName = configDefault(home, "harness", "claude")
			}
			if !harness.IsSupported(harnessName) {
				return usageValue(harnessFromFlag, fmt.Errorf("harness %q not recognized", harnessName))
			}
			if model == "" {
				model = configDefault(home, "model", "")
			}
			if effort == "" {
				effort = configDefault(home, "effort", "")
			}

			proj, exists, err := project.Find(home, t.Project)
			if err != nil {
				return err
			}
			if !exists {
				return &ExitError{Err: fmt.Errorf("project %q not registered", t.Project), Code: 3}
			}

			releaseProject, err := state.Lock(home, "project:"+proj.Name)
			if err != nil {
				return fmt.Errorf("lock project %q: %w", proj.Name, err)
			}
			defer releaseProject()

			oldWorktree := t.Worktree
			oldWorkspaceID := t.Herdr.WorkspaceID
			oldTabID := t.Herdr.TabID

			clonePath := filepath.Join(home, "projects", proj.Name)
			wt, err := worktree.Get(clonePath, "hand:"+id)
			if err != nil {
				return fmt.Errorf("acquire treehouse worktree: %w", err)
			}
			releaseWorktree, err := state.Lock(home, "worktree:"+wt)
			if err != nil {
				return reportSpawnCleanup(fmt.Errorf("lock worktree %q: %w", wt, err), worktree.Return(wt, true))
			}
			defer releaseWorktree()

			if conflict, err := worktree.CheckCollision(home, wt, id); err != nil {
				return reportSpawnCleanup(err, worktree.Return(wt, true))
			} else if conflict != "" {
				return reportSpawnCleanup(&ExitError{Err: fmt.Errorf("worktree collision: %s already holds %s", conflict, wt), Code: 3}, worktree.Return(wt, true))
			}

			ws, found, err := client.FindWorkspaceByLabel(proj.Name)
			createdWorkspace := false
			if err == nil && !found {
				ws, err = client.WorkspaceCreate(clonePath, proj.Name)
				createdWorkspace = err == nil
			}
			if err != nil {
				return reportSpawnCleanup(fmt.Errorf("herdr workspace lookup/create failed: %w", err), worktree.Return(wt, true))
			}

			tab, pane, err := client.TabCreate(ws.WorkspaceID, wt, id)
			if err != nil {
				cleanupErrs := []error{worktree.Return(wt, true)}
				if createdWorkspace {
					cleanupErrs = append(cleanupErrs, client.WorkspaceClose(ws.WorkspaceID))
				}
				return reportSpawnCleanup(fmt.Errorf("herdr tab create failed: %w", err), cleanupErrs...)
			}

			launchCmd, err := harness.Build(harnessName, harness.Options{
				Worktree: wt,
				Brief:    briefAbs,
				Model:    model,
				Effort:   effort,
			})
			if err != nil {
				return reportSpawnCleanup(err, closeTaskTab(client, ws.WorkspaceID, tab.TabID), worktree.Return(wt, true))
			}

			if err := client.PaneRun(pane.PaneID, launchCmd); err != nil {
				return reportSpawnCleanup(fmt.Errorf("send launch command failed: %w", err), closeTaskTab(client, ws.WorkspaceID, tab.TabID), worktree.Return(wt, true))
			}

			// Dashboard is deliberately left untouched: the task's row stays
			// KindScout even though the underlying task becomes a ship task.
			t.Kind = state.KindShip
			t.Harness = harnessName
			t.Model = model
			t.Effort = effort
			t.Worktree = wt
			t.Herdr = state.Herdr{
				Session:     "default",
				WorkspaceID: ws.WorkspaceID,
				TabID:       tab.TabID,
				PaneID:      pane.PaneID,
			}
			if err := state.Write(home, t); err != nil {
				return reportSpawnCleanup(fmt.Errorf("write task state: %w", err), closeTaskTab(client, ws.WorkspaceID, tab.TabID), worktree.Return(wt, true))
			}

			if err := closeTaskTab(client, oldWorkspaceID, oldTabID); err != nil && !errors.Is(err, errTaskTabNotFound) {
				if _, printErr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: herdr tab close failed: %v\n", err); printErr != nil {
					return printErr
				}
			}
			if err := worktree.Return(oldWorktree, true); err != nil {
				if _, printErr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: return scout worktree failed: %v\n", err); printErr != nil {
					return printErr
				}
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "promoted %s: scout -> ship project=%s harness=%s\n", id, proj.Name, harnessName); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&harnessName, "harness", "", "harness for the new ship worker (default: config/harness, or claude)")
	cmd.Flags().StringVar(&model, "model", "", "model override for harnesses that support it")
	cmd.Flags().StringVar(&effort, "effort", "", "effort override for harnesses that support it")
	return cmd
}
