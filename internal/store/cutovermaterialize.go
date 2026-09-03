package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const canonicalV19CutoverProjectIDDomain = "hand:v19-cutover:canonical-project:v1"
const canonicalV19CutoverWorkspaceBindingIDDomain = "hand:v19-cutover:canonical-workspace-binding:v1"
const canonicalV19CutoverPolicyRevisionIDDomain = "hand:v19-cutover:canonical-policy-revision:v1"
const canonicalV19CutoverLegacyImportIDDomain = "hand:v19-cutover:canonical-legacy-import:v1"
const canonicalV19CutoverImportProjectEvidenceDomain = "hand:v19-cutover:canonical-import-project-evidence:v1"

type canonicalV19CutoverMaterialization struct {
	MigrationID   string
	Path          string
	SHA256        string
	ManifestSHA256 string
	ImportID      string
	ProjectCount  int
}

type canonicalV19CutoverImportPlan struct {
	MigrationID   string
	FleetID       string
	ImportedAt    string
	SourceSHA256  string
	ManifestSHA256 string
	ImportID      string
	Projects      []canonicalV19CutoverProjectImport
}

type canonicalV19CutoverProjectImport struct {
	SourceProjectID       string
	ProjectID             string
	WorkspaceBindingID    string
	PolicyRevisionID      string
	Ordinal                int
	DisplayName            string
	RepositoryLocator      string
	RepositoryIdentity     string
	CommonGitDir           string
	PhysicalIdentity       string
	Revision               string
	PolicyDigest           string
	EvidenceDigest         string
}

// Materializes only the positively evidenced cutover rows into an already fresh
// exact-#344 target. It never publishes the target or changes the active DB path.
func materializeCanonicalV19CutoverTarget(homeDir string, bridge legacyV18CutoverFrozenBridge, artifact legacyV18CutoverManifestArtifact, target canonicalV19CutoverTarget) (canonicalV19CutoverMaterialization, error) {
	manifest, err := validateCanonicalV19CutoverMaterializationEvidence(homeDir, bridge, artifact)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}
	if target.MigrationID != bridge.MigrationID {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("materialize canonical v19 cutover target: target migration identity=%s, frozen bridge=%s", target.MigrationID, bridge.MigrationID)
	}
	expectedTargetPath := legacyV18CutoverCanonicalTargetPath(homeDir, bridge.MigrationID)
	if filepath.Clean(target.Path) != filepath.Clean(expectedTargetPath) {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("materialize canonical v19 cutover target: target path=%s, want deterministic %s", target.Path, expectedTargetPath)
	}
	if err := requireLegacyV18CutoverDirectRegularFile(target.Path, "canonical target"); err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}

	plan := buildCanonicalV19CutoverImportPlan(manifest, artifact.SHA256)
	sqlDB, err := open(target.Path)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("materialize canonical v19 cutover target: open target: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = sqlDB.Close()
		}
	}()
	if err := validateCanonicalV19Schema(sqlDB); err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("materialize canonical v19 cutover target: validate fresh target: %w", err)
	}
	rows, err := canonicalV19CutoverTotalDataRows(sqlDB)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}
	if rows != 0 {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("materialize canonical v19 cutover target: target has %d existing data rows, want 0", rows)
	}
	if err := insertCanonicalV19CutoverImport(sqlDB, plan); err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}
	if err := validateCanonicalV19Schema(sqlDB); err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("materialize canonical v19 cutover target: validate committed target: %w", err)
	}
	if err := validateCanonicalV19CutoverImportRows(sqlDB, plan); err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}
	if err := sqlDB.Close(); err != nil {
		closed = true
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("materialize canonical v19 cutover target: close committed target: %w", err)
	}
	closed = true
	if err := syncCanonicalV19CutoverTarget(target.Path); err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}

	reopened, err := open(target.Path)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("materialize canonical v19 cutover target: reopen committed target: %w", err)
	}
	validationErr := validateCanonicalV19Schema(reopened)
	if validationErr == nil {
		validationErr = validateCanonicalV19CutoverImportRows(reopened, plan)
	}
	closeErr := reopened.Close()
	if validationErr != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("materialize canonical v19 cutover target: revalidate committed target: %w", validationErr)
	}
	if closeErr != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("materialize canonical v19 cutover target: close revalidated target: %w", closeErr)
	}
	if err := syncCanonicalV19CutoverTarget(target.Path); err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}
	digest, err := legacyV18CutoverFileSHA256(target.Path)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("materialize canonical v19 cutover target: hash committed target: %w", err)
	}
	return canonicalV19CutoverMaterialization{
		MigrationID:    bridge.MigrationID,
		Path:           target.Path,
		SHA256:         digest,
		ManifestSHA256: artifact.SHA256,
		ImportID:       plan.ImportID,
		ProjectCount:   len(plan.Projects),
	}, nil
}

