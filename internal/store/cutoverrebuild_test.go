package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRebuildCanonicalV19CutoverTempFromArchiveAfterBridgeRetirement(t *testing.T) {
	home, bridge, archive, artifact, _, materialized := canonicalV19CutoverRecoveryFixture(t)
	before := recoveryEvidenceDigests(t, archive.Path, artifact.Path)
	if err := os.Remove(materialized.Path); err != nil {
		t.Fatal(err)
	}
	retiredPath := legacyV18CutoverRetiredBridgePath(home, bridge.MigrationID)
	if err := moveLegacyV18CutoverNoReplaceDurable(Path(home), retiredPath); err != nil {
		t.Fatal(err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryRebuildCanonicalTemp {
		t.Fatalf("pre-rebuild recovery state = %#v", state)
	}

	rebuilt, err := rebuildCanonicalV19CutoverTemp(home)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.MigrationID != bridge.MigrationID || rebuilt.ManifestSHA256 != artifact.SHA256 || rebuilt.ProjectCount != materialized.ProjectCount || rebuilt.ImportID != materialized.ImportID {
		t.Fatalf("rebuilt materialization = %#v, want identity from %#v", rebuilt, materialized)
	}
	afterState, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if afterState.Disposition != legacyV18CutoverRecoveryPublishCanonicalTemp || afterState.Materialized != rebuilt {
		t.Fatalf("post-rebuild recovery state = %#v", afterState)
	}
	after := recoveryEvidenceDigests(t, archive.Path, artifact.Path)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("rebuild changed authoritative archive evidence: before=%v after=%v", before, after)
	}
	retiredDigest, err := legacyV18CutoverFileSHA256(retiredPath)
	if err != nil {
		t.Fatal(err)
	}
	if retiredDigest != bridge.BridgeSHA256 {
		t.Fatalf("retired bridge digest=%s, want %s", retiredDigest, bridge.BridgeSHA256)
	}
}

func TestRebuildCanonicalV19CutoverTempDiscardsCorruptDirectResidue(t *testing.T) {
	home, _, archive, artifact, _, materialized := canonicalV19CutoverRecoveryFixture(t)
	before := recoveryEvidenceDigests(t, archive.Path, artifact.Path)
	if err := os.Remove(Path(home)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(materialized.Path, []byte("corrupt non-authoritative canonical temp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	journalPath := materialized.Path + "-journal"
	if err := os.WriteFile(journalPath, []byte("crash residue\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryRebuildCanonicalTemp {
		t.Fatalf("corrupt-temp recovery state = %#v", state)
	}

	rebuilt, err := rebuildCanonicalV19CutoverTemp(home)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Path != materialized.Path || rebuilt.ManifestSHA256 != artifact.SHA256 {
		t.Fatalf("rebuilt materialization = %#v", rebuilt)
	}
	if _, err := os.Lstat(journalPath); !os.IsNotExist(err) {
		t.Fatalf("journal residue remains after rebuild: %v", err)
	}
	finalState, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if finalState.Disposition != legacyV18CutoverRecoveryPublishCanonicalTemp || finalState.Materialized != rebuilt {
		t.Fatalf("post-rebuild state = %#v", finalState)
	}
	after := recoveryEvidenceDigests(t, archive.Path, artifact.Path)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("corrupt-temp rebuild changed authoritative evidence: before=%v after=%v", before, after)
	}
}

func TestRebuildCanonicalV19CutoverTempRefusesUnsafePath(t *testing.T) {
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

	if _, err := rebuildCanonicalV19CutoverTemp(home); err == nil || !strings.Contains(err.Error(), "recovery disposition=refuse") {
		t.Fatalf("unsafe temp rebuild error = %v", err)
	}
	info, err := os.Lstat(target.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("unsafe temp path was replaced")
	}
}

func TestRebuildCanonicalV19CutoverTempRequiresMigrationLock(t *testing.T) {
	home, _, _, _, target := canonicalV19CutoverMaterializationFixture(t)
	if err := os.Remove(Path(home)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target.Path); err != nil {
		t.Fatal(err)
	}
	release, err := Lock(home, MigrationLock, true)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if _, err := rebuildCanonicalV19CutoverTemp(home); err == nil || !strings.Contains(err.Error(), "acquire MigrationLock") {
		t.Fatalf("contended rebuild error = %v", err)
	}
}
