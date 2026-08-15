package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

type reconciliationAction string

const (
	reconciliationActionKeep                 reconciliationAction = "keep"
	reconciliationActionContinueProvisioning reconciliationAction = "continue-provisioning"
	reconciliationActionConfirmLaunch        reconciliationAction = "confirm-launch"
	reconciliationActionMarkRunning          reconciliationAction = "mark-running"
	reconciliationActionCleanupTerminal      reconciliationAction = "cleanup-terminal-resources"
	reconciliationActionNeedsRepair          reconciliationAction = "needs-repair"
	reconciliationActionBlocked              reconciliationAction = "blocked"
)

type treehouseLeaseState string

const (
	treehouseLeaseUnobserved treehouseLeaseState = "unobserved"
	treehouseLeaseExact      treehouseLeaseState = "exact"
	treehouseLeaseAbsent     treehouseLeaseState = "absent"
	treehouseLeaseMismatch   treehouseLeaseState = "mismatch"
	treehouseLeaseUnprovable treehouseLeaseState = "unprovable"
)

type worktreeState string

const (
	worktreeUnobserved worktreeState = "unobserved"
	worktreeClean      worktreeState = "clean"
	worktreeDirty      worktreeState = "dirty"
	worktreeMissing    worktreeState = "missing"
)

type herdrOwnershipState string

const (
	herdrOwnershipUnobserved herdrOwnershipState = "unobserved"
	herdrOwnershipExact      herdrOwnershipState = "exact"
	herdrOwnershipAbsent     herdrOwnershipState = "absent"
	herdrOwnershipMismatch   herdrOwnershipState = "mismatch"
	herdrOwnershipIncomplete herdrOwnershipState = "incomplete"
)

const (
	repairCodeProvisioningLaunchAmbiguous = "provisioning-launch-ambiguous"
	repairCodeProvisioningPaneMissing     = "provisioning-pane-missing"
	repairCodeLaunchSubmittedPaneMissing  = "launch-submitted-pane-missing"
	repairCodeLaunchAgentMismatch         = "launch-agent-mismatch"
	repairCodeRunningPaneMissing          = "running-pane-missing"
	repairCodeRunningPaneIdentityMismatch = "running-pane-identity-mismatch"
	repairCodeHerdrOwnershipIncomplete    = "herdr-ownership-incomplete"
	repairCodeHerdrOwnershipMismatch      = "herdr-ownership-mismatch"
	repairCodeWorktreeDirty               = "worktree-dirty"
	repairCodeWorktreeOwnershipMismatch   = "worktree-ownership-mismatch"
	repairCodeLegacyWorktreeUnprovable    = "legacy-worktree-ownership-unprovable"
)

type treehouseObservation struct {
	State treehouseLeaseState
}

type worktreeObservation struct {
	State worktreeState
}

type herdrObservation struct {
	State herdrOwnershipState
	Agent string
}

type reconciliationObservation struct {
	Treehouse        treehouseObservation
	Worktree         worktreeObservation
	Herdr            herdrObservation
	ObservationError bool
}

type reconciliationDecision struct {
	Action         reconciliationAction
	RepairCode     string
	RepairReason   string
	Harness        string
	Model          string
	Effort         string
	ExecutionClass string
	PlannedAgainst string
	Profile        string
	RoutingSource  string
}

type ReconcileRequest struct {
	Context context.Context
	Home    string
	ID      string
}