func validateCanonicalV19CutoverMaterializationEvidence(homeDir string, bridge legacyV18CutoverFrozenBridge, artifact legacyV18CutoverManifestArtifact) (legacyV18CutoverManifest, error) {
	if !bridge.Committed {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: source freeze is not committed")
	}
	if err := validateLegacyV18CutoverMigrationID(bridge.MigrationID); err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: %w", err)
	}
	if err := validateFleetID(bridge.FleetID); err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: Fleet ID: %w", err)
	}
	if err := validateLegacyV18CutoverSHA256(bridge.SourceSHA256); err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: source digest: %w", err)
	}
	if err := validateLegacyV18CutoverSHA256(bridge.BridgeSHA256); err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: frozen bridge digest: %w", err)
	}
	expectedMigrationID, err := legacyV18CutoverMigrationIdentity(bridge.FleetID, bridge.SourceSHA256)
	if err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: derive migration identity: %w", err)
	}
	if bridge.MigrationID != expectedMigrationID {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: migration identity=%s, want %s", bridge.MigrationID, expectedMigrationID)
	}
	expectedCertificate := legacyV18CutoverFreezeCertificateVersion + ":" + bridge.SourceSHA256
	if bridge.Certificate != expectedCertificate {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: frozen bridge certificate=%q, want %q", bridge.Certificate, expectedCertificate)
	}
	if artifact.MigrationID != bridge.MigrationID {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: manifest migration identity=%s, bridge=%s", artifact.MigrationID, bridge.MigrationID)
	}

	if err := requireLegacyV18CutoverDirectRegularFile(Path(homeDir), "frozen bridge"); err != nil {
		return legacyV18CutoverManifest{}, err
	}
	beforeDigest, err := legacyV18CutoverFileSHA256(Path(homeDir))
	if err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: hash frozen bridge: %w", err)
	}
	if beforeDigest != bridge.BridgeSHA256 {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: frozen bridge digest=%s, want %s", beforeDigest, bridge.BridgeSHA256)
	}
	frozenDB, err := openLegacyV18CutoverSQLite(Path(homeDir), "ro", legacyV18CutoverGateTimeout, true)
	if err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: open frozen bridge: %w", err)
	}
	validationErr := validateLegacyV18CutoverFrozenBridge(frozenDB, bridge.FleetID, bridge.SourceSHA256)
	closeErr := frozenDB.Close()
	if validationErr != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: validate frozen bridge: %w", validationErr)
	}
	if closeErr != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: close frozen bridge: %w", closeErr)
	}
	afterDigest, err := legacyV18CutoverFileSHA256(Path(homeDir))
	if err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: rehash frozen bridge: %w", err)
	}
	if afterDigest != bridge.BridgeSHA256 {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: frozen bridge changed during validation: got %s, want %s", afterDigest, bridge.BridgeSHA256)
	}

	archivePath := legacyV18CutoverOriginalArchivePath(homeDir, bridge.MigrationID)
	if err := requireLegacyV18CutoverDirectRegularFile(archivePath, "original archive"); err != nil {
		return legacyV18CutoverManifest{}, err
	}
	archiveDigest, err := legacyV18CutoverFileSHA256(archivePath)
	if err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: hash original archive: %w", err)
	}
	if archiveDigest != bridge.SourceSHA256 {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: original archive digest=%s, want source %s", archiveDigest, bridge.SourceSHA256)
	}

	manifest, err := readLegacyV18CutoverManifest(homeDir, artifact)
	if err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: %w", err)
	}
	if manifest.Fleet.FleetID != bridge.FleetID || manifest.Source.DBSHA256 != bridge.SourceSHA256 || manifest.Freeze.CertificateValue != bridge.Certificate {
		return legacyV18CutoverManifest{}, fmt.Errorf("materialize canonical v19 cutover target: manifest does not bind the exact frozen source evidence")
	}
	return manifest, nil
}

