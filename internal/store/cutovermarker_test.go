package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type legacyV18CutoverMarkerFixtureState struct {
	home         string
	fleetID      string
	sourceSHA256 string
	migrationID  string
	candidate    legacyV18CutoverArchiveCandidate
	archive      legacyV18CutoverOriginalArchive
	bridge       legacyV18CutoverFrozenBridge
	manifest     legacyV18CutoverManifestArtifact
	materialized canonicalV19CutoverMaterialization
	publication  canonicalV19CutoverPublication
}

func newLegacyV18CutoverMarkerFixture(t *testing.T) legacyV18CutoverMarkerFixtureState {
	t.Helper()
	home := t.TempDir()
	fleetID := "f_" + strings.Repeat("a", 32)
	sourceSHA256 := strings.Repeat("b", 64)
	migrationID, err := legacyV18CutoverMigrationIdentity(fleetID, sourceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	candidate := legacyV18CutoverArchiveCandidate{
		MigrationID: migrationID,
		Path:        legacyV18CutoverArchiveCandidatePath(home, migrationID),
		SHA256:      sourceSHA256,
	}
	archive := legacyV18CutoverOriginalArchive{
		MigrationID: migrationID,
		Directory:   legacyV18CutoverOriginalArchiveDir(home, migrationID),
		Path:        legacyV18CutoverOriginalArchivePath(home, migrationID),
		SHA256:      sourceSHA256,
	}
	bridge := legacyV18CutoverFrozenBridge{
		MigrationID:  migrationID,
		FleetID:      fleetID,
		SourceSHA256: sourceSHA256,
		BridgeSHA256: strings.Repeat("c", 64),
		Certificate:  legacyV18CutoverFreezeCertificateVersion + ":" + sourceSHA256,
		Committed:    true,
	}
	manifest := legacyV18CutoverManifestArtifact{
		MigrationID: migrationID,
		Path:        legacyV18CutoverManifestPath(home, migrationID),
		SHA256:      strings.Repeat("d", 64),
		ImportedAt:  "2026-09-04T00:00:00Z",
	}
	materialized := canonicalV19CutoverMaterialization{
		MigrationID:    migrationID,
		Path:           legacyV18CutoverCanonicalTargetPath(home, migrationID),
		SHA256:         strings.Repeat("e", 64),
		ManifestSHA256: manifest.SHA256,
		ImportID:       "li_" + strings.Repeat("f", 32),
		ProjectCount:   2,
	}
	publication := canonicalV19CutoverPublication{
		MigrationID:       migrationID,
		FleetID:           fleetID,
		SourceSHA256:      sourceSHA256,
		ManifestSHA256:    manifest.SHA256,
		TargetSHA256:      materialized.SHA256,
		ImportID:          materialized.ImportID,
		ProjectCount:      materialized.ProjectCount,
		RetiredBridgePath: legacyV18CutoverRetiredBridgePath(home, migrationID),
	}
	return legacyV18CutoverMarkerFixtureState{
		home:         home,
		fleetID:      fleetID,
		sourceSHA256: sourceSHA256,
		migrationID:  migrationID,
		candidate:    candidate,
		archive:      archive,
		bridge:       bridge,
		manifest:     manifest,
		materialized: materialized,
		publication:  publication,
	}
}

func (f legacyV18CutoverMarkerFixtureState) input(phase legacyV18CutoverMarkerPhase) legacyV18CutoverMarkerInput {
	input := legacyV18CutoverMarkerInput{
		Phase:        phase,
		MigrationID:  f.migrationID,
		FleetID:      f.fleetID,
		SourceSHA256: f.sourceSHA256,
	}
	switch phase {
	case legacyV18CutoverMarkerArchiveCandidate:
		input.ArchiveCandidate = &f.candidate
	case legacyV18CutoverMarkerOriginalArchived:
		input.OriginalArchive = &f.archive
	case legacyV18CutoverMarkerFrozenBridge:
		input.OriginalArchive = &f.archive
		input.FrozenBridge = &f.bridge
		input.Manifest = &f.manifest
	case legacyV18CutoverMarkerCanonicalTemp:
		input.OriginalArchive = &f.archive
		input.FrozenBridge = &f.bridge
		input.Manifest = &f.manifest
		input.Materialized = &f.materialized
	case legacyV18CutoverMarkerCanonicalPublished:
		input.OriginalArchive = &f.archive
		input.FrozenBridge = &f.bridge
		input.Manifest = &f.manifest
		input.Publication = &f.publication
	}
	return input
}

func TestBuildLegacyV18CutoverMarkerProjectsExactEvidence(t *testing.T) {
	fixture := newLegacyV18CutoverMarkerFixture(t)
	marker, err := buildLegacyV18CutoverMarker(fixture.home, fixture.input(legacyV18CutoverMarkerCanonicalTemp))
	if err != nil {
		t.Fatal(err)
	}
	if marker.FormatVersion != legacyV18CutoverMarkerVersion || marker.Phase != legacyV18CutoverMarkerCanonicalTemp || marker.MigrationID != fixture.migrationID || marker.FleetID != fixture.fleetID || marker.SourceSHA256 != fixture.sourceSHA256 {
		t.Fatalf("marker identity = %#v", marker)
	}
	if marker.Paths.ArchiveCandidate != filepath.ToSlash(filepath.Join("state", filepath.Base(fixture.candidate.Path))) {
		t.Fatalf("archive candidate path = %q", marker.Paths.ArchiveCandidate)
	}
	if marker.Paths.OriginalArchive != filepath.ToSlash(filepath.Join("state", "v19-cutover-"+fixture.migrationID, "hand.db")) || marker.Paths.Manifest != filepath.ToSlash(filepath.Join("state", "v19-cutover-"+fixture.migrationID, legacyV18CutoverManifestFileName)) {
		t.Fatalf("archive paths = %#v", marker.Paths)
	}
	if marker.Paths.FrozenBridge != "state/hand.db" || marker.Paths.CanonicalTarget != "state/hand.db" || !strings.HasPrefix(marker.Paths.CanonicalTemp, "state/") {
		t.Fatalf("active/temp paths = %#v", marker.Paths)
	}
	for _, path := range []string{marker.Paths.ArchiveCandidate, marker.Paths.OriginalArchive, marker.Paths.FrozenBridge, marker.Paths.RetiredFrozenBridge, marker.Paths.Manifest, marker.Paths.CanonicalTemp, marker.Paths.CanonicalTarget} {
		if filepath.IsAbs(path) || strings.Contains(path, fixture.home) || strings.Contains(path, "..") {
			t.Fatalf("marker path is not Fleet-relative: %q", path)
		}
	}
	if marker.Evidence.ArchiveCandidateSHA256 != fixture.sourceSHA256 || marker.Evidence.OriginalArchiveSHA256 != fixture.sourceSHA256 || marker.Evidence.FrozenBridgeSHA256 != fixture.bridge.BridgeSHA256 || marker.Evidence.ManifestSHA256 != fixture.manifest.SHA256 || marker.Evidence.CanonicalTempSHA256 != fixture.materialized.SHA256 || marker.Evidence.CanonicalTargetSHA256 != "" || marker.Evidence.ImportID != fixture.materialized.ImportID || marker.Evidence.ProjectCount != fixture.materialized.ProjectCount {
		t.Fatalf("marker evidence = %#v", marker.Evidence)
	}
	if marker.Target != exactLegacyV18CutoverMarkerTarget() || marker.Target.AuthorityCommit != canonicalV19AuthorityCommit || marker.Target.DDLSHA256 != canonicalV19DDLSHA256 || marker.Target.SchemaFingerprint != canonicalV19SchemaFingerprint {
		t.Fatalf("marker target = %#v", marker.Target)
	}
}

func TestWriteLegacyV18CutoverMarkerAdvancesDurably(t *testing.T) {
	fixture := newLegacyV18CutoverMarkerFixture(t)
	phases := []legacyV18CutoverMarkerPhase{
		legacyV18CutoverMarkerArchiveCandidate,
		legacyV18CutoverMarkerOriginalArchived,
		legacyV18CutoverMarkerFrozenBridge,
		legacyV18CutoverMarkerCanonicalTemp,
		legacyV18CutoverMarkerCanonicalPublished,
	}
	var previousDigest string
	for _, phase := range phases {
		artifact, err := writeLegacyV18CutoverMarker(fixture.home, fixture.input(phase))
		if err != nil {
			t.Fatalf("write phase %s: %v", phase, err)
		}
		if artifact.Path != legacyV18CutoverMarkerPath(fixture.home) || artifact.MigrationID != fixture.migrationID || artifact.Phase != phase {
			t.Fatalf("artifact for %s = %#v", phase, artifact)
		}
		if artifact.SHA256 == previousDigest {
			t.Fatalf("phase %s did not change deterministic marker digest", phase)
		}
		previousDigest = artifact.SHA256
		marker, readArtifact, err := readLegacyV18CutoverMarker(fixture.home)
		if err != nil {
			t.Fatalf("read phase %s: %v", phase, err)
		}
		if marker.Phase != phase || readArtifact.SHA256 != artifact.SHA256 {
			t.Fatalf("read phase %s marker=%#v artifact=%#v", phase, marker, readArtifact)
		}
		if _, err := os.Lstat(legacyV18CutoverMarkerCandidatePath(fixture.home)); !os.IsNotExist(err) {
			t.Fatalf("marker candidate after phase %s: %v", phase, err)
		}
	}
	payload, err := os.ReadFile(legacyV18CutoverMarkerPath(fixture.home))
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) == 0 || payload[len(payload)-1] != '\n' || strings.Contains(string(payload), fixture.home) {
		t.Fatalf("marker payload is not canonical Fleet-relative JSON: %q", payload)
	}
}

