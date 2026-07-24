package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "status [id]",
		Short: "Show fleet overview or single-task detail",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			client := herdr.NewClient()

			if len(args) == 1 {
				return runStatusSingle(cmd, home, client, args[0], asJSON)
			}
			return runStatusFleet(cmd, home, client, asJSON)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	return cmd
}

// paneAgentStatus degrades gracefully to "unknown" when herdr is unreachable or the
// pane can't be queried, per SPECS.md's fail-open policy for read operations.
func paneAgentStatus(client *herdr.Client, paneID string) string {
	if paneID == "" {
		return string(herdr.StatusUnknown)
	}
	pane, err := client.PaneGet(paneID)
	if err != nil || pane.AgentStatus == "" {
		return string(herdr.StatusUnknown)
	}
	return string(pane.AgentStatus)
}

type statusJSON struct {
	ID         string      `json:"id"`
	Project    string      `json:"project"`
	Kind       string      `json:"kind"`
	Harness    string      `json:"harness,omitempty"`
	AgentState string      `json:"agent_state"`
	Worktree   string      `json:"worktree"`
	Herdr      state.Herdr `json:"herdr"`
	PR         string      `json:"pr"`
	CreatedAt  string      `json:"created_at"`
}

func runStatusFleet(cmd *cobra.Command, home string, client *herdr.Client, asJSON bool) error {
	tasks, err := state.List(home)
	if err != nil {
		return err
	}

	rows := make([]statusJSON, 0, len(tasks))
	for _, t := range tasks {
		rows = append(rows, statusJSON{
			ID: t.ID, Project: t.Project, Kind: t.Kind, Harness: t.Harness,
			AgentState: paneAgentStatus(client, t.Herdr.PaneID),
			Worktree:   t.Worktree, Herdr: t.Herdr, PR: t.PR, CreatedAt: t.CreatedAt,
		})
	}

	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}

	w := cmd.OutOrStdout()
	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "%-16s%-12s%-8s%-12s%s\n", r.ID, r.Project, r.Kind, r.AgentState, formatAge(r.CreatedAt)); err != nil {
			return err
		}
	}
	return nil
}

func runStatusSingle(cmd *cobra.Command, home string, client *herdr.Client, id string, asJSON bool) error {
	t, err := state.Read(home, id)
	if err != nil {
		return err
	}
	agentState := paneAgentStatus(client, t.Herdr.PaneID)

	if asJSON {
		out := statusJSON{
			ID: t.ID, Project: t.Project, Kind: t.Kind, Harness: t.Harness,
			AgentState: agentState, Worktree: t.Worktree, Herdr: t.Herdr, PR: t.PR, CreatedAt: t.CreatedAt,
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	pr := t.PR
	if pr == "" {
		pr = "(none)"
	}

	w := cmd.OutOrStdout()
	lines := []string{
		fmt.Sprintf("Task:       %s", t.ID),
		fmt.Sprintf("Project:    %s", t.Project),
		fmt.Sprintf("Kind:       %s", t.Kind),
		fmt.Sprintf("Harness:    %s", t.Harness),
		fmt.Sprintf("Model:      %s", t.Model),
		fmt.Sprintf("State:      %s", agentState),
		fmt.Sprintf("Worktree:   %s", t.Worktree),
		fmt.Sprintf("Herdr:      %s / %s", t.Herdr.Session, t.Herdr.TabID),
		fmt.Sprintf("Created:    %s", formatAge(t.CreatedAt)),
		fmt.Sprintf("PR:         %s", pr),
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func formatAge(createdAt string) string {
	age := dashboard.FormatAge(createdAt)
	if age == "just now" || age == "unknown" {
		return age
	}
	return age + " ago"
}
