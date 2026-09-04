package store

import (
	"fmt"
	"os"
	"strings"
)

type legacyV18CutoverRecoveryDisposition string

const (
	legacyV18CutoverRecoveryNoState              legacyV18CutoverRecoveryDisposition = "no-state"
	legacyV18CutoverRecoveryLegacySource         legacyV18CutoverRecoveryDisposition = "legacy-source"
	legacyV18CutoverRecoveryCanonicalAuthority   legacyV18CutoverRecoveryDisposition = "canonical-authority"
	legacyV18CutoverRecoveryRebuildCanonicalTemp legacyV18CutoverRecoveryDisposition = "rebuild-canonical-temp"
	legacyV18CutoverRecoveryPublishCanonicalTemp legacyV18CutoverRecoveryDisposition = "publish-canonical-temp"
	legacyV18CutoverRecoveryRefuse               legacyV18CutoverRecoveryDisposition = "refuse"
)

type legacyV18CutoverRecoveryState struct {
	Disposition  legacyV18CutoverRecoveryDisposition
	Reason       string
	MigrationID  string
	FleetID      string
	SourceSHA256 string
	BridgeSHA256 string
	Manifest     legacyV18CutoverManifestArtifact
	Materialized canonicalV19CutoverMaterialization
}

// Classifies startup cutover/recovery authority without writing, syncing,
// renaming, migrating, checkpointing, or repairing any Fleet-local state.
func inspectLegacyV18CutoverRecovery(homeDir string) (legacyV18CutoverRecoveryState, error) {
	activePath := Path(homeDir)
	info, err := os.Lstat(activePath)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return legacyV18CutoverRecoveryState{
				Disposition: legacyV18CutoverRecoveryRefuse,
				Reason:      fmt.Sprintf("active state database %s is not a direct regular file", activePath),
			}, nil
		}
		return inspectLegacyV18CutoverRecoveryWithActive(homeDir)
	case os.IsNotExist(err):
		return inspectLegacyV18CutoverRecoveryWithoutActive(homeDir)
	default:
		return legacyV18CutoverRecoveryState{}, fmt.Errorf("inspect v19 cutover recovery active database: %w", err)
	}
}

func inspectLegacyV18CutoverRecoveryWithActive(homeDir string) (legacyV18CutoverRecoveryState, error) {
	activePath := Path(homeDir)
	sqlDB, err := openLegacyV18CutoverSQLite(activePath, "ro", legacyV18CutoverGateTimeout, true)
	if err != nil {
		return legacyV18CutoverRecoveryState{
			Disposition: legacyV18CutoverRecoveryRefuse,
			Reason:      fmt.Sprintf("active state database cannot be opened read-only: %v", err),
		}, nil
	}

	canonical, candidateErr := canonicalV19Candidate(sqlDB)
	if candidateErr != nil {
		_ = sqlDB.Close()
		return legacyV18CutoverRecoveryState{
			Disposition: legacyV18CutoverRecoveryRefuse,
			Reason:      fmt.Sprintf("active state database canonical-family inspection failed: %v", candidateErr),
		}, nil
	}
	if canonical {
		validationErr := validateCanonicalV19Schema(sqlDB)
		fleetID := ""
		if validationErr == nil {
			fleetID, validationErr = validateCanonicalV19CutoverActiveFleet(sqlDB)
		}
		closeErr := sqlDB.Close()
		if validationErr != nil {
			return legacyV18CutoverRecoveryState{
				Disposition: legacyV18CutoverRecoveryRefuse,
				Reason:      fmt.Sprintf("canonical v19 active database is invalid and cannot fall back to legacy evidence: %v", validationErr),
			}, nil
		}
		if closeErr != nil {
			return legacyV18CutoverRecoveryState{
				Disposition: legacyV18CutoverRecoveryRefuse,
				Reason:      fmt.Sprintf("close validated canonical v19 active database: %v", closeErr),
			}, nil
		}
		return legacyV18CutoverRecoveryState{
			Disposition: legacyV18CutoverRecoveryCanonicalAuthority,
			FleetID:     fleetID,
		}, nil
	}

	version, versionErr := schemaUserVersion(sqlDB)
	if versionErr != nil {
		_ = sqlDB.Close()
		return legacyV18CutoverRecoveryState{
			Disposition: legacyV18CutoverRecoveryRefuse,
			Reason:      fmt.Sprintf("active state database user_version inspection failed: %v", versionErr),
		}, nil
	}
	if version == legacyV18CutoverFrozenUserVersion {
		_ = sqlDB.Close()
		return inspectLegacyV18CutoverFrozenRecovery(homeDir)
	}

	_, legacyErr := validateLegacyV18CutoverSource(sqlDB)
	closeErr := sqlDB.Close()
	if legacyErr == nil && closeErr == nil {
		return legacyV18CutoverRecoveryState{Disposition: legacyV18CutoverRecoveryLegacySource}, nil
	}
	if legacyErr == nil {
		legacyErr = closeErr
	}
	return legacyV18CutoverRecoveryState{
		Disposition: legacyV18CutoverRecoveryRefuse,
		Reason:      fmt.Sprintf("active state database is neither valid canonical v19, exact frozen bridge, nor exact supported legacy source: %v", legacyErr),
	}, nil
}

