package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

type lifecyclePhase string

const (
	phaseAttemptCreated        lifecyclePhase = "attempt-created"
	phaseWorktreeRecorded      lifecyclePhase = "worktree-recorded"
	phaseHerdrRecorded         lifecyclePhase = "herdr-recorded"
	phaseLaunchSubmitted       lifecyclePhase = "launch-submitted"
	phaseLaunchConfirmed       lifecyclePhase = "launch-confirmed"
	phaseScoutPaneReleased     lifecyclePhase = "scout-pane-released"
	phaseScoutWorktreeReturned lifecyclePhase = "scout-worktree-returned"
	phaseCompletionAppended    lifecyclePhase = "completion-appended"
	phaseWorktreeReturned      lifecyclePhase = "worktree-returned"
)

type worktreeDependencies struct {
	get            func(string, string) (worktree.Lease, error)
	returnWorktree func(string, bool) error
	checkCollision func(string, worktree.Lease, string) (string, error)
}

type dependencies struct {
	now              func() time.Time
	herdr            func() herdrClient
	worktree         worktreeDependencies
	buildHarness     func(string, harness.Options) (string, error)
	confirmLaunch    func(herdrClient, string, string) error
	appendCompletion func(string, completion.Record) error
	phase            func(lifecyclePhase) error
}

type Runtime struct{ deps dependencies }

func New() *Runtime { return &Runtime{deps: defaultDependencies()} }

func defaultDependencies() dependencies {
	return dependencies{
		now:              func() time.Time { return time.Now().UTC() },
		herdr:            newHerdrClient,
		worktree:         worktreeDependencies{get: worktree.Get, returnWorktree: worktree.Return, checkCollision: worktree.CheckCollision},
		buildHarness:     harness.Build,
		confirmLaunch:    confirmLaunch,
		appendCompletion: completion.Append,
		phase:            func(lifecyclePhase) error { return nil },
	}
}

type provisioningRequest struct {
	home                string
	projectName         string
	clonePath           string
	briefPath           string
	harness             string
	model               string
	effort              string
	briefHasFrontMatter bool
	attempt             state.Attempt
}

func (r *Runtime) provision(ctx context.Context, req provisioningRequest) (string, error) {
	releaseProject, err := state.Lock(req.home, "project:"+req.projectName)
	if err != nil {
		return "", fmt.Errorf("lock project %q: %w", req.projectName, err)
	}
	defer releaseProject()
	return r.provisionLocked(ctx, req)
}

