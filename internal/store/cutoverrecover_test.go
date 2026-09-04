package store

import (
	"os"
	"strings"
	"testing"
)

func TestRecoverCanonicalV19CutoverPublishesReadyTemp(t *testing.T) {
	home, bridge, archive, artifact, _, materialized := canonicalV19CutoverPublicationFixture(t)
	before := recoveryEvidenceDigests(t, archive.Path, artifact.Path)

	state, err := recoverCanonicalV19Cutover(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryCanonicalAuthority || state.FleetID != bridge.FleetID {
		t.Fatalf("final recovery state = %#v", state)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != materialized.SHA256 {
		t.Fatalf("published active digest = %s, %v; want %s", got, err, materialized.SHA256)
	}
	if _, err := os.Lstat(materialized.Path); !os.IsNotExist(err) {
		t.Fatalf("canonical temp remains after recovery publication: %v", err)
	}
	retiredPath := legacyV18CutoverRetiredBridgePath(home, bridge.MigrationID)
	if got, err := legacyV18CutoverFileSHA256(retiredPath); err != nil || got != bridge.BridgeSHA256 {
		t.Fatalf("retired bridge digest = %s, %v; want %s", got, err, bridge.BridgeSHA256)
	}
	after := recoveryEvidenceDigests(t, archive.Path, artifact.Path)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("recovery changed authoritative archive evidence: before=%v after=%v", before, after)
	}
}

func TestRecoverCanonicalV19CutoverRebuildsThenPublishesAfterBridgeRetirement(t *testing.T) {
	home, bridge, archive, artifact, _, materialized := canonicalV19CutoverPublicationFixture(t)
	before := recoveryEvidenceDigests(t, archive.Path, artifact.Path)
	if err := os.Remove(materialized.Path); err != nil {
		t.Fatal(err)
	}
	retiredPath := legacyV18CutoverRetiredBridgePath(home, bridge.MigrationID)
	if err := moveLegacyV18CutoverNoReplaceDurable(Path(home), retiredPath); err != nil {
		t.Fatal(err)
	}

	pre, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if pre.Disposition != legacyV18CutoverRecoveryRebuildCanonicalTemp {
		t.Fatalf("pre-recovery state = %#v", pre)
	}

	state, err := recoverCanonicalV19Cutover(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryCanonicalAuthority || state.FleetID != bridge.FleetID {
		t.Fatalf("final recovery state = %#v", state)
	}
	if got, err := legacyV18CutoverFileSHA256(retiredPath); err != nil || got != bridge.BridgeSHA256 {
		t.Fatalf("retired bridge changed during rebuild+publish: %s, %v", got, err)
	}
	if _, err := os.Lstat(materialized.Path); !os.IsNotExist(err) {
		t.Fatalf("rebuilt canonical temp remains after publication: %v", err)
	}
	after := recoveryEvidenceDigests(t, archive.Path, artifact.Path)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("rebuild+publish changed authoritative archive evidence: before=%v after=%v", before, after)
	}
}

func TestRecoverCanonicalV19CutoverDoesNotStartFreshCutover(t *testing.T) {
	t.Run("legacy source", func(t *testing.T) {
		home := createLegacyV18CutoverTestSource(t)
		setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
		before, err := legacyV18CutoverFileSHA256(Path(home))
		if err != nil {
			t.Fatal(err)
		}
		state, err := recoverCanonicalV19Cutover(home)
		if err != nil {
			t.Fatal(err)
		}
		if state.Disposition != legacyV18CutoverRecoveryLegacySource {
			t.Fatalf("legacy recovery state = %#v", state)
		}
		after, err := legacyV18CutoverFileSHA256(Path(home))
		if err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Fatalf("recovery driver mutated legacy source: before=%s after=%s", before, after)
		}
	})

	t.Run("no state", func(t *testing.T) {
		home := t.TempDir()
		state, err := recoverCanonicalV19Cutover(home)
		if err != nil {
			t.Fatal(err)
		}
		if state.Disposition != legacyV18CutoverRecoveryNoState {
			t.Fatalf("empty recovery state = %#v", state)
		}
		if _, err := os.Lstat(Path(home)); !os.IsNotExist(err) {
			t.Fatalf("recovery driver created active state: %v", err)
		}
	})
}

func TestRecoverCanonicalV19CutoverRefusesInvalidCanonicalAuthority(t *testing.T) {
	home, _, archive, artifact, _, _ := canonicalV19CutoverPublicationFixture(t)
	if _, err := publishCanonicalV19Cutover(home); err != nil {
		t.Fatal(err)
	}
	db, err := openLegacyV18CutoverSQLite(Path(home), "rw", legacyV18CutoverGateTimeout, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 18`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	activeBefore, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	evidenceBefore := recoveryEvidenceDigests(t, archive.Path, artifact.Path)

	state, err := recoverCanonicalV19Cutover(home)
	if err == nil || state.Disposition != legacyV18CutoverRecoveryRefuse || !strings.Contains(err.Error(), "cannot fall back to legacy evidence") {
		t.Fatalf("invalid canonical recovery = %#v, %v", state, err)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != activeBefore {
		t.Fatalf("invalid canonical active changed during refusal: %s, %v; want %s", got, err, activeBefore)
	}
	evidenceAfter := recoveryEvidenceDigests(t, archive.Path, artifact.Path)
	if strings.Join(evidenceBefore, "\n") != strings.Join(evidenceAfter, "\n") {
		t.Fatalf("refusal changed archive evidence: before=%v after=%v", evidenceBefore, evidenceAfter)
	}
}

func TestRecoverCanonicalV19CutoverPreservesMigrationLockSerialization(t *testing.T) {
	home, bridge, _, _, _, materialized := canonicalV19CutoverPublicationFixture(t)
	release, err := Lock(home, MigrationLock, true)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	state, err := recoverCanonicalV19Cutover(home)
	if err == nil || state.Disposition != legacyV18CutoverRecoveryPublishCanonicalTemp || !strings.Contains(err.Error(), "MigrationLock") {
		t.Fatalf("contended recovery = %#v, %v", state, err)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != bridge.BridgeSHA256 {
		t.Fatalf("active frozen bridge changed while lock busy: %s, %v", got, err)
	}
	if got, err := legacyV18CutoverFileSHA256(materialized.Path); err != nil || got != materialized.SHA256 {
		t.Fatalf("canonical temp changed while lock busy: %s, %v", got, err)
	}
}
