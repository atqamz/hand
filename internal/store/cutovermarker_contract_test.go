package store

import (
	"strings"
	"testing"
)

func TestLegacyV18CutoverMarkerDoesNotChangeRecoveryAuthority(t *testing.T) {
	fixture := newLegacyV18CutoverMarkerFixture(t)
	if _, err := writeLegacyV18CutoverMarker(fixture.home, fixture.input(legacyV18CutoverMarkerArchiveCandidate)); err != nil {
		t.Fatal(err)
	}
	state, err := inspectLegacyV18CutoverRecovery(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryNoState {
		t.Fatalf("marker-only recovery disposition = %s (%s)", state.Disposition, state.Reason)
	}

	marker, _, err := readLegacyV18CutoverMarker(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	marker.Target.AuthorityCommit = strings.Repeat("0", 40)
	if err := validateLegacyV18CutoverMarker(fixture.home, marker); err == nil || !strings.Contains(err.Error(), "exact current #344 authority") {
		t.Fatalf("target-contract mismatch error = %v", err)
	}
}
