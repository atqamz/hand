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

func (r *Runtime) Spawn(ctx context.Context, req SpawnRequest) (Result, error) {
	projectInfo, exists, err := project.FindReadOnly(req.Home, req.Project)
	if err != nil {
		return Result{}, err
	}
	if !exists {
		return Result{}, Precondition(fmt.Errorf("project %q not registered", req.Project))
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
	kind := req.Kind
	if kind == "" {
		kind = state.KindShip
	}
	route, err := resolveExecution(req.Home, briefPath, kind, req.Profile, req.ProfileFromFlag, req.Harness, req.HarnessFromFlag, req.Model, req.ModelFromFlag, req.Effort, req.EffortFromFlag)
	if err != nil {
		return fail(err)
	}
	warnings = append(warnings, route.Warnings...)
	projectInfo, exists, err = project.Find(req.Home, req.Project)
	if err != nil {
		return fail(err)
	}
	if !exists {
		return fail(Precondition(fmt.Errorf("project %q not registered", req.Project)))
	}

	releaseClaim, err := state.Claim(req.Home, req.ID)
	if err != nil {
		if errors.Is(err, state.ErrTaskActive) {
			return fail(Precondition(err))
		}
		return fail(err)
	}
	defer releaseClaim()

	held, hasHold, err := state.ReadHold(req.Home, req.ID)
	if err != nil {
		return fail(err)
	}
	if hasHold {
		return fail(Precondition(fmt.Errorf("id %q has an open hold (%s: %s); clear it first: hand hold clear %s", req.ID, held.Kind, held.Reason, req.ID)))
	}

	clonePath := filepath.Join(req.Home, "projects", projectInfo.Name)
	var releaseProject func()
	if route.ExecutionClass == brief.ExecutionClassMechanical {
		releaseProject, err = state.Lock(req.Home, "project:"+projectInfo.Name)
		if err != nil {
			return fail(fmt.Errorf("lock project %q: %w", projectInfo.Name, err))
		}
		defer releaseProject()
		if err := r.preflightExecution(route, clonePath); err != nil {
			return fail(err)
		}
	}

	createdAt := r.deps.now().Format(time.RFC3339)
	attempt, err := state.CreateTaskWithAttempt(req.Home, state.Task{
		ID: req.ID, Project: projectInfo.Name, Kind: kind, Brief: briefRel, Lifecycle: state.TaskOpen, CreatedAt: createdAt,
	}, state.Attempt{
		TaskID: req.ID, Lifecycle: state.AttemptProvisioning, Harness: route.Harness, Model: route.Model, Effort: route.Effort,
		ExecutionClass: string(route.ExecutionClass), PlannedAgainst: route.PlannedAgainst, RequestedProfile: route.Profile, RoutingSource: string(route.Source), CreatedAt: createdAt,
	})
	if err != nil {
		return fail(fmt.Errorf("write provisioning state: %w", err))
	}
	if err := r.afterPhase(phaseAttemptCreated); err != nil {
		return fail(err)
	}

	provisionRequest := provisioningRequest{
		home: req.Home, projectName: projectInfo.Name, clonePath: clonePath, briefPath: briefPath,
		briefHasFrontMatter: route.BriefHasFrontMatter, attempt: attempt,
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
		ID: req.ID, Project: projectInfo.Name, Kind: kind, ExecutionClass: attempt.ExecutionClass, Profile: attempt.RequestedProfile,
		RoutingSource: attempt.RoutingSource, PlannedAgainst: attempt.PlannedAgainst, Harness: attempt.Harness, Model: attempt.Model, Effort: attempt.Effort, Worktree: worktreePath,
		Warnings: warnings, Help: []string{"Run `hand status " + req.ID + "` to read what this worker reports", "Run `hand send " + req.ID + " <message>` to steer it"},
	}, nil
}
