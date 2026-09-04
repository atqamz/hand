package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errLegacyV18CutoverPublicationUnsafe = errors.New("v19 cutover publication is not mechanically safe")

type canonicalV19CutoverPublication struct {
	MigrationID       string
	FleetID           string
	SourceSHA256      string
	ManifestSHA256    string
	TargetSHA256      string
	ImportID          string
	ProjectCount      int
	RetiredBridgePath string
}

func legacyV18CutoverRetiredBridgePath(homeDir, migrationID string) string {
	return filepath.Join(legacyV18CutoverOriginalArchiveDir(homeDir, migrationID), "frozen-bridge.db")
}

// Publishes only a fully materialized canonical temp that the read-only 5D1
// recovery classifier already proves belongs to one exact archive/manifest
// identity. It is intentionally private and has no production startup caller.
func publishCanonicalV19Cutover(homeDir string) (canonicalV19CutoverPublication, error) {
	releaseMigration, err := Lock(homeDir, MigrationLock, true)
	if err != nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: acquire MigrationLock: %w", err)
	}
	defer releaseMigration()

	before, err := inspectLegacyV18CutoverRecovery(homeDir)
	if err != nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: inspect recovery authority: %w", err)
	}
	if before.Disposition != legacyV18CutoverRecoveryPublishCanonicalTemp {
		return canonicalV19CutoverPublication{}, fmt.Errorf("%w: recovery disposition=%s: %s", errLegacyV18CutoverPublicationUnsafe, before.Disposition, before.Reason)
	}
	if err := validateCanonicalV19CutoverPublicationState(before); err != nil {
		return canonicalV19CutoverPublication{}, err
	}

	evidence, err := inspectLegacyV18CutoverArchiveEvidence(homeDir, before.MigrationID)
	if err != nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: revalidate archive evidence: %w", err)
	}
	if err := requireCanonicalV19CutoverPublicationEvidence(before, evidence); err != nil {
		return canonicalV19CutoverPublication{}, err
	}
	if err := syncAndRevalidateCanonicalV19CutoverTemp(before.Materialized); err != nil {
		return canonicalV19CutoverPublication{}, err
	}

	activePath := Path(homeDir)
	retiredPath := legacyV18CutoverRetiredBridgePath(homeDir, before.MigrationID)
	activeInfo, activeErr := os.Lstat(activePath)
	switch {
	case activeErr == nil:
		if before.BridgeSHA256 == "" {
			return canonicalV19CutoverPublication{}, fmt.Errorf("%w: active database exists but recovery evidence has no frozen bridge digest", errLegacyV18CutoverPublicationUnsafe)
		}
		if activeInfo.Mode()&os.ModeSymlink != 0 || !activeInfo.Mode().IsRegular() {
			return canonicalV19CutoverPublication{}, fmt.Errorf("%w: active frozen bridge %s is not a direct regular file", errLegacyV18CutoverPublicationUnsafe, activePath)
		}
		if err := revalidateCanonicalV19CutoverFrozenBridgeForPublication(homeDir, before, evidence); err != nil {
			return canonicalV19CutoverPublication{}, err
		}
		if err := syncLegacyV18CutoverFile(activePath); err != nil {
			return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: flush frozen bridge before retirement: %w", err)
		}
		if err := moveLegacyV18CutoverNoReplaceDurable(activePath, retiredPath); err != nil {
			return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: retire frozen bridge: %w", err)
		}
	case os.IsNotExist(activeErr):
		// Recovery may resume after a crash that already retired the frozen bridge.
		// Exact archive+manifest+temp evidence is sufficient; the bridge is mechanism
		// state and is not required to remain available after retirement.
	default:
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: inspect active database before retirement: %w", activeErr)
	}

	afterRetire, err := inspectLegacyV18CutoverRecovery(homeDir)
	if err != nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: reclassify after bridge retirement: %w", err)
	}
	if afterRetire.Disposition != legacyV18CutoverRecoveryPublishCanonicalTemp {
		return canonicalV19CutoverPublication{}, fmt.Errorf("%w: after bridge retirement recovery disposition=%s: %s", errLegacyV18CutoverPublicationUnsafe, afterRetire.Disposition, afterRetire.Reason)
	}
	if err := requireSameCanonicalV19CutoverPublicationState(before, afterRetire); err != nil {
		return canonicalV19CutoverPublication{}, err
	}
	if err := syncAndRevalidateCanonicalV19CutoverTemp(afterRetire.Materialized); err != nil {
		return canonicalV19CutoverPublication{}, err
	}
	if _, err := os.Lstat(activePath); err == nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("%w: active database unexpectedly reappeared before canonical publication", errLegacyV18CutoverPublicationUnsafe)
	} else if !os.IsNotExist(err) {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: inspect active destination: %w", err)
	}
	if err := moveLegacyV18CutoverNoReplaceDurable(afterRetire.Materialized.Path, activePath); err != nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: publish canonical temp: %w", err)
	}

	publication, err := validatePublishedCanonicalV19Cutover(homeDir, afterRetire, retiredPath)
	if err != nil {
		return canonicalV19CutoverPublication{}, err
	}
	return publication, nil
}

