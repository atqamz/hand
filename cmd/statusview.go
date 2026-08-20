package cmd

import (
	"fmt"
	"strings"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/watcher"
)

// One task as both status renderers see it: the durable row plus everything derived for display, so the
// fleet table and the single-task detail can never disagree about what a task is doing.
type taskView struct {
	task          state.Task
	attempt       *state.Attempt
	attempts      []state.Attempt
	agentState    string
	reportedState string
	reportedLine  string
	reported      *reportedJSON
	lastReportAt  string
	// found for a real mtime, absent for no report file, unknown for a stat error - atqamz/hand#270,
	// so an I/O fault on the report channel never renders identically to a worker that never reported.
	lastReportObserved ghutil.ObservationState
	reportFile         string
	unreadable         bool
	unacked            bool
	// Whether some watcher has ever announced this task's terminal report - a separate fact from unacked,
	// which is whether a supervisor has acknowledged it through `hand ack` (atqamz/hand#267).
	unannounced        bool
	// The two conditions hand watch's own Kind vocabulary already named and hand status never had a
	// counterpart classifier for - atqamz/hand#268's disagreements 2 and 4 (attention half). Both are
	// computed only for an open task's one running attempt; see buildTaskView.
	unreachable bool
	parked      bool
	latestSend  *state.SendAttempt
	held        bool
	hold        state.Hold
	// Empty where no live lookup applied, which is neither a finding nor an absence.
	prObserved ghutil.ObservationState
	// The URL prObserved == ghutil.ObservationFound answers with. Rendered alongside task.PR rather
	// than folded into it, so a found-but-unrecorded PR never takes the shape a recorded one renders
	// in (atqamz/hand#266, docs/adr/attention-is-one-derivation-over-three-channels.md invariant 1).
	prObservedURL string
	// Empty where the gate-run check does not apply to this task, which is neither a finding nor
	// an absence either: found, absent and unknown are the only three answers it ever gives.
	gateObserved ghutil.ObservationState
}

// Reports whether the gate-run check found something an operator should look at: a found run and a
// check that does not apply are both fine, so only absent and unknown count. watcher.GateKind is the
// one place that decision is made, shared with hand watch's own gate classifier (atqamz/hand#268).
func gateProblem(v taskView) bool {
	_, ok := watcher.GateKind(v.gateObserved)
	return ok
}

