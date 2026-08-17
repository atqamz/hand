package cmd

import (
	"fmt"
	"sort"

	"github.com/atqamz/hand/internal/state"
)

// The one deterministic, machine-consumable classification hand session start reports:
// the highest-priority fleet condition that objectively requires attention before fresh work starts.
type nextAction struct {
	Kind    string
	Task    string
	Command string
	Reason  string
}

const (
	nextActionNeedsConfig      = "needs-config"
	nextActionNeedsRepair      = "needs-repair"
	nextActionSendUncertain    = "send-uncertain"
	nextActionSendPartial      = "send-partial"
	nextActionReportUnreadable = "report-unreadable"
	nextActionUnreported       = "unreported"
	nextActionUnacknowledged   = "unacknowledged"
	nextActionHold             = "hold"
	nextActionGate             = "gate"
	nextActionNeedsProject     = "needs-project"
	nextActionQueued           = "queued"
	nextActionMonitor          = "monitor"
	nextActionIdle             = "idle"
)

// Ranks fleet conditions so an existing unresolved obligation always outranks fresh queued work.
// atqamz/hand#234 owns the precedence table this sequence of guard clauses implements.
func classifyNextAction(cfg workerConfig, projectCount int, backlog backlogSummary, views []taskView, holds []state.Hold) nextAction {
	if cfg.harness == "" {
		return nextAction{Kind: nextActionNeedsConfig, Command: "hand config set harness <name>", Reason: workerConfigHelp(cfg)[0]}
	}

	sorted := sortedByTaskID(views)

	if v, ok := firstView(sorted, func(v taskView) bool { return v.task.RepairCode != "" }); ok {
		return nextAction{Kind: nextActionNeedsRepair, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "resolve its repair ambiguity")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return v.latestSend != nil && v.latestSend.State == state.SendUncertain }); ok {
		return nextAction{Kind: nextActionSendUncertain, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "confirm whether its uncertain send reached the worker")}
	}
	if v, ok := firstView(sorted, isSendPartial); ok {
		return nextAction{Kind: nextActionSendPartial, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "confirm whether its send needs to be retried")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return v.reportedState == state.ReportNeedsDecision }); ok {
		return nextAction{Kind: state.ReportNeedsDecision, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "answer the operator decision it is waiting on")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return v.reportedState == state.ReportFailed }); ok {
		return nextAction{Kind: state.ReportFailed, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "investigate why it failed")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return v.unreadable }); ok {
		return nextAction{Kind: nextActionReportUnreadable, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "investigate its unreadable report")}
	}
	if v, ok := firstView(sorted, unreportedStop); ok {
		return nextAction{Kind: nextActionUnreported, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: fmt.Sprintf("Run `%s`; it stopped without reporting a terminal state", statusCommand(v.task.ID))}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return v.reportedState == state.ReportBlocked }); ok {
		return nextAction{Kind: state.ReportBlocked, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "resolve what is blocking it")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return v.reportedState == state.ReportPaused }); ok {
		return nextAction{Kind: state.ReportPaused, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "resume or resolve why it is paused")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return v.unacked }); ok {
		return nextAction{Kind: nextActionUnacknowledged, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "act on its unacknowledged worker event")}
	}
	if h, ok := firstHold(holds); ok {
		return nextAction{Kind: nextActionHold, Task: h.ID, Command: statusCommand(h.ID),
			Reason: statusReason(h.ID, "resolve its active hold")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return v.gateIssue != "" }); ok {
		return nextAction{Kind: nextActionGate, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "confirm its delivery gate status")}
	}
	if projectCount == 0 {
		return nextAction{Kind: nextActionNeedsProject, Command: "hand project add <repo-url>",
			Reason: "Run `hand project add <repo-url>` to register the first project"}
	}
	if backlog.Queued > 0 {
		return nextAction{Kind: nextActionQueued, Command: "hand spawn <id> <project>",
			Reason: "Read `data/backlog.md` and prepare the queued task; dispatch it with `hand spawn <id> <project>` when its brief is ready"}
	}
	if len(sorted) > 0 {
		return nextAction{Kind: nextActionMonitor, Command: "hand watch --until-event",
			Reason: "Run `hand watch --until-event` as a background task; re-arm it after an event or when intentionally resuming after interruption or takeover"}
	}
	return nextAction{Kind: nextActionIdle, Reason: "The fleet is ready and idle"}
}

func statusCommand(id string) string { return fmt.Sprintf("hand status %s", id) }

func statusReason(id, action string) string {
	return fmt.Sprintf("Run `%s` and %s", statusCommand(id), action)
}

// SendPending is in-flight rather than an attention condition (mirrors taskFlags in statusview.go), and
// send-uncertain is classified separately above and always outranks send-partial, so excluding both here
// is what keeps this predicate scoped to exactly the remaining SendNeedsAttention case.
func isSendPartial(v taskView) bool {
	if v.latestSend == nil || v.latestSend.State == state.SendPending || v.latestSend.State == state.SendUncertain {
		return false
	}
	return state.SendNeedsAttention(*v.latestSend)
}

// Reconciliation history row order is not part of the store's contract, so every category above picks
// its first match from this byte-wise, task.ID-ascending copy - the fleet's entire tie-breaker.
func sortedByTaskID(views []taskView) []taskView {
	sorted := make([]taskView, len(views))
	copy(sorted, views)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].task.ID < sorted[j].task.ID })
	return sorted
}

func firstView(views []taskView, match func(taskView) bool) (taskView, bool) {
	for _, v := range views {
		if match(v) {
			return v, true
		}
	}
	return taskView{}, false
}

func firstHold(holds []state.Hold) (state.Hold, bool) {
	if len(holds) == 0 {
		return state.Hold{}, false
	}
	sorted := make([]state.Hold, len(holds))
	copy(sorted, holds)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	return sorted[0], true
}
