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