func buildCanonicalV19CutoverImportPlan(manifest legacyV18CutoverManifest, manifestSHA256 string) canonicalV19CutoverImportPlan {
	plan := canonicalV19CutoverImportPlan{
		MigrationID:    manifest.MigrationID,
		FleetID:        manifest.Fleet.FleetID,
		ImportedAt:     manifest.ImportedAt,
		SourceSHA256:   manifest.Source.DBSHA256,
		ManifestSHA256: manifestSHA256,
	}
	plan.ImportID = canonicalV19CutoverOpaqueID("li_", canonicalV19CutoverLegacyImportIDDomain, manifest.MigrationID, manifestSHA256)
	plan.Projects = make([]canonicalV19CutoverProjectImport, 0, len(manifest.Projects))
	for i, project := range manifest.Projects {
		projectID := canonicalV19CutoverOpaqueID(
			"p_",
			canonicalV19CutoverProjectIDDomain,
			manifest.MigrationID,
			project.RepositoryLocator,
			project.RepositoryIdentitySHA256,
			project.CommonGitDir,
			project.PhysicalIdentitySHA256,
		)
		workspaceID := canonicalV19CutoverOpaqueID(
			"wb_",
			canonicalV19CutoverWorkspaceBindingIDDomain,
			projectID,
			project.RepositoryLocator,
			project.RepositoryIdentitySHA256,
			project.CommonGitDir,
			project.PhysicalIdentitySHA256,
			project.Revision,
		)
		policyID := canonicalV19CutoverOpaqueID("pr_", canonicalV19CutoverPolicyRevisionIDDomain, projectID, project.PolicyInputSHA256)
		importProject := canonicalV19CutoverProjectImport{
			SourceProjectID:    project.SourceProjectID,
			ProjectID:          projectID,
			WorkspaceBindingID: workspaceID,
			PolicyRevisionID:   policyID,
			Ordinal:            i + 1,
			DisplayName:        project.DisplayName,
			RepositoryLocator:  project.RepositoryLocator,
			RepositoryIdentity: project.RepositoryIdentitySHA256,
			CommonGitDir:       project.CommonGitDir,
			PhysicalIdentity:   project.PhysicalIdentitySHA256,
			Revision:           project.Revision,
			PolicyDigest:       project.PolicyInputSHA256,
		}
		importProject.EvidenceDigest = canonicalV19CutoverDigest(
			canonicalV19CutoverImportProjectEvidenceDomain,
			manifestSHA256,
			project.SourceProjectID,
			projectID,
			workspaceID,
			policyID,
			project.RepositoryLocator,
			project.RepositoryIdentitySHA256,
			project.CommonGitDir,
			project.PhysicalIdentitySHA256,
			project.Revision,
			project.PolicyInputSHA256,
		)
		plan.Projects = append(plan.Projects, importProject)
	}
	return plan
}

func canonicalV19CutoverOpaqueID(prefix, domain string, parts ...string) string {
	return prefix + canonicalV19CutoverDigest(domain, parts...)[:32]
}

func canonicalV19CutoverDigest(domain string, parts ...string) string {
	var payload strings.Builder
	payload.WriteString(domain)
	for _, part := range parts {
		fmt.Fprintf(&payload, "\x00%d:", len(part))
		payload.WriteString(part)
	}
	return canonicalV19SHA256([]byte(payload.String()))
}

