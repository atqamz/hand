package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/age"
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var asJSON, full bool
	var fields []string

	cmd := &cobra.Command{
		Use:   "status [id]",
		Short: "Show fleet overview or single-task detail",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rejectFieldsWithJSON(fields, asJSON); err != nil {
				return err
			}
			// Resolved before the home is: a name no column carries costs the
			// caller nothing to be told about, and a fleet scan to find out.
			def := fleetDefaultFields
			if len(args) == 1 {
				def = detailDefaultFields
			}
			cols, err := pickFields(taskFields, fields, def)
			if err != nil {
				return err
			}
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			client := herdr.NewClient()

			if len(args) == 1 {
				return runStatusSingle(cmd, home, client, args[0], asJSON, full, cols)
			}
			return runStatusFleet(cmd, home, client, asJSON, cols)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON instead of TOON")
	cmd.Flags().BoolVar(&full, "full", false, "show the reported line and history untruncated, with no history dedup (single task only)")
	cmd.Flags().StringSliceVar(&fields, "fields", nil, fieldsFlagUsage(taskFields, fleetDefaultFields))
	return cmd
}

// Bounds the rendered length of one report line (state prefix plus note) in the human-readable
// single-task view, in runes so a multi-byte character never lands half-cut. A worker's status prose has
// run 2.7-4.3 KB for a single task, and the terse report AGENTS.md asks for fits well inside this.
const reportSummaryBudget = 200

// The state-vocabulary prefix ("done: ", "blocked: ", ...) is never part of
// what gets cut - it is the highest-value part of the line - and a cut line
// always names its full size and the command that recovers it.
func truncateReportLine(line state.ReportLine, budget int, id string) string {
	prefixLen := 0
	if !line.Malformed {
		prefixLen = len(line.State) + len(": ")
	}
	return axi.Truncate(reportLineText(line), max(budget, prefixLen), "hand status "+id+" --full")
}

// Renders the last report line the way both status views show it: an unreadable channel named as such,
// and the unacknowledged clause on the classified line the fleet view flags rather than on trailing free
// text.
func reportSummary(id string, lines []state.ReportLine, readErr error, unacked, full bool) string {
	if readErr != nil {
		return fmt.Sprintf("report %s: %v", reportUnreadable, readErr)
	}
	if len(lines) == 0 {
		return ""
	}
	line := lines[len(lines)-1]
	suffix := ""
	if unacked {
		if classified, ok := state.LastReportedState(lines); ok {
			line, suffix = classified, " (unacknowledged)"
		}
	}
	// --full is the explicit opt-out from reportSummaryBudget, and --json bypasses it as well: a machine
	// consumer needs the whole field.
	if full {
		return reportLineText(line) + suffix
	}
	return truncateReportLine(line, reportSummaryBudget, id) + suffix
}

// Read-only status degrades to "unknown" when herdr or the pane cannot be queried.
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

// Mirrors one classified line from state.ReportLine for JSON output: malformed lines carry their raw text
// in Note with State left empty, and an unreadable report file carries the read error in Note under the
// reportUnreadable state.
type reportedJSON struct {
	State string `json:"state"`
	Note  string `json:"note"`
}

