package store

import (
	"os"
	"strings"
	"testing"
)

func TestPublishCanonicalV19CutoverRetiresFrozenBridgeAndPublishesExactTemp(t *testing.T) {
	home, bridge, archive, artifact, _, materialized := canonicalV19CutoverPublicationFixture(t)
	archiveBefore, err := legacyV18CutoverFileSHA256(archive.Path)
	if err != nil {
		t.Fatal(err)
	}
	manifestBefore, err := legacyV18CutoverFileSHA256(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}

	publication, err := publishCanonicalV19Cutover(home)
	if err != nil {
		t.Fatal(err)
	}
	if publication.MigrationID != bridge.MigrationID || publication.FleetID != bridge.FleetID || publication.SourceSHA256 != bridge.SourceSHA256 || publication.ManifestSHA256 != artifact.SHA256 || publication.TargetSHA256 != materialized.SHA256 || publication.ImportID != materialized.ImportID || publication.ProjectCount != materialized.ProjectCount {
		t.Fatalf("publication = %#v", publication)
	}
	if publication.RetiredBridgePath != legacyV18CutoverRetiredBridgePath(home, bridge.MigrationID) {
		t.Fatalf("retired bridge path = %s", publication.RetiredBridgePath)
	}
	if _, err := os.Lstat(materialized.Path); !os.IsNotExist(err) {
		t.Fatalf("canonical temp after publication: %v", err)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != materialized.SHA256 {
		t.Fatalf("published active digest = %s, %v; want %s", got, err, materialized.SHA256)
	}
	if got, err := legacyV18CutoverFileSHA256(publication.RetiredBridgePath); err != nil || got != bridge.BridgeSHA256 {
		t.Fatalf("retired bridge digest = %s, %v; want %s", got, err, bridge.BridgeSHA256)
	}
	retiredDB, err := openLegacyV18CutoverSQLite(publication.RetiredBridgePath, "ro", legacyV18CutoverGateTimeout, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLegacyV18CutoverFrozenBridge(retiredDB, bridge.FleetID, bridge.SourceSHA256); err != nil {
		_ = retiredDB.Close()
		t.Fatalf("retired bridge validation: %v", err)
	}
	if err := retiredDB.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := legacyV18CutoverFileSHA256(archive.Path); err != nil || got != archiveBefore {
		t.Fatalf("original archive changed: %s, %v; want %s", got, err, archiveBefore)
	}
	if got, err := legacyV18CutoverFileSHA256(artifact.Path); err != nil || got != manifestBefore {
		t.Fatalf("manifest changed: %s, %v; want %s", got, err, manifestBefore)
	}
	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryCanonicalAuthority || state.FleetID != bridge.FleetID {
		t.Fatalf("final recovery state = %#v", state)
	}
}

func TestPublishCanonicalV19CutoverResumesAfterFrozenBridgeAlreadyRetired(t *testing.T) {
	home, bridge, _, _, _, materialized := canonicalV19CutoverPublicationFixture(t)
	retiredPath := legacyV18CutoverRetiredBridgePath(home, bridge.MigrationID)
	if err := syncLegacyV18CutoverFile(Path(home)); err != nil {
		t.Fatal(err)
	}
	if err := moveLegacyV18CutoverNoReplaceDurable(Path(home), retiredPath); err != nil {
		t.Fatal(err)
	}
	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryPublishCanonicalTemp || state.BridgeSHA256 != "" {
		t.Fatalf("recovery after bridge retirement = %#v", state)
	}

	publication, err := publishCanonicalV19Cutover(home)
	if err != nil {
		t.Fatal(err)
	}
	if publication.TargetSHA256 != materialized.SHA256 || publication.RetiredBridgePath != retiredPath {
		t.Fatalf("resumed publication = %#v", publication)
	}
}

func TestPublishCanonicalV19CutoverAllowsActiveMissingWithExactArchiveAndTemp(t *testing.T) {
	home, _, _, _, _, materialized := canonicalV19CutoverPublicationFixture(t)
	if err := os.Remove(Path(home)); err != nil {
		t.Fatal(err)
	}
	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryPublishCanonicalTemp {
		t.Fatalf("active-missing recovery = %#v", state)
	}
	publication, err := publishCanonicalV19Cutover(home)
	if err != nil {
		t.Fatal(err)
	}
	if publication.TargetSHA256 != materialized.SHA256 {
		t.Fatalf("active-missing publication = %#v", publication)
	}
}

func TestPublishCanonicalV19CutoverRefusesInvalidTempBeforeRetiringBridge(t *testing.T) {
	home, bridge, _, _, target, _ := canonicalV19CutoverPublicationFixture(t)
	bridgeBefore, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target.Path, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := publishCanonicalV19Cutover(home); err == nil || !strings.Contains(err.Error(), "recovery disposition=rebuild-canonical-temp") {
		t.Fatalf("invalid temp publication error = %v", err)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != bridgeBefore || got != bridge.BridgeSHA256 {
		t.Fatalf("frozen bridge changed after invalid temp refusal: %s, %v", got, err)
	}
	if _, err := os.Lstat(legacyV18CutoverRetiredBridgePath(home, bridge.MigrationID)); !os.IsNotExist(err) {
		t.Fatalf("retired bridge unexpectedly exists after refusal: %v", err)
	}
}

func TestPublishCanonicalV19CutoverRefusesUnexpectedRetiredDestination(t *testing.T) {
	home, bridge, _, _, _, materialized := canonicalV19CutoverPublicationFixture(t)
	retiredPath := legacyV18CutoverRetiredBridgePath(home, bridge.MigrationID)
	if err := os.WriteFile(retiredPath, []byte("unexpected"), 0o600); err != nil {
		t.Fatal(err)
	}
	bridgeBefore, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishCanonicalV19Cutover(home); err == nil || !strings.Contains(err.Error(), "retire frozen bridge") {
		t.Fatalf("unexpected destination publication error = %v", err)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != bridgeBefore {
		t.Fatalf("active bridge changed after destination refusal: %s, %v", got, err)
	}
	if got, err := legacyV18CutoverFileSHA256(materialized.Path); err != nil || got != materialized.SHA256 {
		t.Fatalf("canonical temp changed after destination refusal: %s, %v", got, err)
	}
}

func TestPublishCanonicalV19CutoverRequiresMigrationLock(t *testing.T) {
	home, bridge, _, _, _, materialized := canonicalV19CutoverPublicationFixture(t)
	release, err := Lock(home, MigrationLock, true)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := publishCanonicalV19Cutover(home); err == nil || !strings.Contains(err.Error(), "MigrationLock") {
		t.Fatalf("concurrent publication error = %v", err)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != bridge.BridgeSHA256 {
		t.Fatalf("active bridge changed while MigrationLock busy: %s, %v", got, err)
	}
	if got, err := legacyV18CutoverFileSHA256(materialized.Path); err != nil || got != materialized.SHA256 {
		t.Fatalf("canonical temp changed while MigrationLock busy: %s, %v", got, err)
	}
}

func TestPublishCanonicalV19CutoverNeverOverwritesCanonicalAuthority(t *testing.T) {
	home, _, _, _, _, materialized := canonicalV19CutoverPublicationFixture(t)
	if _, err := publishCanonicalV19Cutover(home); err != nil {
		t.Fatal(err)
	}
	before, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishCanonicalV19Cutover(home); err == nil || !strings.Contains(err.Error(), "canonical-authority") {
		t.Fatalf("second publication error = %v", err)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != before || got != materialized.SHA256 {
		t.Fatalf("canonical authority changed on retry: %s, %v", got, err)
	}
}

func canonicalV19CutoverPublicationFixture(t *testing.T) (string, legacyV18CutoverFrozenBridge, legacyV18CutoverOriginalArchive, legacyV18CutoverManifestArtifact, canonicalV19CutoverTarget, canonicalV19CutoverMaterialization) {
	t.Helper()
	home, bridge, archive, artifact, target := canonicalV19CutoverMaterializationFixture(t)
	materialized, err := materializeCanonicalV19CutoverTarget(home, bridge, artifact, target)
	if err != nil {
		t.Fatal(err)
	}
	return home, bridge, archive, artifact, target, materialized
}
