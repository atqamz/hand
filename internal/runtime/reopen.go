package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
)

func (r *Runtime) Reopen(ctx context.Context, req ReopenRequest) (Result, error) {
	release, err := state.Lock(req.Home, "task:"+req.ID)
	if err != nil {
		return Result{}, fmt.Errorf("lock task %q: %w", req.ID, err)
	}
	defer release()

	history, err := state.ReadHistory(req.Home, req.ID)
	if err != nil {
		if errors.Is(err, state.ErrTaskNotFound) {
			return Result{}, Precondition(err)
		}
		return Result{}, err
	}
	if history.Task.Lifecycle != state.TaskTerminal {
		return Result{}, Precondition(fmt.Errorf("task %q is already open", req.ID))
	}
	if held, hasHold, err := state.ReadHold(req.Home, req.ID); err != nil {
		return Result{}, err
	} else if hasHold {
		return Result{}, Precondition(fmt.Errorf("id %q has an open hold (%s: %s); clear it first: hand hold clear %s", req.ID, held.Kind, held.Reason, req.ID))
	}

	task := history.Task
	projectInfo, exists, err := project.Find(req.Home, task.Project)
	if err != nil {
		return Result{}, err
	}
	if !exists {
		return Result{}, Precondition(fmt.Errorf("project %q not registered", task.Project))
	}
	warnings, err := r.gatePreflight(projectInfo, filepath.Join(req.Home, "projects", projectInfo.Name), req.SkipGateCheck)
	if err != nil {
		return Result{}, err
	}
	fail := func(err error) (Result, error) { return Result{}, WithWarnings(err, warnings) }

	briefRel := filepath.Join("data", req.ID, "brief.md")
	briefPath := filepath.Join(req.Home, briefRel)
	if _, err := os.Stat(briefPath); err != nil {
		return fail(Precondition(fmt.Errorf("brief not found at %s", briefRel)))
	}
	harnessName, err := requestedHarness(req.Harness, req.HarnessFromFlag)
	if err != nil {
		return fail(err)
	}
	tier, err := ResolveTier(req.Home, briefPath, harnessName, req.Model, req.Effort)
	if err != nil {
		return fail(classifyTierError(err))
	}
	warnings = append(warnings, tier.Warnings...)
	clonePath := filepath.Join(req.Home, "projects", projectInfo.Name)
	var releaseProject func()
	if tier.ExecutionClass == brief.ExecutionClassMechanical {
		releaseProject, err = state.Lock(req.Home, "project:"+projectInfo.Name)
		if err != nil {
			return fail(fmt.Errorf("lock project %q: %w", projectInfo.Name, err))
		}
		defer releaseProject()
		if err := r.preflightTier(tier, clonePath); err != nil {
			return fail(err)
		}
	}

	createdAt := r.deps.now().Format(time.RFC3339)
	attempt, err := state.ReopenTask(req.Home, state.Attempt{
		TaskID: req.ID, Lifecycle: state.AttemptProvisioning, Harness: harnessName, Model: tier.Model, Effort: tier.Effort, CreatedAt: createdAt,
	})
	if err != nil {
		return fail(fmt.Errorf("write reopened provisioning state: %w", err))
	}
	if err := r.afterPhase(phaseAttemptCreated); err != nil {
		return fail(err)
	}

	provisionRequest := provisioningRequest{
		home: req.Home, projectName: projectInfo.Name, clonePath: clonePath, briefPath: briefPath,
		harness: harnessName, model: tier.Model, effort: tier.Effort, executionClass: tier.ExecutionClass, plannedAgainst: tier.PlannedAgainst,
		briefHasFrontMatter: tier.BriefHasFrontMatter, attempt: attempt,
	}
	var worktreePath string
	if releaseProject != nil {
		worktreePath, err = r.provisionLocked(ctx, provisionRequest)
	} else {
		worktreePath, err = r.provision(ctx, provisionRequest)
	}
	if err != nil {
		return fail(err)
	}
	return Result{
		ID: req.ID, Attempt: "new", Project: task.Project, Kind: task.Kind, Harness: harnessName, Worktree: worktreePath,
		Warnings: warnings, Help: []string{"Run `hand status " + req.ID + "` to read this attempt"},
	}, nil
}
