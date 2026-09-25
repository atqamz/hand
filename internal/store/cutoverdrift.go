package store

import (
	"fmt"
	"path/filepath"
)

// LegacyV18CutoverFrozenEvidence is the baseline the post-witness drift gate re-observes.
// The plan is derived from the original archive, never from the frozen bridge.
type LegacyV18CutoverFrozenEvidence struct {
	Plan     LegacyV18CutoverObservationPlan
	Projects []LegacyV18CutoverManifestProjectInput
}

// InspectLegacyV18CutoverFrozenEvidence reads the exact v2 bridge, its manifest, and its
// original archive read-only; any other state is an error.
func InspectLegacyV18CutoverFrozenEvidence(homeDir string) (LegacyV18CutoverFrozenEvidence, error) {
	bridge, err := discoverLegacyV18CutoverFrozenBridge(homeDir)
	if err != nil {
		return LegacyV18CutoverFrozenEvidence{}, fmt.Errorf("inspect frozen cutover evidence: %w", err)
	}
	if bridge.CertificateVersion != legacyV18CutoverFreezeCertificateVersion {
		return LegacyV18CutoverFrozenEvidence{}, fmt.Errorf("inspect frozen cutover evidence: certificate %s carries no committed boot evidence", bridge.CertificateVersion)
	}
	evidence, err := inspectLegacyV18CutoverArchiveEvidence(homeDir, bridge.MigrationID)
	if err != nil {
		return LegacyV18CutoverFrozenEvidence{}, fmt.Errorf("inspect frozen cutover evidence: %w", err)
	}
	if evidence.Artifact.SHA256 != bridge.ManifestSHA256 {
		return LegacyV18CutoverFrozenEvidence{}, fmt.Errorf("inspect frozen cutover evidence: manifest digest=%s, certificate binds %s", evidence.Artifact.SHA256, bridge.ManifestSHA256)
	}
	archiveDB, err := openLegacyV18CutoverSQLite(evidence.Archive.Path, "ro", legacyV18CutoverGateTimeout, true)
	if err != nil {
		return LegacyV18CutoverFrozenEvidence{}, fmt.Errorf("inspect frozen cutover evidence: open original archive: %w", err)
	}
	plan, planErr := classifyLegacyV18CutoverDurableState(homeDir, archiveDB)
	if closeErr := archiveDB.Close(); planErr == nil {
		planErr = closeErr
	}
	if planErr != nil {
		return LegacyV18CutoverFrozenEvidence{}, fmt.Errorf("inspect frozen cutover evidence: plan from original archive: %w", planErr)
	}
	frozen := LegacyV18CutoverFrozenEvidence{Plan: exportLegacyV18CutoverObservationPlan(plan)}
	for _, project := range evidence.Manifest.Projects {
		frozen.Projects = append(frozen.Projects, LegacyV18CutoverManifestProjectInput{
			SourceProjectID:      project.SourceProjectID,
			Locator:              filepath.ToSlash(project.RepositoryLocator),
			RepositoryPhysicalID: project.RepositoryPhysicalID,
			CommonDirPhysicalID:  project.CommonDirPhysicalID,
			Revision:             project.Revision,
			LegacyName:           project.DisplayName,
		})
	}
	return frozen, nil
}
