package cmd

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/home"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var asJSON, full bool

	cmd := &cobra.Command{
		Use:   "status [id]",
		Short: "Show fleet overview or single-task detail",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			client := herdr.NewClient()

			if len(args) == 1 {
				return runStatusSingle(cmd, home, client, args[0], asJSON, full)
			}
			return runStatusFleet(cmd, home, client, asJSON)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	cmd.Flags().BoolVar(&full, "full", false, "show the reported line and history untruncated, with no history dedup (single task only)")
	return cmd
}

// reportSummaryBudget bounds the rendered length of one report line (state
// prefix plus note) in the human-readable single-task view, in runes so a
// multi-byte character never lands half-cut. A worker's status prose has run
// 2.7-4.3 KB for a single task; this keeps a normal terse report (which is
// what the vocabulary in CLAUDE.md/AGENTS.md asks for) untouched while
// bounding the pathological case. --json and --full both bypass it, since a
// machine consumer needs the whole field and --full is the explicit opt-out.
const reportSummaryBudget = 200

// truncateReportLine renders line the same way reportLineText does, then caps
// it to budget runes. The state-vocabulary prefix ("done: ", "blocked: ", ...)
// is never part of what gets cut - it is the highest-value part of the line -
// and a cut line always carries a visible marker naming how much was dropped,
// so a short report can never be mistaken for a truncated one.
func truncateReportLine(line state.ReportLine, budget int) string {
	full := reportLineText(line)
	runes := []rune(full)
	if len(runes) <= budget {
		return full
	}
	prefixLen := 0
	if !line.Malformed {
		prefixLen = len(line.State) + len(": ")
	}
	if budget < prefixLen {
		budget = prefixLen
	}
	return fmt.Sprintf("%s... [+%d chars]", string(runes[:budget]), len(runes)-budget)
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
// output; Malformed lines carry their raw text in Note with State left empty,
// and an unreadable report file carries the read error in Note under the
// reportUnreadable state.
type reportedJSON struct {
	State string `json:"state"`
	Note  string `json:"note"`
}

type statusJSON struct {
	ID             string        `json:"id"`
	Project        string        `json:"project"`
	Kind           string        `json:"kind"`
	Harness        string        `json:"harness,omitempty"`
	AgentState     string        `json:"agent_state"`
	Worktree       string        `json:"worktree"`
	Herdr          state.Herdr   `json:"herdr"`
	PR             string        `json:"pr"`
	MergeExecuted  bool          `json:"merged"`
	MergeAnnounced bool          `json:"pr_merged_observed"`
	CreatedAt      string        `json:"created_at"`
	LastReportAt   string        `json:"last_report_at,omitempty"`
	Reported       *reportedJSON `json:"reported,omitempty"`
	ReportHistory  []string      `json:"report_history,omitempty"`
	Held           *holdJSON     `json:"held,omitempty"`
}

// fleetJSON wraps the task rows with the fleet's holds, which name any id -
// not only a live task - so a torn-down task's still-open hold keeps
// surfacing here after its task row is gone. TaskCount is always present,
// zero included, so an empty fleet is a positive statement ("no tasks") and
// not the same absence of output a broken command would also produce.
type fleetJSON struct {
	TaskCount int          `json:"task_count"`
	Tasks     []statusJSON `json:"tasks"`
	Holds     []holdJSON   `json:"holds"`
}

// holdJSON mirrors state.Hold, plus Inconsistent, which is set instead of the
// row being dropped when a value can't be trusted at face value - see
// holdInconsistency.
type holdJSON struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Reason       string `json:"reason"`
	BlockedOn    string `json:"blocked_on,omitempty"`
	SetAt        string `json:"set_at"`
	Inconsistent string `json:"inconsistent,omitempty"`
}

// holdInconsistency names why a hold row can't be trusted at face value, so
// that ListHolds surfacing every row (rather than filtering) turns into a
// visible flag instead of a silently wrong render: an unrecognized kind, a
// blocked hold with nothing to point at, or an operator hold carrying a
// blocked_on nothing set. Nothing in this codebase writes such a row today -
// only hand hold set, which validates first - so seeing one here means
// something outside hand touched state/hand.db directly.
func holdInconsistency(h state.Hold) string {
	switch h.Kind {
	case state.HoldKindOperator:
		if h.BlockedOn != "" {
			return fmt.Sprintf("operator hold carries a blocked_on %q", h.BlockedOn)
		}
		return ""
	case state.HoldKindBlocked:
		if h.BlockedOn == "" {
			return "blocked hold has no blocked_on"
		}
		return ""
	default:
		return fmt.Sprintf("unrecognized kind %q", h.Kind)
	}
}