func (v taskView) execution() state.Attempt {
	if v.attempt != nil {
		return *v.attempt
	}
	return state.Attempt{}
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// A worker whose pane is not busy and whose last word was working (or nothing
// at all) has stopped without saying so, which is the one fleet-view marker
// that is about absence rather than about something reported.
func unreportedStop(v taskView) bool {
	if v.unreadable {
		return false
	}
	if v.reportedState != "" && v.reportedState != state.ReportWorking {
		return false
	}
	return herdr.Status(v.agentState).NotBusy()
}

// Packs the markers the plain-text fleet view used to append to the state column as parenthetical
// suffixes, one token each so a caller can test for one without parsing prose.
func taskFlags(v taskView) []string {
	var flags []string
	if v.unreadable {
		flags = append(flags, "report-unreadable")
	}
	if v.unacked {
		flags = append(flags, "unacknowledged")
	}
	if v.unannounced {
		flags = append(flags, "unannounced")
	}
	if unreportedStop(v) {
		flags = append(flags, "unreported")
	}
	if v.unreachable {
		flags = append(flags, "unreachable")
	}
	if v.parked {
		flags = append(flags, "parked")
	}
	if v.task.DeliveredAt != "" {
		flags = append(flags, "delivered")
	}
	switch {
	case v.task.PR == "":
	case v.task.MergeExecuted:
		flags = append(flags, "merged")
	case v.task.MergeAnnounced:
		flags = append(flags, "merged-external")
	}
	if kind, ok := watcher.GateKind(v.gateObserved); ok {
		flags = append(flags, kind)
	}
	if v.task.RepairCode != "" {
		flags = append(flags, "needs-repair")
	}
	if v.latestSend != nil {
		switch {
		case v.latestSend.State == state.SendPending, v.latestSend.State == state.SendUncertain:
			flags = append(flags, "send-"+string(v.latestSend.State))
		case state.SendNeedsAttention(*v.latestSend):
			flags = append(flags, "send-partial")
		}
	}
	return flags
}

// The predicate behind the fleet view's attention aggregate - the fleet's one definition of attention
// every consumer reads (atqamz/hand#268, docs/adr/attention-is-one-derivation-over-three-channels.md).
// No elapsed-time staleness condition here, matching CatchUpFilter's own deliberate KindStale exclusion.
func needsAttention(v taskView) bool {
	if v.unreadable || v.unacked || gateProblem(v) || v.task.RepairCode != "" {
		return true
	}
	if v.unreachable || v.parked {
		return true
	}
	if v.latestSend != nil && state.SendNeedsAttention(*v.latestSend) {
		return true
	}
	switch v.reportedState {
	case state.ReportPaused, state.ReportBlocked, state.ReportNeedsDecision, state.ReportFailed:
		return true
	}
	return unreportedStop(v)
}

// The vocabulary --fields draws from, for both status views. One registry rather than one per view: a
// field means the same thing wherever it is asked for.
var taskFields = []axi.Column[taskView]{
	{Name: "id", Value: func(v taskView) string { return v.task.ID }},
	{Name: "project", Value: func(v taskView) string { return v.task.Project }},
	{Name: "kind", Value: func(v taskView) string { return v.task.Kind }},
	{Name: "execution_class", Value: func(v taskView) string { return orNone(v.execution().ExecutionClass) }},
	{Name: "profile", Value: func(v taskView) string { return orNone(v.execution().RequestedProfile) }},
	{Name: "task_lifecycle", Value: func(v taskView) string { return string(v.task.Lifecycle) }},
	{Name: "attempt_ordinal", Value: func(v taskView) string {
		if v.attempt == nil {
			return "none"
		}
		return fmt.Sprintf("%d", v.attempt.Ordinal)
	}},
	{Name: "attempt_lifecycle", Value: func(v taskView) string {
		if v.attempt == nil {
			return "none"
		}
		return string(v.attempt.Lifecycle)
	}},
	{Name: "harness", Value: func(v taskView) string { return orNone(v.execution().Harness) }},
	{Name: "model", Value: func(v taskView) string { return orNone(v.execution().Model) }},
	{Name: "effort", Value: func(v taskView) string { return orNone(v.execution().Effort) }},
	{Name: "planned_against", Value: func(v taskView) string { return orNone(v.execution().PlannedAgainst) }},
	{Name: "routing_source", Value: func(v taskView) string { return orNone(v.execution().RoutingSource) }},
	{Name: "state", Value: func(v taskView) string { return v.agentState }},
	{Name: "reported", Value: func(v taskView) string {
		if v.unreadable {
			return reportUnreadable
		}
		return orNone(v.reportedState)
	}},
	{Name: "report", Value: func(v taskView) string { return orNone(v.reportedLine) }},
	{Name: "age", Value: func(v taskView) string { return formatAge(v.task.CreatedAt) }},
	{Name: "created", Value: func(v taskView) string { return orNone(v.task.CreatedAt) }},
	{Name: "last_report", Value: func(v taskView) string {
		if v.lastReportObserved == ghutil.ObservationUnknown {
			return "unknown"
		}
		return formatReportAge(v.lastReportAt)
	}},
	{Name: "pr", Value: func(v taskView) string {
		if v.task.PR != "" {
			return v.task.PR
		}
		switch v.prObserved {
		case ghutil.ObservationUnknown:
			return "unknown"
		case ghutil.ObservationFound:
			return fmt.Sprintf("none (observed only: %s)", v.prObservedURL)
		}
		return "none"
	}},
	{Name: "worktree", Value: func(v taskView) string { return orNone(v.execution().Worktree) }},
	{Name: "herdr", Value: func(v taskView) string {
		e := v.execution()
		return orNone(strings.Trim(e.Herdr.Session+"/"+e.Herdr.TabID, "/"))
	}},
	{Name: "brief", Value: func(v taskView) string { return orNone(v.task.Brief) }},
	{Name: "delivered", Value: func(v taskView) string {
		if v.task.DeliveredAt == "" {
			return "none"
		}
		return fmt.Sprintf("%s (%s)", v.task.DeliveredReason, v.task.DeliveredAt)
	}},
	{Name: "send_id", Value: func(v taskView) string {
		if v.latestSend == nil {
			return "none"
		}
		return fmt.Sprintf("%d", v.latestSend.ID)
	}},
	{Name: "send_attempt", Value: func(v taskView) string {
		if v.latestSend == nil {
			return "none"
		}
		return fmt.Sprintf("%d", v.latestSend.AttemptID)
	}},
	{Name: "send_state", Value: func(v taskView) string {
		if v.latestSend == nil {
			return "none"
		}
		return string(v.latestSend.State)
	}},
	{Name: "send_origin", Value: func(v taskView) string {
		if v.latestSend == nil {
			return "none"
		}
		return string(v.latestSend.Origin)
	}},
	{Name: "send_reason", Value: func(v taskView) string {
		if v.latestSend == nil {
			return "none"
		}
		return orNone(v.latestSend.ReasonCode)
	}},
	{Name: "held", Value: func(v taskView) string {
		if !v.held {
			return "none"
		}
		return holdDetail(v.hold)
	}},
	{Name: "gate", Value: func(v taskView) string { return orNone(string(v.gateObserved)) }},
	{Name: "report_file", Value: func(v taskView) string { return orNone(v.reportFile) }},
	{Name: "flags", Value: func(v taskView) string { return orNone(strings.Join(taskFlags(v), " ")) }},
	{Name: "repair", Value: func(v taskView) string {
		if v.task.RepairCode == "" {
			return "none"
		}
		return "needs-repair"
	}},
	{Name: "repair_code", Value: func(v taskView) string { return orNone(v.task.RepairCode) }},
	{Name: "repair_attempt", Value: func(v taskView) string {
		if v.task.RepairAttemptID == 0 {
			return "none"
		}
		return fmt.Sprintf("%d", v.task.RepairAttemptID)
	}},
	{Name: "repair_reason", Value: func(v taskView) string { return orNone(v.task.RepairReason) }},
	{Name: "repair_observed_at", Value: func(v taskView) string { return orNone(v.task.RepairObservedAt) }},
}

// The fleet view answers "what is running and what wants me", so it defaults to
// the five fields that answer it; everything else in taskFields is one --fields
// away.
var fleetDefaultFields = []string{"id", "state", "reported", "age", "flags"}

// The single-task view is one item, not a list, so it defaults to everything
// the plain-text detail view printed.
var detailDefaultFields = []string{
	"id", "project", "kind", "execution_class", "profile", "planned_against", "routing_source", "task_lifecycle", "attempt_ordinal", "attempt_lifecycle", "harness", "model", "effort", "state", "worktree", "herdr",
	"age", "last_report", "pr", "reported", "report", "delivered", "send_id", "send_attempt", "send_state", "send_origin", "send_reason", "held", "gate", "flags", "report_file",
}

var holdFields = []axi.Column[state.Hold]{
	{Name: "id", Value: func(h state.Hold) string { return h.ID }},
	{Name: "kind", Value: func(h state.Hold) string { return h.Kind }},
	{Name: "detail", Value: func(h state.Hold) string { return holdDetail(h) }},
	{Name: "age", Value: func(h state.Hold) string { return formatAge(h.SetAt) }},
}
