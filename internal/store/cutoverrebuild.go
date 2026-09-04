package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errLegacyV18CutoverRebuildUnsafe = errors.New("v19 cutover canonical temp rebuild is not mechanically safe")

// Rebuilds only a non-authoritative canonical temp from exact immutable
// archive+manifest evidence after the read-only recovery classifier requires it.
// It never publishes the target, repairs registry projection, or activates cutover.
func rebuildCanonicalV19CutoverTemp(homeDir string) (canonicalV19CutoverMaterialization, error) {
	releaseMigration, err := Lock(homeDir, MigrationLock, true)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: acquire MigrationLock: %w", err)
	}
	defer releaseMigration()

	before, err := inspectLegacyV18CutoverRecovery(homeDir)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: inspect recovery authority: %w", err)
	}
	if before.Disposition != legacyV18CutoverRecoveryRebuildCanonicalTemp {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("%w: recovery disposition=%s: %s", errLegacyV18CutoverRebuildUnsafe, before.Disposition, before.Reason)
	}

	evidence, err := inspectLegacyV18CutoverArchiveEvidence(homeDir, before.MigrationID)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: revalidate archive evidence: %w", err)
	}
	if err := requireCanonicalV19CutoverRebuildEvidence(before, evidence); err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}

	targetPath := legacyV18CutoverCanonicalTargetPath(homeDir, before.MigrationID)
	if err := discardLegacyV18CutoverInvalidCanonicalTemp(targetPath); err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}
	target, err := prepareCanonicalV19CutoverTarget(homeDir, before.MigrationID)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: %w", err)
	}

	materialized, err := materializeCanonicalV19CutoverTargetFromArchive(homeDir, evidence, target)
	if err != nil {
		cleanupErr := discardLegacyV18CutoverInvalidCanonicalTemp(target.Path)
		if cleanupErr != nil {
			return canonicalV19CutoverMaterialization{}, errors.Join(err, cleanupErr)
		}
		return canonicalV19CutoverMaterialization{}, err
	}

	after, err := inspectLegacyV18CutoverRecovery(homeDir)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: reclassify rebuilt evidence: %w", err)
	}
	if after.Disposition != legacyV18CutoverRecoveryPublishCanonicalTemp {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("%w: rebuilt temp recovery disposition=%s: %s", errLegacyV18CutoverRebuildUnsafe, after.Disposition, after.Reason)
	}
	if after.MigrationID != before.MigrationID || after.FleetID != before.FleetID || after.SourceSHA256 != before.SourceSHA256 || after.Manifest != before.Manifest || after.Materialized != materialized {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("%w: recovery identity changed while rebuilding canonical temp", errLegacyV18CutoverRebuildUnsafe)
	}
	return materialized, nil
}

func requireCanonicalV19CutoverRebuildEvidence(state legacyV18CutoverRecoveryState, evidence legacyV18CutoverArchiveEvidence) error {
	if err := validateLegacyV18CutoverMigrationID(state.MigrationID); err != nil {
		return fmt.Errorf("%w: migration identity: %v", errLegacyV18CutoverRebuildUnsafe, err)
	}
	if err := validateFleetID(state.FleetID); err != nil {
		return fmt.Errorf("%w: Fleet identity: %v", errLegacyV18CutoverRebuildUnsafe, err)
	}
	if err := validateLegacyV18CutoverSHA256(state.SourceSHA256); err != nil {
		return fmt.Errorf("%w: source digest: %v", errLegacyV18CutoverRebuildUnsafe, err)
	}
	if evidence.Manifest.MigrationID != state.MigrationID || evidence.Manifest.Fleet.FleetID != state.FleetID || evidence.Manifest.Source.DBSHA256 != state.SourceSHA256 {
		return fmt.Errorf("%w: archive/manifest evidence does not match recovery Fleet/source identity", errLegacyV18CutoverRebuildUnsafe)
	}
	if evidence.Artifact != state.Manifest {
		return fmt.Errorf("%w: manifest artifact changed before canonical temp rebuild", errLegacyV18CutoverRebuildUnsafe)
	}
	return nil
}

