package runtime

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/atqamz/hand/internal/store"
)

// This bounded semantic input is all that 5C may later materialize into
// canonical v19. It deliberately contains no legacy execution, runtime,
// Treehouse, Herdr, Send, or effect history.
type legacyV18CutoverImportPlan struct {
	FleetID  string
	Projects []legacyV18CutoverProjectImportPlan
}

type legacyV18CutoverProjectImportPlan struct {
	SourceProjectID string
	Workspace       legacyV18CutoverWorkspaceImportPlan
	PolicyInputs    legacyV18CutoverPolicyImportPlan
}

type legacyV18CutoverWorkspaceImportPlan struct {
	Locator              string
	RepositoryPhysicalID string
	CommonDirPhysicalID  string
	Revision             string
}

type legacyV18CutoverPolicyImportPlan struct {
	LegacyName     string
	LegacyURL      string
	LegacyMode     string
	LegacyUpstream string
}

func buildLegacyV18CutoverImportPlan(plan store.LegacyV18CutoverObservationPlan, evidence []legacyV18CutoverProjectManifestEvidence) (legacyV18CutoverImportPlan, error) {
	if err := store.ValidateFleetID(plan.FleetID); err != nil {
		return legacyV18CutoverImportPlan{}, fmt.Errorf("build legacy v18 cutover import plan: Fleet identity: %w", err)
	}

	sourceByID := make(map[string]store.LegacyV18CutoverProjectObservation, len(plan.Projects))
	for _, project := range plan.Projects {
		if project.ProjectID == "" {
			return legacyV18CutoverImportPlan{}, fmt.Errorf("build legacy v18 cutover import plan: source Project identity is empty")
		}
		if _, exists := sourceByID[project.ProjectID]; exists {
			return legacyV18CutoverImportPlan{}, fmt.Errorf("build legacy v18 cutover import plan: duplicate source Project identity %q", project.ProjectID)
		}
		sourceByID[project.ProjectID] = project
	}

	result := legacyV18CutoverImportPlan{FleetID: plan.FleetID}
	result.Projects = make([]legacyV18CutoverProjectImportPlan, 0, len(evidence))
	seenProjects := make(map[string]struct{}, len(evidence))
	seenLocators := make(map[string]string, len(evidence))
	seenPhysical := make(map[string]string, len(evidence)*2)
	for _, captured := range evidence {
		source, ok := sourceByID[captured.ProjectID]
		if !ok {
			return legacyV18CutoverImportPlan{}, fmt.Errorf("build legacy v18 cutover import plan: evidence Project %q is absent from held source plan", captured.ProjectID)
		}
		if _, duplicate := seenProjects[captured.ProjectID]; duplicate {
			return legacyV18CutoverImportPlan{}, fmt.Errorf("build legacy v18 cutover import plan: duplicate evidence Project identity %q", captured.ProjectID)
		}
		seenProjects[captured.ProjectID] = struct{}{}

		if captured.LegacyName != source.Name || captured.LegacyURL != source.URL || captured.LegacyMode != source.Mode || captured.LegacyUpstream != source.Upstream {
			return legacyV18CutoverImportPlan{}, fmt.Errorf("build legacy v18 cutover import plan: Project %q provenance changed after source observation", captured.ProjectID)
		}
		if err := validateLegacyV18CutoverImportLocator(captured.ProjectID, source.Name, captured.Locator); err != nil {
			return legacyV18CutoverImportPlan{}, err
		}
		if owner, exists := seenLocators[captured.Locator]; exists {
			return legacyV18CutoverImportPlan{}, fmt.Errorf("build legacy v18 cutover import plan: repository locator alias %q between Projects %q and %q", captured.Locator, owner, captured.ProjectID)
		}
		seenLocators[captured.Locator] = captured.ProjectID
		if !validLegacyV18CutoverGitObjectID(captured.Revision) {
			return legacyV18CutoverImportPlan{}, fmt.Errorf("build legacy v18 cutover import plan: Project %q revision %q is not an exact Git object ID", captured.ProjectID, captured.Revision)
		}
		if err := recordLegacyV18CutoverImportPhysicalIdentity(seenPhysical, captured.RepositoryPhysicalID, captured.ProjectID, "repository"); err != nil {
			return legacyV18CutoverImportPlan{}, err
		}
		if err := recordLegacyV18CutoverImportPhysicalIdentity(seenPhysical, captured.CommonDirPhysicalID, captured.ProjectID, "common-dir"); err != nil {
			return legacyV18CutoverImportPlan{}, err
		}

		result.Projects = append(result.Projects, legacyV18CutoverProjectImportPlan{
			SourceProjectID: captured.ProjectID,
			Workspace: legacyV18CutoverWorkspaceImportPlan{
				Locator:              captured.Locator,
				RepositoryPhysicalID: captured.RepositoryPhysicalID,
				CommonDirPhysicalID:  captured.CommonDirPhysicalID,
				Revision:             captured.Revision,
			},
			PolicyInputs: legacyV18CutoverPolicyImportPlan{
				LegacyName:     captured.LegacyName,
				LegacyURL:      captured.LegacyURL,
				LegacyMode:     captured.LegacyMode,
				LegacyUpstream: captured.LegacyUpstream,
			},
		})
	}

	if len(seenProjects) != len(sourceByID) {
		missing := make([]string, 0, len(sourceByID)-len(seenProjects))
		for projectID := range sourceByID {
			if _, ok := seenProjects[projectID]; !ok {
				missing = append(missing, projectID)
			}
		}
		sort.Strings(missing)
		return legacyV18CutoverImportPlan{}, fmt.Errorf("build legacy v18 cutover import plan: held source Projects missing positive import evidence: %v", missing)
	}

	sort.Slice(result.Projects, func(i, j int) bool {
		return result.Projects[i].SourceProjectID < result.Projects[j].SourceProjectID
	})
	return result, nil
}

func validateLegacyV18CutoverImportLocator(projectID, legacyName, locator string) error {
	if locator == "" || strings.HasPrefix(locator, "/") || path.Clean(locator) != locator {
		return fmt.Errorf("build legacy v18 cutover import plan: Project %q locator %q is not canonical Fleet-relative evidence", projectID, locator)
	}
	expected := path.Join("projects", legacyName)
	if expected == "." || expected == "projects" || locator != expected || !strings.HasPrefix(locator, "projects/") {
		return fmt.Errorf("build legacy v18 cutover import plan: Project %q locator=%q, want canonical Fleet-relative %q", projectID, locator, expected)
	}
	return nil
}

func recordLegacyV18CutoverImportPhysicalIdentity(seen map[string]string, identity, projectID, role string) error {
	if identity == "" {
		return fmt.Errorf("build legacy v18 cutover import plan: Project %q %s physical identity is empty", projectID, role)
	}
	subject := projectID + "/" + role
	if prior, exists := seen[identity]; exists {
		return fmt.Errorf("build legacy v18 cutover import plan: physical identity alias %q between %s and %s", identity, prior, subject)
	}
	seen[identity] = subject
	return nil
}
