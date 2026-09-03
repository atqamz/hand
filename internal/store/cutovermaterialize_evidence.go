package store

import (
	"fmt"
	"os"
	"path/filepath"
)

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
