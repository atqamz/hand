package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gitrepo "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/store"
	"github.com/atqamz/hand/internal/worktree"
)

var errLegacyV18CutoverProjectTreehouseUnsafe = errors.New("legacy v18 cutover Project/Treehouse state is not quiescent")

type legacyV18CutoverProviderBlocker struct {
	Code    string
	Subject string
	Detail  string
}

type legacyV18CutoverProviderBlockedError struct {
	Blockers []legacyV18CutoverProviderBlocker
}

func (e *legacyV18CutoverProviderBlockedError) Error() string {
	if e == nil || len(e.Blockers) == 0 {
		return errLegacyV18CutoverProjectTreehouseUnsafe.Error()
	}
	parts := make([]string, 0, len(e.Blockers))
	for _, blocker := range e.Blockers {
		part := blocker.Code + " " + blocker.Subject
		if blocker.Detail != "" {
			part += ": " + blocker.Detail
		}
		parts = append(parts, part)
	}
	return errLegacyV18CutoverProjectTreehouseUnsafe.Error() + ": " + strings.Join(parts, "; ")
}

func (e *legacyV18CutoverProviderBlockedError) Unwrap() error {
	return errLegacyV18CutoverProjectTreehouseUnsafe
}

type legacyV18CutoverProjectEvidence struct {
	ProjectID string
	Name      string
	ClonePath string
	CommonDir string
	Revision  string
}

type legacyV18CutoverProjectTreehouseEvidence struct {
	Projects []legacyV18CutoverProjectEvidence
}

type legacyV18CutoverProjectTreehouseDeps struct {
	resolveRoot        func(string) (string, error)
	commonDir          func(string) (string, error)
	isBare             func(string) (bool, error)
	headCommit         func(string) (string, error)
	poolSearchRoots    func(string, string) []string
	discoverPoolSlots  func(string, ...string) ([]worktree.PoolSlot, error)
	poolSlotCollisions func([]worktree.PoolSlot) [][]worktree.PoolSlot
	poolStatus         func(string) ([]worktree.PoolEntry, error)
	observeLease       func(string, string, string) worktree.LeaseObservation
}

func defaultLegacyV18CutoverProjectTreehouseDeps() legacyV18CutoverProjectTreehouseDeps {
	return legacyV18CutoverProjectTreehouseDeps{
		resolveRoot:        gitrepo.ResolveRoot,
		commonDir:          gitrepo.CommonDir,
		isBare:             gitrepo.IsBare,
		headCommit:         gitrepo.HeadCommit,
		poolSearchRoots:    worktree.PoolSearchRoots,
		discoverPoolSlots:  worktree.DiscoverPoolSlots,
		poolSlotCollisions: worktree.PoolSlotCollisions,
		poolStatus:         worktree.PoolStatus,
		observeLease:       worktree.ObserveLease,
	}
}

func observeLegacyV18CutoverProjectTreehouse(ctx context.Context, homeDir string, guard *store.LegacyV18CutoverGuard) (legacyV18CutoverProjectTreehouseEvidence, error) {
	return observeLegacyV18CutoverProjectTreehouseWithDeps(ctx, homeDir, guard, defaultLegacyV18CutoverProjectTreehouseDeps())
}

func observeLegacyV18CutoverProjectTreehouseWithDeps(ctx context.Context, homeDir string, guard *store.LegacyV18CutoverGuard, deps legacyV18CutoverProjectTreehouseDeps) (legacyV18CutoverProjectTreehouseEvidence, error) {
	if guard == nil {
		return legacyV18CutoverProjectTreehouseEvidence{}, store.ErrLegacyV18CutoverGuardClosed
	}
	plan, err := guard.ObservationPlan()
	if err != nil {
		return legacyV18CutoverProjectTreehouseEvidence{}, fmt.Errorf("read held legacy v18 cutover observation plan: %w", err)
	}
	return observeLegacyV18CutoverProjectTreehousePlan(ctx, homeDir, plan, deps)
}

