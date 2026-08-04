package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/atqamz/secondhand/internal/age"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/home"
	"github.com/atqamz/secondhand/internal/project"
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
	ID              string        `json:"id"`
	Project         string        `json:"project"`
	Kind            string        `json:"kind"`
	Harness         string        `json:"harness,omitempty"`
	AgentState      string        `json:"agent_state"`
	Worktree        string        `json:"worktree"`
	Herdr           state.Herdr   `json:"herdr"`
	PR              string        `json:"pr"`
	MergeExecuted   bool          `json:"merged"`
	MergeAnnounced  bool          `json:"pr_merged_observed"`
	DeliveredAt     string        `json:"delivered_at,omitempty"`
	DeliveredReason string        `json:"delivered_reason,omitempty"`
	CreatedAt       string        `json:"created_at"`
	LastReportAt    string        `json:"last_report_at,omitempty"`
	Reported        *reportedJSON `json:"reported,omitempty"`
	ReportHistory   []string      `json:"report_history,omitempty"`
	Held            *holdJSON     `json:"held,omitempty"`
	GateRunIssue    string        `json:"gate_run_issue,omitempty"`
	// Omitted when false so a consumer written before this field sees no change
	// on the fleet it already understands.
	Unacknowledged bool `json:"unacknowledged,omitempty"`
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

