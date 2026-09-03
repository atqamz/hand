package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectLegacyV18CutoverRecoveryFrozenBridgeWithMaterializedTempIsPublishReady(t *testing.T) {
	home, bridge, archive, artifact, _, materialized := canonicalV19CutoverRecoveryFixture(t)
	before := recoveryEvidenceDigests(t, Path(home), archive.Path, artifact.Path, materialized.Path)

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryPublishCanonicalTemp {
		t.Fatalf("recovery disposition = %q (%s), want %q", state.Disposition, state.Reason, legacyV18CutoverRecoveryPublishCanonicalTemp)
	}
	if state.MigrationID != bridge.MigrationID || state.FleetID != bridge.FleetID || state.SourceSHA256 != bridge.SourceSHA256 || state.BridgeSHA256 != bridge.BridgeSHA256 {
		t.Fatalf("recovery bridge evidence = %#v, want %#v", state, bridge)
	}
	if state.Manifest != artifact || state.Materialized != materialized {
		t.Fatalf("recovery publication evidence = %#v; manifest=%#v materialized=%#v", state, artifact, materialized)
	}
	after := recoveryEvidenceDigests(t, Path(home), archive.Path, artifact.Path, materialized.Path)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("read-only recovery classifier changed evidence: before=%v after=%v", before, after)
	}
}

func TestInspectLegacyV18CutoverRecoveryValidCanonicalActiveOutranksBrokenLegacyEvidence(t *testing.T) {
	home, bridge, archive, artifact, target, _ := canonicalV19CutoverRecoveryFixture(t)
	if err := os.Remove(Path(home)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(target.Path, Path(home)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive.Path, []byte("broken stale archive evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path, []byte("broken stale manifest evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryCanonicalAuthority || state.FleetID != bridge.FleetID {
		t.Fatalf("canonical authority state = %#v", state)
	}
}

func TestInspectLegacyV18CutoverRecoveryCorruptCanonicalActiveNeverFallsBackToArchive(t *testing.T) {
	home, _, _, _, target, _ := canonicalV19CutoverRecoveryFixture(t)
	if err := os.Remove(Path(home)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(target.Path, Path(home)); err != nil {
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

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryRefuse || !strings.Contains(state.Reason, "cannot fall back to legacy evidence") {
		t.Fatalf("corrupt canonical recovery state = %#v", state)
	}
}

func TestInspectLegacyV18CutoverRecoveryFrozenBridgeRequiresMatchingArchive(t *testing.T) {
	home, bridge, archive, _, _ := canonicalV19CutoverMaterializationFixture(t)
	if err := os.WriteFile(archive.Path, []byte("not the frozen source\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryRefuse || state.MigrationID != bridge.MigrationID || !strings.Contains(state.Reason, "matching immutable original archive") {
		t.Fatalf("mismatched archive recovery state = %#v", state)
	}
}

func TestInspectLegacyV18CutoverRecoveryMissingActiveWithValidArchiveAndTempIsPublishReady(t *testing.T) {
	home, _, _, artifact, _, materialized := canonicalV19CutoverRecoveryFixture(t)
	if err := os.Remove(Path(home)); err != nil {
		t.Fatal(err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryPublishCanonicalTemp || state.Manifest != artifact || state.Materialized != materialized {
		t.Fatalf("missing-active publication recovery state = %#v", state)
	}
}

func TestInspectLegacyV18CutoverRecoveryMissingActiveWithArchiveAndNoTempRequiresRebuild(t *testing.T) {
	home, bridge, _, artifact, target := canonicalV19CutoverMaterializationFixture(t)
	if err := os.Remove(Path(home)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target.Path); err != nil {
		t.Fatal(err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryRebuildCanonicalTemp || state.MigrationID != bridge.MigrationID || state.Manifest != artifact || !strings.Contains(state.Reason, "absent") {
		t.Fatalf("missing temp recovery state = %#v", state)
	}
}

func TestInspectLegacyV18CutoverRecoveryMissingActiveWithCorruptDirectTempRequiresRebuild(t *testing.T) {
	home, bridge, _, artifact, target := canonicalV19CutoverMaterializationFixture(t)
	if err := os.Remove(Path(home)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target.Path, []byte("corrupt non-authoritative temp\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryRebuildCanonicalTemp || state.MigrationID != bridge.MigrationID || state.Manifest != artifact || !strings.Contains(state.Reason, "must be rebuilt") {
		t.Fatalf("corrupt temp recovery state = %#v", state)
	}
}

func TestInspectLegacyV18CutoverRecoveryRefusesUnsafeTempPath(t *testing.T) {
	home, _, _, _, target := canonicalV19CutoverMaterializationFixture(t)
	if err := os.Remove(Path(home)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target.Path); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other.db")
	if err := os.WriteFile(other, []byte("other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, target.Path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryRefuse || !strings.Contains(state.Reason, "not a direct regular file") {
		t.Fatalf("unsafe temp recovery state = %#v", state)
	}
}

func TestInspectLegacyV18CutoverRecoveryExactLegacySourceOutranksLooseCandidate(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	loose := filepath.Join(Dir(home), ".v19-cutover-v1-"+strings.Repeat("a", 64)+"-original.db.candidate")
	if err := os.WriteFile(loose, []byte("non-authoritative candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryLegacySource {
		t.Fatalf("legacy source recovery state = %#v", state)
	}
}

func TestInspectLegacyV18CutoverRecoveryMissingActiveWithOnlyLooseCandidateRefuses(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(Dir(home), ".v19-cutover-v1-"+strings.Repeat("a", 64)+"-original.db.candidate")
	if err := os.WriteFile(loose, []byte("non-authoritative candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryRefuse || !strings.Contains(state.Reason, "non-authoritative") {
		t.Fatalf("loose candidate recovery state = %#v", state)
	}
}

func TestInspectLegacyV18CutoverRecoveryMalformedFrozenSentinelRefuses(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	db, err := openLegacyV18CutoverSQLite(Path(home), "rw", legacyV18CutoverGateTimeout, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 22`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryRefuse || !strings.Contains(state.Reason, "frozen bridge") {
		t.Fatalf("malformed frozen sentinel recovery state = %#v", state)
	}
}

func TestInspectLegacyV18CutoverRecoveryNoState(t *testing.T) {
	home := t.TempDir()
	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryNoState {
		t.Fatalf("empty recovery state = %#v", state)
	}
}

func canonicalV19CutoverRecoveryFixture(t *testing.T) (string, legacyV18CutoverFrozenBridge, legacyV18CutoverOriginalArchive, legacyV18CutoverManifestArtifact, canonicalV19CutoverTarget, canonicalV19CutoverMaterialization) {
	t.Helper()
	home, bridge, archive, artifact, target := canonicalV19CutoverMaterializationFixture(t)
	materialized, err := materializeCanonicalV19CutoverTarget(home, bridge, artifact, target)
	if err != nil {
		t.Fatal(err)
	}
	return home, bridge, archive, artifact, target, materialized
}

func recoveryEvidenceDigests(t *testing.T, paths ...string) []string {
	t.Helper()
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		digest, err := legacyV18CutoverFileSHA256(path)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, digest)
	}
	return out
}