func observeLegacyV18CutoverProjectTreehousePlan(ctx context.Context, homeDir string, plan store.LegacyV18CutoverObservationPlan, deps legacyV18CutoverProjectTreehouseDeps) (legacyV18CutoverProjectTreehouseEvidence, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateLegacyV18CutoverProjectTreehouseDeps(deps); err != nil {
		return legacyV18CutoverProjectTreehouseEvidence{}, err
	}

	var blockers []legacyV18CutoverProviderBlocker
	addBlocker := func(code, subject, detail string) {
		blockers = append(blockers, legacyV18CutoverProviderBlocker{Code: code, Subject: subject, Detail: detail})
	}
	worktreesByProject := make(map[string][]store.LegacyV18CutoverWorktreeObservation)
	for _, recorded := range plan.Worktrees {
		worktreesByProject[recorded.ProjectID] = append(worktreesByProject[recorded.ProjectID], recorded)
	}

	projectsRoot := filepath.Join(homeDir, "projects")
	entries, err := os.ReadDir(projectsRoot)
	if os.IsNotExist(err) && len(plan.Projects) == 0 {
		entries = nil
		err = nil
	}
	if err != nil {
		addBlocker("project-namespace-unobservable", "projects", err.Error())
	} else {
		for _, entry := range entries {
			entryPath := filepath.Join(projectsRoot, entry.Name())
			var matched *store.LegacyV18CutoverProjectObservation
			for i := range plan.Projects {
				project := &plan.Projects[i]
				if !gitrepo.SamePath(entryPath, project.ClonePath) {
					continue
				}
				if matched != nil {
					addBlocker("project-path-ambiguous", "project-path:"+entry.Name(), "managed Project path matches more than one durable Project identity")
					matched = nil
					break
				}
				matched = project
			}
			if matched == nil {
				addBlocker("project-orphan-path", "project-path:"+entry.Name(), "managed projects namespace contains no unique durable Project identity")
				continue
			}
			if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
				addBlocker("project-path-unsafe", "project:"+matched.ProjectID, "managed Project path is not a direct directory")
			}
		}
	}

	type physicalProject struct {
		cloneInfo  os.FileInfo
		commonInfo os.FileInfo
		evidence   legacyV18CutoverProjectEvidence
	}
	physical := make([]physicalProject, 0, len(plan.Projects))
	for _, project := range plan.Projects {
		if err := ctx.Err(); err != nil {
			return legacyV18CutoverProjectTreehouseEvidence{}, err
		}
		subject := "project:" + project.ProjectID
		cloneInfo, err := os.Lstat(project.ClonePath)
		if err != nil {
			addBlocker("project-clone-unobservable", subject, err.Error())
			continue
		}
		if cloneInfo.Mode()&os.ModeSymlink != 0 || !cloneInfo.IsDir() {
			addBlocker("project-clone-unsafe", subject, "clone path is not a direct directory")
			continue
		}
		root, err := deps.resolveRoot(project.ClonePath)
		if err != nil {
			addBlocker("project-git-unobservable", subject, err.Error())
			continue
		}
		if !gitrepo.SamePath(root, project.ClonePath) {
			addBlocker("project-git-root-mismatch", subject, fmt.Sprintf("Git root=%q clone=%q", root, project.ClonePath))
			continue
		}
		bare, err := deps.isBare(project.ClonePath)
		if err != nil {
			addBlocker("project-git-unobservable", subject, err.Error())
			continue
		}
		if bare {
			addBlocker("project-git-root-mismatch", subject, "managed Project clone is bare")
			continue
		}
		common, err := deps.commonDir(project.ClonePath)
		if err != nil {
			addBlocker("project-git-unobservable", subject, err.Error())
			continue
		}
		expectedCommon := filepath.Join(project.ClonePath, ".git")
		if !gitrepo.SamePath(common, expectedCommon) {
			addBlocker("project-git-common-dir-mismatch", subject, fmt.Sprintf("common-dir=%q want=%q", common, expectedCommon))
			continue
		}
		commonInfo, err := os.Stat(common)
		if err != nil {
			addBlocker("project-git-unobservable", subject, fmt.Sprintf("stat common-dir: %v", err))
			continue
		}
		if !commonInfo.IsDir() {
			addBlocker("project-git-common-dir-mismatch", subject, "common-dir is not a directory")
			continue
		}
		revision, err := deps.headCommit(project.ClonePath)
		if err != nil {
			addBlocker("project-revision-unobservable", subject, err.Error())
			continue
		}
		if !validLegacyV18CutoverGitObjectID(revision) {
			addBlocker("project-revision-invalid", subject, "HEAD is not a 40- or 64-hex Git object ID")
			continue
		}
		physical = append(physical, physicalProject{
			cloneInfo:  cloneInfo,
			commonInfo: commonInfo,
			evidence: legacyV18CutoverProjectEvidence{
				ProjectID: project.ProjectID,
				Name:      project.Name,
				ClonePath: filepath.Clean(project.ClonePath),
				CommonDir: filepath.Clean(common),
				Revision:  revision,
			},
		})
	}

	for i := range physical {
		for j := i + 1; j < len(physical); j++ {
			if os.SameFile(physical[i].cloneInfo, physical[j].cloneInfo) || os.SameFile(physical[i].commonInfo, physical[j].commonInfo) {
				addBlocker("project-physical-alias", "project:"+physical[i].evidence.ProjectID, "physical repository aliases project:"+physical[j].evidence.ProjectID)
			}
		}
	}

	for _, project := range plan.Projects {
		if err := ctx.Err(); err != nil {
			return legacyV18CutoverProjectTreehouseEvidence{}, err
		}
		observeLegacyV18CutoverTreehouseProject(plan.FleetID, homeDir, project, worktreesByProject[project.ProjectID], deps, addBlocker)
	}

	if len(blockers) != 0 {
		sort.Slice(blockers, func(i, j int) bool {
			if blockers[i].Code != blockers[j].Code {
				return blockers[i].Code < blockers[j].Code
			}
			if blockers[i].Subject != blockers[j].Subject {
				return blockers[i].Subject < blockers[j].Subject
			}
			return blockers[i].Detail < blockers[j].Detail
		})
		return legacyV18CutoverProjectTreehouseEvidence{}, &legacyV18CutoverProviderBlockedError{Blockers: blockers}
	}

	evidence := legacyV18CutoverProjectTreehouseEvidence{Projects: make([]legacyV18CutoverProjectEvidence, 0, len(physical))}
	for _, project := range physical {
		evidence.Projects = append(evidence.Projects, project.evidence)
	}
	sort.Slice(evidence.Projects, func(i, j int) bool { return evidence.Projects[i].ProjectID < evidence.Projects[j].ProjectID })
	return evidence, nil
}

