package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func readLegacyV18CutoverManifest(homeDir string, artifact legacyV18CutoverManifestArtifact) (legacyV18CutoverManifest, error) {
	if err := validateLegacyV18CutoverMigrationID(artifact.MigrationID); err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("read legacy v18 cutover manifest: %w", err)
	}
	if err := validateLegacyV18CutoverSHA256(artifact.SHA256); err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("read legacy v18 cutover manifest: %w", err)
	}
	importedAt, err := validateLegacyV18CutoverManifestTimestamp(artifact.ImportedAt)
	if err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("read legacy v18 cutover manifest: %w", err)
	}
	expectedPath := legacyV18CutoverManifestPath(homeDir, artifact.MigrationID)
	if filepath.Clean(artifact.Path) != filepath.Clean(expectedPath) {
		return legacyV18CutoverManifest{}, fmt.Errorf("read legacy v18 cutover manifest: path=%s, want deterministic %s", artifact.Path, expectedPath)
	}
	if err := requireLegacyV18CutoverDirectRegularFile(artifact.Path, "manifest"); err != nil {
		return legacyV18CutoverManifest{}, err
	}
	if err := syncLegacyV18CutoverFile(artifact.Path); err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("read legacy v18 cutover manifest: flush manifest: %w", err)
	}
	if err := syncLegacyV18CutoverDirectoryParent(artifact.Path); err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("read legacy v18 cutover manifest: flush manifest directory: %w", err)
	}
	payload, err := os.ReadFile(artifact.Path)
	if err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("read legacy v18 cutover manifest: %w", err)
	}
	if got := canonicalV19SHA256(payload); got != artifact.SHA256 {
		return legacyV18CutoverManifest{}, fmt.Errorf("read legacy v18 cutover manifest: SHA-256=%s, want %s", got, artifact.SHA256)
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var manifest legacyV18CutoverManifest
	if err := decoder.Decode(&manifest); err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("read legacy v18 cutover manifest: decode: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return legacyV18CutoverManifest{}, fmt.Errorf("read legacy v18 cutover manifest: trailing JSON value")
		}
		return legacyV18CutoverManifest{}, fmt.Errorf("read legacy v18 cutover manifest: trailing data: %w", err)
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return legacyV18CutoverManifest{}, fmt.Errorf("read legacy v18 cutover manifest: re-encode: %w", err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(payload, canonical) {
		return legacyV18CutoverManifest{}, fmt.Errorf("read legacy v18 cutover manifest: bytes are not canonical deterministic JSON")
	}
	if err := validatePersistedLegacyV18CutoverManifest(manifest, artifact.MigrationID, importedAt); err != nil {
		return legacyV18CutoverManifest{}, err
	}
	return manifest, nil
}

func validatePersistedLegacyV18CutoverManifest(manifest legacyV18CutoverManifest, migrationID, importedAt string) error {
	if manifest.FormatVersion != legacyV18CutoverManifestVersion || manifest.MigrationID != migrationID || manifest.ImportedAt != importedAt {
		return fmt.Errorf("read legacy v18 cutover manifest: manifest identity does not match exact artifact evidence")
	}
	if err := validateFleetID(manifest.Fleet.FleetID); err != nil {
		return fmt.Errorf("read legacy v18 cutover manifest: Fleet ID: %w", err)
	}
	if manifest.Source.Contract != legacyV18CutoverSourceContract ||
		manifest.Source.SemanticVersion != legacyV18CutoverSemanticSourceVersion ||
		manifest.Source.SQLiteUserVersion != legacyV072SchemaVersion ||
		manifest.Source.LayoutFingerprint != legacyV072LayoutFingerprint ||
		manifest.Source.Tables != legacyV072TableCount ||
		manifest.Source.Indexes != legacyV072IndexCount ||
		manifest.Source.Triggers != legacyV072TriggerCount {
		return fmt.Errorf("read legacy v18 cutover manifest: source contract identity does not match exact supported v0.7.2 source")
	}
	if err := validateLegacyV18CutoverSHA256(manifest.Source.DBSHA256); err != nil {
		return fmt.Errorf("read legacy v18 cutover manifest: source DB digest: %w", err)
	}
	if manifest.OriginalArchive.RelativePath != "hand.db" || manifest.OriginalArchive.DBSHA256 != manifest.Source.DBSHA256 {
		return fmt.Errorf("read legacy v18 cutover manifest: original archive does not match exact source DB evidence")
	}
	expectedMigrationID, err := legacyV18CutoverMigrationIdentity(manifest.Fleet.FleetID, manifest.Source.DBSHA256)
	if err != nil {
		return fmt.Errorf("read legacy v18 cutover manifest: derive migration identity: %w", err)
	}
	if expectedMigrationID != migrationID {
		return fmt.Errorf("read legacy v18 cutover manifest: migration identity=%s, want %s from Fleet/source evidence", migrationID, expectedMigrationID)
	}
	expectedCertificate := legacyV18CutoverFreezeCertificateVersion + ":" + manifest.Source.DBSHA256
	if manifest.Freeze.CertificateVersion != legacyV18CutoverFreezeCertificateVersion ||
		manifest.Freeze.CertificateValue != expectedCertificate ||
		manifest.Freeze.CertificateSHA256 != canonicalV19SHA256([]byte(expectedCertificate)) ||
		manifest.Freeze.BridgeUserVersion != legacyV18CutoverFrozenUserVersion {
		return fmt.Errorf("read legacy v18 cutover manifest: freeze certificate identity does not match exact source evidence")
	}
	if manifest.Target.AuthorityCommit != canonicalV19AuthorityCommit ||
		manifest.Target.DDLPath != canonicalV19AuthorityDDLPath ||
		manifest.Target.DDLGitBlobSHA1 != canonicalV19AuthorityDDLGitBlobSHA1 ||
		manifest.Target.GzipSHA256 != canonicalV19GzipSHA256 ||
		manifest.Target.DDLSHA256 != canonicalV19DDLSHA256 ||
		manifest.Target.SchemaFingerprint != canonicalV19SchemaFingerprint ||
		manifest.Target.SQLiteUserVersion != canonicalV19SchemaVersion ||
		manifest.Target.Tables != canonicalV19TableCount ||
		manifest.Target.Indexes != canonicalV19IndexCount ||
		manifest.Target.Triggers != canonicalV19TriggerCount {
		return fmt.Errorf("read legacy v18 cutover manifest: target identity does not match locked #344 contract")
	}
	if manifest.Projects == nil {
		return fmt.Errorf("read legacy v18 cutover manifest: Projects must be an explicit array")
	}
	if manifest.Sidecars == nil || len(manifest.Sidecars) != 0 {
		return fmt.Errorf("read legacy v18 cutover manifest: sidecars must be the explicit empty evidence set")
	}
	return validatePersistedLegacyV18CutoverManifestProjects(manifest.Projects)
}

func validatePersistedLegacyV18CutoverManifestProjects(projects []legacyV18CutoverManifestProject) error {
	seenSource := make(map[string]struct{}, len(projects))
	seenLocator := make(map[string]struct{}, len(projects))
	seenPhysical := make(map[string]string, len(projects)*2)
	previousKey := ""
	for _, project := range projects {
		if project.SourceProjectID == "" {
			return fmt.Errorf("read legacy v18 cutover manifest: source Project ID is empty")
		}
		if _, exists := seenSource[project.SourceProjectID]; exists {
			return fmt.Errorf("read legacy v18 cutover manifest: duplicate source Project ID %q", project.SourceProjectID)
		}
		seenSource[project.SourceProjectID] = struct{}{}
		if err := validateLegacyV18CutoverManifestLocator(project.RepositoryLocator, project.DisplayName); err != nil {
			return fmt.Errorf("read legacy v18 cutover manifest: %w", err)
		}
		if _, exists := seenLocator[project.RepositoryLocator]; exists {
			return fmt.Errorf("read legacy v18 cutover manifest: duplicate repository locator %q", project.RepositoryLocator)
		}
		seenLocator[project.RepositoryLocator] = struct{}{}
		key := project.RepositoryLocator + "\x00" + project.SourceProjectID
		if previousKey != "" && key <= previousKey {
			return fmt.Errorf("read legacy v18 cutover manifest: Projects are not in canonical locator/source order")
		}
		previousKey = key
		if project.RepositoryPhysicalID == "" || project.CommonDirPhysicalID == "" {
			return fmt.Errorf("read legacy v18 cutover manifest: Project %q physical identity evidence is incomplete", project.SourceProjectID)
		}
		if project.RepositoryPhysicalID == project.CommonDirPhysicalID {
			return fmt.Errorf("read legacy v18 cutover manifest: Project %q repository and common-dir physical identities alias", project.SourceProjectID)
		}
		for role, physicalID := range map[string]string{"repository": project.RepositoryPhysicalID, "common-dir": project.CommonDirPhysicalID} {
			if previous, exists := seenPhysical[physicalID]; exists {
				return fmt.Errorf("read legacy v18 cutover manifest: Project %q %s physical identity aliases %s", project.SourceProjectID, role, previous)
			}
			seenPhysical[physicalID] = "Project " + project.SourceProjectID + " " + role
		}
		if project.RepositoryIdentitySHA256 != legacyV18CutoverManifestIdentitySHA256(legacyV18CutoverRepositoryIdentityDomain, project.RepositoryPhysicalID) {
			return fmt.Errorf("read legacy v18 cutover manifest: Project %q repository identity digest does not match physical evidence", project.SourceProjectID)
		}
		if project.CommonGitDir != project.RepositoryLocator+"/.git" || project.PhysicalIdentitySHA256 != legacyV18CutoverManifestIdentitySHA256(legacyV18CutoverCommonGitDirIdentityDomain, project.CommonDirPhysicalID) {
			return fmt.Errorf("read legacy v18 cutover manifest: Project %q common Git directory evidence does not match physical identity", project.SourceProjectID)
		}
		if err := validateLegacyV18CutoverManifestRevision(project.Revision); err != nil {
			return fmt.Errorf("read legacy v18 cutover manifest: Project %q: %w", project.SourceProjectID, err)
		}
		if err := validateLegacyV18CutoverSHA256(project.PolicyInputSHA256); err != nil {
			return fmt.Errorf("read legacy v18 cutover manifest: Project %q policy input digest: %w", project.SourceProjectID, err)
		}
	}
	return nil
}