type statusJSON struct {
	ID               string        `json:"id"`
	Project          string        `json:"project"`
	Kind             string        `json:"kind"`
	ExecutionClass   string        `json:"execution_class,omitempty"`
	Profile          string        `json:"profile,omitempty"`
	PlannedAgainst   string        `json:"planned_against,omitempty"`
	RoutingSource    string        `json:"routing_source,omitempty"`
	TaskLifecycle    string        `json:"task_lifecycle"`
	AttemptOrdinal   int           `json:"attempt_ordinal,omitempty"`
	AttemptLifecycle string        `json:"attempt_lifecycle,omitempty"`
	Harness          string        `json:"harness,omitempty"`
	Model            string        `json:"model,omitempty"`
	Effort           string        `json:"effort,omitempty"`
	AgentState       string        `json:"agent_state"`
	Worktree         string        `json:"worktree"`
	Herdr            state.Herdr   `json:"herdr"`
	PR               string        `json:"pr"`
	MergeExecuted    bool          `json:"merged"`
	MergeAnnounced   bool          `json:"pr_merged_observed"`
	DeliveredAt      string        `json:"delivered_at,omitempty"`
	DeliveredReason  string        `json:"delivered_reason,omitempty"`
	CreatedAt        string        `json:"created_at"`
	LastReportAt     string        `json:"last_report_at,omitempty"`
	Reported         *reportedJSON `json:"reported,omitempty"`
	ReportHistory    []string      `json:"report_history,omitempty"`
	Held             *holdJSON     `json:"held,omitempty"`
	GateRunIssue     string        `json:"gate_run_issue,omitempty"`
	RepairCode       string        `json:"repair_code,omitempty"`
	RepairReason     string        `json:"repair_reason,omitempty"`
	RepairAttemptID  int64         `json:"repair_attempt,omitempty"`
	RepairObservedAt string        `json:"repair_observed_at,omitempty"`
	// Omitted when false so a consumer written before this field sees no change
	// on the fleet it already understands.
	Unacknowledged bool          `json:"unacknowledged,omitempty"`
	Attempts       []attemptJSON `json:"attempts,omitempty"`
	LatestSend     *sendJSON     `json:"latest_send,omitempty"`
}

type sendJSON struct {
	ID          int64  `json:"id"`
	TaskID      string `json:"task_id"`
	AttemptID   int64  `json:"attempt_id"`
	Origin      string `json:"origin"`
	State       string `json:"state"`
	ReasonCode  string `json:"reason_code,omitempty"`
	CreatedAt   string `json:"created_at"`
	FinalizedAt string `json:"finalized_at,omitempty"`
}

