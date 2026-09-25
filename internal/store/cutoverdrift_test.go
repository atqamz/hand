package store

import (
	"strings"
	"testing"
)

// #348 revision 4 "Drift gate": the baseline is the certificate-bound manifest plus a plan from the original archive.
func TestInspectLegacyV18CutoverFrozenEvidenceReadsCertificateBoundBaseline(t *testing.T) {
	home, bridge, _, _, _ := canonicalV19CutoverMaterializationFixture(t)
	frozen, err := InspectLegacyV18CutoverFrozenEvidence(home)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Plan.FleetID != bridge.FleetID || len(frozen.Projects) != 2 {
		t.Fatalf("frozen evidence = %#v", frozen)
	}
	alpha := frozen.Projects[0]
	if alpha.SourceProjectID != "p_00000000000000000000000000000001" || alpha.Locator != "projects/alpha" || alpha.LegacyName != "alpha" ||
		alpha.RepositoryPhysicalID != "unix-v2:ino=0000000000000002:btime=1.000000000" || alpha.Revision != strings.Repeat("a", 40) {
		t.Fatalf("frozen alpha Project = %#v", alpha)
	}

	downgradeLegacyV18CutoverBridgeToV1(t, home, bridge.SourceSHA256)
	if _, err := InspectLegacyV18CutoverFrozenEvidence(home); err == nil || !strings.Contains(err.Error(), "carries no committed boot evidence") {
		t.Fatalf("v1 bridge frozen evidence = %v, want refusal", err)
	}
}