func validateCanonicalV19CutoverPublicationState(state legacyV18CutoverRecoveryState) error {
	if err := validateLegacyV18CutoverMigrationID(state.MigrationID); err != nil {
		return fmt.Errorf("%w: migration identity: %v", errLegacyV18CutoverPublicationUnsafe, err)
	}
	if err := validateFleetID(state.FleetID); err != nil {
		return fmt.Errorf("%w: Fleet identity: %v", errLegacyV18CutoverPublicationUnsafe, err)
	}
	if err := validateLegacyV18CutoverSHA256(state.SourceSHA256); err != nil {
		return fmt.Errorf("%w: source digest: %v", errLegacyV18CutoverPublicationUnsafe, err)
	}
	if state.Manifest.MigrationID != state.MigrationID || state.Materialized.MigrationID != state.MigrationID {
		return fmt.Errorf("%w: manifest/materialized migration identity does not match recovery state", errLegacyV18CutoverPublicationUnsafe)
	}
	if err := validateLegacyV18CutoverSHA256(state.Manifest.SHA256); err != nil {
		return fmt.Errorf("%w: manifest digest: %v", errLegacyV18CutoverPublicationUnsafe, err)
	}
	if state.Materialized.ManifestSHA256 != state.Manifest.SHA256 {
		return fmt.Errorf("%w: materialized manifest digest=%s, recovery manifest=%s", errLegacyV18CutoverPublicationUnsafe, state.Materialized.ManifestSHA256, state.Manifest.SHA256)
	}
	if err := validateLegacyV18CutoverSHA256(state.Materialized.SHA256); err != nil {
		return fmt.Errorf("%w: canonical temp digest: %v", errLegacyV18CutoverPublicationUnsafe, err)
	}
	expectedPath := legacyV18CutoverCanonicalTargetPath(filepath.Dir(filepath.Dir(state.Materialized.Path)), state.MigrationID)
	if filepath.Clean(state.Materialized.Path) != filepath.Clean(expectedPath) {
		return fmt.Errorf("%w: canonical temp path=%s is not deterministic", errLegacyV18CutoverPublicationUnsafe, state.Materialized.Path)
	}
	return nil
}

func requireCanonicalV19CutoverPublicationEvidence(state legacyV18CutoverRecoveryState, evidence legacyV18CutoverArchiveEvidence) error {
	if evidence.Manifest.MigrationID != state.MigrationID || evidence.Manifest.Fleet.FleetID != state.FleetID || evidence.Manifest.Source.DBSHA256 != state.SourceSHA256 {
		return fmt.Errorf("%w: archive/manifest evidence no longer matches recovery Fleet/source identity", errLegacyV18CutoverPublicationUnsafe)
	}
	if evidence.Artifact.SHA256 != state.Manifest.SHA256 || filepath.Clean(evidence.Artifact.Path) != filepath.Clean(state.Manifest.Path) {
		return fmt.Errorf("%w: manifest artifact changed before publication", errLegacyV18CutoverPublicationUnsafe)
	}
	return nil
}

func syncAndRevalidateCanonicalV19CutoverTemp(materialized canonicalV19CutoverMaterialization) error {
	if err := requireLegacyV18CutoverDirectRegularFile(materialized.Path, "canonical temp"); err != nil {
		return fmt.Errorf("publish canonical v19 cutover: %w", err)
	}
	if err := requireLegacyV18CutoverNoSQLiteSidecars(materialized.Path, "canonical temp"); err != nil {
		return fmt.Errorf("publish canonical v19 cutover: %w", err)
	}
	if err := syncLegacyV18CutoverFile(materialized.Path); err != nil {
		return fmt.Errorf("publish canonical v19 cutover: flush canonical temp: %w", err)
	}
	digest, err := legacyV18CutoverFileSHA256(materialized.Path)
	if err != nil {
		return fmt.Errorf("publish canonical v19 cutover: hash canonical temp after flush: %w", err)
	}
	if digest != materialized.SHA256 {
		return fmt.Errorf("%w: canonical temp digest=%s after flush, want %s", errLegacyV18CutoverPublicationUnsafe, digest, materialized.SHA256)
	}
	return nil
}