func observeLegacyV18CutoverTreehouseProject(fleetID, homeDir string, project store.LegacyV18CutoverProjectObservation, recorded []store.LegacyV18CutoverWorktreeObservation, deps legacyV18CutoverProjectTreehouseDeps, addBlocker func(string, string, string)) {
	subject := "project:" + project.ProjectID
	pool, err := deps.poolStatus(project.ClonePath)
	if err != nil {
		addBlocker("treehouse-pool-unobservable", subject, err.Error())
		return
	}
	for i := range pool {
		for j := i + 1; j < len(pool); j++ {
			if gitrepo.SamePath(pool[i].Path, pool[j].Path) {
				addBlocker("treehouse-pool-path-duplicate", "worktree:"+pool[i].Path, "treehouse status reports the same physical slot more than once")
			}
		}
	}
	for _, entry := range pool {
		switch entry.Status {
		case "available":
			if entry.LeaseID != "" || entry.LeaseHolder != "" {
				addBlocker("treehouse-available-slot-has-lease-metadata", "worktree:"+entry.Path, fmt.Sprintf("lease_id=%q lease_holder=%q", entry.LeaseID, entry.LeaseHolder))
			}
		case "leased":
			if !legacyV18CutoverForeignTreehouseLease(entry, fleetID) {
				addBlocker("treehouse-live-or-unknown-lease", "worktree:"+entry.Path, fmt.Sprintf("lease_id=%q lease_holder=%q", entry.LeaseID, entry.LeaseHolder))
			}
		default:
			addBlocker("treehouse-pool-state-unknown", "worktree:"+entry.Path, fmt.Sprintf("status=%q", entry.Status))
		}
	}

	for _, expected := range recorded {
		observation := deps.observeLease(project.ClonePath, expected.WorktreePath, expected.LeaseID)
		switch observation.State {
		case worktree.LeaseAbsent:
		case worktree.LeaseMismatch, worktree.LeaseUnprovable:
			entry, ok := legacyV18CutoverPoolEntry(pool, expected.WorktreePath)
			if !ok || entry.Status != "leased" || !legacyV18CutoverForeignTreehouseLease(entry, fleetID) {
				addBlocker("treehouse-recorded-lease-unresolved", "attempt:"+fmt.Sprintf("%d", expected.AttemptID), fmt.Sprintf("state=%q observed_lease=%q", observation.State, observation.LeaseID))
			}
		case worktree.LeaseExact:
			addBlocker("treehouse-recorded-lease-live", "attempt:"+fmt.Sprintf("%d", expected.AttemptID), "recorded Treehouse lease is still live")
		case worktree.LeaseUnknown:
			addBlocker("treehouse-recorded-lease-unknown", "attempt:"+fmt.Sprintf("%d", expected.AttemptID), observation.Probe.Reason)
		default:
			addBlocker("treehouse-recorded-lease-unknown", "attempt:"+fmt.Sprintf("%d", expected.AttemptID), fmt.Sprintf("state=%q", observation.State))
		}
	}

	roots := deps.poolSearchRoots(homeDir, project.ClonePath)
	slots, err := deps.discoverPoolSlots(project.ClonePath, roots...)
	if err != nil {
		addBlocker("treehouse-inventory-unobservable", subject, err.Error())
		return
	}
	for _, collision := range deps.poolSlotCollisions(slots) {
		paths := make([]string, 0, len(collision))
		for _, slot := range collision {
			paths = append(paths, slot.Path)
		}
		sort.Strings(paths)
		addBlocker("treehouse-slot-collision", subject, strings.Join(paths, ","))
	}
	for _, slot := range slots {
		if _, current := legacyV18CutoverPoolEntry(pool, slot.Path); current {
			continue
		}
		observation := deps.observeLease(project.ClonePath, slot.Path, "")
		if observation.State != worktree.LeaseAbsent {
			addBlocker("treehouse-orphan-slot-unresolved", "worktree:"+slot.Path, fmt.Sprintf("state=%q lease_id=%q", observation.State, observation.LeaseID))
		}
	}
}

func legacyV18CutoverPoolEntry(pool []worktree.PoolEntry, path string) (worktree.PoolEntry, bool) {
	for _, entry := range pool {
		if gitrepo.SamePath(entry.Path, path) {
			return entry, true
		}
	}
	return worktree.PoolEntry{}, false
}

func legacyV18CutoverForeignTreehouseLease(entry worktree.PoolEntry, fleetID string) bool {
	ownerFleet, _, ok := worktree.ParseLeaseHolder(entry.LeaseHolder)
	return ok && ownerFleet != fleetID && entry.LeaseID != ""
}

func validLegacyV18CutoverGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func validateLegacyV18CutoverProjectTreehouseDeps(deps legacyV18CutoverProjectTreehouseDeps) error {
	if deps.resolveRoot == nil || deps.commonDir == nil || deps.isBare == nil || deps.headCommit == nil ||
		deps.poolSearchRoots == nil || deps.discoverPoolSlots == nil || deps.poolSlotCollisions == nil || deps.poolStatus == nil || deps.observeLease == nil {
		return fmt.Errorf("legacy v18 cutover Project/Treehouse observer dependencies are incomplete")
	}
	return nil
}
