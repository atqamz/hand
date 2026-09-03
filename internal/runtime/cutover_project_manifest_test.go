package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

func TestBuildLegacyV18CutoverProjectManifestEvidenceCapturesVerifiedProjectFacts(t *testing.T) {
	home, clone, plan, observerDeps := legacyV18CutoverProjectTreehouseFixture(t)
	plan.Projects[0].URL = "https://example.invalid/demo.git"
	plan.Projects[0].Mode = "clone"
	plan.Projects[0].Upstream = "origin/main"
	observed, err := observeLegacyV18CutoverProjectTreehousePlan(context.Background(), home, plan, observerDeps)
	if err != nil {
		t.Fatal(err)
	}

	got, err := buildLegacyV18CutoverProjectManifestEvidenceWithDeps(home, plan, observed, legacyV18CutoverProjectManifestTestDeps(observerDeps))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("manifest Project evidence = %#v, want one Project", got)
	}
	project := got[0]
	if project.ProjectID != "project-1" || project.Locator != "projects/demo" || project.Revision != strings.Repeat("a", 40) {
		t.Fatalf("manifest Project identity evidence = %#v", project)
	}
	if project.RepositoryPhysicalID == "" || project.CommonDirPhysicalID == "" {
		t.Fatalf("manifest Project physical evidence = %#v, want repository and common-dir identities", project)
	}
	if strings.Contains(project.RepositoryPhysicalID, clone) || strings.Contains(project.CommonDirPhysicalID, clone) {
		t.Fatalf("physical identity leaked absolute path: %#v", project)
	}
	if project.LegacyName != "demo" || project.LegacyURL != "https://example.invalid/demo.git" || project.LegacyMode != "clone" || project.LegacyUpstream != "origin/main" {
		t.Fatalf("legacy provenance evidence = %#v", project)
	}
}

func TestLegacyV18CutoverPhysicalIdentityIsStableForObservedDirectory(t *testing.T) {
	home, clone, _, _ := legacyV18CutoverProjectTreehouseFixture(t)
	_ = home
	info, err := os.Lstat(clone)
	if err != nil {
		t.Fatal(err)
	}
	first, err := legacyV18CutoverPhysicalIdentity(clone, info)
	if err != nil {
		t.Fatal(err)
	}
	second, err := legacyV18CutoverPhysicalIdentity(clone, info)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || second != first {
		t.Fatalf("physical identity first=%q second=%q, want stable non-empty identity", first, second)
	}
}

func TestBuildLegacyV18CutoverProjectManifestEvidenceRejectsMissingProviderEvidence(t *testing.T) {
	home, _, plan, observerDeps := legacyV18CutoverProjectTreehouseFixture(t)
	_, err := buildLegacyV18CutoverProjectManifestEvidenceWithDeps(home, plan, legacyV18CutoverProjectTreehouseEvidence{}, legacyV18CutoverProjectManifestTestDeps(observerDeps))
	if err == nil || !strings.Contains(err.Error(), "missing provider evidence") {
		t.Fatalf("missing provider evidence error = %v", err)
	}
}

func TestBuildLegacyV18CutoverProjectManifestEvidenceRejectsHEADDrift(t *testing.T) {
	home, _, plan, observerDeps := legacyV18CutoverProjectTreehouseFixture(t)
	observed, err := observeLegacyV18CutoverProjectTreehousePlan(context.Background(), home, plan, observerDeps)
	if err != nil {
		t.Fatal(err)
	}
	deps := legacyV18CutoverProjectManifestTestDeps(observerDeps)
	calls := 0
	deps.headCommit = func(string) (string, error) {
		calls++
		if calls == 1 {
			return strings.Repeat("a", 40), nil
		}
		return strings.Repeat("b", 40), nil
	}
	_, err = buildLegacyV18CutoverProjectManifestEvidenceWithDeps(home, plan, observed, deps)
	if err == nil || !strings.Contains(err.Error(), "HEAD changed during evidence capture") {
		t.Fatalf("HEAD drift error = %v", err)
	}
}

func TestLegacyV18CutoverProjectLocatorRejectsNonCanonicalPath(t *testing.T) {
	home := t.TempDir()
	project := store.LegacyV18CutoverProjectObservation{
		ProjectID: "project-1",
		Name:      "demo",
		ClonePath: filepath.Join(home, "elsewhere", "demo"),
	}
	if _, err := legacyV18CutoverProjectLocator(home, project); err == nil || !strings.Contains(err.Error(), "not canonical Fleet-relative") {
		t.Fatalf("non-canonical locator error = %v", err)
	}
}

func legacyV18CutoverProjectManifestTestDeps(observerDeps legacyV18CutoverProjectTreehouseDeps) legacyV18CutoverProjectManifestDeps {
	return legacyV18CutoverProjectManifestDeps{
		resolveRoot:      observerDeps.resolveRoot,
		commonDir:        observerDeps.commonDir,
		isBare:           observerDeps.isBare,
		headCommit:       observerDeps.headCommit,
		physicalIdentity: legacyV18CutoverPhysicalIdentity,
	}
}
