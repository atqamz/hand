package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/store"
)

type legacyV18CutoverDriftVerdict string

const (
	legacyV18CutoverDriftPass    legacyV18CutoverDriftVerdict = "pass"
	legacyV18CutoverDriftUnknown legacyV18CutoverDriftVerdict = "observation-unknown"
	legacyV18CutoverDriftFound   legacyV18CutoverDriftVerdict = "drift"
)

type legacyV18CutoverDriftSubject struct {
	Verdict legacyV18CutoverDriftVerdict
	Code    string
	Subject string
	Detail  string
}

// `observation-unknown` is retryable; `drift` means a fact recorded at the freeze no longer holds.
type legacyV18CutoverDriftResult struct {
	Verdict  legacyV18CutoverDriftVerdict
	Subjects []legacyV18CutoverDriftSubject
}

type legacyV18CutoverDriftDeps struct {
	headCommit       func(string) (string, error)
	physicalIdentity func(string, os.FileInfo) (string, error)
	treehouse        legacyV18CutoverProjectTreehouseDeps
	herdr            legacyV18CutoverHerdrDeps
}

// Runs only after a positive boot witness: the restart killed every process and Herdr pane alive at the freeze.
func evaluateLegacyV18CutoverDrift(ctx context.Context, homeDir string, frozen store.LegacyV18CutoverFrozenEvidence, deps legacyV18CutoverDriftDeps) legacyV18CutoverDriftResult {
	result := legacyV18CutoverDriftResult{Verdict: legacyV18CutoverDriftPass}
	add := func(verdict legacyV18CutoverDriftVerdict, code, subject, detail string) {
		result.Subjects = append(result.Subjects, legacyV18CutoverDriftSubject{Verdict: verdict, Code: code, Subject: subject, Detail: detail})
		if verdict == legacyV18CutoverDriftFound || result.Verdict == legacyV18CutoverDriftPass {
			result.Verdict = verdict
		}
	}
	projectNames := make(map[string]bool, len(frozen.Projects))
	for _, project := range frozen.Projects {
		projectNames[project.LegacyName] = true
		observeLegacyV18CutoverProjectDrift(homeDir, project, deps, add)
	}
	if _, err := observeLegacyV18CutoverProjectTreehousePlan(ctx, homeDir, frozen.Plan, deps.treehouse); err != nil {
		var blocked *legacyV18CutoverProviderBlockedError
		if !errors.As(err, &blocked) {
			blocked = &legacyV18CutoverProviderBlockedError{Blockers: []legacyV18CutoverProviderBlocker{{Code: "treehouse-unobservable", Subject: "treehouse", Detail: err.Error(), Unknown: true}}}
		}
		for _, blocker := range blocked.Blockers {
			verdict := legacyV18CutoverDriftFound
			if blocker.Unknown {
				verdict = legacyV18CutoverDriftUnknown
			}
			add(verdict, blocker.Code, blocker.Subject, blocker.Detail)
		}
	}
	observeLegacyV18CutoverHerdrAfterRestart(ctx, frozen.Plan.FleetID, projectNames, deps.herdr, add)
	return result
}

func observeLegacyV18CutoverProjectDrift(homeDir string, project store.LegacyV18CutoverManifestProjectInput, deps legacyV18CutoverDriftDeps, add func(legacyV18CutoverDriftVerdict, string, string, string)) {
	subject := "project:" + project.SourceProjectID
	clone := filepath.Join(homeDir, filepath.FromSlash(project.Locator))
	for _, part := range []struct{ role, path, want string }{
		{"repository", clone, project.RepositoryPhysicalID},
		{"common-dir", filepath.Join(clone, ".git"), project.CommonDirPhysicalID},
	} {
		info, err := os.Lstat(part.path)
		switch {
		case os.IsNotExist(err):
			add(legacyV18CutoverDriftFound, "project-missing", subject, part.role+" "+part.path+" is absent")
			return
		case err != nil:
			add(legacyV18CutoverDriftUnknown, "project-unobservable", subject, err.Error())
			return
		case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
			add(legacyV18CutoverDriftFound, "project-not-directory", subject, part.role+" "+part.path+" is not a direct directory")
			return
		}
		identity, err := deps.physicalIdentity(part.path, info)
		if err != nil {
			add(legacyV18CutoverDriftUnknown, "project-identity-unobservable", subject, part.role+": "+err.Error())
		} else if identity != part.want {
			add(legacyV18CutoverDriftFound, "project-identity-changed", subject, part.role+" identity "+identity+", frozen "+part.want)
		}
	}
	revision, err := deps.headCommit(clone)
	if err != nil {
		add(legacyV18CutoverDriftUnknown, "project-head-unobservable", subject, err.Error())
	} else if revision != project.Revision {
		add(legacyV18CutoverDriftFound, "project-head-changed", subject, "HEAD "+revision+", frozen "+project.Revision)
	}
}

// Herdr identities recorded before the restart belong to a dead server; only session and label attribute a resource now.
func observeLegacyV18CutoverHerdrAfterRestart(ctx context.Context, fleetID string, projectNames map[string]bool, deps legacyV18CutoverHerdrDeps, add func(legacyV18CutoverDriftVerdict, string, string, string)) {
	current := herdr.SessionName(fleetID)
	for _, session := range []string{current, "default"} {
		subject := "herdr-session:" + session
		observation := deps.observeSession(ctx, session)
		if observation.Name != session {
			add(legacyV18CutoverDriftUnknown, "herdr-session-identity-mismatch", subject, "provider named "+observation.Name)
			continue
		}
		if observation.State == herdr.SessionStopped {
			continue
		}
		if observation.State != herdr.SessionRunningCompatible {
			add(legacyV18CutoverDriftUnknown, "herdr-session-unobservable", subject, string(observation.State)+": "+observation.Reason)
			continue
		}
		inventory := deps.inventoryFor(session)
		if inventory == nil {
			add(legacyV18CutoverDriftUnknown, "herdr-provider-unavailable", subject, "Herdr inventory client is unavailable")
			continue
		}
		workspaces, err := inventory.WorkspaceList()
		if err != nil {
			add(legacyV18CutoverDriftUnknown, "herdr-workspace-inventory-unobservable", subject, err.Error())
			continue
		}
		for _, workspace := range workspaces {
			resource := "herdr-workspace:" + workspace.WorkspaceID
			rest, handLabel := strings.CutPrefix(workspace.Label, "hand:")
			owner, _, fleetLabel := strings.Cut(rest, ":")
			switch {
			case session == current || (handLabel && fleetLabel && owner == fleetID):
				add(legacyV18CutoverDriftUnknown, "herdr-fleet-resource-live", resource, "close this Fleet's workspace "+workspace.Label+" and retry")
			case handLabel && !fleetLabel && projectNames[rest]:
				add(legacyV18CutoverDriftUnknown, "herdr-legacy-label-ambiguous", resource, "workspace "+workspace.Label+" may belong to another Fleet; do not close it, wait for that Fleet or abort")
			}
		}
	}
}
