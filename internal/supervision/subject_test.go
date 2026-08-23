package supervision

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/orientation"
)

func actionable(id, generation, kind string) orientation.ActionableEvidence {
	return orientation.ActionableEvidence{
		TargetID: id, TargetKind: "task", Generation: []string{generation},
		Kind: kind, Reason: "r", Provenance: "report",
	}
}

func TestEpisodeKeySeparatesExactCurrentnessAndAction(t *testing.T) {
	base := Episode{FleetID: "f_1", TargetID: "t1", TargetKind: "task", Currentness: currentnessOf("f_1", "g1"), Kind: "blocked"}
	same := base
	if base.Key() != same.Key() || base.Key() == "" {
		t.Fatal("identical episodes must share one key")
	}
	nextCurrentness := base
	nextCurrentness.Currentness = currentnessOf("f_1", "g2")
	if base.Key() == nextCurrentness.Key() {
		t.Fatal("changed currentness is a new episode")
	}
	otherAction := base
	otherAction.Kind = "needs-decision"
	if base.Key() == otherAction.Key() {
		t.Fatal("different actionable kind is a different episode")
	}
	otherTarget := base
	otherTarget.TargetID = "t2"
	if base.Key() == otherTarget.Key() {
		t.Fatal("a different target is a different episode")
	}
}

func currentnessOf(fleetID, generation string) orientation.CurrentnessToken {
	return orientation.TargetFor(fleetID, orientation.TargetEvidence{ID: "x", Kind: "task", Generation: []string{generation}}).Currentness
}

// The 65th genuinely new subject behind a full 64-item rendered slice is the
// starvation this seam exists to prevent: eligibility derives from unbounded
// evidence, so every subject produces an episode.
func TestFromEvidenceExceedsOrientationDisplayBound(t *testing.T) {
	evidence := orientation.Evidence{FleetID: "f_1"}
	for i := range 100 {
		evidence.Actionable = append(evidence.Actionable,
			actionable(strings.Repeat("t", 1)+string(rune('a'+i/26))+string(rune('a'+i%26)), "gen", "kind"))
	}
	episodes := FromEvidence(evidence)
	if len(episodes) != 100 {
		t.Fatalf("episodes = %d, want every unbounded subject represented (100)", len(episodes))
	}
	bounded := orientation.Build(evidence)
	if len(bounded.Actionable) > 64 {
		t.Fatalf("rendered orientation = %d items, want the display bound intact", len(bounded.Actionable))
	}
}

func TestFromEvidenceCoalescesDuplicateKeys(t *testing.T) {
	evidence := orientation.Evidence{
		FleetID:    "f_1",
		Actionable: []orientation.ActionableEvidence{actionable("t1", "g1", "blocked"), actionable("t1", "g1", "blocked")},
	}
	episodes := FromEvidence(evidence)
	if len(episodes) != 1 {
		t.Fatalf("episodes = %d, want duplicates coalesced into one", len(episodes))
	}
}

func TestWakeTextIsBoundedMechanismInput(t *testing.T) {
	text := WakeText("f_abc")
	if !strings.Contains(text, "f_abc") || !strings.Contains(text, "`hand orient`") {
		t.Fatalf("text = %q, want fleet identity and the orient instruction", text)
	}
	huge := strings.Repeat("F", 500)
	fleet := huge + "tail-dropped"
	text = WakeText(fleet)
	if runes := len([]rune(text)); runes > maxWakeReason {
		t.Fatalf("text = %d runes, want bounded at %d", runes, maxWakeReason)
	}
}
