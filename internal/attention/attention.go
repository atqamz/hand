package attention

import "fmt"

const (
	KindReportUnreadable = "report-unreadable"
	KindUnacknowledged   = "unacknowledged"
	KindUnannounced      = "unannounced"
	KindUnreported       = "unreported"
	KindRuntimeUnknown   = "runtime-unknown"
	KindUnreachable      = "unreachable"
	KindParked           = "parked"
	KindNeedsRepair      = "needs-repair"
	KindSendUncertain    = "send-uncertain"
	KindSendPartial      = "send-partial"
	KindReportPaused     = "paused"
	KindReportBlocked    = "blocked"
	KindReportDecision   = "needs-decision"
	KindReportFailed     = "failed"
	KindReportDone       = "report-done"
	KindGate             = "gate"
	KindSendPending      = "send-pending"
)

const (
	ProvenanceReport          = "report"
	ProvenanceAcknowledgement = "acknowledgement"
	ProvenanceWake            = "wake"
	ProvenanceEvidence        = "evidence"
	ProvenanceRuntime         = "runtime"
	ProvenanceRepair          = "repair"
	ProvenanceSend            = "send"
	ProvenanceGate            = "gate"
)

type Evidence struct {
	ID               string
	ReportUnreadable bool
	Unacknowledged   bool
	Unannounced      bool
	Unreported       bool
	RuntimeUnknown   bool
	Unreachable      bool
	Parked           bool
	Repair           bool
	SendUncertain    bool
	SendPending      bool
	SendPartial      bool
	Held             bool
	ReportedState    string
	ReportClaim      bool
	GateProblem      string
}

type Subject struct {
	Kind       string
	Reason     string
	Provenance string
	Actionable bool
}

func Derive(e Evidence) []Subject {
	var subjects []Subject
	add := func(kind, reason, provenance string, actionable bool) {
		if e.Held {
			actionable = false
		}
		subjects = append(subjects, Subject{Kind: kind, Reason: reason, Provenance: provenance, Actionable: actionable})
	}
	if e.ReportUnreadable {
		add(KindReportUnreadable, "report channel is unreadable", ProvenanceReport, true)
	}
	if e.Unacknowledged {
		add(KindUnacknowledged, "worker report is not acknowledged", ProvenanceAcknowledgement, true)
	}
	if e.Unannounced {
		add(KindUnannounced, "terminal report is not announced by a watcher", ProvenanceWake, false)
	}
	if e.Unreported {
		add(KindUnreported, "worker stopped without a terminal report", ProvenanceRuntime, true)
	}
	if e.RuntimeUnknown {
		add(KindRuntimeUnknown, "runtime state is unknown", ProvenanceEvidence, true)
	}
	if e.Unreachable {
		add(KindUnreachable, "worker runtime is unreachable", ProvenanceRuntime, true)
	}
	if e.Parked {
		add(KindParked, "worker has exceeded its report silence bound", ProvenanceRuntime, true)
	}
	if e.Repair {
		add(KindNeedsRepair, "task needs repair", ProvenanceRepair, true)
	}
	if e.SendUncertain {
		add(KindSendUncertain, "send outcome is uncertain", ProvenanceSend, true)
	}
	if e.SendPending {
		add(KindSendPending, "send is still in flight", ProvenanceSend, true)
	}
	if e.SendPartial {
		add(KindSendPartial, "send may have been partially delivered", ProvenanceSend, true)
	}
	if e.ReportClaim {
		switch e.ReportedState {
		case "done":
			add(KindReportDone, "worker claims completion; independent evidence is separate", ProvenanceReport, false)
		case "paused":
			add(KindReportPaused, "worker reports paused", ProvenanceReport, true)
		case "blocked":
			add(KindReportBlocked, "worker reports blocked", ProvenanceReport, true)
		case "needs-decision":
			add(KindReportDecision, "worker requests an operator decision", ProvenanceReport, true)
		case "failed":
			add(KindReportFailed, "worker reports failure", ProvenanceReport, true)
		}
	}
	if e.GateProblem != "" {
		add(KindGate, e.GateProblem, ProvenanceGate, true)
	}
	return subjects
}

func NeedsAttention(e Evidence) bool {
	for _, subject := range Derive(e) {
		if subject.Actionable {
			return true
		}
	}
	return false
}

func UnreportedRuntime(runtimeState, reportedState string) bool {
	return NeedsAttention(Evidence{Unreported: (runtimeState == "idle" || runtimeState == "done") && (reportedState == "" || reportedState == "working")})
}

func (s Subject) String() string {
	if s.Reason == "" {
		return s.Kind
	}
	return fmt.Sprintf("%s: %s", s.Kind, s.Reason)
}
