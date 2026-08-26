package cmd

import (
	"fmt"
	"sort"

	"github.com/atqamz/hand/internal/attention"
	"github.com/atqamz/hand/internal/state"
)

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
	nextActionRuntimeUnknown   = "runtime-unknown"
	nextActionUnreachable      = "unreachable"
	nextActionParked           = "parked"
	nextActionUnacknowledged   = "unacknowledged"
	nextActionHold             = "hold"
	nextActionGate             = "gate"
	nextActionNeedsProject     = "needs-project"
	nextActionQueued           = "queued"
	nextActionMonitor          = "monitor"
	nextActionIdle             = "idle"
)

// Ranks fleet conditions so an existing unresolved obligation always outranks fresh queued work.
// A ranking over cmd/statusview.go's unified attention definition, not a second one
// (atqamz/hand#268): every predicate below is the same one needsAttention calls.
func classifyNextAction(cfg workerConfig, projectCount int, backlog backlogSummary, views []taskView, holds []state.Hold) nextAction {
	if cfg.harness == "" {
		return nextAction{Kind: nextActionNeedsConfig, Command: "hand config set harness <name>", Reason: workerConfigHelp(cfg)[0]}
	}

	sorted := sortedByTaskID(views)

	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindNeedsRepair) }); ok {
		return nextAction{Kind: nextActionNeedsRepair, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "resolve its repair ambiguity")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindSendUncertain) }); ok {
		return nextAction{Kind: nextActionSendUncertain, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "confirm whether its uncertain send reached the worker")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindSendPartial) }); ok {
		return nextAction{Kind: nextActionSendPartial, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "confirm whether its send needs to be retried")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindReportDecision) }); ok {
		return nextAction{Kind: state.ReportNeedsDecision, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "answer the operator decision it is waiting on")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindReportFailed) }); ok {
		return nextAction{Kind: state.ReportFailed, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "investigate why it failed")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindReportUnreadable) }); ok {
		return nextAction{Kind: nextActionReportUnreadable, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "investigate its unreadable report")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindUnreported) }); ok {
		return nextAction{Kind: nextActionUnreported, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: fmt.Sprintf("Run `%s`; it stopped without reporting a terminal state", statusCommand(v.task.ID))}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindRuntimeUnknown) }); ok {
		return nextAction{Kind: nextActionRuntimeUnknown, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "investigate why its worker runtime is unknown")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindUnreachable) }); ok {
		return nextAction{Kind: nextActionUnreachable, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "investigate why its worker runtime is unreachable")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindParked) }); ok {
		return nextAction{Kind: nextActionParked, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "investigate why its worker has been silent")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindReportBlocked) }); ok {
		return nextAction{Kind: state.ReportBlocked, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "resolve what is blocking it")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindReportPaused) }); ok {
		return nextAction{Kind: state.ReportPaused, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "resume or resolve why it is paused")}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindUnacknowledged) }); ok {
		return nextAction{Kind: nextActionUnacknowledged, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "act on its unacknowledged worker event")}
	}
	if h, ok := firstOrphanHold(holds, views); ok {
		return nextAction{Kind: nextActionHold, Task: h.ID, Command: "none",
			Reason: "Operator or supervisor judgment is needed to resolve its active hold"}
	}
	if v, ok := firstView(sorted, func(v taskView) bool { return hasAttentionKind(v, attention.KindGate) }); ok {
		return nextAction{Kind: nextActionGate, Task: v.task.ID, Command: statusCommand(v.task.ID),
			Reason: statusReason(v.task.ID, "confirm its delivery gate status")}
	}
	if projectCount == 0 {
		return nextAction{Kind: nextActionNeedsProject, Command: "hand project add <source>",
			Reason: "Run `hand project add <source>` or `hand project create <name>` to register the first project"}
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

func firstOrphanHold(holds []state.Hold, views []taskView) (state.Hold, bool) {
	if len(holds) == 0 {
		return state.Hold{}, false
	}
	known := make(map[string]bool, len(views))
	for _, view := range views {
		known[view.task.ID] = true
	}
	sorted := make([]state.Hold, len(holds))
	copy(sorted, holds)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, hold := range sorted {
		if !known[hold.ID] {
			return hold, true
		}
	}
	return state.Hold{}, false
}
