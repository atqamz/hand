package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/age"
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/watcher"
	"github.com/atqamz/hand/internal/workerobs"
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
			client, err := currentHerdrClient(home)
			if err != nil {
				return err
			}

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

// Read-only status degrades to "unknown" when herdr or the pane cannot be queried. reachable is false
// only for a claimed pane that failed to answer, never a task with no pane to probe. Unlike hand
// watch's dwelled ClassifyUnreachable, one failed probe is enough - a live read has no blink to filter.
func probePaneStatus(client *herdr.Client, attempt *state.Attempt) (agentState string, reachable bool) {
	if attempt == nil || attempt.Herdr.PaneID == "" {
		return string(herdr.StatusUnknown), true
	}
	pane, err := client.PaneGet(attempt.Herdr.PaneID)
	if err != nil {
		return string(herdr.StatusUnknown), false
	}
	normalized, normalizeErr := workerobs.Normalize(*attempt, pane, client)
	if normalizeErr != nil {
		return string(herdr.StatusUnknown), true
	}
	if normalized.AgentStatus == "" {
		return string(herdr.StatusUnknown), false
	}
	return string(normalized.AgentStatus), true
}

// Mirrors one classified line from state.ReportLine for JSON output: malformed lines carry their raw text
// in Note with State left empty, and an unreadable report file carries the read error in Note under the
// reportUnreadable state.
type reportedJSON struct {
	State string `json:"state"`
	Note  string `json:"note"`
}

type statusJSON struct {
	ID               string      `json:"id"`
	Project          string      `json:"project"`
	Kind             string      `json:"kind"`
	ExecutionClass   string      `json:"execution_class,omitempty"`
	Profile          string      `json:"profile,omitempty"`
	PlannedAgainst   string      `json:"planned_against,omitempty"`
	RoutingSource    string      `json:"routing_source,omitempty"`
	TaskLifecycle    string      `json:"task_lifecycle"`
	AttemptOrdinal   int         `json:"attempt_ordinal,omitempty"`
	AttemptLifecycle string      `json:"attempt_lifecycle,omitempty"`
	Harness          string      `json:"harness,omitempty"`
	Model            string      `json:"model,omitempty"`
	Effort           string      `json:"effort,omitempty"`
	AgentState       string      `json:"agent_state"`
	Worktree         string      `json:"worktree"`
	Herdr            state.Herdr `json:"herdr"`
	PR               string      `json:"pr"`
	// Omitted where no live lookup applied, so a consumer sees "unknown" only where an
	// empty pr field is a failed observation rather than a PR that is not there.
	PRObservation string `json:"pr_observation,omitempty"`
	// Set only alongside PRObservation "found", carrying the URL GitHub answered with. Never mirrored
	// into PR: that field is the durable record alone, so a found-but-unrecorded PR can never render
	// as though hand merge's durable check would also see it (atqamz/hand#266).
	PRObservedURL string `json:"pr_observed_url,omitempty"`
	// Set only through hand pr's --cross-repo opt-in (atqamz/hand#423): its presence alone marks
	// PR as a deliberate delivery to a repository other than the project's own or its declared
	// upstream, so a consumer never has to infer that from the URL.
	PRCrossRepoReason string        `json:"pr_cross_repo_reason,omitempty"`
	MergeExecuted     bool          `json:"merged"`
	MergeAnnounced    bool          `json:"pr_merged_observed"`
	DeliveredAt       string        `json:"delivered_at,omitempty"`
	DeliveredReason   string        `json:"delivered_reason,omitempty"`
	CreatedAt         string        `json:"created_at"`
	LastReportAt      string        `json:"last_report_at,omitempty"`
	Reported          *reportedJSON `json:"reported,omitempty"`
	ReportHistory     []string      `json:"report_history,omitempty"`
	Held              *holdJSON     `json:"held,omitempty"`
	// Omitted where a report file exists (found) or genuinely never has (absent), the same way
	// PRObservation is: a consumer sees "unknown" only where an empty last_report_at is a failed
	// stat rather than a task that has never reported.
	LastReportObservation string `json:"last_report_observation,omitempty"`
	// Omitted where the gate-run check does not apply to this task, the same way PRObservation
	// is: found, absent and unknown are otherwise distinct answers, never collapsed together.
	GateObservation  string `json:"gate_observation,omitempty"`
	RepairCode       string `json:"repair_code,omitempty"`
	RepairReason     string `json:"repair_reason,omitempty"`
	RepairAttemptID  int64  `json:"repair_attempt,omitempty"`
	RepairObservedAt string `json:"repair_observed_at,omitempty"`
	// Omitted when false so a consumer written before this field sees no change
	// on the fleet it already understands.
	Unacknowledged bool `json:"unacknowledged,omitempty"`
	// Distinct from Unacknowledged: whether some watcher has ever announced this report, rather than
	// whether a supervisor has acknowledged it (atqamz/hand#267).
	Unannounced bool `json:"unannounced,omitempty"`
	// The two conditions atqamz/hand#268 gave hand status a counterpart classifier for: a pane silent
	// past its report state's bound, and one that claims a pane but never answered it.
	Parked             bool          `json:"parked,omitempty"`
	Unreachable        bool          `json:"unreachable,omitempty"`
	Attempts           []attemptJSON `json:"attempts,omitempty"`
	LatestSend         *sendJSON     `json:"latest_send,omitempty"`
	HerdrSessionName   string        `json:"herdr_session_name"`
	HerdrSessionState  string        `json:"herdr_session_state"`
	HerdrSessionReason string        `json:"herdr_session_reason"`
}