func (r *Runtime) provisionLocked(ctx context.Context, req provisioningRequest) (string, error) {
	_ = ctx
	lease, err := r.deps.worktree.get(req.clonePath, "hand:"+req.attempt.TaskID)
	if err != nil {
		return "", fmt.Errorf("acquire treehouse worktree: %w", err)
	}
	worktreePath := lease.Path
	if err := state.RecordAttemptWorktree(req.home, req.attempt.TaskID, req.attempt.ID, worktreePath, lease.ID); err != nil {
		return "", reportCleanup(fmt.Errorf("record worktree ownership: %w", err), r.deps.worktree.returnWorktree(worktreePath, true))
	}
	if err := r.afterPhase(phaseWorktreeRecorded); err != nil {
		return "", r.failProvision(req, worktreePath, nil, false, err)
	}

	releaseWorktree, err := state.Lock(req.home, "worktree:"+worktreePath)
	if err != nil {
		return "", r.failProvision(req, worktreePath, nil, false, fmt.Errorf("lock worktree %q: %w", worktreePath, err))
	}
	defer releaseWorktree()

	conflict, err := r.deps.worktree.checkCollision(req.home, lease, req.attempt.TaskID)
	if err != nil {
		return "", r.failProvision(req, worktreePath, nil, false, err)
	}
	if conflict != "" {
		err := Precondition(fmt.Errorf("worktree collision: %s already holds %s", conflict, worktreePath))
		return "", r.failProvision(req, worktreePath, nil, false, err)
	}

	client := r.deps.herdr()
	workspace, tab, pane, rollback, err := acquireTaskWorkspace(client, worktreePath, req.attempt.TaskID, req.projectName)
	if err != nil {
		return "", r.failProvision(req, worktreePath, nil, false, err)
	}
	herdrRecorded := false
	fail := func(cause error) (string, error) {
		return "", r.failProvision(req, worktreePath, rollback, herdrRecorded, cause)
	}

	startedAt := r.deps.now().Format(time.RFC3339)
	if err := state.RecordAttemptHerdr(req.home, req.attempt.TaskID, req.attempt.ID, state.Herdr{
		Session: "default", WorkspaceID: workspace.WorkspaceID, TabID: tab.TabID, PaneID: pane.PaneID,
	}, startedAt); err != nil {
		return fail(fmt.Errorf("record Herdr ownership: %w", err))
	}
	herdrRecorded = true
	if err := r.afterPhase(phaseHerdrRecorded); err != nil {
		return fail(err)
	}

	launchCommand, err := r.deps.buildHarness(req.harness, harness.Options{
		Worktree: worktreePath, Brief: req.briefPath, FleetHome: req.home, Model: req.model, Effort: req.effort,
		BriefHasFrontMatter: req.briefHasFrontMatter,
	})
	if err != nil {
		return fail(err)
	}
	if err := client.PaneRun(pane.PaneID, launchCommand); err != nil {
		return fail(fmt.Errorf("send launch command failed: %w", err))
	}
	if err := state.MarkLaunchSubmitted(req.home, req.attempt.TaskID, req.attempt.ID, r.deps.now().Format(time.RFC3339)); err != nil {
		return fail(fmt.Errorf("record launch submission: %w", err))
	}
	if err := r.afterPhase(phaseLaunchSubmitted); err != nil {
		return fail(err)
	}

	if err := r.deps.confirmLaunch(client, pane.PaneID, req.harness); err != nil {
		return fail(fmt.Errorf("confirm worker started: %w", err))
	}
	if err := state.MarkLaunchConfirmed(req.home, req.attempt.TaskID, req.attempt.ID, r.deps.now().Format(time.RFC3339)); err != nil {
		return fail(fmt.Errorf("record launch confirmation: %w", err))
	}
	if err := r.afterPhase(phaseLaunchConfirmed); err != nil {
		return fail(err)
	}
	if err := state.MarkAttemptRunning(req.home, req.attempt.TaskID, req.attempt.ID); err != nil {
		return fail(fmt.Errorf("record running attempt: %w", err))
	}
	return worktreePath, nil
}

func (r *Runtime) afterPhase(phase lifecyclePhase) error { return r.deps.phase(phase) }

func (r *Runtime) failProvision(req provisioningRequest, worktreePath string, rollback func() error, herdrRecorded bool, cause error) error {
	var cleanup []error
	if rollback != nil {
		if err := rollback(); err != nil {
			cleanup = append(cleanup, err)
		} else if herdrRecorded {
			if err := state.ClearAttemptHerdr(req.home, req.attempt.TaskID, req.attempt.ID); err != nil {
				cleanup = append(cleanup, err)
			}
		}
	}
	if worktreePath != "" {
		if err := r.returnProvisioningWorktree(req.home, req.attempt.TaskID, req.attempt.ID, worktreePath); err != nil {
			cleanup = append(cleanup, err)
		}
	}
	return reportCleanup(cause, cleanup...)
}

func (r *Runtime) returnProvisioningWorktree(home, taskID string, attemptID int64, path string) error {
	if err := r.deps.worktree.returnWorktree(path, true); err != nil {
		return err
	}
	if err := state.ClearAttemptWorktree(home, taskID, attemptID); err != nil {
		return fmt.Errorf("clear returned worktree evidence: %w", err)
	}
	return nil
}