func insertCanonicalV19CutoverImport(sqlDB *sql.DB, plan canonicalV19CutoverImportPlan) error {
	tx, err := sqlDB.Begin()
	if err != nil {
		return fmt.Errorf("materialize canonical v19 cutover target: begin import transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(`INSERT INTO fleet(singleton,fleet_id,created_at) VALUES(1,?,?)`, plan.FleetID, plan.ImportedAt); err != nil {
		return fmt.Errorf("materialize canonical v19 cutover target: insert Fleet: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO legacy_import(id,fleet_id,source_user_version,source_db_sha256,archive_db_sha256,manifest_sha256,target_ddl_sha256,target_schema_fingerprint,imported_at) VALUES(?,?,18,?,?,?,?,?,?)`,
		plan.ImportID, plan.FleetID, plan.SourceSHA256, plan.SourceSHA256, plan.ManifestSHA256, canonicalV19DDLSHA256, canonicalV19SchemaFingerprint, plan.ImportedAt); err != nil {
		return fmt.Errorf("materialize canonical v19 cutover target: insert LegacyImport: %w", err)
	}
	for _, project := range plan.Projects {
		if _, err := tx.Exec(`INSERT INTO project(id,fleet_id,ordinal,display_name,created_at) VALUES(?,?,?,?,?)`, project.ProjectID, plan.FleetID, project.Ordinal, project.DisplayName, plan.ImportedAt); err != nil {
			return fmt.Errorf("materialize canonical v19 cutover target: insert Project %q: %w", project.SourceProjectID, err)
		}
		if _, err := tx.Exec(`INSERT INTO workspace_binding(id,project_id,ordinal,repository_locator,repository_identity_digest,common_git_dir,physical_identity_digest,revision,established_at) VALUES(?,?,1,?,?,?,?,?,?)`,
			project.WorkspaceBindingID, project.ProjectID, project.RepositoryLocator, project.RepositoryIdentity, project.CommonGitDir, project.PhysicalIdentity, project.Revision, plan.ImportedAt); err != nil {
			return fmt.Errorf("materialize canonical v19 cutover target: insert WorkspaceBinding %q: %w", project.SourceProjectID, err)
		}
		if _, err := tx.Exec(`INSERT INTO policy_revision(id,project_id,ordinal,policy_digest,created_at) VALUES(?,?,1,?,?)`, project.PolicyRevisionID, project.ProjectID, project.PolicyDigest, plan.ImportedAt); err != nil {
			return fmt.Errorf("materialize canonical v19 cutover target: insert PolicyRevision %q: %w", project.SourceProjectID, err)
		}
		if _, err := tx.Exec(`INSERT INTO legacy_import_project(import_id,project_id,workspace_binding_id,policy_revision_id,evidence_digest) VALUES(?,?,?,?,?)`,
			plan.ImportID, project.ProjectID, project.WorkspaceBindingID, project.PolicyRevisionID, project.EvidenceDigest); err != nil {
			return fmt.Errorf("materialize canonical v19 cutover target: insert LegacyImportProject %q: %w", project.SourceProjectID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("materialize canonical v19 cutover target: commit import transaction: %w", err)
	}
	committed = true
	return nil
}

func validateCanonicalV19CutoverImportRows(sqlDB *sql.DB, plan canonicalV19CutoverImportPlan) error {
	var fleetID, fleetCreatedAt string
	if err := sqlDB.QueryRow(`SELECT fleet_id,created_at FROM fleet WHERE singleton=1`).Scan(&fleetID, &fleetCreatedAt); err != nil {
		return fmt.Errorf("validate canonical v19 cutover import: read Fleet: %w", err)
	}
	if fleetID != plan.FleetID || fleetCreatedAt != plan.ImportedAt {
		return fmt.Errorf("validate canonical v19 cutover import: Fleet row does not match exact import evidence")
	}
	var importFleetID string
	var sourceVersion int
	var sourceSHA, archiveSHA, manifestSHA, ddlSHA, fingerprint, importedAt string
	if err := sqlDB.QueryRow(`SELECT fleet_id,source_user_version,source_db_sha256,archive_db_sha256,manifest_sha256,target_ddl_sha256,target_schema_fingerprint,imported_at FROM legacy_import WHERE id=?`, plan.ImportID).
		Scan(&importFleetID, &sourceVersion, &sourceSHA, &archiveSHA, &manifestSHA, &ddlSHA, &fingerprint, &importedAt); err != nil {
		return fmt.Errorf("validate canonical v19 cutover import: read LegacyImport: %w", err)
	}
	if importFleetID != plan.FleetID || sourceVersion != legacyV18CutoverSemanticSourceVersion || sourceSHA != plan.SourceSHA256 || archiveSHA != plan.SourceSHA256 || manifestSHA != plan.ManifestSHA256 || ddlSHA != canonicalV19DDLSHA256 || fingerprint != canonicalV19SchemaFingerprint || importedAt != plan.ImportedAt {
		return fmt.Errorf("validate canonical v19 cutover import: LegacyImport row does not match exact evidence")
	}
	for _, project := range plan.Projects {
		var projectFleetID, displayName, createdAt, retiredAt string
		var projectOrdinal int
		if err := sqlDB.QueryRow(`SELECT fleet_id,ordinal,display_name,created_at,retired_at FROM project WHERE id=?`, project.ProjectID).
			Scan(&projectFleetID, &projectOrdinal, &displayName, &createdAt, &retiredAt); err != nil {
			return fmt.Errorf("validate canonical v19 cutover import: read Project %q: %w", project.SourceProjectID, err)
		}
		if projectFleetID != plan.FleetID || projectOrdinal != project.Ordinal || displayName != project.DisplayName || createdAt != plan.ImportedAt || retiredAt != "" {
			return fmt.Errorf("validate canonical v19 cutover import: Project %q does not match exact evidence", project.SourceProjectID)
		}
		var workspaceProjectID, locator, repositoryIdentity, commonGitDir, physicalIdentity, revision, establishedAt, workspaceSupersededAt string
		var workspaceOrdinal int
		if err := sqlDB.QueryRow(`SELECT project_id,ordinal,repository_locator,repository_identity_digest,common_git_dir,physical_identity_digest,revision,established_at,superseded_at FROM workspace_binding WHERE id=?`, project.WorkspaceBindingID).
			Scan(&workspaceProjectID, &workspaceOrdinal, &locator, &repositoryIdentity, &commonGitDir, &physicalIdentity, &revision, &establishedAt, &workspaceSupersededAt); err != nil {
			return fmt.Errorf("validate canonical v19 cutover import: read WorkspaceBinding %q: %w", project.SourceProjectID, err)
		}
		if workspaceProjectID != project.ProjectID || workspaceOrdinal != 1 || locator != project.RepositoryLocator || repositoryIdentity != project.RepositoryIdentity || commonGitDir != project.CommonGitDir || physicalIdentity != project.PhysicalIdentity || revision != project.Revision || establishedAt != plan.ImportedAt || workspaceSupersededAt != "" {
			return fmt.Errorf("validate canonical v19 cutover import: WorkspaceBinding %q does not match exact evidence", project.SourceProjectID)
		}
		var policyProjectID, policyDigest, workerProfileRef, qualificationRef, integrationRef, productionRef, publicationRef, policyCreatedAt, policySupersededAt string
		var policyOrdinal int
		if err := sqlDB.QueryRow(`SELECT project_id,ordinal,policy_digest,worker_profile_ref,qualification_policy_ref,integration_policy_ref,production_policy_ref,publication_policy_ref,created_at,superseded_at FROM policy_revision WHERE id=?`, project.PolicyRevisionID).
			Scan(&policyProjectID, &policyOrdinal, &policyDigest, &workerProfileRef, &qualificationRef, &integrationRef, &productionRef, &publicationRef, &policyCreatedAt, &policySupersededAt); err != nil {
			return fmt.Errorf("validate canonical v19 cutover import: read PolicyRevision %q: %w", project.SourceProjectID, err)
		}
		if policyProjectID != project.ProjectID || policyOrdinal != 1 || policyDigest != project.PolicyDigest || workerProfileRef != "" || qualificationRef != "" || integrationRef != "" || productionRef != "" || publicationRef != "" || policyCreatedAt != plan.ImportedAt || policySupersededAt != "" {
			return fmt.Errorf("validate canonical v19 cutover import: PolicyRevision %q fabricates or changes policy evidence", project.SourceProjectID)
		}
		var mappingWorkspaceID, mappingPolicyID, evidenceDigest string
		if err := sqlDB.QueryRow(`SELECT workspace_binding_id,policy_revision_id,evidence_digest FROM legacy_import_project WHERE import_id=? AND project_id=?`, plan.ImportID, project.ProjectID).
			Scan(&mappingWorkspaceID, &mappingPolicyID, &evidenceDigest); err != nil {
			return fmt.Errorf("validate canonical v19 cutover import: read LegacyImportProject %q: %w", project.SourceProjectID, err)
		}
		if mappingWorkspaceID != project.WorkspaceBindingID || mappingPolicyID != project.PolicyRevisionID || evidenceDigest != project.EvidenceDigest {
			return fmt.Errorf("validate canonical v19 cutover import: LegacyImportProject %q does not match exact evidence", project.SourceProjectID)
		}
	}

	expectedCounts := map[string]int{
		"fleet":                 1,
		"legacy_import":         1,
		"legacy_import_project": len(plan.Projects),
		"project":               len(plan.Projects),
		"workspace_binding":     len(plan.Projects),
		"policy_revision":       len(plan.Projects),
	}
	for table, expected := range expectedCounts {
		var count int
		if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM ` + quoteCanonicalV19Identifier(table)).Scan(&count); err != nil {
			return fmt.Errorf("validate canonical v19 cutover import: count %s: %w", table, err)
		}
		if count != expected {
			return fmt.Errorf("validate canonical v19 cutover import: %s rows=%d, want %d", table, count, expected)
		}
	}
	total, err := canonicalV19CutoverTotalDataRows(sqlDB)
	if err != nil {
		return err
	}
	expectedTotal := 2 + 4*len(plan.Projects)
	if total != expectedTotal {
		return fmt.Errorf("validate canonical v19 cutover import: total data rows=%d, want %d; fabricated canonical facts present", total, expectedTotal)
	}
	return nil
}

func canonicalV19CutoverTotalDataRows(sqlDB *sql.DB) (int, error) {
	rows, err := sqlDB.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return 0, fmt.Errorf("inspect canonical v19 cutover data rows: list tables: %w", err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("inspect canonical v19 cutover data rows: read table: %w", err)
		}
		tables = append(tables, table)
	}
	rowsErr := rows.Err()
	closeErr := rows.Close()
	if rowsErr != nil {
		return 0, fmt.Errorf("inspect canonical v19 cutover data rows: list tables: %w", rowsErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("inspect canonical v19 cutover data rows: close table list: %w", closeErr)
	}
	total := 0
	for _, table := range tables {
		var count int
		if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM ` + quoteCanonicalV19Identifier(table)).Scan(&count); err != nil {
			return 0, fmt.Errorf("inspect canonical v19 cutover data rows: count %s: %w", table, err)
		}
		total += count
	}
	return total, nil
}

func quoteCanonicalV19Identifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func syncCanonicalV19CutoverTarget(path string) error {
	if err := requireLegacyV18CutoverDirectRegularFile(path, "canonical target"); err != nil {
		return err
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return fmt.Errorf("materialize canonical v19 cutover target: SQLite sidecar %s remains after close", path+suffix)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("materialize canonical v19 cutover target: inspect SQLite sidecar %s: %w", path+suffix, err)
		}
	}
	if err := syncLegacyV18CutoverFile(path); err != nil {
		return fmt.Errorf("materialize canonical v19 cutover target: flush target: %w", err)
	}
	if err := syncLegacyV18CutoverDirectoryParent(path); err != nil {
		return fmt.Errorf("materialize canonical v19 cutover target: flush target directory: %w", err)
	}
	return nil
}