func holdToJSON(h state.Hold) holdJSON {
	return holdJSON{
		ID: h.ID, Kind: h.Kind, Reason: h.Reason, BlockedOn: h.BlockedOn, SetAt: h.SetAt,
		Inconsistent: holdInconsistency(h),
	}
}

// holdDetail renders a hold's non-identifying fields for the plain-text held
// block. An inconsistency takes over the whole line: a garbled blocked-on or
// reason next to it would read as a valid detail rather than as the flag it is.
func holdDetail(h state.Hold) string {
	if inc := holdInconsistency(h); inc != "" {
		return "inconsistent: " + inc
	}
	if h.Kind == state.HoldKindBlocked {
		return fmt.Sprintf("waiting on %s: %s", h.BlockedOn, h.Reason)
	}
	return h.Reason
}

func mergeSuffix(t state.Task) string {
	switch {
	case t.PR == "":
		return ""
	case t.MergeExecuted:
		return " (merged)"
	case t.MergeAnnounced:
		return " (merged, external)"
	default:
		return ""
	}
}

func runStatusFleet(cmd *cobra.Command, home string, client *herdr.Client, asJSON bool) error {
	tasks, err := state.List(home)
	if err != nil {
		return err
	}
	// Propagated rather than degraded to an empty list: a store fault reading
	// as no holds is exactly the false all-clear this feature exists to avoid.
	holds, err := state.ListHolds(home)
	if err != nil {
		return err
	}

	rows := make([]statusJSON, 0, len(tasks))
	suffixes := make([]string, 0, len(tasks))
	for _, t := range tasks {
		agentState := paneAgentStatus(client, t.Herdr.PaneID)
		lines, readErr := state.ReadReportLines(home, t.ID)
		var last state.ReportLine
		if len(lines) > 0 {
			last = lines[len(lines)-1]
		}
		reported, reportedOK := state.LastReportedState(lines)
		rows = append(rows, statusJSON{
			ID: t.ID, Project: t.Project, Kind: t.Kind, Harness: t.Harness,
			AgentState: agentState,
			Worktree:   t.Worktree, Herdr: t.Herdr, PR: t.PR,
			MergeExecuted: t.MergeExecuted, MergeAnnounced: t.MergeAnnounced, CreatedAt: t.CreatedAt,
			LastReportAt: lastReportAt(home, t.ID),
			Reported:     reportedFrom(last, len(lines) > 0, readErr),
		})
		suffixes = append(suffixes, reportSuffix(agentState, reported, reportedOK, readErr)+mergeSuffix(t))
	}

	if asJSON {
		holdRows := make([]holdJSON, 0, len(holds))
		for _, h := range holds {
			holdRows = append(holdRows, holdToJSON(h))
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(fleetJSON{TaskCount: len(rows), Tasks: rows, Holds: holdRows})
	}

	if len(rows) == 0 {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), "no tasks (0)"); err != nil {
			return err
		}
		return printHeldBlock(cmd, holds)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "id\tproject\tkind\tstate\tage\tlast report"); err != nil {
		return err
	}
	for i, r := range rows {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Project, r.Kind, r.AgentState+suffixes[i], formatAge(r.CreatedAt), formatReportAge(r.LastReportAt)); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return printHeldBlock(cmd, holds)
}

// printHeldBlock is skipped entirely when holds is empty, so a fleet with
// nothing waiting prints exactly what it did before this feature existed.
func printHeldBlock(cmd *cobra.Command, holds []state.Hold) error {
	if len(holds) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), "\nheld:"); err != nil {
		return err
	}
	hw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	for _, h := range holds {
		if _, err := fmt.Fprintf(hw, "  %s\t%s\t%s\t%s\n", h.ID, h.Kind, holdDetail(h), formatAge(h.SetAt)); err != nil {
			return err
		}
	}
	return hw.Flush()
}

// A stat fault reads the same as no report at all: the column is a staleness
// hint, and failing hand status over it would trade a whole fleet view for one
// unreadable timestamp.
func lastReportAt(home, id string) string {
	mtime, ok, err := state.ReportModTime(home, id)
	if err != nil || !ok {
		return ""
	}
	return mtime.UTC().Format(time.RFC3339)
}

// Distinct from "unreported": an I/O fault is not evidence that the worker
// never reported.
const reportUnreadable = "unreadable"

// A pane state and a report answer different questions, so both print. This
// used to speak only for a not-busy pane, which rendered a worker that appended
// `paused:` with its harness still running as a bare `working`.
func reportSuffix(agentState string, reported state.ReportLine, ok bool, readErr error) string {
	if readErr != nil {
		return fmt.Sprintf(" (report %s)", reportUnreadable)
	}
	// `working` is what the column already says, and a busy pane that has not
	// reported yet is not a stop anyone has to explain.
	if !ok || reported.State == "" || reported.State == state.ReportWorking {
		if herdr.Status(agentState).NotBusy() {
			return " (unreported)"
		}
		return ""
	}
	return fmt.Sprintf(" (reported: %s)", reported.State)
}

