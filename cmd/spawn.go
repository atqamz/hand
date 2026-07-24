package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/secondhand/internal/harness"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/atqamz/secondhand/internal/worktree"
	"github.com/spf13/cobra"
)

func newSpawnCmd() *cobra.Command {
	var scout bool
	var harnessName string
	var model string
	var effort string

	cmd := &cobra.Command{
		Use:   "spawn <id> <project>",
		Short: "Spawn a worker agent in an isolated worktree",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			projectName := args[1]

			home, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			proj, exists, err := project.Find(home, projectName)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("project %q not registered", projectName)
			}

			if active, err := state.Exists(home, id); err != nil {
				return err
			} else if active {
				return fmt.Errorf("task %q already active", id)
			}

			briefRel := filepath.Join("data", id, "brief.md")
			briefAbs := filepath.Join(home, briefRel)
			if _, err := os.Stat(briefAbs); err != nil {
				return fmt.Errorf("brief not found at %s", briefRel)
			}

			if harnessName == "" {
				harnessName = configDefault(home, "harness", "claude")
			}
			if !harness.IsSupported(harnessName) {
				return fmt.Errorf("harness %q not recognized", harnessName)
			}
			if model == "" {
				model = configDefault(home, "model", "")
			}
			if effort == "" {
				effort = configDefault(home, "effort", "")
			}

			clonePath := filepath.Join(home, "projects", proj.Name)
			wt, err := worktree.Get(clonePath, "hand:"+id)
			if err != nil {
				return fmt.Errorf("acquire treehouse worktree: %w", err)
			}

			if conflict, err := worktree.CheckCollision(home, wt, id); err != nil {
				_ = worktree.Return(wt, true)
				return err
			} else if conflict != "" {
				_ = worktree.Return(wt, true)
				return fmt.Errorf("worktree collision: %s already holds %s", conflict, wt)
			}

			client := herdr.NewClient()
			ws, found, err := client.FindWorkspaceByLabel(proj.Name)
			if err != nil {
				_ = worktree.Return(wt, true)
				return fmt.Errorf("herdr workspace lookup failed: %w", err)
			}
			if !found {
				ws, err = client.WorkspaceCreate(clonePath, proj.Name)
				if err != nil {
					_ = worktree.Return(wt, true)
					return fmt.Errorf("herdr workspace create failed: %w", err)
				}
			}

			tab, pane, err := client.TabCreate(ws.WorkspaceID, wt, id)
			if err != nil {
				_ = worktree.Return(wt, true)
				return fmt.Errorf("herdr tab create failed: %w", err)
			}

			launchCmd, err := harness.Build(harnessName, harness.Options{
				Worktree: wt,
				Brief:    briefAbs,
				Model:    model,
				Effort:   effort,
			})
			if err != nil {
				_ = client.TabClose(tab.TabID)
				_ = worktree.Return(wt, true)
				return err
			}

			if err := client.PaneRun(pane.PaneID, launchCmd); err != nil {
				_ = client.TabClose(tab.TabID)
				_ = worktree.Return(wt, true)
				return fmt.Errorf("send launch command failed: %w", err)
			}

			kind := state.KindShip
			if scout {
				kind = state.KindScout
			}

			task := state.Task{
				ID:       id,
				Project:  proj.Name,
				Kind:     kind,
				Harness:  harnessName,
				Model:    model,
				Effort:   effort,
				Worktree: wt,
				Brief:    briefRel,
				Herdr: state.Herdr{
					Session:     "default",
					WorkspaceID: ws.WorkspaceID,
					TabID:       tab.TabID,
					PaneID:      pane.PaneID,
				},
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}
			if err := state.Write(home, task); err != nil {
				return fmt.Errorf("write task state: %w", err)
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "spawned %s project=%s kind=%s harness=%s worktree=%s\n", id, proj.Name, kind, harnessName, wt); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&scout, "scout", false, "mark as scout task (deliverable is a report, not a PR)")
	cmd.Flags().StringVar(&harnessName, "harness", "", "agent harness to launch (default: config/harness, or claude)")
	cmd.Flags().StringVar(&model, "model", "", "model override for harnesses that support it")
	cmd.Flags().StringVar(&effort, "effort", "", "effort level for harnesses that support it")
	return cmd
}

func configDefault(home, name, fallback string) string {
	data, err := os.ReadFile(filepath.Join(home, "config", name))
	if err != nil {
		return fallback
	}
	return strings.TrimSpace(string(data))
}