type sendJSON struct {
	ID             int64  `json:"id"`
	TaskID         string `json:"task_id"`
	AttemptID      int64  `json:"attempt_id"`
	Origin         string `json:"origin"`
	State          string `json:"state"`
	ReasonCode     string `json:"reason_code,omitempty"`
	CreatedAt      string `json:"created_at"`
	FinalizedAt    string `json:"finalized_at,omitempty"`
	NeedsAttention bool   `json:"needs_attention,omitempty"`
	RetrySafe      bool   `json:"retry_safe,omitempty"`
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
	TaskCount    int                      `json:"task_count"`
	Tasks        []statusJSON             `json:"tasks"`
	Holds        []holdJSON               `json:"holds"`
	HerdrSession herdr.SessionObservation `json:"herdr_session"`
}

// Mirrors state.Hold, plus Inconsistent, which is set instead of the row being dropped when a value
// cannot be trusted at face value - see holdInconsistency - and Satisfied, set once a blocked hold's
// blocked_on task has gone terminal (atqamz/hand#417).
type holdJSON struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Reason       string `json:"reason"`
	BlockedOn    string `json:"blocked_on,omitempty"`
	SetAt        string `json:"set_at"`
	Inferred     bool   `json:"inferred,omitempty"`
	Inconsistent string `json:"inconsistent,omitempty"`
	Satisfied    bool   `json:"satisfied,omitempty"`
}

// What a blocked hold's blocked_on task looks like right now, read straight from the store: a cleanly
// torn-down blocker has already left ListReconciliationHistories, so reading it there would misreport
// a landed blocker as unknown (atqamz/hand#417).
type blockerState struct {
	found    bool
	terminal bool
}

// Reads one blocked_on task's terminal state directly, independent of the fleet view. ErrTaskNotFound
// degrades to a plain not-found answer - the caller routes that to inconsistency, never satisfaction -
// but every other error propagates, like every other hold read in this file.
func resolveBlocker(home, blockedOn string, readOnly bool) (blockerState, error) {
	read := state.ReadHistory
	if readOnly {
		read = state.ReadHistoryReadOnly
	}
	history, err := read(home, blockedOn)
	if err != nil {
		if errors.Is(err, state.ErrTaskNotFound) {
			return blockerState{}, nil
		}
		return blockerState{}, err
	}
	return blockerState{found: true, terminal: history.Task.Lifecycle == state.TaskTerminal}, nil
}

