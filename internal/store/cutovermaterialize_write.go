package store

import (
	"database/sql"
	"fmt"
	"strings"
)

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
	return "\"" + strings.ReplaceAll(value, "\"", "\"\"") + "\""
}
