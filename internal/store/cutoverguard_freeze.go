package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// Freeze durably binds exact positive import evidence before crossing the one-way
// legacy source freeze boundary. It never builds or publishes canonical v19.
func (g *LegacyV18CutoverGuard) Freeze(ctx context.Context, homeDir string, input LegacyV18CutoverManifestInput) error {
	if !g.held() {
		return ErrLegacyV18CutoverGuardClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("freeze held legacy v18 cutover guard: %w", err)
	}
	if err := validateLegacyV18CutoverManifestInputAgainstPlan(g.plan, input); err != nil {
		return err
	}

	archive, err := promoteLegacyV18CutoverArchiveCandidate(homeDir, g.gate.archiveCandidate)
	if err != nil {
		return fmt.Errorf("freeze held legacy v18 cutover guard: promote original archive: %w", err)
	}
	stableInput, err := stabilizeLegacyV18CutoverManifestInput(homeDir, archive, input)
	if err != nil {
		return err
	}
	artifact, err := writeLegacyV18CutoverManifest(homeDir, archive, stableInput)
	if err != nil {
		return fmt.Errorf("freeze held legacy v18 cutover guard: persist pre-freeze recovery manifest: %w", err)
	}

	bridge, err := freezeLegacyV18CutoverSource(ctx, homeDir, g.gate, archive)
	if err != nil {
		return fmt.Errorf("freeze held legacy v18 cutover guard: %w", err)
	}
	if bridge.MigrationID != archive.MigrationID || bridge.FleetID != stableInput.FleetID || bridge.SourceSHA256 != archive.SHA256 {
		return fmt.Errorf("freeze held legacy v18 cutover guard: committed bridge identity differs from durable archive/manifest evidence")
	}

	state, err := inspectLegacyV18CutoverRecovery(homeDir)
	if err != nil {
		return fmt.Errorf("freeze held legacy v18 cutover guard: inspect committed recovery state: %w", err)
	}
	if state.Disposition != legacyV18CutoverRecoveryRebuildCanonicalTemp && state.Disposition != legacyV18CutoverRecoveryPublishCanonicalTemp {
		return fmt.Errorf("freeze held legacy v18 cutover guard: committed recovery disposition=%s: %s", state.Disposition, state.Reason)
	}
	if state.MigrationID != bridge.MigrationID || state.FleetID != bridge.FleetID || state.SourceSHA256 != bridge.SourceSHA256 || state.Manifest != artifact {
		return fmt.Errorf("freeze held legacy v18 cutover guard: committed recovery identity differs from durable pre-freeze evidence")
	}
	return nil
}

func (g *LegacyV18CutoverGuard) held() bool {
	return g != nil && g.gate != nil && g.gate.conn != nil && g.gate.db != nil && g.locks != nil
}

func validateLegacyV18CutoverManifestInputAgainstPlan(plan LegacyV18CutoverObservationPlan, input LegacyV18CutoverManifestInput) error {
	if input.FleetID != plan.FleetID {
		return fmt.Errorf("freeze held legacy v18 cutover guard: manifest Fleet ID=%q, held Fleet ID=%q", input.FleetID, plan.FleetID)
	}
	if _, err := validateLegacyV18CutoverManifestTimestamp(input.ImportedAt); err != nil {
		return fmt.Errorf("freeze held legacy v18 cutover guard: %w", err)
	}
	if _, err := buildLegacyV18CutoverManifestProjects(input.Projects); err != nil {
		return fmt.Errorf("freeze held legacy v18 cutover guard: %w", err)
	}
	if len(input.Projects) != len(plan.Projects) {
		return fmt.Errorf("freeze held legacy v18 cutover guard: manifest Project evidence count=%d, held source Projects=%d", len(input.Projects), len(plan.Projects))
	}

	planByID := make(map[string]LegacyV18CutoverProjectObservation, len(plan.Projects))
	for _, project := range plan.Projects {
		if _, exists := planByID[project.ProjectID]; exists {
			return fmt.Errorf("freeze held legacy v18 cutover guard: duplicate held source Project identity %q", project.ProjectID)
		}
		planByID[project.ProjectID] = project
	}
	seen := make(map[string]struct{}, len(input.Projects))
	for _, project := range input.Projects {
		source, ok := planByID[project.SourceProjectID]
		if !ok {
			return fmt.Errorf("freeze held legacy v18 cutover guard: manifest Project %q is absent from held source plan", project.SourceProjectID)
		}
		if _, duplicate := seen[project.SourceProjectID]; duplicate {
			return fmt.Errorf("freeze held legacy v18 cutover guard: duplicate manifest Project identity %q", project.SourceProjectID)
		}
		seen[project.SourceProjectID] = struct{}{}
		if project.LegacyName != source.Name || project.LegacyURL != source.URL || project.LegacyMode != source.Mode || project.LegacyUpstream != source.Upstream {
			return fmt.Errorf("freeze held legacy v18 cutover guard: Project %q provenance differs from held source plan", project.SourceProjectID)
		}
	}
	return nil
}

func stabilizeLegacyV18CutoverManifestInput(homeDir string, archive legacyV18CutoverOriginalArchive, input LegacyV18CutoverManifestInput) (LegacyV18CutoverManifestInput, error) {
	path := legacyV18CutoverManifestPath(homeDir, archive.MigrationID)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return input, nil
	}
	if err != nil {
		return LegacyV18CutoverManifestInput{}, fmt.Errorf("freeze held legacy v18 cutover guard: inspect existing recovery manifest: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return LegacyV18CutoverManifestInput{}, fmt.Errorf("freeze held legacy v18 cutover guard: existing recovery manifest %s is not a direct regular file", path)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return LegacyV18CutoverManifestInput{}, fmt.Errorf("freeze held legacy v18 cutover guard: read existing recovery manifest: %w", err)
	}
	var persisted legacyV18CutoverManifest
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return LegacyV18CutoverManifestInput{}, fmt.Errorf("freeze held legacy v18 cutover guard: decode existing recovery manifest: %w", err)
	}
	artifact := legacyV18CutoverManifestArtifact{
		MigrationID: archive.MigrationID,
		Path:        path,
		SHA256:      canonicalV19SHA256(payload),
		ImportedAt:  persisted.ImportedAt,
	}
	if _, err := readLegacyV18CutoverManifest(homeDir, artifact); err != nil {
		return LegacyV18CutoverManifestInput{}, fmt.Errorf("freeze held legacy v18 cutover guard: validate existing recovery manifest: %w", err)
	}

	stable := input
	stable.ImportedAt = persisted.ImportedAt
	expected, err := buildLegacyV18CutoverManifest(homeDir, archive, stable)
	if err != nil {
		return LegacyV18CutoverManifestInput{}, fmt.Errorf("freeze held legacy v18 cutover guard: rebuild existing recovery evidence: %w", err)
	}
	expectedPayload, err := json.Marshal(expected)
	if err != nil {
		return LegacyV18CutoverManifestInput{}, fmt.Errorf("freeze held legacy v18 cutover guard: encode existing recovery evidence: %w", err)
	}
	expectedPayload = append(expectedPayload, '\n')
	if !bytes.Equal(payload, expectedPayload) {
		return LegacyV18CutoverManifestInput{}, fmt.Errorf("freeze held legacy v18 cutover guard: existing recovery manifest differs from fresh positive evidence")
	}
	return stable, nil
}