// Resolves a whole hold list at once, deduplicating by blocked_on so several holds waiting on the same
// task - the issue's own example - read that task once, not once per hold.
func resolveBlockers(home string, holds []state.Hold, readOnly bool) (map[string]blockerState, error) {
	resolved := make(map[string]blockerState)
	for _, h := range holds {
		if h.Kind != state.HoldKindBlocked || h.BlockedOn == "" {
			continue
		}
		if _, ok := resolved[h.BlockedOn]; ok {
			continue
		}
		b, err := resolveBlocker(home, h.BlockedOn, readOnly)
		if err != nil {
			return nil, err
		}
		resolved[h.BlockedOn] = b
	}
	return resolved, nil
}

// Names why a hold row cannot be trusted at face value, so that ListHolds surfacing every row (rather
// than filtering) turns into a visible flag instead of a silently wrong render. blockerFound - see
// resolveBlocker - makes an unknown blocked_on inconsistent too, never satisfied (atqamz/hand#417).
func holdInconsistency(h state.Hold, blockerFound bool) string {
	// An unrecognized kind, a blocked hold with nothing to point at, or an operator or limit hold carrying
	// a blocked_on nothing set. Nothing here writes such a row today - hand hold set validates first, and
	// limit holds set no blocked_on - so one means something outside hand touched state/hand.db directly.
	switch h.Kind {
	case state.HoldKindOperator:
		if h.BlockedOn != "" {
			return fmt.Sprintf("operator hold carries a blocked_on %q", h.BlockedOn)
		}
		if h.Inferred {
			return "operator hold carries inferred, but an operator observes directly"
		}
		return ""
	case state.HoldKindBlocked:
		if h.BlockedOn == "" {
			return "blocked hold has no blocked_on"
		}
		if h.Inferred {
			return "blocked hold carries inferred, but what it waits on is named, not scraped"
		}
		if !blockerFound {
			return fmt.Sprintf("blocked hold waits on unknown task %q", h.BlockedOn)
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

func holdSatisfied(h state.Hold, blocker blockerState) bool {
	return h.Kind == state.HoldKindBlocked && blocker.found && blocker.terminal
}

func holdToJSON(h state.Hold, blocker blockerState) holdJSON {
	return holdJSON{
		ID: h.ID, Kind: h.Kind, Reason: h.Reason, BlockedOn: h.BlockedOn, SetAt: h.SetAt,
		Inferred: h.Inferred, Inconsistent: holdInconsistency(h, blocker.found), Satisfied: holdSatisfied(h, blocker),
	}
}

// Renders a hold's non-identifying fields for the plain-text held block. An inconsistency, then a
// satisfied blocker (atqamz/hand#417, only reported, never auto-cleared), each take over the whole
// line rather than sit beside a stale reason. Inferred instead appends a suffix.
func holdDetail(h state.Hold, blocker blockerState) string {
	if inc := holdInconsistency(h, blocker.found); inc != "" {
		return "inconsistent: " + inc
	}
	if holdSatisfied(h, blocker) {
		return fmt.Sprintf("satisfied: %s is terminal; this hold can be cleared", h.BlockedOn)
	}
	detail := h.Reason
	if h.Kind == state.HoldKindBlocked {
		detail = fmt.Sprintf("waiting on %s: %s", h.BlockedOn, h.Reason)
	}
	if h.Inferred {
		detail += " (inferred from a pane scrape)"
	}
	return detail
}

// The single predicate for whether the gate-run check has anything to say about a task: only a done
// ship task with a recorded PR does. Delegates to watcher.GateApplies, the one copy of this rule hand
// status and hand watch now share, so a silent task never pays for or fails over the check.
func gateRunApplies(t state.Task, reportedDone bool) bool {
	return watcher.GateApplies(t.Kind, t.PR, t.DeliveredAt, reportedDone)
}

// Bounds one no-mistakes process the gate-run check spawns, the same 5 seconds prdetect.go bounds
// its own live GitHub fallback by: a hung subprocess must cost one render its answer, not the whole
// `hand status` invocation.
const gateRunTimeout = 5 * time.Second

// Answers "which PRs did completed no-mistakes runs record" for one clone path.
type gateRunReader func(clonePath string) (map[string]bool, error)

// Caches each clone path's answer for the life of one render, so a fleet with several done ship tasks on
// the same project spawns one no-mistakes process for it, not one per task. Failures are cached too: a
// clone that could not be asked once is not worth re-asking within the same render.
func newGateRunReader(ctx context.Context) gateRunReader {
	type answer struct {
		prs map[string]bool
		err error
	}
	cache := map[string]answer{}
	return func(clonePath string) (map[string]bool, error) {
		a, ok := cache[clonePath]
		if !ok {
			runCtx, cancel := context.WithTimeout(ctx, gateRunTimeout)
			a.prs, a.err = project.GateRunPRs(runCtx, clonePath)
			cancel()
			cache[clonePath] = a
		}
		return a.prs, a.err
	}
}

// Reports whether a done ship task's recorded PR was found in, is absent from, or could not be
// checked against a no-mistakes gate run, in the found/absent/unknown vocabulary atqamz/hand#241
// established. Empty where the check does not apply, which is neither a finding nor an absence.
func gateRunObservation(home string, t state.Task, reportedDone bool, p project.Project, registered bool, runPRs gateRunReader) ghutil.ObservationState {
	if !gateRunApplies(t, reportedDone) {
		return ""
	}
	return project.ObserveGateRun(home, p, registered, t.PR, runPRs)
}

func runStatusFleet(cmd *cobra.Command, home string, client *herdr.Client, asJSON bool, cols []axi.Column[taskView]) error {
	herdrSession := client.ObserveSession(cmd.Context())
	views, holds, blockers, err := fleetViews(cmd.Context(), cmd.ErrOrStderr(), home, client, true)
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
			holdRows = append(holdRows, holdToJSON(h, blockers[h.BlockedOn]))
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(fleetJSON{TaskCount: len(rows), Tasks: rows, Holds: holdRows, HerdrSession: herdrSession})
	}

	var doc axi.Doc
	appendFleet(&doc, views, holds, blockers, cols)
	return doc.Render(cmd.OutOrStdout())
}

func appendHerdrSession(doc *axi.Doc, observation herdr.SessionObservation) {
	doc.Field("herdr_session_name", orNone(observation.Name))
	doc.Field("herdr_session_state", orNone(string(observation.State)))
	doc.Field("herdr_session_reason", orNone(observation.Reason))
}

// Writes the fleet blocks onto doc rather than a writer, so the bare command can put its identity fields
// above the same overview.
func appendFleet(doc *axi.Doc, views []taskView, holds []state.Hold, blockers map[string]blockerState, cols []axi.Column[taskView], leadHelp ...string) {
	attention := appendFleetState(doc, views, holds, blockers, cols)
	// An unanswered configuration question leads: it is the one thing here the fleet cannot proceed
	// without, and doc.Help renders a single list, so it cannot be a second block.
	doc.Help(append(slices.Clone(leadHelp), fleetHelp(views, attention)...)...)
}

func appendFleetState(doc *axi.Doc, views []taskView, holds []state.Hold, blockers map[string]blockerState, cols []axi.Column[taskView]) int {
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
	axi.Table(doc, "holds", holds, holdFields(blockers))
	return attention
}

func fleetViews(ctx context.Context, warnOut io.Writer, home string, client *herdr.Client, readOnly bool) ([]taskView, []state.Hold, map[string]blockerState, error) {
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
		return nil, nil, nil, err
	}
	// Propagated rather than degraded to an empty list: a store fault reading
	// as no holds is exactly the false all-clear this feature exists to avoid.
	holds, err := listHolds(home)
	if err != nil {
		return nil, nil, nil, err
	}
	// Propagated for the same reason: a blocked hold's blocker misread as "unknown" would report
	// inconsistency in place of the satisfaction the operator needs to see (atqamz/hand#417).
	blockers, err := resolveBlockers(home, holds, readOnly)
	if err != nil {
		return nil, nil, nil, err
	}

	// Best-effort, like the project registry read elsewhere in this fleet view: a fault degrades every
	// task's gate-run check to silent rather than failing the whole fleet overview over it.
	projects, projectsErr := listProjects(home)
	if projectsErr != nil {
		// Named on stderr all the same - silently dropping every (gate: ...) marker fleet-wide would render
		// an ungated PR as clean, the false all-clear this feature exists to avoid.
		if _, err := fmt.Fprintf(warnOut, "warning: project registry unreadable, gate-run checks skipped: %v\n", projectsErr); err != nil {
			return nil, nil, nil, err
		}
	}
	projectByName := make(map[string]project.Project, len(projects))
	for _, p := range projects {
		projectByName[p.Name] = p
	}
	runPRs := newGateRunReader(ctx)
	bounds, err := parkedBoundsFromConfig(home)
	if err != nil {
		return nil, nil, nil, err
	}

	views := make([]taskView, 0, len(histories))
	holdsByID := make(map[string]state.Hold, len(holds))
	for _, hold := range holds {
		holdsByID[hold.ID] = hold
	}
	for _, history := range histories {
		t := history.Task
		v, _ := buildTaskView(home, client, history, false, readOnly, bounds)
		if hold, ok := holdsByID[t.ID]; ok {
			v.held = true
			v.hold = hold
			v.holdBlocker = blockers[hold.BlockedOn]
		}
		p, registered := projectByName[t.Project]
		v.gateObserved = gateRunObservation(home, t, v.reportedState == state.ReportDone, p, registered, runPRs)
		views = append(views, v)
	}
	return views, holds, blockers, nil
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

// A stat fault degrades to unknown rather than absent: state.ReportModTime already tells the two
// apart, and failing hand status over one unreadable timestamp would trade away a whole fleet view
// for it - so the fault travels as an observation instead (atqamz/hand#270).
func lastReportAt(home, id string) (string, ghutil.ObservationState) {
	mtime, ok, err := state.ReportModTime(home, id)
	if err != nil {
		return "", ghutil.ObservationUnknown
	}
	if !ok {
		return "", ghutil.ObservationAbsent
	}
	return mtime.UTC().Format(time.RFC3339), ghutil.ObservationFound
}

// Named only for unknown: found already has the timestamp and absent has neither, so naming those
// too would repeat what LastReportAt's own emptiness already says.
func lastReportObservationJSON(o ghutil.ObservationState) string {
	if o != ghutil.ObservationUnknown {
		return ""
	}
	return string(o)
}

// Distinct from "unreported": an I/O fault is not evidence that the worker
// never reported.
const reportUnreadable = "unreadable"

// Shared by unannounced and unacknowledged: whether this task's terminal report sits past cur, whichever
// durable cursor the caller names. Takes the state the caller already derived rather than reading the
// file a second time, so a worker appending between the two reads can never surface a stale flag.
func terminalReportPast(data []byte, reported state.ReportLine, reportedOK bool, readErr error, cur state.ReportCursor) (bool, error) {
	// A read that fails folds into the caller's own report-read error: the file was readable a moment ago
	// and is not now, which is what that error already says, and swallowing it would render an unread
	// completion as covered.
	if readErr != nil {
		return false, readErr
	}
	// False for anything but a terminal state, so the flag can only ever qualify the state this row prints.
	if !reportedOK || !state.TerminalReport(reported.State) {
		return false, nil
	}
	return state.TerminalReportInData(data, cur), nil
}

// Asks state whether some watcher has ever announced this task's terminal report - report_offset and
// report_digest are the watcher's own cursor, distinct from whether a supervisor has acknowledged it
// (atqamz/hand#267, docs/adr/attention-is-one-derivation-over-three-channels.md).
func unannounced(data []byte, t state.Task, reported state.ReportLine, reportedOK bool, readErr error) (bool, error) {
	return terminalReportPast(data, reported, reportedOK, readErr, state.ReportCursor{Offset: t.ReportOffset, Digest: t.ReportDigest})
}

// Asks state whether a supervisor has acknowledged this task's terminal report through `hand ack`,
// the durable act atqamz/hand#267 records apart from watcher announcement.
func unacknowledged(data []byte, t state.Task, reported state.ReportLine, reportedOK bool, readErr error) (bool, error) {
	return terminalReportPast(data, reported, reportedOK, readErr, state.ReportCursor{Offset: t.AcknowledgedOffset, Digest: t.AcknowledgedDigest})
}

// Derives everything both status views show from one already-read history, and returns the report lines
// alongside so the detail view's history block and the summary line above it can never come from two
// reads of the file. bounds only applies to an open task's one running attempt.
func buildTaskView(home string, client *herdr.Client, history state.TaskHistory, full, readOnly bool, bounds watcher.ParkedBounds) (taskView, []state.ReportLine) {
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
	attemptClient := herdrClientForAttempt(attempt, client)
	agentState, reachable := probePaneStatus(attemptClient, attempt)
	data, readErr := state.ReadReportData(home, t.ID)
	lines := state.ReportLinesInData(data)
	reported, reportedOK := state.LastReportedState(lines)
	unacked, readErr := unacknowledged(data, t, reported, reportedOK, readErr)
	unannounced, readErr := unannounced(data, t, reported, reportedOK, readErr)

	var last state.ReportLine
	if len(lines) > 0 {
		last = lines[len(lines)-1]
	}
	reportedState := ""
	if reportedOK {
		reportedState = reported.State
	}
	active := t.Lifecycle == state.TaskOpen && history.ActiveAttempt != nil && attempt.Lifecycle == state.AttemptRunning
	reportAt, reportAtObserved := lastReportAt(home, t.ID)
	parked, parkedActionable := taskParked(home, t, e, agentState, reportedState, bounds)
	v := taskView{
		task:               t,
		attempt:            attempt,
		attempts:           attempts,
		agentState:         agentState,
		reportedState:      reportedState,
		reportedLine:       reportSummary(t.ID, lines, readErr, unacked, full),
		lastReportAt:       reportAt,
		lastReportObserved: reportAtObserved,
		reportFile:         state.ReportPath(home, t.ID),
		unreadable:         readErr != nil,
		unacked:            unacked,
		unannounced:        unannounced,
		unreachable:        active && !reachable,
		parked:             active && parked,
		parkedActionable:   active && parkedActionable,
		reported:           reportedFrom(last, len(lines) > 0, readErr),
	}
	if attempt != nil {
		latestSend := state.LatestSendMetadata
		if readOnly {
			latestSend = state.LatestSendMetadataReadOnly
		}
		if latest, found, err := latestSend(home, t.ID, attempt.ID); err == nil && found {
			v.latestSend = &latest
		} else if len(history.Sends) != 0 {
			for i := len(history.Sends) - 1; i >= 0; i-- {
				if history.Sends[i].AttemptID == attempt.ID {
					latest := history.Sends[i]
					v.latestSend = &latest
					break
				}
			}
		}
	}
	return v, lines
}

// Reports whether an open task's active pane has gone silent past its last reported state's bound,
// the status-side counterpart ClassifyParked never had (atqamz/hand#32, atqamz/hand#268's
// disagreement 2), and separately whether that verdict is corroborated enough to be actionable (atqamz/hand#492).
func taskParked(home string, t state.Task, attempt state.Attempt, agentState, reportedState string, bounds watcher.ParkedBounds) (parked, actionable bool) {
	silentSince, err := watcher.ReportEvidenceTime(home, t, attempt)
	if err != nil {
		return false, false
	}
	naive := watcher.Parked(reportedState, t.DeliveredAt, silentSince, time.Now(), bounds)
	if !naive {
		return false, false
	}
	// A pane this render observed working or blocked is the runtime observation `hand reconcile` calls
	// `liveness: working`, one-shot and needing no baseline. A watcher's own durable confirmation of
	// this exact episode (AlreadyAnnounced) means this render is not the first to find it either way.
	live := herdr.Status(agentState) == herdr.StatusWorking || herdr.Status(agentState) == herdr.StatusBlocked
	if live || watcher.AlreadyAnnounced(attempt, silentSince) {
		return true, false
	}
	return true, true
}

// Reads config/parked-*-bound, shared by newWatchCmd, once per render rather than once per task, so a
// fleet of a hundred tasks reads config three times, not three hundred.
func parkedBoundsFromConfig(home string) (watcher.ParkedBounds, error) {
	paused, err := configSeconds(home, "parked-paused-bound", defaultParkedPausedBound)
	if err != nil {
		return watcher.ParkedBounds{}, err
	}
	done, err := configSeconds(home, "parked-done-bound", defaultParkedDoneBound)
	if err != nil {
		return watcher.ParkedBounds{}, err
	}
	other, err := configSeconds(home, "parked-other-bound", defaultParkedOtherBound)
	if err != nil {
		return watcher.ParkedBounds{}, err
	}
	return watcher.ParkedBounds{Paused: paused, Done: done, Other: other}, nil
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
		AgentState: v.agentState, Worktree: e.Worktree, Herdr: e.Herdr, PR: v.task.PR, PRObservation: string(v.prObserved), PRObservedURL: v.prObservedURL, PRCrossRepoReason: v.task.PRCrossRepoReason,
		MergeExecuted: v.task.MergeExecuted, MergeAnnounced: v.task.MergeAnnounced,
		DeliveredAt: v.task.DeliveredAt, DeliveredReason: v.task.DeliveredReason,
		CreatedAt: v.task.CreatedAt, LastReportAt: v.lastReportAt, LastReportObservation: lastReportObservationJSON(v.lastReportObserved),
		Reported: v.reported, GateObservation: string(v.gateObserved), RepairCode: v.task.RepairCode, RepairReason: v.task.RepairReason, RepairAttemptID: v.task.RepairAttemptID, RepairObservedAt: v.task.RepairObservedAt, Unacknowledged: v.unacked, Unannounced: v.unannounced, Parked: v.parked, Unreachable: v.unreachable, Attempts: history,
		LatestSend: latestSendJSON(v.latestSend),
	}
}

func latestSendJSON(send *state.SendAttempt) *sendJSON {
	if send == nil {
		return nil
	}
	return &sendJSON{ID: send.ID, TaskID: send.TaskID, AttemptID: send.AttemptID, Origin: string(send.Origin), State: string(send.State), ReasonCode: send.ReasonCode, CreatedAt: send.CreatedAt, FinalizedAt: send.FinalizedAt, NeedsAttention: state.SendNeedsAttention(*send), RetrySafe: state.SendRetrySafe(*send)}
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
	herdrSession := client.ObserveSession(cmd.Context())
	history, err := state.ReadHistoryReadOnly(home, id)
	if err != nil {
		return asPrecondition(err)
	}
	observed := detectPRForStatus(cmd.Context(), home, history)
	t := history.Task

	bounds, err := parkedBoundsFromConfig(home)
	if err != nil {
		return err
	}
	// An unreadable report degrades exactly as it does in the fleet view: the
	// fault is named on the report field and the rest of the detail view still
	// prints, rather than the whole command failing over one bad read.
	v, reportLines := buildTaskView(home, client, history, full, true, bounds)
	v.prObserved = observed.State
	v.prObservedURL = observed.URL

	// Propagated, not degraded: see the same comment in runStatusFleet.
	hold, held, err := state.ReadHoldReadOnly(home, id)
	if err != nil {
		return err
	}
	v.hold, v.held = hold, held
	var blocker blockerState
	if held && hold.Kind == state.HoldKindBlocked && hold.BlockedOn != "" {
		blocker, err = resolveBlocker(home, hold.BlockedOn, true)
		if err != nil {
			return err
		}
		v.holdBlocker = blocker
	}

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
		v.gateObserved = gateRunObservation(home, t, reportedDone, p, registered, newGateRunReader(cmd.Context()))
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
		out.HerdrSessionName = herdrSession.Name
		out.HerdrSessionState = string(herdrSession.State)
		out.HerdrSessionReason = herdrSession.Reason
		out.ReportHistory = history
		if held {
			j := holdToJSON(hold, blocker)
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
	// hand merge reads the durable pr column, not this observation, and refuses with "no PR recorded"
	// against exactly this task until it is recorded - naming the recording step first is what keeps
	// hand status from promising a command hand merge is guaranteed to refuse (atqamz/hand#266).
	case v.reportedState == state.ReportDone && v.task.PR == "" && v.prObserved == ghutil.ObservationFound:
		help = append(help, "Run `hand pr "+v.task.ID+" "+v.prObservedURL+"` to record the PR github reports, then `hand merge "+v.task.ID+"` once merging is authorized")
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