type ReconcileResult struct {
	ID             string `json:"id"`
	Outcome        string `json:"result"`
	Action         string `json:"action"`
	Iterations     int    `json:"iterations"`
	AttemptID      int64  `json:"attempt,omitempty"`
	RepairCode     string `json:"repair_code,omitempty"`
	RepairReason   string `json:"repair_reason,omitempty"`
	Harness        string `json:"harness,omitempty"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	ExecutionClass string `json:"execution_class,omitempty"`
	Profile        string `json:"profile,omitempty"`
	PlannedAgainst string `json:"planned_against,omitempty"`
	RoutingSource  string `json:"routing_source,omitempty"`
}

type ReconcileReport struct {
	Results []ReconcileResult `json:"results"`
}

const (
	reconcileOutcomeHealthy    = "healthy"
	reconcileOutcomeRecovered  = "recovered"
	reconcileOutcomeRepair     = "needs-repair"
	reconcileOutcomeBlocked    = "blocked"
	reconcileOutcomeNoProgress = "no-progress"
)

const reconcileIterationLimit = 8

func (r *Runtime) Reconcile(req ReconcileRequest) (ReconcileReport, error) {
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if req.ID != "" {
		result, err := r.reconcileTask(ctx, req.Home, req.ID)
		return ReconcileReport{Results: []ReconcileResult{result}}, err
	}
	histories, err := state.ListReconciliationHistories(req.Home)
	if err != nil {
		return ReconcileReport{}, err
	}
	report := ReconcileReport{Results: make([]ReconcileResult, 0, len(histories))}
	var errs []error
	for _, history := range histories {
		result, err := r.reconcileTask(ctx, req.Home, history.Task.ID)
		report.Results = append(report.Results, result)
		if err != nil {
			errs = append(errs, fmt.Errorf("task %q: %w", history.Task.ID, err))
		}
	}
	return report, errors.Join(errs...)
}

func (r *Runtime) reconcileTask(ctx context.Context, home, id string) (ReconcileResult, error) {
	release, err := state.Lock(home, "task:"+id)
	if err != nil {
		return ReconcileResult{ID: id, Outcome: reconcileOutcomeBlocked}, fmt.Errorf("lock task %q: %w", id, err)
	}
	defer release()

	result := ReconcileResult{ID: id}
	previousFingerprint := ""
	for iteration := 1; iteration <= reconcileIterationLimit; iteration++ {
		result.Iterations = iteration
		history, err := state.ReadHistory(home, id)
		if err != nil {
			return result, err
		}
		mergeObserved, mergeMismatch, err := r.observeMerge(ctx, history.Task)
		if err != nil {
			result.Outcome = reconcileOutcomeBlocked
			return result, err
		}
		if mergeMismatch {
			decision := repairDecision(reconciliationDecision{}, "merge-fact-mismatch", "durable state claims a merged PR but authoritative GitHub evidence says it is not merged")
			attemptID := int64(0)
			if history.ActiveAttempt != nil {
				attemptID = history.ActiveAttempt.ID
			}
			if err := r.recordRepair(home, history.Task, state.Attempt{ID: attemptID}, decision); err != nil {
				return result, err
			}
			result.Outcome, result.RepairCode, result.RepairReason = reconcileOutcomeRepair, decision.RepairCode, decision.RepairReason
			return result, nil
		}
		if mergeObserved && history.Task.RepairCode == "merge-fact-mismatch" {
			if err := state.ClearTaskRepair(home, id, history.Task.RepairCode); err != nil {
				return result, err
			}
			result.Outcome = reconcileOutcomeRecovered
			continue
		}
		historical, err := r.pendingHistoricalAttempt(home, history)
		if err != nil {
			return result, err
		}
		if historical != nil {
			result.AttemptID = historical.ID
			progress, decision, err := r.reconcileHistoricalAttempt(home, history.Task, *historical)
			if err != nil {
				result.Outcome = reconcileOutcomeBlocked
				return result, err
			}
			if decision.Action == reconciliationActionNeedsRepair {
				if err := r.recordRepair(home, history.Task, *historical, decision); err != nil {
					return result, err
				}
				result.Outcome, result.RepairCode, result.RepairReason = reconcileOutcomeRepair, decision.RepairCode, decision.RepairReason
				return result, nil
			}
			if progress {
				result.Action = string(reconciliationActionCleanupTerminal)
				result.Outcome = reconcileOutcomeRecovered
				continue
			}
		}
		if history.ActiveAttempt == nil {
			result.Outcome = reconcileOutcomeHealthy
			return result, nil
		}
		attempt := *history.ActiveAttempt
		result.AttemptID = attempt.ID
		result.Harness, result.Model, result.Effort = attempt.Harness, attempt.Model, attempt.Effort
		result.ExecutionClass, result.Profile = attempt.ExecutionClass, attempt.RequestedProfile
		result.PlannedAgainst, result.RoutingSource = attempt.PlannedAgainst, attempt.RoutingSource
		if attempt.TeardownTerminalAttempt != "" {
			progress, decision, err := r.reconcileHistoricalAttempt(home, history.Task, attempt)
			if err != nil {
				result.Outcome = reconcileOutcomeBlocked
				return result, err
			}
			if decision.Action == reconciliationActionNeedsRepair {
				if err := r.recordRepair(home, history.Task, attempt, decision); err != nil {
					return result, err
				}
				result.Outcome, result.RepairCode, result.RepairReason = reconcileOutcomeRepair, decision.RepairCode, decision.RepairReason
				return result, nil
			}
			if progress {
				result.Action = string(reconciliationActionCleanupTerminal)
				result.Outcome = reconcileOutcomeRecovered
				continue
			}
		}
		observation, err := r.observeAttempt(home, history.Task, attempt)
		if err != nil {
			result.Outcome = reconcileOutcomeBlocked
			return result, err
		}
		decision := decideReconciliation(history.Task, attempt, observation)
		result.Action = string(decision.Action)
		switch decision.Action {
		case reconciliationActionNeedsRepair:
			if err := r.recordRepair(home, history.Task, attempt, decision); err != nil {
				return result, err
			}
			result.Outcome, result.RepairCode, result.RepairReason = reconcileOutcomeRepair, decision.RepairCode, decision.RepairReason
			return result, nil
		case reconciliationActionBlocked:
			result.Outcome = reconcileOutcomeBlocked
			return result, fmt.Errorf("reconciliation observation was incomplete")
		}
		if shouldClearRepair(history.Task, attempt, observation) {
			if err := state.ClearTaskRepair(home, id, history.Task.RepairCode); err != nil {
				return result, err
			}
			result.Outcome = reconcileOutcomeRecovered
			continue
		}
		if decision.Action == reconciliationActionKeep {
			result.Outcome = reconcileOutcomeHealthy
			return result, nil
		}
		fingerprint := fmt.Sprintf("%d:%s:%s:%s:%s:%s", attempt.ID, attempt.Lifecycle, attempt.Worktree, attempt.Herdr.PaneID, attempt.LaunchSubmittedAt, attempt.LaunchConfirmedAt)
		if fingerprint == previousFingerprint {
			result.Outcome = reconcileOutcomeNoProgress
			return result, nil
		}
		previousFingerprint = fingerprint
		if err := r.applyReconciliationAction(ctx, home, history.Task, attempt, decision); err != nil {
			return result, err
		}
		result.Outcome = reconcileOutcomeRecovered
	}
	result.Outcome = reconcileOutcomeNoProgress
	return result, nil
}

func (r *Runtime) observeMerge(ctx context.Context, task state.Task) (bool, bool, error) {
	if task.PR == "" || (!task.MergeExecuted && !task.MergeAnnounced) {
		return false, false, nil
	}
	prMerged := r.deps.prMerged
	if prMerged == nil {
		prMerged = ghutil.PRIsMerged
	}
	merged, err := prMerged(ctx, task.PR)
	if err != nil {
		return false, false, fmt.Errorf("observe merged PR %s: %w", task.PR, err)
	}
	return merged, !merged, nil
}

func (r *Runtime) pendingHistoricalAttempt(_ string, history state.TaskHistory) (*state.Attempt, error) {
	for i := range history.Attempts {
		attempt := &history.Attempts[i]
		if history.ActiveAttempt != nil && attempt.ID == history.ActiveAttempt.ID {
			continue
		}
		if attempt.Lifecycle == state.AttemptProvisioning || attempt.Lifecycle == state.AttemptRunning {
			continue
		}
		if hasHerdrIdentity(attempt.Herdr) && attempt.TeardownHerdrState != state.TeardownResourceReleased {
			return attempt, nil
		}
		if attempt.Worktree != "" && attempt.TeardownWorktreeState != state.TeardownResourceReleased {
			return attempt, nil
		}
		if attempt.TeardownCompletionState != "" {
			return attempt, nil
		}
	}
	return nil, nil
}

func (r *Runtime) reconcileHistoricalAttempt(home string, task state.Task, attempt state.Attempt) (bool, reconciliationDecision, error) {
	if hasHerdrIdentity(attempt.Herdr) && attempt.TeardownHerdrState != state.TeardownResourceReleased {
		observation, err := observeHerdrOwnership(r.deps.herdr(), attempt.Herdr, task.ID, task.Project)
		if err != nil {
			return false, reconciliationDecision{}, err
		}
		switch observation.State {
		case herdrOwnershipIncomplete, herdrOwnershipMismatch:
			return false, repairDecision(reconciliationDecision{}, "teardown-resource-ambiguous", "historical Herdr resource ownership cannot be proven safely"), nil
		case herdrOwnershipAbsent:
			if cleared, err := clearHistoricalRepair(home, task, attempt, "teardown-resource-ambiguous", "herdr-ownership-mismatch"); err != nil {
				return false, reconciliationDecision{}, err
			} else if cleared {
				return true, reconciliationDecision{}, nil
			}
			if err := state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "herdr", state.TeardownResourceReleased); err != nil {
				return false, reconciliationDecision{}, err
			}
			return true, reconciliationDecision{}, nil
		case herdrOwnershipExact:
			if cleared, err := clearHistoricalRepair(home, task, attempt, "teardown-resource-ambiguous", "herdr-ownership-mismatch"); err != nil {
				return false, reconciliationDecision{}, err
			} else if cleared {
				return true, reconciliationDecision{}, nil
			}
			if attempt.TeardownHerdrState == "" {
				if err := state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "herdr", state.TeardownResourceReleasing); err != nil {
					return false, reconciliationDecision{}, err
				}
			}
			if err := closeTaskTab(r.deps.herdr(), attempt.Herdr.WorkspaceID, attempt.Herdr.TabID); err != nil {
				_ = state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "herdr", state.TeardownResourceAmbiguous)
				return false, reconciliationDecision{}, fmt.Errorf("close historical Herdr resource: %w", err)
			}
			if err := state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "herdr", state.TeardownResourceReleased); err != nil {
				return false, reconciliationDecision{}, err
			}
			return true, reconciliationDecision{}, nil
		}
	}

	if attempt.Worktree != "" && attempt.TeardownWorktreeState != state.TeardownResourceReleased {
		observeLease := r.deps.worktree.observeLease
		if observeLease == nil {
			observeLease = worktree.ObserveLease
		}
		lease, err := observeLease(attempt.Worktree, attempt.LeaseID)
		if err != nil {
			return false, reconciliationDecision{}, fmt.Errorf("observe historical Treehouse lease: %w", err)
		}
		switch lease.State {
		case worktree.LeaseUnprovable:
			return false, repairDecision(reconciliationDecision{}, "legacy-worktree-ownership-unprovable", "historical worktree has no exact lease identity"), nil
		case worktree.LeaseMismatch:
			if attempt.TeardownWorktreeState == state.TeardownResourceReleasing {
				if err := state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "worktree", state.TeardownResourceReleased); err != nil {
					return false, reconciliationDecision{}, err
				}
				return true, reconciliationDecision{}, nil
			}
			return false, repairDecision(reconciliationDecision{}, "worktree-ownership-mismatch", "historical worktree path is held by a different Treehouse lease"), nil
		case worktree.LeaseAbsent:
			if cleared, err := clearHistoricalRepair(home, task, attempt, "worktree-ownership-mismatch"); err != nil {
				return false, reconciliationDecision{}, err
			} else if cleared {
				return true, reconciliationDecision{}, nil
			}
			if err := state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "worktree", state.TeardownResourceReleased); err != nil {
				return false, reconciliationDecision{}, err
			}
			return true, reconciliationDecision{}, nil
		case worktree.LeaseExact:
			observeClean := r.deps.worktree.observeClean
			if observeClean == nil {
				observeClean = worktree.ObserveCleanliness
			}
			clean, err := observeClean(attempt.Worktree)
			if err != nil {
				return false, reconciliationDecision{}, fmt.Errorf("observe historical worktree cleanliness: %w", err)
			}
			if clean != worktree.Clean {
				return false, repairDecision(reconciliationDecision{}, repairCodeWorktreeDirty, "historical worktree is dirty; automatic reconciliation will not discard it"), nil
			}
			if cleared, err := clearHistoricalRepair(home, task, attempt, repairCodeWorktreeDirty, "worktree-ownership-mismatch"); err != nil {
				return false, reconciliationDecision{}, err
			} else if cleared {
				return true, reconciliationDecision{}, nil
			}
			if attempt.TeardownWorktreeState == "" {
				if err := state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "worktree", state.TeardownResourceReleasing); err != nil {
					return false, reconciliationDecision{}, err
				}
			}
			returnLease := r.deps.worktree.returnWithID
			if attempt.LeaseID == "" || returnLease == nil {
				if r.deps.worktree.returnWorktree == nil {
					return false, reconciliationDecision{}, fmt.Errorf("no worktree return operation configured")
				}
				if err := r.deps.worktree.returnWorktree(attempt.Worktree, false); err != nil {
					return false, reconciliationDecision{}, fmt.Errorf("return historical worktree: %w", err)
				}
			} else if err := returnLease(attempt.Worktree, attempt.LeaseID, false); err != nil {
				return false, reconciliationDecision{}, fmt.Errorf("return historical worktree lease: %w", err)
			}
			if err := state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "worktree", state.TeardownResourceReleased); err != nil {
				return false, reconciliationDecision{}, err
			}
			return true, reconciliationDecision{}, nil
		}
	}

	if attempt.TeardownCompletionState == state.TeardownCompletionAppended {
		_, found, err := completion.FindAttempt(home, attempt.ID)
		if err != nil {
			return false, reconciliationDecision{}, err
		}
		if !found {
			return false, repairDecision(reconciliationDecision{}, "completion-evidence-mismatch", "durable completion state says appended but no exact completion record exists"), nil
		}
		if cleared, err := clearHistoricalRepair(home, task, attempt, "completion-evidence-mismatch"); err != nil {
			return false, reconciliationDecision{}, err
		} else if cleared {
			return true, reconciliationDecision{}, nil
		}
		if attempt.TeardownTerminalAttempt != "" {
			if attempt.Lifecycle == attempt.TeardownTerminalAttempt {
				return false, reconciliationDecision{}, nil
			}
			if err := state.TerminalizeTaskAndAttempt(home, task.ID, attempt.ID, attempt.Lifecycle, attempt.TeardownTerminalAttempt); err != nil {
				return false, reconciliationDecision{}, fmt.Errorf("finish recovered teardown: %w", err)
			}
			return true, reconciliationDecision{}, nil
		}
		return false, reconciliationDecision{}, nil
	}
	if attempt.TeardownCompletionState == state.TeardownCompletionPending {
		record, found, err := completion.FindAttempt(home, attempt.ID)
		if err != nil {
			return false, reconciliationDecision{}, err
		}
		if !found {
			launched := attempt.LaunchSubmittedAt != "" || attempt.LaunchConfirmedAt != "" || attempt.Lifecycle != state.AttemptInterrupted
			record = completionFor(task, attempt.TeardownDisposition, launched)
			record.AttemptID, record.AttemptLifecycle = attempt.ID, string(attempt.TeardownTerminalAttempt)
			record.TornDownAt = r.deps.now().Format(time.RFC3339)
			if err := r.deps.appendCompletion(home, record); err != nil {
				return false, reconciliationDecision{}, fmt.Errorf("append recovered completion: %w", err)
			}
		}
		if err := state.SetAttemptTeardownCompletionState(home, task.ID, attempt.ID, attempt.Lifecycle, state.TeardownCompletionAppended); err != nil {
			return false, reconciliationDecision{}, err
		}
		return true, reconciliationDecision{}, nil
	}
	if attempt.TeardownTerminalAttempt != "" && attempt.TeardownCompletionState == "" {
		if err := state.SetAttemptTeardownCompletionState(home, task.ID, attempt.ID, attempt.Lifecycle, state.TeardownCompletionPending); err != nil {
			return false, reconciliationDecision{}, err
		}
		return true, reconciliationDecision{}, nil
	}
	return false, reconciliationDecision{}, nil
}

func clearHistoricalRepair(home string, task state.Task, attempt state.Attempt, codes ...string) (bool, error) {
	if task.RepairCode == "" || (task.RepairAttemptID != 0 && task.RepairAttemptID != attempt.ID) {
		return false, nil
	}
	for _, code := range codes {
		if task.RepairCode == code {
			if err := state.ClearTaskRepair(home, task.ID, code); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

func (r *Runtime) observeAttempt(_ string, task state.Task, attempt state.Attempt) (reconciliationObservation, error) {
	observation := reconciliationObservation{}
	if attempt.Lifecycle == state.AttemptProvisioning && attempt.Herdr.PaneID != "" && attempt.LaunchSubmittedAt == "" {
		return observation, nil
	}
	if attempt.Worktree != "" {
		observeLease := r.deps.worktree.observeLease
		if observeLease == nil {
			observeLease = worktree.ObserveLease
		}
		lease, err := observeLease(attempt.Worktree, attempt.LeaseID)
		if err != nil {
			return observation, fmt.Errorf("observe Treehouse lease: %w", err)
		}
		observation.Treehouse.State = treehouseLeaseState(lease.State)
		if lease.State == worktree.LeaseExact {
			observeClean := r.deps.worktree.observeClean
			if observeClean == nil {
				observeClean = worktree.ObserveCleanliness
			}
			clean, err := observeClean(attempt.Worktree)
			if err != nil {
				return observation, fmt.Errorf("observe worktree cleanliness: %w", err)
			}
			observation.Worktree.State = worktreeState(clean)
		}
	}
	if attempt.Herdr.PaneID != "" {
		var err error
		observation.Herdr, err = observeHerdrOwnership(r.deps.herdr(), attempt.Herdr, task.ID, task.Project)
		if err != nil {
			return observation, err
		}
		if observation.Herdr.State == herdrOwnershipUnobserved {
			return observation, fmt.Errorf("observe Herdr ownership: no observation")
		}
		if observation.Herdr.State == herdrOwnershipIncomplete {
			return observation, nil
		}
		if observation.Herdr.State == herdrOwnershipAbsent || observation.Herdr.State == herdrOwnershipMismatch {
			return observation, nil
		}
	}
	return observation, nil
}

func (r *Runtime) applyReconciliationAction(ctx context.Context, home string, task state.Task, attempt state.Attempt, decision reconciliationDecision) error {
	switch decision.Action {
	case reconciliationActionContinueProvisioning:
		projectInfo, found, err := project.FindReadOnly(home, task.Project)
		if err != nil {
			return err
		}
		if !found {
			return Precondition(fmt.Errorf("project %q not registered", task.Project))
		}
		briefPath := filepath.Join(home, task.Brief)
		_, hasFrontMatter, err := brief.Parse(briefPath)
		if err != nil {
			return fmt.Errorf("parse persisted brief: %w", err)
		}
		_, err = r.provision(ctx, provisioningRequest{
			home: home, projectName: projectInfo.Name, clonePath: filepath.Join(home, "projects", projectInfo.Name),
			briefPath: briefPath, briefHasFrontMatter: hasFrontMatter, attempt: attempt,
			resumeExisting: attempt.Worktree != "",
		})
		return err
	case reconciliationActionConfirmLaunch:
		if err := r.deps.confirmLaunch(r.deps.herdr(), attempt.Herdr.PaneID, attempt.Harness); err != nil {
			return fmt.Errorf("confirm persisted launch: %w", err)
		}
		return state.MarkLaunchConfirmed(home, task.ID, attempt.ID, r.deps.now().Format(time.RFC3339))
	case reconciliationActionMarkRunning:
		return state.MarkAttemptRunning(home, task.ID, attempt.ID)
	default:
		return nil
	}
}

func (r *Runtime) recordRepair(home string, task state.Task, attempt state.Attempt, decision reconciliationDecision) error {
	if task.RepairCode == decision.RepairCode && task.RepairReason == decision.RepairReason && task.RepairAttemptID == attempt.ID {
		return nil
	}
	return state.SetTaskRepair(home, task.ID, decision.RepairCode, decision.RepairReason, attempt.ID, r.deps.now().Format(time.RFC3339))
}

func shouldClearRepair(task state.Task, attempt state.Attempt, observation reconciliationObservation) bool {
	if task.RepairCode == "" || (task.RepairAttemptID != 0 && task.RepairAttemptID != attempt.ID) {
		return false
	}
	if observation.Herdr.State == herdrOwnershipExact && observation.Herdr.Agent == attempt.Harness {
		switch task.RepairCode {
		case repairCodeProvisioningPaneMissing, repairCodeLaunchSubmittedPaneMissing, repairCodeLaunchAgentMismatch, repairCodeRunningPaneMissing, repairCodeRunningPaneIdentityMismatch, repairCodeHerdrOwnershipIncomplete, repairCodeHerdrOwnershipMismatch:
			return true
		}
	}
	if observation.Treehouse.State == treehouseLeaseExact && observation.Worktree.State == worktreeClean {
		switch task.RepairCode {
		case repairCodeWorktreeDirty, repairCodeWorktreeOwnershipMismatch:
			return true
		}
	}
	return false
}

func decideReconciliation(_ state.Task, attempt state.Attempt, observation reconciliationObservation) reconciliationDecision {
	decision := reconciliationDecision{
		Harness: attempt.Harness, Model: attempt.Model, Effort: attempt.Effort,
		ExecutionClass: attempt.ExecutionClass, PlannedAgainst: attempt.PlannedAgainst,
		Profile: attempt.RequestedProfile, RoutingSource: attempt.RoutingSource,
	}
	if observation.ObservationError {
		decision.Action = reconciliationActionBlocked
		return decision
	}

	if attempt.Lifecycle == state.AttemptProvisioning {
		return decideProvisioning(attempt, observation, decision)
	}
	if attempt.Lifecycle == state.AttemptRunning {
		if attempt.Herdr.PaneID == "" {
			return repairDecision(decision, repairCodeHerdrOwnershipIncomplete, "running Attempt has no persisted Herdr pane identity")
		}
		if observation.Herdr.State == herdrOwnershipExact && observation.Herdr.Agent == attempt.Harness {
			decision.Action = reconciliationActionKeep
			return decision
		}
		if observation.Herdr.State == herdrOwnershipAbsent {
			return repairDecision(decision, repairCodeRunningPaneMissing, "persisted running Attempt has no matching Herdr pane")
		}
		if observation.Herdr.State == herdrOwnershipMismatch {
			return repairDecision(decision, repairCodeRunningPaneIdentityMismatch, "persisted running Attempt points at a different Herdr resource")
		}
		if observation.Herdr.State == herdrOwnershipIncomplete {
			return repairDecision(decision, repairCodeHerdrOwnershipIncomplete, "running Attempt Herdr ownership cannot be proven")
		}
		if observation.Herdr.State == herdrOwnershipExact {
			return repairDecision(decision, repairCodeLaunchAgentMismatch, "running Attempt pane does not prove the persisted harness is present")
		}
	}
	return decision
}

func decideProvisioning(attempt state.Attempt, observation reconciliationObservation, decision reconciliationDecision) reconciliationDecision {
	if attempt.Herdr.WorkspaceID != "" || attempt.Herdr.TabID != "" {
		if attempt.Herdr.PaneID == "" {
			return repairDecision(decision, repairCodeHerdrOwnershipIncomplete, "persisted Herdr workspace or tab identity has no pane identity")
		}
	}
	if attempt.Herdr.PaneID != "" {
		if attempt.LaunchSubmittedAt == "" {
			return repairDecision(decision, repairCodeProvisioningLaunchAmbiguous, "persisted Herdr pane exists but launch submission may have happened before the crash")
		}
		if observation.Herdr.State == herdrOwnershipAbsent {
			return repairDecision(decision, repairCodeLaunchSubmittedPaneMissing, "launch was recorded but the expected Herdr pane is absent")
		}
		if observation.Herdr.State == herdrOwnershipMismatch {
			return repairDecision(decision, repairCodeHerdrOwnershipMismatch, "launch was recorded but Herdr identity does not match the Attempt")
		}
		if observation.Herdr.State == herdrOwnershipIncomplete {
			return repairDecision(decision, repairCodeHerdrOwnershipIncomplete, "Herdr ownership for the recorded launch is incomplete")
		}
		if observation.Herdr.State != herdrOwnershipExact {
			return repairDecision(decision, repairCodeHerdrOwnershipIncomplete, "Herdr ownership for the recorded launch was not proven")
		}
		if observation.Herdr.Agent != attempt.Harness {
			return repairDecision(decision, repairCodeLaunchAgentMismatch, "the expected persisted harness is not observed in the recorded pane")
		}
		if attempt.LaunchConfirmedAt != "" {
			decision.Action = reconciliationActionMarkRunning
			return decision
		}
		decision.Action = reconciliationActionConfirmLaunch
		return decision
	}
	if attempt.LaunchSubmittedAt != "" || attempt.LaunchConfirmedAt != "" {
		return repairDecision(decision, repairCodeProvisioningPaneMissing, "launch evidence exists but no Herdr pane identity was persisted")
	}

	if attempt.Worktree != "" {
		switch observation.Treehouse.State {
		case treehouseLeaseMismatch:
			return repairDecision(decision, repairCodeWorktreeOwnershipMismatch, "recorded worktree path is leased under a different Treehouse lease ID")
		case treehouseLeaseUnprovable:
			return repairDecision(decision, repairCodeLegacyWorktreeUnprovable, "recorded worktree ownership has no provable Treehouse lease identity")
		case treehouseLeaseAbsent:
			return repairDecision(decision, repairCodeWorktreeOwnershipMismatch, "recorded Treehouse lease is absent")
		case treehouseLeaseExact:
			if observation.Worktree.State == worktreeDirty {
				return repairDecision(decision, repairCodeWorktreeDirty, "recorded worktree is dirty; automatic reconciliation will not discard it")
			}
			if observation.Worktree.State == worktreeClean {
				decision.Action = reconciliationActionContinueProvisioning
				return decision
			}
		}
		return repairDecision(decision, repairCodeWorktreeOwnershipMismatch, "recorded worktree ownership or cleanliness was not proven")
	}

	decision.Action = reconciliationActionContinueProvisioning
	return decision
}

func repairDecision(decision reconciliationDecision, code, reason string) reconciliationDecision {
	decision.Action = reconciliationActionNeedsRepair
	decision.RepairCode = code
	decision.RepairReason = reason
	return decision
}
