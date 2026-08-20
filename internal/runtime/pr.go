package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
)

func DetectPR(ctx context.Context, homeDir string, task state.Task, active state.Attempt, projectInfo project.Project) (state.Task, ghutil.PRObservation, error) {
	observation := observePR(ctx, homeDir, active, projectInfo)
	if !observation.Found() {
		return task, observation, nil
	}
	if observation.Merged {
		task.MergeAnnounced = true
	}
	updated, _, err := RecordPR(ctx, homeDir, task, observation.URL)
	if err != nil {
		return task, observation, err
	}
	return updated, observation, nil
}

// The read-only detector answers what GitHub reports without writing task.PR or task.MergeAnnounced, unlike
// DetectPR: a rendering caller must never receive a task shaped as though the durable record already
// carried a value only this observation found (ADR attention-is-one-derivation..., invariant 1).
func DetectPRReadOnly(ctx context.Context, homeDir string, active state.Attempt, projectInfo project.Project) ghutil.PRObservation {
	return observePR(ctx, homeDir, active, projectInfo)
}

// Live worktree state wins when it is determinable, since it is more current than what attempt
// creation recorded; the durably stored branch is the fallback, since a detached or torn-down
// worktree cannot answer at all - not evidence that no branch, and so no PR, ever existed.
func observePR(ctx context.Context, homeDir string, active state.Attempt, projectInfo project.Project) ghutil.PRObservation {
	branch, liveErr := currentBranch(active.Worktree)
	if liveErr != nil {
		branch = active.Branch
	}
	if branch == "" {
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

func RecordPR(ctx context.Context, homeDir string, task state.Task, url string) (state.Task, bool, error) {
	if !state.ValidatePRURL(url) {
		return task, false, Usage(fmt.Errorf("invalid PR URL %q: must match https://github.com/<owner>/<repo>/pull/<number>", url))
	}
	if task.PR != "" && task.PR != url {
		return task, false, Precondition(fmt.Errorf("task %s already has a different PR recorded: %s", task.ID, task.PR))
	}
	if task.PR == url {
		return task, true, nil
	}
	if task.Lifecycle == state.TaskTerminal {
		return task, false, Precondition(fmt.Errorf("task %s is torn down; run hand reopen %s before recording a PR on it", task.ID, task.ID))
	}
	projectInfo, exists, err := project.Find(homeDir, task.Project)
	if err != nil {
		return task, false, err
	}
	if !exists {
		return task, false, Precondition(fmt.Errorf("project %q not registered", task.Project))
	}
	ghCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := project.ValidatePR(ghCtx, homeDir, projectInfo, url); err != nil {
		return task, false, Precondition(err)
	}
	if err := state.SetTaskPR(homeDir, task.ID, url); err != nil {
		return task, false, fmt.Errorf("write task state: %w", err)
	}
	task.PR = url
	return task, false, nil
}
