package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/launch"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

type lifecyclePhase string

type launchSpec = launch.LaunchSpec

const (
	phaseAttemptCreated        lifecyclePhase = "attempt-created"
	phaseWorktreeRecorded      lifecyclePhase = "worktree-recorded"
	phaseHerdrRecorded         lifecyclePhase = "herdr-recorded"
	phaseLaunchSubmitted       lifecyclePhase = "launch-submitted"
	phaseLaunchConfirmed       lifecyclePhase = "launch-confirmed"
	phaseHerdrReleased         lifecyclePhase = "herdr-released"
	phaseScoutPaneReleased     lifecyclePhase = "scout-pane-released"
	phaseScoutWorktreeReturned lifecyclePhase = "scout-worktree-returned"
	phaseCompletionAppended    lifecyclePhase = "completion-appended"
	phaseWorktreeReturned      lifecyclePhase = "worktree-returned"
)

type worktreeDependencies struct {
	get            func(string, string) (worktree.Lease, error)
	observeLease   func(string, string) worktree.LeaseObservation
	observeClean   func(string) (worktree.Cleanliness, error)
	observeCommits func(string) worktree.CommitSafetyObservation
	headCommit     func(string) (string, error)
	returnWorktree func(string, bool) error
	returnWithID   func(string, string, bool) error
	checkCollision func(string, worktree.Lease, string) (string, error)
}

type dependencies struct {
	now               func() time.Time
	herdr             func() herdrClient
	herdrFor          func(string) herdrClient
	ensureHerdr       func(context.Context, string) error
	worktree          worktreeDependencies
	projectBaseCommit func(string) (string, error)
	buildHarness      func(string, harness.Options) (launch.LaunchSpec, error)
	confirmLaunch     func(herdrClient, string, string, launch.LaunchSpec) error
	appendCompletion  func(string, completion.Record) error
	prMerged          func(context.Context, string) ghutil.PRObservation
	prHead            func(context.Context, string) ghutil.PRObservation
	branchMerged      func(string, string) (bool, error)
	phase             func(lifecyclePhase) error
}

type Runtime struct{ deps dependencies }

func New() *Runtime {
	deps := defaultDependencies()
	deps.herdrFor = newHerdrSessionClient
	return &Runtime{deps: deps}
}

func defaultDependencies() dependencies {
	return dependencies{
		now:               func() time.Time { return time.Now().UTC() },
		herdr:             newHerdrClient,
		ensureHerdr:       func(ctx context.Context, fleetID string) error { return herdr.NewFleetHerdr(fleetID).Ensure(ctx) },
		worktree:          worktreeDependencies{get: worktree.Get, observeLease: worktree.ObserveLease, observeClean: worktree.ObserveCleanliness, observeCommits: worktree.ObserveCommitSafety, headCommit: worktree.HeadCommit, returnWorktree: worktree.Return, returnWithID: worktree.ReturnLease, checkCollision: worktree.CheckCollision},
		projectBaseCommit: projectBaseCommit,
		buildHarness:      harness.Build,
		confirmLaunch:     confirmLaunch,
		appendCompletion:  completion.Append,
		prMerged:          ghutil.ObserveMergeState,
		prHead:            ghutil.ObserveHeadCommit,
		branchMerged:      branchIsMerged,
		phase:             func(lifecyclePhase) error { return nil },
	}
}