// deliveredSuffix marks work that is handed off with its landing left to
// someone outside the fleet. Unlike mergeSuffix it does not hang off a recorded
// PR: a delivered task's deliverable can be a report rather than a PR at all.
func deliveredSuffix(t state.Task) string {
	if t.DeliveredAt == "" {
		return ""
	}
	return " (delivered)"
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

// gateRunApplies is the single predicate for whether the gate-run check has anything to say about a
// task: only a done ship task with a recorded PR does. Everything the check needs - the project
// lookup above all, whose failure the single-task view propagates - hangs off this, so a task the
// check would stay silent on never pays that cost nor fails over it.
func gateRunApplies(t state.Task, reportedDone bool) bool {
	return t.Kind == state.KindShip && t.PR != "" && reportedDone
}

// gateRunReader answers "which PRs did completed no-mistakes runs record" for one clone path.
type gateRunReader func(clonePath string) (map[string]bool, error)

// newGateRunReader caches each clone path's answer for the life of one render, so a fleet with
// several done ship tasks on the same project spawns one no-mistakes process for it, not one per
// task. Failures are cached too: a clone that could not be asked once is not worth re-asking within
// the same render.
func newGateRunReader() gateRunReader {
	type answer struct {
		prs map[string]bool
		err error
	}
	cache := map[string]answer{}
	return func(clonePath string) (map[string]bool, error) {
		a, ok := cache[clonePath]
		if !ok {
			a.prs, a.err = project.GateRunPRs(clonePath)
			cache[clonePath] = a
		}
		return a.prs, a.err
	}
}

// gateRunIssue reports why a done ship task's recorded PR cannot be confirmed to have gone through a
// no-mistakes gate run, using the same "unreachable" bucket gateIssue (cmd/project.go) uses for any
// failure to ask no-mistakes at all - a missing clone, an unrunnable binary, a gate never
// initialized - so a question this check cannot answer never renders as the stronger claim "no run
// found".
//
// A project not registered or not run through no-mistakes stays silent alongside every task
// gateRunApplies rejects, since the check does not apply to it either.
func gateRunIssue(home string, t state.Task, reportedDone bool, p project.Project, registered bool, runPRs gateRunReader) string {
	if !gateRunApplies(t, reportedDone) {
		return ""
	}
	if !registered || p.Mode != project.ModeNoMistakes {
		return ""
	}
	prs, err := runPRs(filepath.Join(home, "projects", p.Name))
	if err != nil {
		return "unreachable"
	}
	if !prs[t.PR] {
		return "no run found"
	}
	return ""
}

func gateRunSuffix(issue string) string {
	if issue == "" {
		return ""
	}
	return " (gate: " + issue + ")"
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

	// Best-effort, like project.List elsewhere in this fleet view: a registry
	// read fault degrades every task's gate-run check to silent rather than
	// failing the whole fleet overview over it. Named on stderr all the same -
	// silently dropping every (gate: ...) marker fleet-wide would render an
	// ungated PR as clean, the false all-clear this feature exists to avoid.
	projects, projectsErr := project.List(home)
	if projectsErr != nil {
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: project registry unreadable, gate-run checks skipped: %v\n", projectsErr); err != nil {
			return err
		}
	}
	projectByName := make(map[string]project.Project, len(projects))
	for _, p := range projects {
		projectByName[p.Name] = p
	}
	runPRs := newGateRunReader()

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
		unacked, readErr := unacknowledged(home, t, reported, reportedOK, readErr)
		p, registered := projectByName[t.Project]
		runIssue := gateRunIssue(home, t, reportedOK && reported.State == state.ReportDone, p, registered, runPRs)
		rows = append(rows, statusJSON{
			ID: t.ID, Project: t.Project, Kind: t.Kind, Harness: t.Harness,
			AgentState: agentState,
			Worktree:   t.Worktree, Herdr: t.Herdr, PR: t.PR,
			MergeExecuted: t.MergeExecuted, MergeAnnounced: t.MergeAnnounced,
			DeliveredAt: t.DeliveredAt, DeliveredReason: t.DeliveredReason, CreatedAt: t.CreatedAt,
			LastReportAt: lastReportAt(home, t.ID),
			Reported:     reportedFrom(last, len(lines) > 0, readErr),
			GateRunIssue: runIssue, Unacknowledged: unacked,
		})
		suffixes = append(suffixes, reportSuffix(agentState, reported, reportedOK, readErr, unacked)+deliveredSuffix(t)+mergeSuffix(t)+gateRunSuffix(runIssue))
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

// unacknowledged asks state whether this task's terminal report reached a
// watcher, folding a read that fails into the caller's own report-read error: the
// file was readable a moment ago and is not now, which is what that error already
// says, and swallowing it would render an unread completion as an acknowledged
// one.
//
// It takes the state the caller already derived and answers false for anything
// but a terminal one, so the flag can only ever qualify the state this row
// prints. Reading the file a second time is a second snapshot, and a worker
// appending between the two would otherwise put "unacknowledged" next to a
// "working" this command reported in the same breath.
func unacknowledged(home string, t state.Task, reported state.ReportLine, reportedOK bool, readErr error) (bool, error) {
	if readErr != nil {
		return false, readErr
	}
	if !reportedOK || !state.TerminalReport(reported.State) {
		return false, nil
	}
	return state.UnacknowledgedTerminalReport(home, t.ID, state.ReportCursor{Offset: t.ReportOffset, Digest: t.ReportDigest})
}

// A pane state and a report answer different questions, so both print. This
// used to speak only for a not-busy pane, which rendered a worker that appended
// `paused:` with its harness still running as a bare `working`.
//
// unacked rides the reported clause rather than a clause of its own because it
// qualifies exactly that state: it is only ever set for a terminal report, which
// is the one branch below that names the reported state at all.
func reportSuffix(agentState string, reported state.ReportLine, ok bool, readErr error, unacked bool) string {
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
	if unacked {
		return fmt.Sprintf(" (reported: %s, unacknowledged)", reported.State)
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
	// The whole file, sliced afterwards: deriving the flag from the 5-line
	// history window instead would let five trailing free-text lines hide a
	// completion the fleet view flags, and the two views must never disagree.
	reportLines, readErr := state.ReadReportLines(home, id)
	lastReported, lastReportedOK := state.LastReportedState(reportLines)
	unacked, readErr := unacknowledged(home, t, lastReported, lastReportedOK, readErr)

	tail := reportLines
	if len(tail) > historyLen {
		tail = tail[len(tail)-historyLen:]
	}
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

	// Looked up only when the check applies, so a registry this id's detail view
	// does not need can never fail the command. When it does apply the failure is
	// propagated, not degraded: a single task's own project is the one fact this
	// check is about, unlike the fleet view's best-effort lookup across every
	// task's project at once.
	reportedDone := lastReportedOK && lastReported.State == state.ReportDone
	var runIssue string
	if gateRunApplies(t, reportedDone) {
		p, registered, err := project.Find(home, t.Project)
		if err != nil {
			return err
		}
		runIssue = gateRunIssue(home, t, reportedDone, p, registered, newGateRunReader())
	}

	if asJSON {
		out := statusJSON{
			ID: t.ID, Project: t.Project, Kind: t.Kind, Harness: t.Harness,
			AgentState: agentState, Worktree: t.Worktree, Herdr: t.Herdr, PR: t.PR,
			MergeExecuted: t.MergeExecuted, MergeAnnounced: t.MergeAnnounced,
			DeliveredAt: t.DeliveredAt, DeliveredReason: t.DeliveredReason, CreatedAt: t.CreatedAt,
			LastReportAt: lastReportAt(home, id),
			Reported:     reportedFrom(last, len(reportLines) > 0, readErr), ReportHistory: history,
			Held: heldJSON, GateRunIssue: runIssue, Unacknowledged: unacked,
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
	// Same clause the fleet view appends, so neither view can call a completion
	// acknowledged that the other flags - and on the same classified line the
	// fleet view names, not whatever free text the worker appended after it.
	case unacked:
		reported = render(lastReported) + " (unacknowledged)"
	case len(tail) > 0:
		reported = render(last)
	}
	// Which history entry the Reported line above already shows. Found rather
	// than assumed last: with the flag applied that line is the classified
	// terminal report, which the worker may have followed with free text, or
	// pushed out of the history window entirely.
	reportedIdx := len(tail) - 1
	if unacked {
		reportedIdx = -1
		for i := len(tail) - 1; i >= 0; i-- {
			if !tail[i].Malformed {
				reportedIdx = i
				break
			}
		}
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
	if t.DeliveredAt != "" {
		lines = append(lines, fmt.Sprintf("Delivered:   %s (%s)", t.DeliveredReason, t.DeliveredAt))
	}
	if held {
		lines = append(lines, fmt.Sprintf("Held:        %s", holdDetail(hold)))
	}
	if runIssue != "" {
		lines = append(lines, fmt.Sprintf("Gate run:    %s", runIssue))
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
	if !full && reportedIdx >= 0 {
		historyTail = slices.Concat(tail[:reportedIdx], tail[reportedIdx+1:])
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
	a := age.FormatAge(createdAt)
	if a == "just now" || a == "unknown" {
		return a
	}
	return a + " ago"
}

func formatReportAge(at string) string {
	if at == "" {
		return "(none)"
	}
	return formatAge(at)
}
