package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

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
		Args:  usageArgs(cobra.MaximumNArgs(1)),
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

// reportedJSON mirrors one classified line from state.ReportLine for JSON
// output; Malformed lines carry their raw text in Note with State left empty.
type reportedJSON struct {
	State string `json:"state"`
	Note  string `json:"note"`
}

type statusJSON struct {
	ID            string        `json:"id"`
	Project       string        `json:"project"`
	Kind          string        `json:"kind"`
	Harness       string        `json:"harness,omitempty"`
	AgentState    string        `json:"agent_state"`
	Worktree      string        `json:"worktree"`
	Herdr         state.Herdr   `json:"herdr"`
	PR            string        `json:"pr"`
	CreatedAt     string        `json:"created_at"`
	Reported      *reportedJSON `json:"reported,omitempty"`
	ReportHistory []string      `json:"report_history,omitempty"`
}

func runStatusFleet(cmd *cobra.Command, home string, client *herdr.Client, asJSON bool) error {
	tasks, err := state.List(home)
	if err != nil {
		return err
	}

	rows := make([]statusJSON, 0, len(tasks))
	for _, t := range tasks {
		agentState := paneAgentStatus(client, t.Herdr.PaneID)
		rows = append(rows, statusJSON{
			ID: t.ID, Project: t.Project, Kind: t.Kind, Harness: t.Harness,
			AgentState: agentState,
			Worktree:   t.Worktree, Herdr: t.Herdr, PR: t.PR, CreatedAt: t.CreatedAt,
			Reported: lastReportedJSON(home, t.ID),
		})
	}

	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	for _, r := range rows {
		agentState := r.AgentState + reportSuffix(home, r.ID, r.AgentState)
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.ID, r.Project, r.Kind, agentState, formatAge(r.CreatedAt)); err != nil {
			return err
		}
	}
	return w.Flush()
}

// reportSuffix flags, in the fleet table's state column, whether a not-busy pane
// (herdr's idle or done - see herdr.Status) left a terminal report behind or not -
// the same distinction SPECS.md's classifier draws between idle-unreported and an
// absorbed stop. Any other agent state is left unadorned; herdr's own state is
// already informative there.
func reportSuffix(home, id, agentState string) string {
	if !herdr.Status(agentState).NotBusy() {
		return ""
	}
	last, ok, err := state.LastReport(home, id)
	if err != nil || !ok || last.Malformed || last.State == "" || last.State == state.ReportWorking {
		return " (unreported)"
	}
	return fmt.Sprintf(" (reported: %s)", last.State)
}

func lastReportedJSON(home, id string) *reportedJSON {
	last, ok, err := state.LastReport(home, id)
	if err != nil || !ok {
		return nil
	}
	if last.Malformed {
		return &reportedJSON{Note: last.Raw}
	}
	return &reportedJSON{State: last.State, Note: last.Note}
}

func runStatusSingle(cmd *cobra.Command, home string, client *herdr.Client, id string, asJSON bool) error {
	t, err := state.Read(home, id)
	if err != nil {
		return asPrecondition(err)
	}
	agentState := paneAgentStatus(client, t.Herdr.PaneID)

	const historyLen = 5
	tail, err := state.ReportTail(home, id, historyLen)
	if err != nil {
		return err
	}
	history := make([]string, len(tail))
	for i, line := range tail {
		history[i] = reportLineText(line)
	}

	if asJSON {
		out := statusJSON{
			ID: t.ID, Project: t.Project, Kind: t.Kind, Harness: t.Harness,
			AgentState: agentState, Worktree: t.Worktree, Herdr: t.Herdr, PR: t.PR, CreatedAt: t.CreatedAt,
			Reported: lastReportedJSON(home, id), ReportHistory: history,
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	pr := t.PR
	if pr == "" {
		pr = "(none)"
	}
	reported := "(none)"
	if len(tail) > 0 {
		reported = reportLineText(tail[len(tail)-1])
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
		fmt.Sprintf("Reported:   %s", reported),
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	if len(tail) > 0 {
		if _, err := fmt.Fprintln(w, "\nReport history (reported by worker, not verified current truth):"); err != nil {
			return err
		}
		for _, line := range tail {
			if _, err := fmt.Fprintf(w, "  %s\n", reportLineText(line)); err != nil {
				return err
			}
		}
	}
	return nil
}

func reportLineText(line state.ReportLine) string {
	if line.Malformed {
		return line.Raw
	}
	return fmt.Sprintf("%s: %s", line.State, line.Note)
}

func formatAge(createdAt string) string {
	age := dashboard.FormatAge(createdAt)
	if age == "just now" || age == "unknown" {
		return age
	}
	return age + " ago"
}
