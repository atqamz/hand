package runtime

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

func TestBuildLegacyV18CutoverImportPlanCarriesOnlyPositiveFactsDeterministically(t *testing.T) {
	plan, evidence := legacyV18CutoverImportPlanFixture()
	// Evidence order is intentionally opposite the source-plan order.
	evidence[0], evidence[1] = evidence[1], evidence[0]

	got, err := buildLegacyV18CutoverImportPlan(plan, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if got.FleetID != plan.FleetID {
		t.Fatalf("FleetID = %q, want %q", got.FleetID, plan.FleetID)
	}
	if len(got.Projects) != 2 {
		t.Fatalf("Projects = %#v, want two", got.Projects)
	}
	if got.Projects[0].SourceProjectID != "project-a" || got.Projects[1].SourceProjectID != "project-b" {
		t.Fatalf("Project ordering = %#v, want deterministic source identity order", got.Projects)
	}

	project := got.Projects[0]
	if project.Workspace.Locator != "projects/alpha" || project.Workspace.RepositoryPhysicalID != "repo-alpha" || project.Workspace.CommonDirPhysicalID != "git-alpha" || project.Workspace.Revision != strings.Repeat("a", 40) {
		t.Fatalf("workspace import facts = %#v", project.Workspace)
	}
	if project.PolicyInputs.LegacyName != "alpha" || project.PolicyInputs.LegacyURL != "https://example.invalid/alpha.git" || project.PolicyInputs.LegacyMode != "clone" || project.PolicyInputs.LegacyUpstream != "origin/main" {
		t.Fatalf("policy import inputs = %#v", project.PolicyInputs)
	}
}

func TestBuildLegacyV18CutoverImportPlanRejectsInvalidFleetIdentity(t *testing.T) {
	plan, evidence := legacyV18CutoverImportPlanFixture()
	plan.FleetID = "not-a-fleet"
	if _, err := buildLegacyV18CutoverImportPlan(plan, evidence); err == nil || !strings.Contains(err.Error(), "Fleet identity") {
		t.Fatalf("invalid Fleet error = %v", err)
	}
}

func TestBuildLegacyV18CutoverImportPlanRejectsMissingProjectEvidence(t *testing.T) {
	plan, evidence := legacyV18CutoverImportPlanFixture()
	if _, err := buildLegacyV18CutoverImportPlan(plan, evidence[:1]); err == nil || !strings.Contains(err.Error(), "missing positive import evidence") {
		t.Fatalf("missing evidence error = %v", err)
	}
}

func TestBuildLegacyV18CutoverImportPlanRejectsUnknownProjectEvidence(t *testing.T) {
	plan, evidence := legacyV18CutoverImportPlanFixture()
	evidence[0].ProjectID = "unknown-project"
	if _, err := buildLegacyV18CutoverImportPlan(plan, evidence); err == nil || !strings.Contains(err.Error(), "absent from held source plan") {
		t.Fatalf("unknown evidence error = %v", err)
	}
}

func TestBuildLegacyV18CutoverImportPlanRejectsProvenanceDrift(t *testing.T) {
	plan, evidence := legacyV18CutoverImportPlanFixture()
	evidence[0].LegacyUpstream = "origin/other"
	if _, err := buildLegacyV18CutoverImportPlan(plan, evidence); err == nil || !strings.Contains(err.Error(), "provenance changed") {
		t.Fatalf("provenance drift error = %v", err)
	}
}

func TestBuildLegacyV18CutoverImportPlanRejectsLocatorAlias(t *testing.T) {
	plan, evidence := legacyV18CutoverImportPlanFixture()
	plan.Projects[1].Name = plan.Projects[0].Name
	evidence[1].LegacyName = evidence[0].LegacyName
	evidence[1].Locator = evidence[0].Locator
	if _, err := buildLegacyV18CutoverImportPlan(plan, evidence); err == nil || !strings.Contains(err.Error(), "repository locator alias") {
		t.Fatalf("locator alias error = %v", err)
	}
}

func TestBuildLegacyV18CutoverImportPlanRejectsPhysicalAlias(t *testing.T) {
	plan, evidence := legacyV18CutoverImportPlanFixture()
	evidence[1].RepositoryPhysicalID = evidence[0].CommonDirPhysicalID
	if _, err := buildLegacyV18CutoverImportPlan(plan, evidence); err == nil || !strings.Contains(err.Error(), "physical identity alias") {
		t.Fatalf("physical alias error = %v", err)
	}
}

func TestBuildLegacyV18CutoverImportPlanRejectsInvalidRevision(t *testing.T) {
	plan, evidence := legacyV18CutoverImportPlanFixture()
	evidence[0].Revision = "HEAD"
	if _, err := buildLegacyV18CutoverImportPlan(plan, evidence); err == nil || !strings.Contains(err.Error(), "not an exact Git object ID") {
		t.Fatalf("invalid revision error = %v", err)
	}
}

func TestBuildLegacyV18CutoverImportPlanRejectsNonCanonicalLocator(t *testing.T) {
	plan, evidence := legacyV18CutoverImportPlanFixture()
	evidence[0].Locator = "projects/../alpha"
	if _, err := buildLegacyV18CutoverImportPlan(plan, evidence); err == nil || !strings.Contains(err.Error(), "not canonical Fleet-relative") {
		t.Fatalf("non-canonical locator error = %v", err)
	}
}

func legacyV18CutoverImportPlanFixture() (store.LegacyV18CutoverObservationPlan, []legacyV18CutoverProjectManifestEvidence) {
	fleetID := "f_" + strings.Repeat("0", 31) + "1"
	plan := store.LegacyV18CutoverObservationPlan{
		FleetID: fleetID,
		Projects: []store.LegacyV18CutoverProjectObservation{
			{
				ProjectID: "project-b",
				Name:      "beta",
				URL:       "https://example.invalid/beta.git",
				Mode:      "direct-pr",
				Upstream:  "origin/release",
			},
			{
				ProjectID: "project-a",
				Name:      "alpha",
				URL:       "https://example.invalid/alpha.git",
				Mode:      "clone",
				Upstream:  "origin/main",
			},
		},
	}
	evidence := []legacyV18CutoverProjectManifestEvidence{
		{
			ProjectID:            "project-b",
			Locator:              "projects/beta",
			RepositoryPhysicalID: "repo-beta",
			CommonDirPhysicalID:  "git-beta",
			Revision:             strings.Repeat("b", 40),
			LegacyName:           "beta",
			LegacyURL:            "https://example.invalid/beta.git",
			LegacyMode:           "direct-pr",
			LegacyUpstream:       "origin/release",
		},
		{
			ProjectID:            "project-a",
			Locator:              "projects/alpha",
			RepositoryPhysicalID: "repo-alpha",
			CommonDirPhysicalID:  "git-alpha",
			Revision:             strings.Repeat("a", 40),
			LegacyName:           "alpha",
			LegacyURL:            "https://example.invalid/alpha.git",
			LegacyMode:           "clone",
			LegacyUpstream:       "origin/main",
		},
	}
	return plan, evidence
}
