package attention

import "testing"

func TestDerivePreservesEvidenceAndAcknowledgementProvenance(t *testing.T) {
	got := Derive(Evidence{
		ID:               "task-1",
		ReportUnreadable: true,
		Unacknowledged:   true,
		Unannounced:      true,
		ReportedState:    "needs-decision",
		ReportClaim:      true,
		Unreported:       true,
	})

	if len(got) != 5 {
		t.Fatalf("subjects = %#v, want five independent subjects", got)
	}
	if got[0].Kind != KindReportUnreadable || got[0].Provenance != ProvenanceReport {
		t.Fatalf("first subject = %#v, want unreadable report provenance", got[0])
	}
	if got[1].Kind != KindUnacknowledged || got[1].Provenance != ProvenanceAcknowledgement {
		t.Fatalf("second subject = %#v, want acknowledgement provenance", got[1])
	}
	if got[2].Kind != KindUnannounced || got[2].Provenance != ProvenanceWake {
		t.Fatalf("third subject = %#v, want watcher provenance", got[2])
	}
	if !NeedsAttention(Evidence{ID: "task-1", Unacknowledged: true}) {
		t.Fatal("unacknowledged report is not actionable")
	}
	if NeedsAttention(Evidence{ID: "task-1", Unannounced: true}) {
		t.Fatal("unannounced report alone is not supervisor acknowledgement attention")
	}
}

func TestDeriveIsDeterministicAndSeparatesUnknownRuntimeFromReportClaim(t *testing.T) {
	evidence := Evidence{
		ID:             "task-1",
		RuntimeUnknown: true,
		ReportedState:  "done",
		ReportClaim:    true,
	}
	got := Derive(evidence)
	if len(got) != 2 {
		t.Fatalf("subjects = %#v, want runtime unknown and report claim", got)
	}
	if got[0].Kind != KindRuntimeUnknown || got[0].Provenance != ProvenanceEvidence {
		t.Fatalf("runtime subject = %#v, want evidence provenance", got[0])
	}
	if got[1].Kind != KindReportDone || got[1].Provenance != ProvenanceReport {
		t.Fatalf("report subject = %#v, want report provenance", got[1])
	}
	want := got
	if again := Derive(evidence); len(again) != len(want) || again[0] != want[0] || again[1] != want[1] {
		t.Fatalf("second derivation = %#v, want deterministic %#v", again, want)
	}
}

func TestDeriveRetainsHeldSubjectsAsNotActionable(t *testing.T) {
	got := Derive(Evidence{ID: "task-1", Held: true, Unreported: true})
	if len(got) != 1 {
		t.Fatalf("subjects = %#v, want one held subject", got)
	}
	if got[0].Kind != KindUnreported || got[0].Reason != "worker stopped without a terminal report" {
		t.Fatalf("subject = %#v, want the underlying unreported condition", got[0])
	}
	if got[0].Actionable {
		t.Fatal("held subject is actionable")
	}
	if NeedsAttention(Evidence{ID: "task-1", Held: true, Unreported: true}) {
		t.Fatal("held subject needs attention")
	}
}

func TestDeriveRaisesSendNotSubmittedDistinctFromSendUncertain(t *testing.T) {
	got := Derive(Evidence{ID: "task-1", SendNotSubmitted: true})
	if len(got) != 1 {
		t.Fatalf("subjects = %#v, want one send-not-submitted subject", got)
	}
	if got[0].Kind != KindSendNotSubmitted || got[0].Provenance != ProvenanceSend {
		t.Fatalf("subject = %#v, want send-not-submitted evidence provenance", got[0])
	}
	if !got[0].Actionable {
		t.Fatal("send-not-submitted subject is not actionable")
	}
	if !NeedsAttention(Evidence{ID: "task-1", SendNotSubmitted: true}) {
		t.Fatal("send-not-submitted evidence needs attention")
	}
	uncertain := Derive(Evidence{ID: "task-1", SendUncertain: true})
	if uncertain[0].Kind == got[0].Kind {
		t.Fatalf("send-uncertain and send-not-submitted collapsed to the same kind %q", got[0].Kind)
	}
}

// atqamz/hand#417: a satisfied hold is actionable despite Held - the opposite of every other subject,
// which Held always masks.
func TestDeriveMakesASatisfiedHoldActionableDespiteHeld(t *testing.T) {
	got := Derive(Evidence{ID: "task-1", Held: true, HoldSatisfied: true, HoldBlockedOn: "blocker-task"})
	if len(got) != 1 {
		t.Fatalf("subjects = %#v, want exactly the hold-satisfied subject", got)
	}
	want := Subject{Kind: KindHoldSatisfied, Reason: "blocker-task is terminal; this hold can be cleared", Provenance: ProvenanceHold, Actionable: true}
	if got[0] != want {
		t.Fatalf("subject = %#v, want %#v", got[0], want)
	}
	if !NeedsAttention(Evidence{ID: "task-1", Held: true, HoldSatisfied: true, HoldBlockedOn: "blocker-task"}) {
		t.Fatal("satisfied hold does not need attention")
	}
}

func TestDeriveOmitsHoldSatisfiedWhenNotSet(t *testing.T) {
	got := Derive(Evidence{ID: "task-1", Held: true, Unreported: true})
	for _, subject := range got {
		if subject.Kind == KindHoldSatisfied {
			t.Fatalf("subjects = %#v, want no hold-satisfied subject", got)
		}
	}
}

// atqamz/hand#492: Parked alone is not enough to earn a supervisor's attention - the naive verdict
// still renders (Parked stays a subject either way), but only a corroborated one is actionable.
func TestDeriveRequiresParkedActionableForAttention(t *testing.T) {
	uncorroborated := Derive(Evidence{ID: "task-1", Parked: true})
	if len(uncorroborated) != 1 || uncorroborated[0].Kind != KindParked {
		t.Fatalf("subjects = %#v, want one uncorroborated parked subject", uncorroborated)
	}
	if uncorroborated[0].Actionable {
		t.Fatal("an uncorroborated park is actionable")
	}
	if NeedsAttention(Evidence{ID: "task-1", Parked: true}) {
		t.Fatal("an uncorroborated park needs attention")
	}

	corroborated := Derive(Evidence{ID: "task-1", Parked: true, ParkedActionable: true})
	if len(corroborated) != 1 || !corroborated[0].Actionable {
		t.Fatalf("subjects = %#v, want a corroborated, actionable parked subject", corroborated)
	}
	if !NeedsAttention(Evidence{ID: "task-1", Parked: true, ParkedActionable: true}) {
		t.Fatal("a corroborated park does not need attention")
	}
}

func TestUnreportedRuntimeMatchesWatcherCatchUpRule(t *testing.T) {
	for _, test := range []struct {
		runtime, reported string
		want              bool
	}{
		{runtime: "done", reported: "", want: true},
		{runtime: "idle", reported: "working", want: true},
		{runtime: "done", reported: "done", want: false},
		{runtime: "working", reported: "", want: false},
	} {
		if got := UnreportedRuntime(test.runtime, test.reported); got != test.want {
			t.Fatalf("UnreportedRuntime(%q, %q) = %t, want %t", test.runtime, test.reported, got, test.want)
		}
	}
}
