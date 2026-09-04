package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestLegacyV18CutoverGuardFreezeLeavesRecoverableFrozenBridge(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	guard, err := AcquireLegacyV18CutoverGuard(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := guard.ObservationPlan()
	if err != nil {
		_ = guard.Close()
		t.Fatal(err)
	}
	input := LegacyV18CutoverManifestInput{
		FleetID:    plan.FleetID,
		ImportedAt: "2026-09-04T04:20:00.123456789Z",
		Projects:   []LegacyV18CutoverManifestProjectInput{},
	}

	if err := guard.Freeze(context.Background(), home, input); err != nil {
		_ = guard.Close()
		t.Fatal(err)
	}
	if _, err := guard.ObservationPlan(); !errors.Is(err, ErrLegacyV18CutoverGuardClosed) {
		_ = guard.Close()
		t.Fatalf("ObservationPlan after freeze = %v, want %v", err, ErrLegacyV18CutoverGuardClosed)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryRebuildCanonicalTemp {
		t.Fatalf("post-freeze recovery state = %#v", state)
	}
	if state.FleetID != plan.FleetID || state.Manifest.ImportedAt != input.ImportedAt {
		t.Fatalf("post-freeze recovery identity = %#v", state)
	}
	if _, err := os.Lstat(state.Manifest.Path); err != nil {
		t.Fatalf("durable pre-freeze manifest: %v", err)
	}
	bridge, err := discoverLegacyV18CutoverFrozenBridge(home)
	if err != nil {
		t.Fatal(err)
	}
	if bridge.MigrationID != state.MigrationID || bridge.SourceSHA256 != state.SourceSHA256 {
		t.Fatalf("bridge=%#v recovery=%#v", bridge, state)
	}
}

func TestLegacyV18CutoverGuardFreezeReusesExactManifestAcrossPrefreezeRetry(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	firstGuard, err := AcquireLegacyV18CutoverGuard(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	firstPlan, err := firstGuard.ObservationPlan()
	if err != nil {
		_ = firstGuard.Close()
		t.Fatal(err)
	}
	archive, err := promoteLegacyV18CutoverArchiveCandidate(home, firstGuard.gate.archiveCandidate)
	if err != nil {
		_ = firstGuard.Close()
		t.Fatal(err)
	}
	firstInput := LegacyV18CutoverManifestInput{
		FleetID:    firstPlan.FleetID,
		ImportedAt: "2026-09-04T04:21:00Z",
		Projects:   []LegacyV18CutoverManifestProjectInput{},
	}
	firstArtifact, err := writeLegacyV18CutoverManifest(home, archive, firstInput)
	if err != nil {
		_ = firstGuard.Close()
		t.Fatal(err)
	}
	if err := firstGuard.Close(); err != nil {
		t.Fatal(err)
	}

	legacyBefore, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	secondGuard, err := AcquireLegacyV18CutoverGuard(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	secondPlan, err := secondGuard.ObservationPlan()
	if err != nil {
		_ = secondGuard.Close()
		t.Fatal(err)
	}
	secondInput := LegacyV18CutoverManifestInput{
		FleetID:    secondPlan.FleetID,
		ImportedAt: "2026-09-04T04:22:00Z",
		Projects:   []LegacyV18CutoverManifestProjectInput{},
	}
	if err := secondGuard.Freeze(context.Background(), home, secondInput); err != nil {
		_ = secondGuard.Close()
		t.Fatal(err)
	}
	if err := secondGuard.Close(); err != nil {
		t.Fatal(err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryRebuildCanonicalTemp || state.Manifest != firstArtifact {
		t.Fatalf("retry recovery state = %#v, first manifest = %#v", state, firstArtifact)
	}
	archiveDigest, err := legacyV18CutoverFileSHA256(legacyV18CutoverOriginalArchivePath(home, state.MigrationID))
	if err != nil {
		t.Fatal(err)
	}
	if archiveDigest != legacyBefore || state.SourceSHA256 != legacyBefore {
		t.Fatalf("retry changed original evidence: archive=%s recovery=%s original=%s", archiveDigest, state.SourceSHA256, legacyBefore)
	}
}

func TestLegacyV18CutoverGuardFreezeRejectsFabricatedProjectEvidenceBeforeSourceMutation(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	before, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireLegacyV18CutoverGuard(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := guard.ObservationPlan()
	if err != nil {
		_ = guard.Close()
		t.Fatal(err)
	}
	input := LegacyV18CutoverManifestInput{
		FleetID:    plan.FleetID,
		ImportedAt: "2026-09-04T04:23:00Z",
		Projects: []LegacyV18CutoverManifestProjectInput{{
			SourceProjectID:      "fabricated-project",
			Locator:              "projects/fabricated",
			RepositoryPhysicalID: "unix-v1:dev=1:ino=1",
			CommonDirPhysicalID:  "unix-v1:dev=1:ino=2",
			Revision:             strings.Repeat("a", 40),
			LegacyName:           "fabricated",
		}},
	}
	if err := guard.Freeze(context.Background(), home, input); err == nil || !strings.Contains(err.Error(), "Project evidence count") {
		_ = guard.Close()
		t.Fatalf("fabricated Project freeze error = %v", err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("rejected fabricated evidence changed source: before=%s after=%s", before, after)
	}
	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryLegacySource {
		t.Fatalf("rejected fabricated evidence recovery state = %#v", state)
	}
}

func TestLegacyV18CutoverGuardFreezeRequiresHeldGuard(t *testing.T) {
	var guard *LegacyV18CutoverGuard
	if err := guard.Freeze(context.Background(), t.TempDir(), LegacyV18CutoverManifestInput{}); !errors.Is(err, ErrLegacyV18CutoverGuardClosed) {
		t.Fatalf("nil guard Freeze = %v, want %v", err, ErrLegacyV18CutoverGuardClosed)
	}
}
