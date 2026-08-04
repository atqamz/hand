package cmd

import (
	"context"
	"strings"
	"time"

	"github.com/atqamz/secondhand/internal/ghutil"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
)

// detectPR looks for a PR on proj's repo, and on its declared upstream when it has
// one, whose head ref is t's current branch, for a
// task whose PR was never recorded because a no-mistakes gate opened it directly
// instead of going through hand pr (issue #69). It records what it finds through
// recordPR, so a detected PR is subject to the exact same conflict guard and repo
// check hand pr itself enforces, and reuses MergeAnnounced (pr_merged_observed) for
// "already merged, not by hand merge" rather than inventing a new field - the same
// meaning the watcher's own gh poll gives that field for an externally merged PR.
//
// Called only where t.PR == "" already; a task with a PR on record already answers
// this question and never reaches here.
//
// A fork project's PR lives on the upstream while hand pushes the branch to the
// fork (atqamz/secondhand#78), so the upstream is searched too, restricted to head
// refs in the fork - the upstream also carries same-named branches from every other
// contributor's fork. A PR matching in both repos is ambiguous, resolved by
// FindPRByBranch's own tier rule rather than by preferring either repo.
func detectPR(ctx context.Context, home string, t state.Task, proj project.Project) (state.Task, error) {
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
		targets = append(targets, ghutil.PRSearchTarget{Repo: proj.Upstream, HeadRepo: repoSlug})
	}
	url, merged, found, err := ghutil.FindPRByBranch(ctx, branch, targets...)
	if err != nil {
		return t, err
	}
	if !found {
		return t, nil
	}
	if merged {
		t.MergeAnnounced = true
	}
	updated, _, err := recordPR(ctx, home, t, url)
	if err != nil {
		return t, err
	}
	return updated, nil
}

// detectPRForStatus is detectPR made safe for a read command: hand status holds no
// lock on the task, so it takes its own non-blocking one - mirroring the watcher's
// own recordAutoPR - and re-reads the task under it in case a concurrent hand pr or
// teardown recorded a PR first. It never fails the command: a task with no branch,
// an unregistered or local-only project, a lock held elsewhere, a branch whose PRs
// are ambiguous, or a failed gh call all just leave t as read, so a forge round trip
// on an already-recorded PR is the only cost this can ever add, and only that task
// pays it once. Unlike hand teardown's landed-work guard, nothing here is gated on
// the answer, so an ambiguous branch degrades like any other detection failure
// instead of refusing. A scout task never answers for a PR - its deliverable is
// data/<id>/report.md - so it skips the lookup entirely, the same short-circuit
// checkLandedWork opens with.
func detectPRForStatus(ctx context.Context, home string, t state.Task) state.Task {
	if t.PR != "" || t.Kind == state.KindScout {
		return t
	}
	proj, exists, err := project.Find(home, t.Project)
	if err != nil || !exists || proj.Mode == project.ModeLocalOnly {
		return t
	}

	unlock, err := state.TryLock(home, "task:"+t.ID)
	if err != nil {
		return t
	}
	defer unlock()

	fresh, err := state.Read(home, t.ID)
	if err != nil {
		return t
	}
	if fresh.PR != "" {
		return fresh
	}

	ghCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	detected, err := detectPR(ghCtx, home, fresh, proj)
	if err != nil {
		return t
	}
	return detected
}
