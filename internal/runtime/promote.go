package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

func (r *Runtime) Promote(ctx context.Context, req PromoteRequest) (Result, error) {
	history, err := state.ReadHistoryReadOnly(req.Home, req.ID)
	if err != nil {
		if errors.Is(err, state.ErrTaskNotFound) {
			return Result{}, Precondition(err)
		}
		return Result{}, err
	}
	if history.ActiveAttempt == nil {
		return Result{}, Precondition(fmt.Errorf("task %q has no active scout attempt", req.ID))
	}
	task := history.Task
	scout := *history.ActiveAttempt
	if task.Kind != state.KindScout {
		return Result{}, Precondition(fmt.Errorf("task %q is not a scout", req.ID))
	}
	reportRel := filepath.Join("data", req.ID, "report.md")
	if _, err := os.Stat(filepath.Join(req.Home, reportRel)); err != nil {
		return Result{}, Precondition(fmt.Errorf("scout report not found at %s", reportRel))
	}
	client := r.deps.herdr()
	status := herdr.StatusUnknown
	if scout.Herdr.PaneID != "" {
		if pane, err := client.PaneGet(scout.Herdr.PaneID); err == nil && pane.AgentStatus != "" {
			status = pane.AgentStatus
		}
	}
	if !status.NotBusy() && status != herdr.StatusUnknown {
		return Result{}, Precondition(fmt.Errorf("task %q is not a completed scout (agent state: %s)", req.ID, status))
	}

	briefRel := filepath.Join("data", req.ID, "brief.md")
	briefPath := filepath.Join(req.Home, briefRel)
	if _, err := os.Stat(briefPath); err != nil {
		return Result{}, Precondition(fmt.Errorf("brief not found at %s", briefRel))
	}
	projectInfo, exists, err := project.FindReadOnly(req.Home, task.Project)
	if err != nil {
		return Result{}, err
	}
	if !exists {
		return Result{}, Precondition(fmt.Errorf("project %q not registered", task.Project))
	}
	clonePath := filepath.Join(req.Home, "projects", projectInfo.Name)
	warnings, err := r.gatePreflight(projectInfo, clonePath, req.SkipGateCheck)
	if err != nil {
		return Result{}, err
	}
	fail := func(err error) (Result, error) { return Result{}, WithWarnings(err, warnings) }
	route, err := resolveExecution(req.Home, briefPath, state.KindShip, req.Profile, req.ProfileFromFlag, req.Harness, req.HarnessFromFlag, req.Model, req.ModelFromFlag, req.Effort, req.EffortFromFlag)
	if err != nil {
		return fail(err)
	}
	warnings = append(warnings, route.Warnings...)
	history, err = state.ReadHistory(req.Home, req.ID)
	if err != nil {
		return fail(err)
	}
	if history.Task.Kind != state.KindScout || history.ActiveAttempt == nil || history.Task.Project != task.Project || history.Task.Kind != task.Kind || history.ActiveAttempt.ID != scout.ID || history.ActiveAttempt.Lifecycle != scout.Lifecycle {
		return fail(Precondition(fmt.Errorf("task %q changed while preparing promotion; retry", req.ID)))
	}
	task = history.Task
	scout = *history.ActiveAttempt
	projectInfo, exists, err = project.Find(req.Home, task.Project)
	if err != nil {
		return fail(err)
	}
	if !exists {
		return fail(Precondition(fmt.Errorf("project %q not registered", task.Project)))
	}
	release, err := state.Lock(req.Home, "task:"+req.ID)
	if err != nil {
		return Result{}, fmt.Errorf("lock task %q: %w", req.ID, err)
	}
	defer release()
	latest, err := state.ReadHistory(req.Home, req.ID)
	if err != nil {
		return Result{}, err
	}
	if latest.Task.Kind != state.KindScout || latest.ActiveAttempt == nil {
		return Result{}, Precondition(fmt.Errorf("task %q changed while preparing promotion; retry", req.ID))
	}
	if latest.ActiveAttempt.ID != scout.ID || latest.ActiveAttempt.Lifecycle != scout.Lifecycle {
		return Result{}, Precondition(fmt.Errorf("task %q scout attempt changed while preparing promotion; retry", req.ID))
	}
	if latest.Task.Project != task.Project || latest.Task.Kind != task.Kind {
		return Result{}, Precondition(fmt.Errorf("task %q changed while preparing promotion; retry", req.ID))
	}
	task = latest.Task
	scout = *latest.ActiveAttempt

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
	shipAttempt, err := state.PromoteTask(req.Home, req.ID, scout.ID, scout.Lifecycle, state.Attempt{
		TaskID: req.ID, Lifecycle: state.AttemptProvisioning, Harness: route.Harness, Model: route.Model, Effort: route.Effort,
		ExecutionClass: string(route.ExecutionClass), PlannedAgainst: route.PlannedAgainst, RequestedProfile: route.Profile, RoutingSource: string(route.Source), CreatedAt: createdAt,
	})
	if err != nil {
		return fail(fmt.Errorf("write promoted provisioning state: %w", err))
	}
	if err := r.afterPhase(phaseAttemptCreated); err != nil {
		return fail(err)
	}
	scout.Lifecycle = state.AttemptCompleted
	if releaseProject == nil {
		releaseProject, err = state.Lock(req.Home, "project:"+projectInfo.Name)
		if err != nil {
			return fail(fmt.Errorf("lock project %q: %w", projectInfo.Name, err))
		}
		defer releaseProject()
	}

	cleanupWarnings, err := r.cleanupScout(req.Home, req.ID, scout)
	if err != nil {
		return Result{}, WithWarnings(err, append(warnings, cleanupWarnings...))
	}
	warnings = append(warnings, cleanupWarnings...)

	worktreePath, err := r.provisionLocked(ctx, provisioningRequest{
		home: req.Home, projectName: projectInfo.Name, clonePath: clonePath, briefPath: briefPath,
		briefHasFrontMatter: route.BriefHasFrontMatter, attempt: shipAttempt,
	})
	if err != nil {
		return fail(err)
	}
	if err := state.ClearHoldIfKind(req.Home, req.ID, state.HoldKindLimit); err != nil {
		warnings = append(warnings, fmt.Sprintf("warning: clear usage-limit hold failed: %v", err))
	}
	return Result{
		ID: req.ID, Project: projectInfo.Name, Kind: state.KindShip, Was: state.KindScout, ExecutionClass: shipAttempt.ExecutionClass, Profile: shipAttempt.RequestedProfile,
		RoutingSource: shipAttempt.RoutingSource, PlannedAgainst: shipAttempt.PlannedAgainst, Harness: shipAttempt.Harness, Model: shipAttempt.Model, Effort: shipAttempt.Effort, Worktree: worktreePath,
		Warnings: warnings,
		Help:     []string{"The scout's worktree and pane are gone; run `hand status " + req.ID + "` to read the ship worker", "The scout's delivery no longer counts for this task, so `hand deliver " + req.ID + "` runs again on the code"},
	}, nil
}

