package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	gitrepo "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/store"
)

type legacyV18CutoverProjectManifestEvidence struct {
	ProjectID            string
	Locator              string
	RepositoryPhysicalID string
	CommonDirPhysicalID  string
	Revision             string
	LegacyName           string
	LegacyURL            string
	LegacyMode           string
	LegacyUpstream       string
}

type legacyV18CutoverProjectManifestDeps struct {
	resolveRoot      func(string) (string, error)
	commonDir        func(string) (string, error)
	isBare           func(string) (bool, error)
	headCommit       func(string) (string, error)
	physicalIdentity func(string, os.FileInfo) (string, error)
}

func defaultLegacyV18CutoverProjectManifestDeps() legacyV18CutoverProjectManifestDeps {
	return legacyV18CutoverProjectManifestDeps{
		resolveRoot:      gitrepo.ResolveRoot,
		commonDir:        gitrepo.CommonDir,
		isBare:           gitrepo.IsBare,
		headCommit:       gitrepo.HeadCommit,
		physicalIdentity: legacyV18CutoverPhysicalIdentity,
	}
}

func buildLegacyV18CutoverProjectManifestEvidence(homeDir string, plan store.LegacyV18CutoverObservationPlan, observed legacyV18CutoverProjectTreehouseEvidence) ([]legacyV18CutoverProjectManifestEvidence, error) {
	return buildLegacyV18CutoverProjectManifestEvidenceWithDeps(homeDir, plan, observed, defaultLegacyV18CutoverProjectManifestDeps())
}

