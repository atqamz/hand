package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

func DetectPR(ctx context.Context, homeDir string, task state.Task, active state.Attempt, projectInfo project.Project) (state.Task, ghutil.PRObservation, error) {
	observation := observePR(ctx, homeDir, task.PR, active, projectInfo)
	if !observation.Found() {
		return task, observation, nil
	}
	if observation.Merged {
		task.MergeAnnounced = true
	}
	// Never crossRepo: re-detection follows the branch hand itself pushed, so anything it finds
	// already belongs to the project's own repo or its declared upstream (atqamz/hand#423 is
	// asserted only through hand pr's explicit opt-in, never discovered here).
	updated, _, err := RecordPR(ctx, homeDir, task, observation.URL, false, "")
	if err != nil {
		return task, observation, err
	}
	return updated, observation, nil
}

// The read-only detector answers what GitHub reports without writing task.PR or task.MergeAnnounced, unlike
// DetectPR: a rendering caller must never receive a task shaped as though the durable record already
// carried a value only this observation found (ADR attention-is-one-derivation..., invariant 1).
func DetectPRReadOnly(ctx context.Context, homeDir string, active state.Attempt, projectInfo project.Project) ghutil.PRObservation {
	return observePR(ctx, homeDir, "", active, projectInfo)
}

// A recorded PR is stronger than branch-based re-detection: it was already validated and survives
// a detached or torn-down worktree. Read-only callers without the task record pass an empty PR.
func observePR(ctx context.Context, homeDir, recordedPR string, active state.Attempt, projectInfo project.Project) ghutil.PRObservation {
	if recordedPR != "" {
		return ghutil.PRObservation{State: ghutil.ObservationFound, URL: recordedPR}
	}

	// Live worktree state wins when it is determinable, since it is more current than what attempt
	// creation recorded; the durably stored branch is the fallback, since a torn-down worktree cannot
	// answer at all - not evidence that no branch, and so no PR, ever existed.
	branch := active.Branch
	var liveErr error
	if active.Worktree != "" {
		branch, liveErr = currentBranch(active.Worktree)
		if liveErr != nil {
			branch = active.Branch
		}
	}
	if branch == "" {
		if active.Worktree != "" {
			if observation, proven := observeDetachedHeadAbsence(active.Worktree); proven {
				return observation
			}
		}
		return ghutil.UnknownPRObservation("git symbolic-ref --short -q HEAD", fmt.Sprintf("resolve the branch to search for in %s: %v", active.Worktree, liveErr))
	}
	repoSlug, err := project.RepoSlug(homeDir, projectInfo)
	if err != nil {
		return ghutil.UnknownPRObservation("git config --get remote.origin.url", fmt.Sprintf("resolve the repo to search: %v", err))
	}
	targets := []ghutil.PRSearchTarget{{Repo: repoSlug}}
	if projectInfo.Upstream != "" && !strings.EqualFold(projectInfo.Upstream, repoSlug) {
		targets = append(targets, ghutil.PRSearchTarget{Repo: projectInfo.Upstream, HeadRepo: repoSlug})
	}
	return ghutil.ObservePRByBranch(ctx, branch, targets...)
}

// A detached, branchless worktree disproves a pull request outright, rather than merely failing to
// name one to search for, once no branch was ever pushed to open it from and no commit here could
// have reached GitHub except through one (atqamz/hand#428). proven is false on any inconclusive read.
func observeDetachedHeadAbsence(worktreePath string) (observation ghutil.PRObservation, proven bool) {
	detached, err := git.IsDetachedHead(worktreePath)
	if err != nil || !detached {
		return ghutil.PRObservation{}, false
	}
	localOnly, err := worktree.LocalOnlyCommitCount(worktreePath)
	if err != nil || localOnly != 0 {
		return ghutil.PRObservation{}, false
	}
	return ghutil.PRObservation{State: ghutil.ObservationAbsent, Probe: ghutil.Probe{
		Command: "git symbolic-ref -q HEAD; " + worktree.LocalOnlyCommitCommand,
		Reason:  "HEAD is detached, no branch was ever recorded for this attempt, and the worktree holds no commit missing from a remote-tracking ref, so no branch could have opened a pull request and no commit could have reached GitHub except through one",
	}}, true
}

// crossRepo is hand pr's --cross-repo opt-in (atqamz/hand#423). reason is required whenever
// crossRepo is true - an unexplained cross-repo record could never be reconstructed later.
func RecordPR(ctx context.Context, homeDir string, task state.Task, url string, crossRepo bool, reason string) (state.Task, bool, error) {
	if !state.ValidatePRURL(url) {
		return task, false, Usage(fmt.Errorf("invalid PR URL %q: must match https://github.com/<owner>/<repo>/pull/<number>", url))
	}
	if crossRepo && reason == "" {
		return task, false, Usage(fmt.Errorf("--cross-repo requires --reason describing the deliberate delivery elsewhere"))
	}
	if task.PR != "" && task.PR != url {
		return task, false, Precondition(fmt.Errorf("task %s already has a different PR recorded: %s", task.ID, task.PR))
	}
	if task.PR == url {
		return task, true, nil
	}
	// A torn-down task may still record where its work landed (atqamz/hand#424): this only supplies
	// evidence, write-once and validated like every other call, and never reopens or reactivates the
	// task - state.SetTaskPR touches no lifecycle column.
	projectInfo, exists, err := project.Find(homeDir, task.Project)
	if err != nil {
		return task, false, err
	}
	if !exists {
		return task, false, Precondition(fmt.Errorf("project %q not registered", task.Project))
	}
	ghCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := project.ValidatePR(ghCtx, homeDir, projectInfo, url, crossRepo); err != nil {
		return task, false, Precondition(err)
	}
	if crossRepo {
		if err := state.SetTaskPRCrossRepo(homeDir, task.ID, url, reason); err != nil {
			return task, false, fmt.Errorf("write task state: %w", err)
		}
		task.PRCrossRepoReason = reason
	} else if err := state.SetTaskPR(homeDir, task.ID, url); err != nil {
		return task, false, fmt.Errorf("write task state: %w", err)
	}
	task.PR = url
	return task, false, nil
}
