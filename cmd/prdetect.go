package cmd

import (
	"context"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
)

// Looks for a PR whose head ref is t's current branch, for a task whose PR was never recorded because a
// no-mistakes gate opened it directly instead of going through hand pr (atqamz/hand#69). Called only
// where t.PR == "" already, so a task with a PR on record never reaches here.
func detectPR(ctx context.Context, home string, t state.Task, proj project.Project) (state.Task, error) {
	if active, err := state.ActiveAttempt(home, t.ID); err == nil {
		t.Worktree = active.Worktree
	}
	branch, err := currentBranch(t.Worktree)
	if err != nil {
		return t, err
	}
	repoSlug, err := project.RepoSlug(home, proj)
	if err != nil {
		return t, err
	}
	targets := []ghutil.PRSearchTarget{{Repo: repoSlug}}
	// EqualFold, not ==: GitHub slugs are case-insensitive, so an upstream declared
	// as the project's own repo in different casing would otherwise be searched
	// twice and make every PR its own same-tier duplicate.
	if proj.Upstream != "" && !strings.EqualFold(proj.Upstream, repoSlug) {
		// A fork project's PR lives on the upstream while hand pushes the branch to the fork
		// (atqamz/hand#78), so the upstream is searched too, restricted to head refs in the fork - it
		// also carries same-named branches from every other contributor's fork.
		targets = append(targets, ghutil.PRSearchTarget{Repo: proj.Upstream, HeadRepo: repoSlug})
	}
	// A PR matching in both repos is ambiguous, resolved by FindPRByBranch's own tier rule rather than by
	// preferring either repo.
	url, merged, found, err := ghutil.FindPRByBranch(ctx, branch, targets...)
	if err != nil {
		return t, err
	}
	if !found {
		return t, nil
	}
	if merged {
		// MergeAnnounced (pr_merged_observed) reused for "already merged, not by hand merge" rather than a new
		// field - the same meaning the watcher's own gh poll gives it for an externally merged PR.
		t.MergeAnnounced = true
	}
	// Recorded through recordPR, so a detected PR is subject to the exact same conflict guard and repo
	// check hand pr itself enforces.
	updated, _, err := recordPR(ctx, home, t, url)
	if err != nil {
		return t, err
	}
	return updated, nil
}

// detectPR made safe for a read command: it never fails the command, so a task with no branch, an
// unregistered or local-only project, a lock held elsewhere, an ambiguous branch, or a failed gh call all
// just leave t as read.
func detectPRForStatus(ctx context.Context, home string, t state.Task) state.Task {
	// A scout task never answers for a PR - its deliverable is data/<id>/report.md - so it skips the lookup
	// entirely, the same short-circuit checkLandedWork opens with. A forge round trip on an already-recorded
	// PR is the only cost this can ever add, and only that task pays it once.
	if t.PR != "" || t.Kind == state.KindScout {
		return t
	}
	proj, exists, err := project.Find(home, t.Project)
	if err != nil || !exists || proj.Mode == project.ModeLocalOnly {
		return t
	}

	// hand status holds no lock on the task, so it takes its own non-blocking one, mirroring the watcher's
	// own recordAutoPR.
	unlock, err := state.TryLock(home, "task:"+t.ID)
	if err != nil {
		return t
	}
	defer unlock()

	// Re-read under the lock in case a concurrent hand pr or teardown recorded a PR first.
	fresh, err := state.Read(home, t.ID)
	if err != nil {
		return t
	}
	if fresh.PR != "" {
		return fresh
	}

	ghCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Unlike hand teardown's landed-work guard, nothing here is gated on the answer, so an ambiguous branch
	// degrades like any other detection failure instead of refusing.
	detected, err := detectPR(ghCtx, home, fresh, proj)
	if err != nil {
		return t
	}
	return detected
}