func inspectLegacyV18CutoverFrozenRecovery(homeDir string) (legacyV18CutoverRecoveryState, error) {
	bridge, err := discoverLegacyV18CutoverFrozenBridge(homeDir)
	if err != nil {
		return legacyV18CutoverRecoveryState{
			Disposition: legacyV18CutoverRecoveryRefuse,
			Reason:      fmt.Sprintf("active frozen bridge is not exact recovery evidence: %v", err),
		}, nil
	}

	evidence, err := inspectLegacyV18CutoverArchiveEvidence(homeDir, bridge.MigrationID)
	if err != nil {
		return legacyV18CutoverRecoveryState{
			Disposition:  legacyV18CutoverRecoveryRefuse,
			Reason:       fmt.Sprintf("frozen bridge lacks exact matching immutable original archive evidence: %v", err),
			MigrationID:  bridge.MigrationID,
			FleetID:      bridge.FleetID,
			SourceSHA256: bridge.SourceSHA256,
			BridgeSHA256: bridge.BridgeSHA256,
		}, nil
	}
	if evidence.Manifest.Fleet.FleetID != bridge.FleetID || evidence.Manifest.Source.DBSHA256 != bridge.SourceSHA256 || evidence.Manifest.Freeze.CertificateValue != bridge.Certificate {
		return legacyV18CutoverRecoveryState{
			Disposition:  legacyV18CutoverRecoveryRefuse,
			Reason:       "frozen bridge and immutable archive manifest do not bind the same Fleet/source certificate",
			MigrationID:  bridge.MigrationID,
			FleetID:      bridge.FleetID,
			SourceSHA256: bridge.SourceSHA256,
			BridgeSHA256: bridge.BridgeSHA256,
		}, nil
	}
	return classifyLegacyV18CutoverTempRecovery(homeDir, bridge, evidence)
}

func inspectLegacyV18CutoverRecoveryWithoutActive(homeDir string) (legacyV18CutoverRecoveryState, error) {
	evidence, found, looseArtifacts, err := discoverLegacyV18CutoverArchiveEvidence(homeDir)
	if err != nil {
		return legacyV18CutoverRecoveryState{
			Disposition: legacyV18CutoverRecoveryRefuse,
			Reason:      err.Error(),
		}, nil
	}
	if !found {
		if looseArtifacts {
			return legacyV18CutoverRecoveryState{
				Disposition: legacyV18CutoverRecoveryRefuse,
				Reason:      "active state database is missing and only non-authoritative or incomplete cutover artifacts remain",
			}, nil
		}
		return legacyV18CutoverRecoveryState{Disposition: legacyV18CutoverRecoveryNoState}, nil
	}

	bridge := legacyV18CutoverFrozenBridge{
		MigrationID:  evidence.Manifest.MigrationID,
		FleetID:      evidence.Manifest.Fleet.FleetID,
		SourceSHA256: evidence.Manifest.Source.DBSHA256,
		Certificate:  evidence.Manifest.Freeze.CertificateValue,
		Committed:    true,
	}
	return classifyLegacyV18CutoverTempRecovery(homeDir, bridge, evidence)
}

func classifyLegacyV18CutoverTempRecovery(homeDir string, bridge legacyV18CutoverFrozenBridge, evidence legacyV18CutoverArchiveEvidence) (legacyV18CutoverRecoveryState, error) {
	materialized, exists, unsafePath, err := inspectCanonicalV19CutoverMaterializedTemp(homeDir, evidence)
	state := legacyV18CutoverRecoveryState{
		Disposition:  legacyV18CutoverRecoveryRebuildCanonicalTemp,
		MigrationID:  evidence.Manifest.MigrationID,
		FleetID:      evidence.Manifest.Fleet.FleetID,
		SourceSHA256: evidence.Manifest.Source.DBSHA256,
		BridgeSHA256: bridge.BridgeSHA256,
		Manifest:     evidence.Artifact,
	}
	if unsafePath {
		state.Disposition = legacyV18CutoverRecoveryRefuse
		state.Reason = err.Error()
		return state, nil
	}
	if err != nil {
		state.Reason = fmt.Sprintf("canonical temp is invalid/non-authoritative and must be rebuilt from exact archive evidence: %v", err)
		return state, nil
	}
	if !exists {
		state.Reason = "canonical temp is absent and must be rebuilt from exact archive evidence"
		return state, nil
	}
	state.Disposition = legacyV18CutoverRecoveryPublishCanonicalTemp
	state.Materialized = materialized
	return state, nil
}

func validateCanonicalV19CutoverActiveFleet(sqlDB sqliteQueryer) (string, error) {
	var count int
	var fleetID string
	if err := sqlDB.QueryRow(`SELECT COUNT(*), COALESCE(MIN(fleet_id), '') FROM fleet WHERE singleton = 1`).Scan(&count, &fleetID); err != nil {
		return "", fmt.Errorf("read canonical Fleet singleton: %w", err)
	}
	if count != 1 {
		return "", fmt.Errorf("canonical Fleet singleton rows=%d, want 1", count)
	}
	if err := validateFleetID(fleetID); err != nil {
		return "", fmt.Errorf("canonical Fleet identity: %w", err)
	}
	return fleetID, nil
}

func isLegacyV18CutoverLooseArtifact(name string) bool {
	return strings.HasPrefix(name, ".v19-cutover-") || strings.HasPrefix(name, "v19-cutover-")
}
