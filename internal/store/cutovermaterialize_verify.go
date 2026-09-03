package store

import (
	"database/sql"
	"fmt"
)

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