type provisioningRequest struct {
	home                string
	projectName         string
	clonePath           string
	briefPath           string
	briefHasFrontMatter bool
	resumeExisting      bool
	paneStartedAt       string
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
	fleetID, err := state.FleetIDReadOnly(req.home)
	if err != nil {
		return "", fmt.Errorf("read Fleet identity for Herdr session: %w", err)
	}
	session := req.attempt.Herdr.Session
	if session == "" {
		session = herdr.SessionName(fleetID)
	}
	if session == herdr.SessionName(fleetID) && r.deps.ensureHerdr != nil {
		if err := r.deps.ensureHerdr(ctx, fleetID); err != nil {
			return "", fmt.Errorf("ensure Fleet Herdr session: %w", err)
		}
	}
	lease := worktree.Lease{Path: req.attempt.Worktree, ID: req.attempt.LeaseID}
	if lease.Path == "" {
		lease, err = r.deps.worktree.get(req.clonePath, "hand:"+fleetID+":"+req.attempt.TaskID)
		if err != nil {
			return "", fmt.Errorf("acquire treehouse worktree: %w", err)
		}
	}
	worktreePath := lease.Path
	if req.attempt.Worktree == "" {
		// Best-effort: an attempt whose branch cannot be determined here simply has none
		// durably recorded, and PR detection falls back to a live re-derivation as before.
		branch, _ := currentBranch(worktreePath)
		if err := state.RecordAttemptWorktree(req.home, req.attempt.TaskID, req.attempt.ID, worktreePath, branch, lease.ID); err != nil {
			return "", reportCleanup(fmt.Errorf("record worktree ownership: %w", err), r.deps.worktree.returnLease(lease, true))
		}
	}
	var releaseWorktree func()
	if brief.ExecutionClass(req.attempt.ExecutionClass) == brief.ExecutionClassMechanical {
		releaseWorktree, err = state.Lock(req.home, "worktree:"+worktreePath)
		if err != nil {
			return "", r.failProvision(req, lease, nil, false, fmt.Errorf("lock worktree %q: %w", worktreePath, err))
		}
		defer releaseWorktree()
		actual, err := r.deps.worktree.headCommit(worktreePath)
		if err != nil {
			return "", r.failProvision(req, lease, nil, false, Precondition(fmt.Errorf("verify mechanical worktree HEAD: %w; refusing to launch", err)))
		}
		if actual != req.attempt.PlannedAgainst {
			return "", r.failProvision(req, lease, nil, false, Precondition(fmt.Errorf("mechanical plan became stale during worktree acquisition: planned against %s, acquired worktree is %s; refusing to launch; re-check and rewrite the brief before dispatch", req.attempt.PlannedAgainst, actual)))
		}
	}
	if err := r.afterPhase(phaseWorktreeRecorded); err != nil {
		return "", r.failProvision(req, lease, nil, false, err)
	}

	if releaseWorktree == nil {
		releaseWorktree, err = state.Lock(req.home, "worktree:"+worktreePath)
		if err != nil {
			return "", r.failProvision(req, lease, nil, false, fmt.Errorf("lock worktree %q: %w", worktreePath, err))
		}
		defer releaseWorktree()
	}

	conflict, err := r.deps.worktree.checkCollision(req.home, lease, req.attempt.TaskID)
	if err != nil {
		return "", r.failProvision(req, lease, nil, false, err)
	}
	if conflict != "" {
		err := Precondition(fmt.Errorf("worktree collision: %s already holds %s", conflict, worktreePath))
		return "", r.failProvision(req, lease, nil, false, err)
	}
	if req.resumeExisting {
		observation := r.observeWorktreeLease(worktreePath, lease.ID)
		if observation.State != worktree.LeaseExact {
			unproven := &worktree.UnprovenLeaseError{WorktreePath: worktreePath, ExpectedLeaseID: lease.ID, Observation: observation}
			return "", r.failProvision(req, lease, nil, false, Precondition(fmt.Errorf("resumed worktree lease is %s: %w; refusing to launch", observation.State, unproven)))
		}
	}

	workerSpec, err := r.deps.buildHarness(req.attempt.Harness, harness.Options{
		Worktree: worktreePath, Brief: req.briefPath, Model: req.attempt.Model, Effort: req.attempt.Effort,
		ExecutionClass: brief.ExecutionClass(req.attempt.ExecutionClass), BriefHasFrontMatter: req.briefHasFrontMatter,
	})
	if err != nil {
		return "", r.failProvision(req, lease, nil, false, err)
	}

	client := r.herdrClient(session)
	workerSpec, err = composeWorkerSpec(workerSpec, req.home, herdrWorkerEnvironment(client))
	if err != nil {
		return "", r.failProvision(req, lease, nil, false, err)
	}
	workspace, tab, pane, rollback, err := acquireTaskWorkspace(client, workerSpec.Cwd, workerSpec.Env, req.attempt.TaskID, req.projectName, session)
	if err != nil {
		return "", r.failProvision(req, lease, nil, false, err)
	}
	herdrRecorded := false
	fail := func(cause error) (string, error) {
		return "", r.failProvision(req, lease, rollback, herdrRecorded, cause)
	}

	startedAt := req.paneStartedAt
	if startedAt == "" {
		startedAt = r.deps.now().Format(time.RFC3339)
	}
	if err := state.RecordAttemptHerdr(req.home, req.attempt.TaskID, req.attempt.ID, state.Herdr{
		Session: session, WorkspaceID: workspace.WorkspaceID, TabID: tab.TabID, PaneID: pane.PaneID,
	}, startedAt); err != nil {
		return fail(fmt.Errorf("record Herdr ownership: %w", err))
	}
	herdrRecorded = true
	if err := r.afterPhase(phaseHerdrRecorded); err != nil {
		return fail(err)
	}

	if err := client.PaneRunSpec(pane.PaneID, workerSpec); err != nil {
		return fail(fmt.Errorf("send launch command failed: %w", err))
	}
	if err := state.MarkLaunchSubmitted(req.home, req.attempt.TaskID, req.attempt.ID, r.deps.now().Format(time.RFC3339)); err != nil {
		return fail(fmt.Errorf("record launch submission: %w", err))
	}
	if err := r.afterPhase(phaseLaunchSubmitted); err != nil {
		return fail(err)
	}

	if err := r.deps.confirmLaunch(client, pane.PaneID, req.attempt.Harness, workerSpec); err != nil {
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

func composeWorkerSpec(spec launch.LaunchSpec, home string, managed map[string]string) (launch.LaunchSpec, error) {
	if _, found := managed[harness.RoleEnv]; found {
		return launch.LaunchSpec{}, fmt.Errorf("managed worker environment cannot override %s", harness.RoleEnv)
	}
	if _, found := managed[harness.HomeEnv]; found {
		return launch.LaunchSpec{}, fmt.Errorf("managed worker environment cannot override %s", harness.HomeEnv)
	}
	return spec.MergeEnv(map[string]string{
		harness.RoleEnv: harness.WorkerRole,
		harness.HomeEnv: home,
	}, managed)
}

func (r *Runtime) afterPhase(phase lifecyclePhase) error { return r.deps.phase(phase) }

func (r *Runtime) failProvision(req provisioningRequest, lease worktree.Lease, rollback func() error, herdrRecorded bool, cause error) error {
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
	if lease.Path != "" && !req.resumeExisting {
		if err := r.returnProvisioningWorktree(req.home, req.attempt.TaskID, req.attempt.ID, lease); err != nil {
			cleanup = append(cleanup, err)
		}
	}
	return reportCleanup(cause, cleanup...)
}

func (r *Runtime) returnProvisioningWorktree(home, taskID string, attemptID int64, lease worktree.Lease) error {
	if err := r.deps.worktree.returnLease(lease, true); err != nil {
		return err
	}
	if err := state.ClearAttemptWorktree(home, taskID, attemptID); err != nil {
		return fmt.Errorf("clear returned worktree evidence: %w", err)
	}
	return nil
}

func (d worktreeDependencies) returnLease(lease worktree.Lease, force bool) error {
	if lease.ID != "" && d.returnWithID != nil {
		return d.returnWithID(lease.Path, lease.ID, force)
	}
	if d.returnWorktree != nil {
		return d.returnWorktree(lease.Path, force)
	}
	return worktree.ReturnLease(lease.Path, lease.ID, force)
}
