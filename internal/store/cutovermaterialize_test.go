package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestMaterializeCanonicalV19CutoverTargetWritesOnlyPositiveEvidence(t *testing.T) {
	home, bridge, archive, artifact, target := canonicalV19CutoverMaterializationFixture(t)
	archiveBefore, err := legacyV18CutoverFileSHA256(archive.Path)
	if err != nil {
		t.Fatal(err)
	}
	manifestBefore, err := legacyV18CutoverFileSHA256(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}

	result, err := materializeCanonicalV19CutoverTarget(home, bridge, artifact, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.MigrationID != bridge.MigrationID || result.Path != target.Path || result.ManifestSHA256 != artifact.SHA256 || result.ProjectCount != 2 || result.ImportID == "" || result.SHA256 == "" {
		t.Fatalf("materialization = %#v", result)
	}
	if got, err := legacyV18CutoverFileSHA256(target.Path); err != nil || got != result.SHA256 {
		t.Fatalf("materialized target digest = %s, %v; want %s", got, err, result.SHA256)
	}

	manifest, err := readLegacyV18CutoverManifest(home, artifact)
	if err != nil {
		t.Fatal(err)
	}
	plan := buildCanonicalV19CutoverImportPlan(manifest, artifact.SHA256)
	if result.ImportID != plan.ImportID {
		t.Fatalf("materialized import ID = %s, want %s", result.ImportID, plan.ImportID)
	}
	for i, project := range plan.Projects {
		if len(project.ProjectID) != len("p_")+32 || !strings.HasPrefix(project.ProjectID, "p_") {
			t.Fatalf("canonical Project ID = %q, want p_<32hex>", project.ProjectID)
		}
		if project.ProjectID == manifest.Projects[i].SourceProjectID {
			t.Fatalf("canonical Project ID reused legacy source identity %q", project.ProjectID)
		}
	}

	db, err := open(target.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCanonicalV19Schema(db); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := validateCanonicalV19CutoverImportRows(db, plan); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	for _, table := range []string{"task", "plan", "attempt", "attempt_worktree_binding", "session_binding", "executor_binding", "worker_input", "worker_input_acknowledgement", "worker_wake_operation", "worker_report", "external_operation", "terminal_receipt", "artifact"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + quoteCanonicalV19Identifier(table)).Scan(&count); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
		if count != 0 {
			_ = db.Close()
			t.Fatalf("fabricated %s rows = %d, want 0", table, count)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	archiveAfter, err := legacyV18CutoverFileSHA256(archive.Path)
	if err != nil {
		t.Fatal(err)
	}
	manifestAfter, err := legacyV18CutoverFileSHA256(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if archiveAfter != archiveBefore || archiveAfter != bridge.SourceSHA256 {
		t.Fatalf("original archive changed during canonical materialization: before=%s after=%s", archiveBefore, archiveAfter)
	}
	if manifestAfter != manifestBefore || manifestAfter != artifact.SHA256 {
		t.Fatalf("manifest changed during canonical materialization: before=%s after=%s", manifestBefore, manifestAfter)
	}
	frozenDB, err := openLegacyV18CutoverSQLite(Path(home), "ro", legacyV18CutoverGateTimeout, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = frozenDB.Close() }()
	if err := validateLegacyV18CutoverFrozenBridge(frozenDB, bridge.FleetID, bridge.SourceSHA256); err != nil {
		t.Fatalf("active source stopped being the exact frozen bridge: %v", err)
	}
}

func TestMaterializeCanonicalV19CutoverTargetRefusesUncommittedFreezeWithoutRows(t *testing.T) {
	home, bridge, _, artifact, target := canonicalV19CutoverMaterializationFixture(t)
	bridge.Committed = false
	if _, err := materializeCanonicalV19CutoverTarget(home, bridge, artifact, target); err == nil || !strings.Contains(err.Error(), "source freeze is not committed") {
		t.Fatalf("uncommitted freeze error = %v", err)
	}
	assertCanonicalV19CutoverTargetEmpty(t, target)
}

func TestMaterializeCanonicalV19CutoverTargetRefusesBridgeDigestDriftWithoutRows(t *testing.T) {
	home, bridge, _, artifact, target := canonicalV19CutoverMaterializationFixture(t)
	bridge.BridgeSHA256 = strings.Repeat("b", 64)
	if _, err := materializeCanonicalV19CutoverTarget(home, bridge, artifact, target); err == nil || !strings.Contains(err.Error(), "frozen bridge digest") {
		t.Fatalf("bridge digest drift error = %v", err)
	}
	assertCanonicalV19CutoverTargetEmpty(t, target)
}

func TestMaterializeCanonicalV19CutoverTargetRefusesManifestTargetDriftWithoutRows(t *testing.T) {
	home, bridge, _, artifact, target := canonicalV19CutoverMaterializationFixture(t)
	payload, err := os.ReadFile(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest legacyV18CutoverManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Target.DDLSHA256 = strings.Repeat("b", 64)
	payload, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(artifact.Path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	artifact.SHA256 = canonicalV19SHA256(payload)
	if _, err := materializeCanonicalV19CutoverTarget(home, bridge, artifact, target); err == nil || !strings.Contains(err.Error(), "target identity does not match locked #344") {
		t.Fatalf("manifest target drift error = %v", err)
	}
	assertCanonicalV19CutoverTargetEmpty(t, target)
}

func TestMaterializeCanonicalV19CutoverTargetRefusesNonFreshTarget(t *testing.T) {
	home, bridge, _, artifact, target := canonicalV19CutoverMaterializationFixture(t)
	db, err := open(target.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO fleet(singleton,fleet_id,created_at) VALUES(1,'other','now')`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := materializeCanonicalV19CutoverTarget(home, bridge, artifact, target); err == nil || !strings.Contains(err.Error(), "existing data rows") {
		t.Fatalf("non-fresh target error = %v", err)
	}
}

func TestCanonicalV19CutoverProjectIdentityExcludesLegacyLabelsAndPolicy(t *testing.T) {
	home, _, _, artifact, _ := canonicalV19CutoverMaterializationFixture(t)
	manifest, err := readLegacyV18CutoverManifest(home, artifact)
	if err != nil {
		t.Fatal(err)
	}
	first := buildCanonicalV19CutoverImportPlan(manifest, artifact.SHA256)
	changed := manifest
	changed.Projects = append([]legacyV18CutoverManifestProject(nil), manifest.Projects...)
	changed.Projects[0].SourceProjectID = "different-source-id"
	changed.Projects[0].DisplayName = "different-display-label"
	changed.Projects[0].PolicyInputSHA256 = strings.Repeat("c", 64)
	second := buildCanonicalV19CutoverImportPlan(changed, artifact.SHA256)
	if first.Projects[0].ProjectID != second.Projects[0].ProjectID {
		t.Fatalf("canonical Project identity changed with legacy label/source/policy: %s vs %s", first.Projects[0].ProjectID, second.Projects[0].ProjectID)
	}
	if first.Projects[0].WorkspaceBindingID != second.Projects[0].WorkspaceBindingID {
		t.Fatalf("WorkspaceBinding identity changed with legacy label/source/policy: %s vs %s", first.Projects[0].WorkspaceBindingID, second.Projects[0].WorkspaceBindingID)
	}
	if first.Projects[0].PolicyRevisionID == second.Projects[0].PolicyRevisionID {
		t.Fatal("PolicyRevision identity did not change with exact policy digest")
	}
}

func canonicalV19CutoverMaterializationFixture(t *testing.T) (string, legacyV18CutoverFrozenBridge, legacyV18CutoverOriginalArchive, legacyV18CutoverManifestArtifact, canonicalV19CutoverTarget) {
	t.Helper()
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := promoteLegacyV18CutoverArchiveCandidate(home, gate.archiveCandidate)
	if err != nil {
		_ = gate.Close()
		t.Fatal(err)
	}
	bridge, err := freezeLegacyV18CutoverSource(context.Background(), home, gate, archive)
	if err != nil {
		_ = gate.Close()
		t.Fatal(err)
	}
	if err := gate.Close(); err != nil {
		t.Fatal(err)
	}
	input := LegacyV18CutoverManifestInput{
		FleetID:    bridge.FleetID,
		ImportedAt: "2026-09-03T15:59:00.123456789Z",
		Projects: []LegacyV18CutoverManifestProjectInput{
			{
				SourceProjectID:      "p_00000000000000000000000000000002",
				Locator:              "projects/beta",
				RepositoryPhysicalID: "unix-v1:dev=0000000000000002:ino=0000000000000002",
				CommonDirPhysicalID:  "unix-v1:dev=0000000000000002:ino=0000000000000003",
				Revision:             strings.Repeat("b", 40),
				LegacyName:           "beta",
				LegacyURL:            "https://example.invalid/beta.git",
				LegacyMode:           "clone",
				LegacyUpstream:       "origin/main",
			},
			{
				SourceProjectID:      "p_00000000000000000000000000000001",
				Locator:              "projects/alpha",
				RepositoryPhysicalID: "unix-v1:dev=0000000000000001:ino=0000000000000002",
				CommonDirPhysicalID:  "unix-v1:dev=0000000000000001:ino=0000000000000003",
				Revision:             strings.Repeat("a", 40),
				LegacyName:           "alpha",
				LegacyURL:            "https://user:secret@example.invalid/alpha.git",
				LegacyMode:           "clone",
				LegacyUpstream:       "origin/main",
			},
		},
	}
	artifact, err := writeLegacyV18CutoverManifest(home, archive, input)
	if err != nil {
		t.Fatal(err)
	}
	target, err := prepareCanonicalV19CutoverTarget(home, bridge.MigrationID)
	if err != nil {
		t.Fatal(err)
	}
	return home, bridge, archive, artifact, target
}

func assertCanonicalV19CutoverTargetEmpty(t *testing.T, target canonicalV19CutoverTarget) {
	t.Helper()
	db, err := open(target.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := validateCanonicalV19Schema(db); err != nil {
		t.Fatal(err)
	}
	rows, err := canonicalV19CutoverTotalDataRows(db)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("canonical target rows after refusal = %d, want 0", rows)
	}
}