type attemptJSON struct {
	Ordinal        int    `json:"ordinal"`
	Lifecycle      string `json:"lifecycle"`
	Harness        string `json:"harness,omitempty"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	ExecutionClass string `json:"execution_class,omitempty"`
	Profile        string `json:"profile,omitempty"`
	PlannedAgainst string `json:"planned_against,omitempty"`
	RoutingSource  string `json:"routing_source,omitempty"`
	Worktree       string `json:"worktree,omitempty"`
}

// Wraps the task rows with the fleet's holds, which name any id - not only an open task - so a
// torn-down task's still-open hold keeps surfacing here after the task leaves the open fleet.
type fleetJSON struct {
	// Always present, zero included, so an empty fleet is a positive statement ("no tasks") and not the
	// same absence of output a broken command would also produce.
	TaskCount int          `json:"task_count"`
	Tasks     []statusJSON `json:"tasks"`
	Holds     []holdJSON   `json:"holds"`
}

// Mirrors state.Hold, plus Inconsistent, which is set instead of the row being dropped when a value
// cannot be trusted at face value - see holdInconsistency.
type holdJSON struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Reason       string `json:"reason"`
	BlockedOn    string `json:"blocked_on,omitempty"`
	SetAt        string `json:"set_at"`
	Inconsistent string `json:"inconsistent,omitempty"`
}

// Names why a hold row cannot be trusted at face value, so that ListHolds surfacing every row (rather
// than filtering) turns into a visible flag instead of a silently wrong render.
func holdInconsistency(h state.Hold) string {
	// An unrecognized kind, a blocked hold with nothing to point at, or an operator or limit hold carrying
	// a blocked_on nothing set. Nothing here writes such a row today - hand hold set validates first, and
	// limit holds set no blocked_on - so one means something outside hand touched state/hand.db directly.
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
	case state.HoldKindLimit:
		if h.BlockedOn != "" {
			return fmt.Sprintf("limit hold carries a blocked_on %q", h.BlockedOn)
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

// Renders a hold's non-identifying fields for the plain-text held block. An inconsistency takes over the
// whole line: a garbled blocked-on or reason next to it would read as a valid detail rather than a flag.
func holdDetail(h state.Hold) string {
	if inc := holdInconsistency(h); inc != "" {
		return "inconsistent: " + inc
	}
	if h.Kind == state.HoldKindBlocked {
		return fmt.Sprintf("waiting on %s: %s", h.BlockedOn, h.Reason)
	}
	return h.Reason
}

// The single predicate for whether the gate-run check has anything to say about a task: only a done ship
// task with a recorded PR does. Everything the check needs - the project lookup above all, whose failure
// the single-task view propagates - hangs off this, so a silent task never pays that cost nor fails over it.
func gateRunApplies(t state.Task, reportedDone bool) bool {
	return t.Kind == state.KindShip && t.PR != "" && reportedDone
}

// Answers "which PRs did completed no-mistakes runs record" for one clone path.
type gateRunReader func(clonePath string) (map[string]bool, error)

// Caches each clone path's answer for the life of one render, so a fleet with several done ship tasks on
// the same project spawns one no-mistakes process for it, not one per task. Failures are cached too: a
// clone that could not be asked once is not worth re-asking within the same render.
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

// Reports why a done ship task's recorded PR cannot be confirmed to have gone through a no-mistakes gate
// run, using the same "unreachable" bucket gateIssue (cmd/project.go) uses for any failure to ask
// no-mistakes at all, so a question this check cannot answer never renders as "no run found".
func gateRunIssue(home string, t state.Task, reportedDone bool, p project.Project, registered bool, runPRs gateRunReader) string {
	if !gateRunApplies(t, reportedDone) {
		return ""
	}
	// A project not registered or not run through no-mistakes stays silent alongside every task
	// gateRunApplies rejects, since the check does not apply to it either.
	if !registered || p.Mode != project.ModeNoMistakes {
		return ""
	}
	prs, err := runPRs(filepath.Join(home, "projects", p.Name))
	if err != nil {
		// A missing clone, an unrunnable binary, or a gate never initialized all land in this bucket.
		return "unreachable"
	}
	if !prs[t.PR] {
		return "no run found"
	}
	return ""
}

func runStatusFleet(cmd *cobra.Command, home string, client *herdr.Client, asJSON bool, cols []axi.Column[taskView]) error {
	views, holds, err := fleetViews(cmd, home, client, true)
	if err != nil {
		return err
	}

	if asJSON {
		rows := make([]statusJSON, 0, len(views))
		for _, v := range views {
			rows = append(rows, v.json())
		}
		holdRows := make([]holdJSON, 0, len(holds))
		for _, h := range holds {
			holdRows = append(holdRows, holdToJSON(h))
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(fleetJSON{TaskCount: len(rows), Tasks: rows, Holds: holdRows})
	}

	var doc axi.Doc
	appendFleet(&doc, views, holds, cols)
	return doc.Render(cmd.OutOrStdout())
}

// Writes the fleet blocks onto doc rather than a writer, so the bare command can put its identity fields
// above the same overview.
func appendFleet(doc *axi.Doc, views []taskView, holds []state.Hold, cols []axi.Column[taskView], leadHelp ...string) {
	attention := appendFleetState(doc, views, holds, cols)
	// An unanswered configuration question leads: it is the one thing here the fleet cannot proceed
	// without, and doc.Help renders a single list, so it cannot be a second block.
	doc.Help(append(slices.Clone(leadHelp), fleetHelp(views, attention)...)...)
}

func appendFleetState(doc *axi.Doc, views []taskView, holds []state.Hold, cols []axi.Column[taskView]) int {
	attention := 0
	for _, v := range views {
		if needsAttention(v) {
			attention++
		}
	}

	doc.Int("count", len(views))
	doc.Int("attention", attention)
	doc.Int("held", len(holds))
	axi.Table(doc, "tasks", views, cols)
	axi.Table(doc, "holds", holds, holdFields)
	return attention
}

func fleetViews(cmd *cobra.Command, home string, client *herdr.Client, readOnly bool) ([]taskView, []state.Hold, error) {
	listHistories := state.ListReconciliationHistories
	listHolds := state.ListHolds
	listProjects := project.List
	if readOnly {
		listHistories = state.ListReconciliationHistoriesReadOnly
		listHolds = state.ListHoldsReadOnly
		listProjects = project.ListReadOnly
	}
	// The one attempt-history read the fleet view makes: rendering re-reads nothing, so a fleet costs
	// one store handle rather than one per task.
	histories, err := listHistories(home)
	if err != nil {
		return nil, nil, err
	}
	// Propagated rather than degraded to an empty list: a store fault reading
	// as no holds is exactly the false all-clear this feature exists to avoid.
	holds, err := listHolds(home)
	if err != nil {
		return nil, nil, err
	}

	// Best-effort, like the project registry read elsewhere in this fleet view: a fault degrades every
	// task's gate-run check to silent rather than failing the whole fleet overview over it.
	projects, projectsErr := listProjects(home)
	if projectsErr != nil {
		// Named on stderr all the same - silently dropping every (gate: ...) marker fleet-wide would render
		// an ungated PR as clean, the false all-clear this feature exists to avoid.
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: project registry unreadable, gate-run checks skipped: %v\n", projectsErr); err != nil {
			return nil, nil, err
		}
	}
	projectByName := make(map[string]project.Project, len(projects))
	for _, p := range projects {
		projectByName[p.Name] = p
	}
	runPRs := newGateRunReader()

	views := make([]taskView, 0, len(histories))
	for _, history := range histories {
		t := history.Task
		v, _ := buildTaskView(home, client, history, false)
		p, registered := projectByName[t.Project]
		v.gateIssue = gateRunIssue(home, t, v.reportedState == state.ReportDone, p, registered, runPRs)
		views = append(views, v)
	}
	return views, holds, nil
}

func fleetHelp(views []taskView, attention int) []string {
	if len(views) == 0 {
		return []string{
			"Run `hand project list` to see which projects are registered",
			"Run `hand spawn <id> <project>` to start a worker",
		}
	}
	help := []string{"Run `hand status <id>` for one task's detail and report history"}
	if attention > 0 {
		help = append(help, "A flagged row is waiting on you: `hand send <id> <message>` to steer it, `hand hold set <id> --kind operator --reason <text>` to park it")
	}
	return append(help, "Run `hand status --fields <a,b>` to pick columns, `hand status --help` for every field name")
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

// Asks state whether this task's terminal report reached a watcher. It takes the state the caller already
// derived rather than reading the file a second time: a second snapshot with a worker appending between
// the two would put "unacknowledged" next to a "working" this command reported in the same breath.
func unacknowledged(home string, t state.Task, reported state.ReportLine, reportedOK bool, readErr error) (bool, error) {
	// A read that fails folds into the caller's own report-read error: the file was readable a moment ago
	// and is not now, which is what that error already says, and swallowing it would render an unread
	// completion as an acknowledged one.
	if readErr != nil {
		return false, readErr
	}
	// False for anything but a terminal state, so the flag can only ever qualify the state this row prints.
	if !reportedOK || !state.TerminalReport(reported.State) {
		return false, nil
	}
	return state.UnacknowledgedTerminalReport(home, t.ID, state.ReportCursor{Offset: t.ReportOffset, Digest: t.ReportDigest})
}

// Derives everything both status views show from one already-read history, and returns the report lines
// alongside so the detail view's history block and the summary line above it can never come from two
// reads of the file.
func buildTaskView(home string, client *herdr.Client, history state.TaskHistory, full bool) (taskView, []state.ReportLine) {
	t := history.Task
	attempts := history.Attempts
	attempt := history.ActiveAttempt
	if attempt == nil && len(attempts) != 0 {
		attempt = &attempts[len(attempts)-1]
	}
	e := state.Attempt{}
	if attempt != nil {
		e = *attempt
	}
	agentState := paneAgentStatus(client, e.Herdr.PaneID)
	lines, readErr := state.ReadReportLines(home, t.ID)
	reported, reportedOK := state.LastReportedState(lines)
	unacked, readErr := unacknowledged(home, t, reported, reportedOK, readErr)

	var last state.ReportLine
	if len(lines) > 0 {
		last = lines[len(lines)-1]
	}
	v := taskView{
		task:         t,
		attempt:      attempt,
		attempts:     attempts,
		agentState:   agentState,
		reportedLine: reportSummary(t.ID, lines, readErr, unacked, full),
		lastReportAt: lastReportAt(home, t.ID),
		reportFile:   state.ReportPath(home, t.ID),
		unreadable:   readErr != nil,
		unacked:      unacked,
		reported:     reportedFrom(last, len(lines) > 0, readErr),
	}
	if len(history.Sends) != 0 {
		latest := history.Sends[len(history.Sends)-1]
		v.latestSend = &latest
	}
	if reportedOK {
		v.reportedState = reported.State
	}
	return v, lines
}

// Bounds the attempt window both status renderers show, so the JSON and plain-text views can never
// disagree about how much of a task's execution history is on screen.
const attemptHistoryLen = 5

func recentAttempts(attempts []state.Attempt) []state.Attempt {
	if len(attempts) > attemptHistoryLen {
		return attempts[len(attempts)-attemptHistoryLen:]
	}
	return attempts
}

func (v taskView) json() statusJSON {
	e := v.execution()
	attempts := recentAttempts(v.attempts)
	history := make([]attemptJSON, len(attempts))
	for i, attempt := range attempts {
		history[i] = attemptJSON{Ordinal: attempt.Ordinal, Lifecycle: string(attempt.Lifecycle), ExecutionClass: attempt.ExecutionClass, Profile: attempt.RequestedProfile, PlannedAgainst: attempt.PlannedAgainst, RoutingSource: attempt.RoutingSource, Harness: attempt.Harness, Model: attempt.Model, Effort: attempt.Effort, Worktree: attempt.Worktree}
	}
	return statusJSON{
		ID: v.task.ID, Project: v.task.Project, Kind: v.task.Kind, ExecutionClass: e.ExecutionClass, Profile: e.RequestedProfile, PlannedAgainst: e.PlannedAgainst, RoutingSource: e.RoutingSource, TaskLifecycle: string(v.task.Lifecycle), AttemptOrdinal: e.Ordinal, AttemptLifecycle: string(e.Lifecycle), Harness: e.Harness, Model: e.Model, Effort: e.Effort,
		AgentState: v.agentState, Worktree: e.Worktree, Herdr: e.Herdr, PR: v.task.PR,
		MergeExecuted: v.task.MergeExecuted, MergeAnnounced: v.task.MergeAnnounced,
		DeliveredAt: v.task.DeliveredAt, DeliveredReason: v.task.DeliveredReason,
		CreatedAt: v.task.CreatedAt, LastReportAt: v.lastReportAt,
		Reported: v.reported, GateRunIssue: v.gateIssue, RepairCode: v.task.RepairCode, RepairReason: v.task.RepairReason, RepairAttemptID: v.task.RepairAttemptID, RepairObservedAt: v.task.RepairObservedAt, Unacknowledged: v.unacked, Attempts: history,
		LatestSend: latestSendJSON(v.latestSend),
	}
}

func latestSendJSON(send *state.SendAttempt) *sendJSON {
	if send == nil {
		return nil
	}
	return &sendJSON{ID: send.ID, TaskID: send.TaskID, AttemptID: send.AttemptID, Origin: string(send.Origin), State: string(send.State), ReasonCode: send.ReasonCode, CreatedAt: send.CreatedAt, FinalizedAt: send.FinalizedAt}
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

func runStatusSingle(cmd *cobra.Command, home string, client *herdr.Client, id string, asJSON, full bool, cols []axi.Column[taskView]) error {
	history, err := state.ReadHistoryReadOnly(home, id)
	if err != nil {
		return asPrecondition(err)
	}
	history.Task = detectPRForStatus(cmd.Context(), home, history)
	t := history.Task

	// An unreadable report degrades exactly as it does in the fleet view: the
	// fault is named on the report field and the rest of the detail view still
	// prints, rather than the whole command failing over one bad read.
	v, reportLines := buildTaskView(home, client, history, full)

	// Propagated, not degraded: see the same comment in runStatusFleet.
	hold, held, err := state.ReadHoldReadOnly(home, id)
	if err != nil {
		return err
	}
	v.hold, v.held = hold, held

	// Looked up only when the check applies, so a registry this id's detail view does not need can never
	// fail the command.
	reportedDone := v.reportedState == state.ReportDone
	if gateRunApplies(t, reportedDone) {
		p, registered, err := project.FindReadOnly(home, t.Project)
		// Propagated, not degraded: a single task's own project is the one fact this check is about, unlike
		// the fleet view's best-effort lookup across every task's project at once.
		if err != nil {
			return err
		}
		v.gateIssue = gateRunIssue(home, t, reportedDone, p, registered, newGateRunReader())
	}

	// The whole file, sliced afterwards: deriving the flag from the 5-line
	// history window instead would let five trailing free-text lines hide a
	// completion the fleet view flags, and the two views must never disagree.
	const historyLen = 5
	tail := reportLines
	if len(tail) > historyLen {
		tail = tail[len(tail)-historyLen:]
	}

	if asJSON {
		history := make([]string, len(tail))
		for i, line := range tail {
			history[i] = reportLineText(line)
		}
		out := v.json()
		out.ReportHistory = history
		if held {
			j := holdToJSON(hold)
			out.Held = &j
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	var doc axi.Doc
	for _, c := range cols {
		doc.Field(c.Name, c.Value(v))
	}
	if v.task.RepairCode != "" {
		doc.Field("repair", "needs-repair")
		doc.Field("repair_code", v.task.RepairCode)
		doc.Field("repair_attempt", fmt.Sprintf("%d", v.task.RepairAttemptID))
		doc.Field("repair_reason", v.task.RepairReason)
		doc.Field("repair_observed_at", v.task.RepairObservedAt)
	}
	doc.List("report_history", historyBlock(v, tail, full))
	doc.List("attempts", attemptHistoryBlock(v))
	doc.Help(detailHelp(v, full)...)
	return doc.Render(cmd.OutOrStdout())
}

func attemptHistoryBlock(v taskView) []string {
	attempts := recentAttempts(v.attempts)
	lines := make([]string, len(attempts))
	for i, attempt := range attempts {
		lines[i] = fmt.Sprintf("Attempt %d: %s (%s, %s, %s)", attempt.Ordinal, attempt.Lifecycle, orNone(attempt.Harness), orNone(attempt.Model), orNone(attempt.Worktree))
	}
	return lines
}

// The report tail with the entry the report field already shows dropped - repeating it was the core of
// atqamz/hand#65, doubling the cost of every terminal report. --full keeps the tail whole.
func historyBlock(v taskView, tail []state.ReportLine, full bool) []string {
	// Which entry the report field shows. Found rather than assumed last: with the unacknowledged flag
	// applied that line is the classified terminal report, which the worker may have followed with text.
	reportedIdx := len(tail) - 1
	if v.unacked {
		reportedIdx = -1
		for i := len(tail) - 1; i >= 0; i-- {
			if !tail[i].Malformed {
				reportedIdx = i
				break
			}
		}
	}
	if !full && reportedIdx >= 0 {
		tail = slices.Concat(tail[:reportedIdx], tail[reportedIdx+1:])
	}
	lines := make([]string, len(tail))
	for i, line := range tail {
		if full {
			lines[i] = reportLineText(line)
		} else {
			lines[i] = truncateReportLine(line, reportSummaryBudget, v.task.ID)
		}
	}
	return lines
}

// Names the one command this task's current state calls for, so a caller reading the detail view does not
// have to work out what comes next from the state vocabulary.
func detailHelp(v taskView, full bool) []string {
	var help []string
	if !full && strings.Contains(v.reportedLine, "(truncated,") {
		help = append(help, "Run `hand status "+v.task.ID+" --full` for the untruncated report and history")
	}
	switch {
	case v.task.DeliveredAt != "" || v.task.MergeExecuted || v.task.MergeAnnounced:
		help = append(help, "Run `hand teardown "+v.task.ID+"` to clean up this task")
	case v.reportedState == state.ReportDone && v.task.PR != "":
		help = append(help, "Run `hand merge "+v.task.ID+"` once merging is authorized, or `hand deliver "+v.task.ID+" --reason <text>` if landing it is someone else's call")
	case v.reportedState == state.ReportNeedsDecision || v.reportedState == state.ReportBlocked:
		help = append(help, "Run `hand send "+v.task.ID+" <message>` to answer this worker")
	default:
		help = append(help, "Run `hand send "+v.task.ID+" <message>` to steer this worker")
	}
	return help
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
		return "none"
	}
	return formatAge(at)
}