func TestWriteLegacyV18CutoverMarkerRejectsPhaseRegression(t *testing.T) {
	fixture := newLegacyV18CutoverMarkerFixture(t)
	if _, err := writeLegacyV18CutoverMarker(fixture.home, fixture.input(legacyV18CutoverMarkerCanonicalTemp)); err != nil {
		t.Fatal(err)
	}
	if _, err := writeLegacyV18CutoverMarker(fixture.home, fixture.input(legacyV18CutoverMarkerFrozenBridge)); err == nil || !strings.Contains(err.Error(), "phase regression") {
		t.Fatalf("regression error = %v", err)
	}
	marker, _, err := readLegacyV18CutoverMarker(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	if marker.Phase != legacyV18CutoverMarkerCanonicalTemp {
		t.Fatalf("marker phase after refused regression = %s", marker.Phase)
	}
}

func TestWriteLegacyV18CutoverMarkerRepairsCorruptAdvisoryBytes(t *testing.T) {
	fixture := newLegacyV18CutoverMarkerFixture(t)
	if err := os.MkdirAll(Dir(fixture.home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyV18CutoverMarkerPath(fixture.home), []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := writeLegacyV18CutoverMarker(fixture.home, fixture.input(legacyV18CutoverMarkerArchiveCandidate))
	if err != nil {
		t.Fatal(err)
	}
	marker, readArtifact, err := readLegacyV18CutoverMarker(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	if marker.Phase != legacyV18CutoverMarkerArchiveCandidate || readArtifact.SHA256 != artifact.SHA256 {
		t.Fatalf("repaired marker=%#v artifact=%#v", marker, readArtifact)
	}
}

func TestReadLegacyV18CutoverMarkerRejectsNonCanonicalJSON(t *testing.T) {
	fixture := newLegacyV18CutoverMarkerFixture(t)
	marker, err := buildLegacyV18CutoverMarker(fixture.home, fixture.input(legacyV18CutoverMarkerArchiveCandidate))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	if err := os.MkdirAll(Dir(fixture.home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyV18CutoverMarkerPath(fixture.home), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readLegacyV18CutoverMarker(fixture.home); err == nil || !strings.Contains(err.Error(), "canonical deterministic JSON") {
		t.Fatalf("noncanonical marker error = %v", err)
	}
}

func TestBuildLegacyV18CutoverMarkerRejectsEvidenceMismatch(t *testing.T) {
	fixture := newLegacyV18CutoverMarkerFixture(t)
	input := fixture.input(legacyV18CutoverMarkerCanonicalTemp)
	bad := *input.Materialized
	bad.ManifestSHA256 = strings.Repeat("0", 64)
	input.Materialized = &bad
	if _, err := buildLegacyV18CutoverMarker(fixture.home, input); err == nil || !strings.Contains(err.Error(), "manifest digest is not exact") {
		t.Fatalf("mismatched materialization error = %v", err)
	}
}

func TestLegacyV18CutoverMarkerIsAdvisoryToRecoveryClassifier(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{legacyV18CutoverMarkerPath(home), legacyV18CutoverMarkerCandidatePath(home)} {
		if err := os.WriteFile(path, []byte("corrupt advisory marker\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if isLegacyV18CutoverLooseArtifact(filepath.Base(path)) {
			t.Fatalf("advisory marker path %q is incorrectly classified as authority-like loose evidence", path)
		}
	}
	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != legacyV18CutoverRecoveryNoState {
		t.Fatalf("recovery disposition with marker-only state = %s (%s)", state.Disposition, state.Reason)
	}
}