func reportedFrom(last state.ReportLine, ok bool, readErr error) *reportedJSON {
	if readErr != nil {
		return &reportedJSON{State: reportUnreadable, Note: readErr.Error()}
	}
	if !ok {
		return nil
	}
	if last.Malformed {
		return &reportedJSON{Note: last.Raw}
	}
	return &reportedJSON{State: last.State, Note: last.Note}
}

func runStatusSingle(cmd *cobra.Command, home string, client *herdr.Client, id string, asJSON, full bool) error {
	t, err := state.Read(home, id)
	if err != nil {
		return asPrecondition(err)
	}
	t = detectPRForStatus(cmd.Context(), home, t)
	agentState := paneAgentStatus(client, t.Herdr.PaneID)

	// Propagated, not degraded: see the same comment in runStatusFleet.
	hold, held, err := state.ReadHold(home, id)
	if err != nil {
		return err
	}

	// An unreadable report degrades exactly as it does in the fleet view: the
	// fault is named on the Reported line and the rest of the detail view still
	// prints, rather than the whole command failing over one bad read.
	const historyLen = 5
	tail, readErr := state.ReportTail(home, id, historyLen)
	history := make([]string, len(tail))
	for i, line := range tail {
		history[i] = reportLineText(line)
	}

	var last state.ReportLine
	if len(tail) > 0 {
		last = tail[len(tail)-1]
	}

	var heldJSON *holdJSON
	if held {
		j := holdToJSON(hold)
		heldJSON = &j
	}

	if asJSON {
		out := statusJSON{
			ID: t.ID, Project: t.Project, Kind: t.Kind, Harness: t.Harness,
			AgentState: agentState, Worktree: t.Worktree, Herdr: t.Herdr, PR: t.PR,
			MergeExecuted: t.MergeExecuted, MergeAnnounced: t.MergeAnnounced, CreatedAt: t.CreatedAt,
			LastReportAt: lastReportAt(home, id),
			Reported:     reportedFrom(last, len(tail) > 0, readErr), ReportHistory: history,
			Held: heldJSON,
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	pr := t.PR
	if pr == "" {
		pr = "(none)"
	} else {
		pr += mergeSuffix(t)
	}
	// One render choice drives both the Reported line and every history entry,
	// so a change to the budget can never reach one of them and miss the other.
	render := reportLineText
	if !full {
		render = func(line state.ReportLine) string { return truncateReportLine(line, reportSummaryBudget) }
	}

	reported := "(none)"
	switch {
	case readErr != nil:
		reported = fmt.Sprintf("report %s: %v", reportUnreadable, readErr)
	case len(tail) > 0:
		reported = render(last)
	}

	w := cmd.OutOrStdout()
	lines := []string{
		fmt.Sprintf("Task:        %s", t.ID),
		fmt.Sprintf("Project:     %s", t.Project),
		fmt.Sprintf("Kind:        %s", t.Kind),
		fmt.Sprintf("Harness:     %s", t.Harness),
		fmt.Sprintf("Model:       %s", t.Model),
		fmt.Sprintf("State:       %s", agentState),
		fmt.Sprintf("Worktree:    %s", t.Worktree),
		fmt.Sprintf("Herdr:       %s / %s", t.Herdr.Session, t.Herdr.TabID),
		fmt.Sprintf("Created:     %s", formatAge(t.CreatedAt)),
		fmt.Sprintf("Last report: %s", formatReportAge(lastReportAt(home, id))),
		fmt.Sprintf("PR:          %s", pr),
		fmt.Sprintf("Reported:    %s", reported),
	}
	if held {
		lines = append(lines, fmt.Sprintf("Held:        %s", holdDetail(hold)))
	}
	if !full && (len(tail) > 0 || readErr != nil) {
		lines = append(lines, fmt.Sprintf("Report file: %s", state.ReportPath(home, id)))
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	// In the default view, the entry already shown on the Reported line above
	// is dropped from the history block below it - repeating it there was the
	// core of atqamz/secondhand#65, doubling the cost of every terminal report.
	// --full restores the exact previous shape: the full tail, untruncated,
	// duplicate entry included.
	historyTail := tail
	if !full && len(tail) > 0 {
		historyTail = tail[:len(tail)-1]
	}
	if len(historyTail) > 0 {
		if _, err := fmt.Fprintln(w, "\nReport history (reported by worker, not verified current truth):"); err != nil {
			return err
		}
		for _, line := range historyTail {
			if _, err := fmt.Fprintf(w, "  %s\n", render(line)); err != nil {
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

func formatReportAge(at string) string {
	if at == "" {
		return "(none)"
	}
	return formatAge(at)
}
