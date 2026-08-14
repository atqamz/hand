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

func DetectPR(ctx context.Context, homeDir string, task state.Task, active state.Attempt, projectInfo project.Project) (state.Task, error) {
	branch, err := currentBranch(active.Worktree)
	if err != nil {
		return task, err
	}
	repoSlug, err := project.RepoSlug(homeDir, projectInfo)
	if err != nil {
		return task, err
	}
	targets := []ghutil.PRSearchTarget{{Repo: repoSlug}}
	if projectInfo.Upstream != "" && !strings.EqualFold(projectInfo.Upstream, repoSlug) {
		targets = append(targets, ghutil.PRSearchTarget{Repo: projectInfo.Upstream, HeadRepo: repoSlug})
	}
	url, merged, found, err := ghutil.FindPRByBranch(ctx, branch, targets...)
	if err != nil {
		return task, err
	}
	if !found {
		return task, nil
	}
	if merged {
		task.MergeAnnounced = true
	}
	updated, _, err := RecordPR(ctx, homeDir, task, url)
	if err != nil {
		return task, err
	}
	return updated, nil
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