func buildLegacyV18CutoverProjectManifestEvidenceWithDeps(homeDir string, plan store.LegacyV18CutoverObservationPlan, observed legacyV18CutoverProjectTreehouseEvidence, deps legacyV18CutoverProjectManifestDeps) ([]legacyV18CutoverProjectManifestEvidence, error) {
	if err := validateLegacyV18CutoverProjectManifestDeps(deps); err != nil {
		return nil, err
	}
	planByID := make(map[string]store.LegacyV18CutoverProjectObservation, len(plan.Projects))
	for _, project := range plan.Projects {
		if _, exists := planByID[project.ProjectID]; exists {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: duplicate source Project identity %q", project.ProjectID)
		}
		planByID[project.ProjectID] = project
	}

	type capturedProject struct {
		cloneInfo  os.FileInfo
		commonInfo os.FileInfo
		evidence   legacyV18CutoverProjectManifestEvidence
	}
	captured := make([]capturedProject, 0, len(observed.Projects))
	seen := make(map[string]struct{}, len(observed.Projects))
	for _, prior := range observed.Projects {
		project, ok := planByID[prior.ProjectID]
		if !ok {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: observed Project %q is absent from held source plan", prior.ProjectID)
		}
		if _, duplicate := seen[prior.ProjectID]; duplicate {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: duplicate observed Project identity %q", prior.ProjectID)
		}
		seen[prior.ProjectID] = struct{}{}
		locator, err := legacyV18CutoverProjectLocator(homeDir, project)
		if err != nil {
			return nil, err
		}
		if !gitrepo.SamePath(prior.ClonePath, project.ClonePath) {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: Project %q clone path changed from %q to %q", project.ProjectID, prior.ClonePath, project.ClonePath)
		}

		cloneInfo, err := os.Lstat(project.ClonePath)
		if err != nil {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: stat Project %q clone: %w", project.ProjectID, err)
		}
		if cloneInfo.Mode()&os.ModeSymlink != 0 || !cloneInfo.IsDir() {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: Project %q clone is not a direct directory", project.ProjectID)
		}
		root, err := deps.resolveRoot(project.ClonePath)
		if err != nil {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: resolve Project %q Git root: %w", project.ProjectID, err)
		}
		if !gitrepo.SamePath(root, project.ClonePath) {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: Project %q Git root=%q clone=%q", project.ProjectID, root, project.ClonePath)
		}
		bare, err := deps.isBare(project.ClonePath)
		if err != nil {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: inspect Project %q Git repository: %w", project.ProjectID, err)
		}
		if bare {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: Project %q repository is bare", project.ProjectID)
		}
		common, err := deps.commonDir(project.ClonePath)
		if err != nil {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: resolve Project %q Git common-dir: %w", project.ProjectID, err)
		}
		expectedCommon := filepath.Join(project.ClonePath, ".git")
		if !gitrepo.SamePath(common, expectedCommon) || !gitrepo.SamePath(common, prior.CommonDir) {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: Project %q common-dir=%q expected=%q observed=%q", project.ProjectID, common, expectedCommon, prior.CommonDir)
		}
		commonInfo, err := os.Lstat(common)
		if err != nil {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: stat Project %q Git common-dir: %w", project.ProjectID, err)
		}
		if commonInfo.Mode()&os.ModeSymlink != 0 || !commonInfo.IsDir() {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: Project %q Git common-dir is not a direct directory", project.ProjectID)
		}

		revision, err := deps.headCommit(project.ClonePath)
		if err != nil {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: read Project %q HEAD: %w", project.ProjectID, err)
		}
		if revision != prior.Revision || !validLegacyV18CutoverGitObjectID(revision) {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: Project %q HEAD=%q observed=%q", project.ProjectID, revision, prior.Revision)
		}
		repositoryPhysicalID, err := deps.physicalIdentity(project.ClonePath, cloneInfo)
		if err != nil {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: read Project %q repository physical identity: %w", project.ProjectID, err)
		}
		commonDirPhysicalID, err := deps.physicalIdentity(common, commonInfo)
		if err != nil {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: read Project %q common-dir physical identity: %w", project.ProjectID, err)
		}
		again, err := deps.headCommit(project.ClonePath)
		if err != nil {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: re-read Project %q HEAD: %w", project.ProjectID, err)
		}
		if again != revision {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: Project %q HEAD changed during evidence capture: first=%q second=%q", project.ProjectID, revision, again)
		}
		cloneAfter, err := os.Lstat(project.ClonePath)
		if err != nil || !os.SameFile(cloneInfo, cloneAfter) {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: Project %q repository identity changed during evidence capture", project.ProjectID)
		}
		commonAfter, err := os.Lstat(common)
		if err != nil || !os.SameFile(commonInfo, commonAfter) {
			return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: Project %q common-dir identity changed during evidence capture", project.ProjectID)
		}

		captured = append(captured, capturedProject{
			cloneInfo:  cloneInfo,
			commonInfo: commonInfo,
			evidence: legacyV18CutoverProjectManifestEvidence{
				ProjectID:            project.ProjectID,
				Locator:              locator,
				RepositoryPhysicalID: repositoryPhysicalID,
				CommonDirPhysicalID:  commonDirPhysicalID,
				Revision:             revision,
				LegacyName:           project.Name,
				LegacyURL:            project.URL,
				LegacyMode:           project.Mode,
				LegacyUpstream:       project.Upstream,
			},
		})
	}
	if len(seen) != len(planByID) {
		missing := make([]string, 0, len(planByID)-len(seen))
		for projectID := range planByID {
			if _, ok := seen[projectID]; !ok {
				missing = append(missing, projectID)
			}
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: held source Projects missing provider evidence: %v", missing)
	}
	for i := range captured {
		for j := i + 1; j < len(captured); j++ {
			if os.SameFile(captured[i].cloneInfo, captured[j].cloneInfo) || os.SameFile(captured[i].commonInfo, captured[j].commonInfo) {
				return nil, fmt.Errorf("freeze legacy v18 cutover Project evidence: physical repository alias between %q and %q", captured[i].evidence.ProjectID, captured[j].evidence.ProjectID)
			}
		}
	}

	result := make([]legacyV18CutoverProjectManifestEvidence, 0, len(captured))
	for _, project := range captured {
		result = append(result, project.evidence)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ProjectID < result[j].ProjectID })
	return result, nil
}

func legacyV18CutoverProjectLocator(homeDir string, project store.LegacyV18CutoverProjectObservation) (string, error) {
	relative, err := filepath.Rel(homeDir, project.ClonePath)
	if err != nil {
		return "", fmt.Errorf("freeze legacy v18 cutover Project evidence: derive Project %q Fleet-relative locator: %w", project.ProjectID, err)
	}
	expected := filepath.Join("projects", project.Name)
	if filepath.IsAbs(relative) || filepath.Clean(relative) != filepath.Clean(expected) {
		return "", fmt.Errorf("freeze legacy v18 cutover Project evidence: Project %q clone %q is not canonical Fleet-relative %q", project.ProjectID, project.ClonePath, expected)
	}
	return filepath.ToSlash(expected), nil
}

func validateLegacyV18CutoverProjectManifestDeps(deps legacyV18CutoverProjectManifestDeps) error {
	if deps.resolveRoot == nil || deps.commonDir == nil || deps.isBare == nil || deps.headCommit == nil || deps.physicalIdentity == nil {
		return fmt.Errorf("legacy v18 cutover Project manifest dependencies are incomplete")
	}
	return nil
}
