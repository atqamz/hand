package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	gitrepo "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/store"
)

// The freeze manifest and the post-restart drift gate must read HEAD and physical identity the same way.
var (
	legacyV18CutoverHeadCommit       = gitrepo.HeadCommit
	legacyV18CutoverPhysicalIdentity = store.CanonicalV19WorktreePhysicalIdentity
)

func defaultLegacyV18CutoverProjectManifestDeps() legacyV18CutoverProjectManifestDeps {
	return legacyV18CutoverProjectManifestDeps{
		resolveRoot:      gitrepo.ResolveRoot,
		commonDir:        gitrepo.CommonDir,
		isBare:           gitrepo.IsBare,
		headCommit:       legacyV18CutoverHeadCommit,
		physicalIdentity: legacyV18CutoverPhysicalIdentity,
	}
}

func defaultLegacyV18CutoverDriftDeps() legacyV18CutoverDriftDeps {
	treehouse := defaultLegacyV18CutoverProjectTreehouseDeps()
	treehouse.headCommit = legacyV18CutoverHeadCommit
	return legacyV18CutoverDriftDeps{
		headCommit:       legacyV18CutoverHeadCommit,
		physicalIdentity: legacyV18CutoverPhysicalIdentity,
		treehouse:        treehouse,
		herdr:            defaultLegacyV18CutoverHerdrDeps(),
	}
}

// OfflineCutover runs one step of the #348 revision 4 offline cutover: the freeze run on an exact
// legacy source, otherwise the completion run, which requires a restart since the freeze.
func OfflineCutover(ctx context.Context, homeDir string) (store.CanonicalV19CutoverRecovery, error) {
	state, err := store.InspectCanonicalV19Cutover(homeDir)
	if err != nil {
		return state, err
	}
	if state.Disposition != store.CanonicalV19CutoverLegacySource {
		return store.RecoverCanonicalV19Cutover(homeDir, LegacyV18CutoverDriftGate)
	}
	observe := func(guard *store.LegacyV18CutoverGuard) (store.LegacyV18CutoverManifestInput, error) {
		return observeLegacyV18CutoverForFreeze(ctx, homeDir, guard)
	}
	window, err := store.FreezeLegacyV18CutoverOffline(ctx, homeDir, observe)
	if err != nil {
		return state, err
	}
	frozen, err := store.InspectCanonicalV19Cutover(homeDir)
	frozen.Disposition = "frozen"
	frozen.Reason = "restart the machine fully, then run hand cutover offline again to complete, or run it with --abort to return to legacy"
	if window > 0 {
		frozen.Reason += fmt.Sprintf("; on Windows, complete within %s of the restart (a later restart reopens the window)", window.Round(time.Minute))
	}
	return frozen, err
}

func observeLegacyV18CutoverForFreeze(ctx context.Context, homeDir string, guard *store.LegacyV18CutoverGuard) (store.LegacyV18CutoverManifestInput, error) {
	plan, err := guard.ObservationPlan()
	if err != nil {
		return store.LegacyV18CutoverManifestInput{}, err
	}
	deps := defaultLegacyV18CutoverDriftDeps()
	observed, err := observeLegacyV18CutoverProjectTreehousePlan(ctx, homeDir, plan, deps.treehouse)
	if err != nil {
		return store.LegacyV18CutoverManifestInput{}, err
	}
	if _, err := observeLegacyV18CutoverHerdrPlan(ctx, plan, deps.herdr); err != nil {
		return store.LegacyV18CutoverManifestInput{}, err
	}
	evidence, err := buildLegacyV18CutoverProjectManifestEvidenceWithDeps(homeDir, plan, observed, defaultLegacyV18CutoverProjectManifestDeps())
	if err != nil {
		return store.LegacyV18CutoverManifestInput{}, err
	}
	input := store.LegacyV18CutoverManifestInput{FleetID: plan.FleetID, ImportedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	for _, project := range evidence {
		input.Projects = append(input.Projects, store.LegacyV18CutoverManifestProjectInput{
			SourceProjectID:      project.ProjectID,
			Locator:              project.Locator,
			RepositoryPhysicalID: project.RepositoryPhysicalID,
			CommonDirPhysicalID:  project.CommonDirPhysicalID,
			Revision:             project.Revision,
			LegacyName:           project.LegacyName,
			LegacyURL:            project.LegacyURL,
			LegacyMode:           project.LegacyMode,
			LegacyUpstream:       project.LegacyUpstream,
		})
	}
	return input, nil
}

// LegacyV18CutoverDriftGate is the production drift gate that store.RecoverCanonicalV19Cutover runs after a positive boot witness.
func LegacyV18CutoverDriftGate(homeDir string, frozen store.LegacyV18CutoverFrozenEvidence) (string, string) {
	result := evaluateLegacyV18CutoverDrift(context.Background(), homeDir, frozen, defaultLegacyV18CutoverDriftDeps())
	parts := make([]string, 0, len(result.Subjects))
	for _, subject := range result.Subjects {
		parts = append(parts, fmt.Sprintf("%s %s %s: %s", subject.Verdict, subject.Code, subject.Subject, subject.Detail))
	}
	return string(result.Verdict), strings.Join(parts, "; ")
}