func (r *Runtime) cleanupScout(homeDir, taskID string, scout state.Attempt) ([]string, error) {
	client := r.deps.herdr()
	var warnings []string
	setState := func(resource, next string) error {
		if scout.ID == 0 {
			return nil
		}
		return state.SetAttemptTeardownResourceState(homeDir, taskID, scout.ID, scout.Lifecycle, resource, next)
	}
	if scout.Herdr.WorkspaceID != "" || scout.Herdr.TabID != "" || scout.Herdr.PaneID != "" {
		switch scout.TeardownHerdrState {
		case state.TeardownResourceReleased:
		case state.TeardownResourceReleasing, state.TeardownResourceAmbiguous:
			warnings = append(warnings, fmt.Sprintf("warning: Herdr ownership for attempt %d is ambiguous; refusing destructive retry", scout.ID))
		default:
			if err := incompleteHerdrOwnership(scout.Herdr); err != nil {
				_ = setState("herdr", state.TeardownResourceAmbiguous)
				warnings = append(warnings, fmt.Sprintf("warning: Herdr ownership incomplete: %v", err))
			} else if err := setState("herdr", state.TeardownResourceReleasing); err != nil {
				warnings = append(warnings, fmt.Sprintf("warning: record Herdr release phase failed: %v", err))
			} else if err := closeTaskTab(client, scout.Herdr.WorkspaceID, scout.Herdr.TabID); err != nil {
				_ = setState("herdr", state.TeardownResourceAmbiguous)
				warnings = append(warnings, fmt.Sprintf("warning: herdr tab close failed: %v", err))
			} else if err := setState("herdr", state.TeardownResourceReleased); err != nil {
				warnings = append(warnings, fmt.Sprintf("warning: record Herdr release evidence failed: %v", err))
			}
		}
	}
	if err := r.afterPhase(phaseScoutPaneReleased); err != nil {
		return warnings, err
	}
	if scout.Worktree == "" {
		warnings = append(warnings, "warning: return scout worktree failed: no owned worktree path")
	} else {
		switch scout.TeardownWorktreeState {
		case state.TeardownResourceReleased:
		case state.TeardownResourceReleasing, state.TeardownResourceAmbiguous:
			warnings = append(warnings, fmt.Sprintf("warning: worktree ownership for attempt %d is ambiguous; refusing destructive retry", scout.ID))
		default:
			if err := setState("worktree", state.TeardownResourceReleasing); err != nil {
				warnings = append(warnings, fmt.Sprintf("warning: record scout worktree release phase failed: %v", err))
			} else if err := r.deps.worktree.returnLease(worktree.Lease{Path: scout.Worktree, ID: scout.LeaseID}, true); err != nil {
				_ = setState("worktree", state.TeardownResourceAmbiguous)
				warnings = append(warnings, fmt.Sprintf("warning: return scout worktree failed: %v", err))
			} else if err := setState("worktree", state.TeardownResourceReleased); err != nil {
				warnings = append(warnings, fmt.Sprintf("warning: record scout worktree release evidence failed: %v", err))
			}
		}
	}
	if err := r.afterPhase(phaseScoutWorktreeReturned); err != nil {
		return warnings, err
	}
	return warnings, nil
}
