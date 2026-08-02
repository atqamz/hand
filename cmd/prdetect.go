package cmd

import (
	"context"
	"time"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/ghutil"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
)

// detectPR looks for a PR on proj's repo whose head ref is t's current branch, for a
// task whose PR was never recorded because a no-mistakes gate opened it directly
// instead of going through hand pr (issue #69). It records what it finds through
// recordPR, so a detected PR is subject to the exact same conflict guard and repo
// check hand pr itself enforces, and reuses MergeAnnounced (pr_merged_observed) for
// "already merged, not by hand merge" rather than inventing a new field - the same
// meaning the watcher's own gh poll gives that field for an externally merged PR.
//
// The dashboard's PR column is filled from here too, so a detected PR reaches both
// halves every other recording path writes and data/dashboard.md never disagrees
// with task state. Unlike hand pr, which exists to record a PR and exits non-zero
// when only the dashboard half lands, this write is best-effort: detection is a
// side effect of commands that were asked for something else, the URL is on the
// task either way, and hand pr stays the explicit repair for a stale column.
//
// Called only where t.PR == "" already; a task with a PR on record already answers
// this question and never reaches here.
func detectPR(ctx context.Context, home string, t state.Task, proj project.Project) (state.Task, error) {
	branch, err := currentBranch(t.Worktree)
	if err != nil {
		return t, err
	}
	repoSlug, err := project.RepoSlug(home, proj)
	if err != nil {
		return t, err
	}
	url, merged, found, err := ghutil.FindPRByBranch(ctx, repoSlug, branch)
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
	_ = dashboard.Update(home, dashboard.UpdateOpts{})
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