func discardLegacyV18CutoverInvalidCanonicalTemp(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("rebuild canonical v19 cutover temp: inspect existing temp: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: canonical temp %s is not a direct regular file", errLegacyV18CutoverRebuildUnsafe, path)
	}

	sidecars := []string{path + "-journal", path + "-wal", path + "-shm"}
	for _, sidecar := range sidecars {
		if sidecarInfo, sidecarErr := os.Lstat(sidecar); sidecarErr == nil {
			if sidecarInfo.Mode()&os.ModeSymlink != 0 || !sidecarInfo.Mode().IsRegular() {
				return fmt.Errorf("%w: canonical temp sidecar %s is not a direct regular file", errLegacyV18CutoverRebuildUnsafe, sidecar)
			}
		} else if !os.IsNotExist(sidecarErr) {
			return fmt.Errorf("rebuild canonical v19 cutover temp: inspect sidecar %s: %w", sidecar, sidecarErr)
		}
	}

	// Remove the non-authoritative DB first. On Windows a stale open handle then
	// fails closed before any sidecar is touched. Partial POSIX cleanup is safe to
	// retry because archive+manifest remain the only recovery authority.
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("rebuild canonical v19 cutover temp: remove invalid temp: %w", err)
	}
	for _, sidecar := range sidecars {
		if err := os.Remove(sidecar); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rebuild canonical v19 cutover temp: remove sidecar %s: %w", sidecar, err)
		}
	}
	if err := syncLegacyV18CutoverDirectoryParent(path); err != nil {
		return fmt.Errorf("rebuild canonical v19 cutover temp: flush removed temp directory: %w", err)
	}
	return nil
}

func materializeCanonicalV19CutoverTargetFromArchive(homeDir string, evidence legacyV18CutoverArchiveEvidence, target canonicalV19CutoverTarget) (canonicalV19CutoverMaterialization, error) {
	migrationID := evidence.Manifest.MigrationID
	if target.MigrationID != migrationID {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: target migration identity=%s, archive=%s", target.MigrationID, migrationID)
	}
	expectedTargetPath := legacyV18CutoverCanonicalTargetPath(homeDir, migrationID)
	if filepath.Clean(target.Path) != filepath.Clean(expectedTargetPath) {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: target path=%s, want deterministic %s", target.Path, expectedTargetPath)
	}
	if err := requireLegacyV18CutoverDirectRegularFile(target.Path, "canonical target"); err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}

	plan := buildCanonicalV19CutoverImportPlan(evidence.Manifest, evidence.Artifact.SHA256)
	sqlDB, err := open(target.Path)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: open target: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = sqlDB.Close()
		}
	}()
	if err := validateCanonicalV19Schema(sqlDB); err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: validate fresh target: %w", err)
	}
	rows, err := canonicalV19CutoverTotalDataRows(sqlDB)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}
	if rows != 0 {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: target has %d existing data rows, want 0", rows)
	}
	if err := insertCanonicalV19CutoverImport(sqlDB, plan); err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}
	if err := validateCanonicalV19Schema(sqlDB); err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: validate committed target: %w", err)
	}
	if err := validateCanonicalV19CutoverImportRows(sqlDB, plan); err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}
	if err := sqlDB.Close(); err != nil {
		closed = true
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: close committed target: %w", err)
	}
	closed = true
	if err := syncCanonicalV19CutoverTarget(target.Path); err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}

	reopened, err := open(target.Path)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: reopen committed target: %w", err)
	}
	validationErr := validateCanonicalV19Schema(reopened)
	if validationErr == nil {
		validationErr = validateCanonicalV19CutoverImportRows(reopened, plan)
	}
	closeErr := reopened.Close()
	if validationErr != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: revalidate committed target: %w", validationErr)
	}
	if closeErr != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: close revalidated target: %w", closeErr)
	}
	if err := syncCanonicalV19CutoverTarget(target.Path); err != nil {
		return canonicalV19CutoverMaterialization{}, err
	}
	digest, err := legacyV18CutoverFileSHA256(target.Path)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, fmt.Errorf("rebuild canonical v19 cutover temp: hash committed target: %w", err)
	}
	return canonicalV19CutoverMaterialization{
		MigrationID:    migrationID,
		Path:           target.Path,
		SHA256:         digest,
		ManifestSHA256: evidence.Artifact.SHA256,
		ImportID:       plan.ImportID,
		ProjectCount:   len(plan.Projects),
	}, nil
}