func revalidateCanonicalV19CutoverFrozenBridgeForPublication(homeDir string, state legacyV18CutoverRecoveryState, evidence legacyV18CutoverArchiveEvidence) error {
	bridge, err := discoverLegacyV18CutoverFrozenBridge(homeDir)
	if err != nil {
		return fmt.Errorf("publish canonical v19 cutover: revalidate frozen bridge: %w", err)
	}
	if bridge.MigrationID != state.MigrationID || bridge.FleetID != state.FleetID || bridge.SourceSHA256 != state.SourceSHA256 || bridge.BridgeSHA256 != state.BridgeSHA256 {
		return fmt.Errorf("%w: frozen bridge identity changed before retirement", errLegacyV18CutoverPublicationUnsafe)
	}
	if evidence.Manifest.Freeze.CertificateValue != bridge.Certificate {
		return fmt.Errorf("%w: frozen bridge certificate no longer matches manifest", errLegacyV18CutoverPublicationUnsafe)
	}
	return nil
}

func requireSameCanonicalV19CutoverPublicationState(before, after legacyV18CutoverRecoveryState) error {
	if before.MigrationID != after.MigrationID || before.FleetID != after.FleetID || before.SourceSHA256 != after.SourceSHA256 || before.Manifest.SHA256 != after.Manifest.SHA256 {
		return fmt.Errorf("%w: recovery identity changed across frozen-bridge retirement", errLegacyV18CutoverPublicationUnsafe)
	}
	if before.Materialized.MigrationID != after.Materialized.MigrationID || before.Materialized.Path != after.Materialized.Path || before.Materialized.SHA256 != after.Materialized.SHA256 || before.Materialized.ManifestSHA256 != after.Materialized.ManifestSHA256 || before.Materialized.ImportID != after.Materialized.ImportID || before.Materialized.ProjectCount != after.Materialized.ProjectCount {
		return fmt.Errorf("%w: canonical temp evidence changed across frozen-bridge retirement", errLegacyV18CutoverPublicationUnsafe)
	}
	return nil
}

func validatePublishedCanonicalV19Cutover(homeDir string, state legacyV18CutoverRecoveryState, retiredPath string) (canonicalV19CutoverPublication, error) {
	activePath := Path(homeDir)
	if err := requireLegacyV18CutoverDirectRegularFile(activePath, "published canonical database"); err != nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: %w", err)
	}
	if err := requireLegacyV18CutoverNoSQLiteSidecars(activePath, "published canonical database"); err != nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: %w", err)
	}
	evidence, err := inspectLegacyV18CutoverArchiveEvidence(homeDir, state.MigrationID)
	if err != nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: revalidate archive after publication: %w", err)
	}
	if err := requireCanonicalV19CutoverPublicationEvidence(state, evidence); err != nil {
		return canonicalV19CutoverPublication{}, err
	}

	sqlDB, err := openLegacyV18CutoverSQLite(activePath, "ro", legacyV18CutoverGateTimeout, true)
	if err != nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: reopen active canonical database read-only: %w", err)
	}
	plan := buildCanonicalV19CutoverImportPlan(evidence.Manifest, evidence.Artifact.SHA256)
	validationErr := validateCanonicalV19Schema(sqlDB)
	if validationErr == nil {
		validationErr = validateCanonicalV19CutoverImportRows(sqlDB, plan)
	}
	if validationErr == nil {
		var fleetID string
		fleetID, validationErr = validateCanonicalV19CutoverActiveFleet(sqlDB)
		if validationErr == nil && fleetID != state.FleetID {
			validationErr = fmt.Errorf("published canonical Fleet ID=%s, want %s", fleetID, state.FleetID)
		}
	}
	closeErr := sqlDB.Close()
	if validationErr != nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: validate published exact #344/import evidence: %w", validationErr)
	}
	if closeErr != nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: close published validation DB: %w", closeErr)
	}
	digest, err := legacyV18CutoverFileSHA256(activePath)
	if err != nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: hash published canonical database: %w", err)
	}
	if digest != state.Materialized.SHA256 {
		return canonicalV19CutoverPublication{}, fmt.Errorf("%w: published canonical digest=%s, want materialized temp=%s", errLegacyV18CutoverPublicationUnsafe, digest, state.Materialized.SHA256)
	}
	finalState, err := inspectLegacyV18CutoverRecovery(homeDir)
	if err != nil {
		return canonicalV19CutoverPublication{}, fmt.Errorf("publish canonical v19 cutover: classify published authority: %w", err)
	}
	if finalState.Disposition != legacyV18CutoverRecoveryCanonicalAuthority || finalState.FleetID != state.FleetID {
		return canonicalV19CutoverPublication{}, fmt.Errorf("%w: published active database did not become canonical authority", errLegacyV18CutoverPublicationUnsafe)
	}
	return canonicalV19CutoverPublication{
		MigrationID:       state.MigrationID,
		FleetID:           state.FleetID,
		SourceSHA256:      state.SourceSHA256,
		ManifestSHA256:    state.Manifest.SHA256,
		TargetSHA256:      digest,
		ImportID:          state.Materialized.ImportID,
		ProjectCount:      state.Materialized.ProjectCount,
		RetiredBridgePath: retiredPath,
	}, nil
}
