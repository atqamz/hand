package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/workerobs"
	"github.com/atqamz/hand/internal/worktree"
)

type reconciliationAction string

const (
	reconciliationActionKeep                 reconciliationAction = "keep"
	reconciliationActionContinueProvisioning reconciliationAction = "continue-provisioning"
	reconciliationActionConfirmLaunch        reconciliationAction = "confirm-launch"
	reconciliationActionMarkRunning          reconciliationAction = "mark-running"
	reconciliationActionCleanupTerminal      reconciliationAction = "cleanup-terminal-resources"
	reconciliationActionConvergeTerminal     reconciliationAction = "converge-terminal-lifecycle"
	reconciliationActionUnwindProvisioning   reconciliationAction = "unwind-failed-provisioning"
	reconciliationActionAbandonWorktree      reconciliationAction = "abandon-worktree"
	reconciliationActionAbandonPane          reconciliationAction = "abandon-pane"
	reconciliationActionAttestNeverStarted   reconciliationAction = "attest-attempt-never-started"
	reconciliationActionRelinquishWorktree   reconciliationAction = "relinquish-worktree-claim"
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
	treehouseLeaseUnknown    treehouseLeaseState = "unknown"
)

type landingState string

const (
	landingLanded   landingState = "landed"
	landingUnlanded landingState = "unlanded"
	landingUnknown  landingState = "unknown"
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

type treehouseObservation struct {
	State treehouseLeaseState
	Probe worktree.LeaseProbe
}

type worktreeObservation struct {
	State worktreeState
}

type herdrObservation struct {
	State       herdrOwnershipState
	Agent       string
	AgentStatus herdr.Status
	Pane        herdr.Pane
}

type reconciliationObservation struct {
	Treehouse        treehouseObservation
	Worktree         worktreeObservation
	Herdr            herdrObservation
	ReportState      string
	Landing          landingState
	ObservationError bool
}

type reconciliationDecision struct {
	Action          reconciliationAction
	TerminalAttempt state.AttemptLifecycle
	Disposition     string
	RepairCode      string
	RepairReason    string
	Harness         string
	Model           string
	Effort          string
	ExecutionClass  string
	PlannedAgainst  string
	Profile         string
	RoutingSource   string
	Detail          string
}

type ReconcileRequest struct {
	Context             context.Context
	Home                string
	ID                  string
	AbandonWorktree     bool
	AbandonPane         bool
	AttemptNeverStarted bool
}

type reconcileAttestations struct {
	Worktree     bool
	Pane         bool
	NeverStarted bool
}

func (a reconcileAttestations) any() bool { return a.Worktree || a.Pane || a.NeverStarted }

type ReconcileResult struct {
	ID             string `json:"id"`
	Outcome        string `json:"result"`
	Action         string `json:"action"`
	Iterations     int    `json:"iterations"`
	AttemptID      int64  `json:"attempt,omitempty"`
	Landing        string `json:"landing,omitempty"`
	RepairCode     string `json:"repair_code,omitempty"`
	RepairReason   string `json:"repair_reason,omitempty"`
	Harness        string `json:"harness,omitempty"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	ExecutionClass string `json:"execution_class,omitempty"`
	Profile        string `json:"profile,omitempty"`
	PlannedAgainst string `json:"planned_against,omitempty"`
	RoutingSource  string `json:"routing_source,omitempty"`
	// Set only for a running Attempt whose Herdr pane proves the persisted harness present: "working" or
	// "blocked" mirror Herdr's own agent_status, and "idle-unreported" is reconcile's own durable fact
	// that nothing has explained an idle or done pane since launch (atqamz/hand#259).
	Liveness string `json:"liveness,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Error    string `json:"error,omitempty"`
}

type ReconcileAnomaly struct {
	Kind           string `json:"kind"`
	WorkspaceID    string `json:"workspace_id"`
	WorkspaceLabel string `json:"workspace_label"`
	TabID          string `json:"tab_id"`
	TabLabel       string `json:"tab_label"`
	OwnerAttemptID int64  `json:"owner_attempt,omitempty"`
	Reason         string `json:"reason"`
}

type ReconcileReport struct {
	Results   []ReconcileResult  `json:"results"`
	Anomalies []ReconcileAnomaly `json:"anomalies,omitempty"`
	Errors    []string           `json:"errors,omitempty"`
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
	if err := project.Migrate(req.Home); err != nil {
		return ReconcileReport{}, fmt.Errorf("migrate project and completion identity: %w", err)
	}
	if err := fleetPreflightReadOnly(req.Home); err != nil {
		return ReconcileReport{}, Precondition(err)
	}
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	attest := reconcileAttestations{Worktree: req.AbandonWorktree, Pane: req.AbandonPane, NeverStarted: req.AttemptNeverStarted}
	if req.ID != "" {
		result, err := r.reconcileTask(ctx, req.Home, req.ID, attest)
		if err != nil {
			result.Error = err.Error()
		}
		return ReconcileReport{Results: []ReconcileResult{result}}, err
	}
	if attest.any() {
		return ReconcileReport{}, Precondition(fmt.Errorf("attesting that Hand relinquishes a recorded resource needs an explicit task ID"))
	}
	histories, err := state.ListReconciliationHistories(req.Home)
	if err != nil {
		return ReconcileReport{}, err
	}
	report := ReconcileReport{Results: make([]ReconcileResult, 0, len(histories))}
	var errs []error
	for _, history := range histories {
		result, err := r.reconcileTask(ctx, req.Home, history.Task.ID, reconcileAttestations{})
		if err != nil {
			result.Error = err.Error()
		}
		report.Results = append(report.Results, result)
		if err != nil {
			errs = append(errs, fmt.Errorf("task %q: %w", history.Task.ID, err))
		}
	}
	anomalies, err := r.observeHerdrOrphans(req.Home)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		errs = append(errs, fmt.Errorf("observe Herdr inventory: %w", err))
	} else {
		report.Anomalies = anomalies
	}
	return report, errors.Join(errs...)
}

func (r *Runtime) reconcileTask(ctx context.Context, home, id string, attest reconcileAttestations) (ReconcileResult, error) {
	release, err := state.Lock(home, "task:"+id)
	if err != nil {
		return ReconcileResult{ID: id, Outcome: reconcileOutcomeBlocked}, fmt.Errorf("lock task %q: %w", id, err)
	}
	defer release()

	result := ReconcileResult{ID: id}
	if _, err := state.NormalizePendingSends(home, id, "reconcile-stale-pending", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return result, fmt.Errorf("normalize pending sends for task %q: %w", id, err)
	}
	previousFingerprint := ""
	for iteration := 1; iteration <= reconcileIterationLimit; iteration++ {
		result.Iterations = iteration
		history, err := state.ReadHistory(home, id)
		if err != nil {
			return result, err
		}
		mergeObserved, mergeMismatch, mergeSource, err := r.observeMerge(ctx, home, history)
		if err != nil {
			result.Landing = string(landingUnknown)
			result.Outcome = reconcileOutcomeBlocked
			return result, err
		}
		if mergeMismatch {
			reason := "durable state claims a merged PR but authoritative GitHub evidence says it is not merged"
			if mergeSource == "local-git" {
				reason = "durable state claims a merged branch but authoritative local Git evidence says it is not merged"
			}
			decision := repairDecision(reconciliationDecision{}, repairCodeMergeFactMismatch, reason)
			attemptID := int64(0)
			if history.ActiveAttempt != nil {
				attemptID = history.ActiveAttempt.ID
			}
			if err := r.recordRepair(home, history.Task, state.Attempt{ID: attemptID}, decision, &result); err != nil {
				return result, err
			}
			return result, nil
		}
		if mergeObserved && history.Task.RepairCode == repairCodeMergeFactMismatch {
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
			progress, decision, err := r.reconcileHistoricalAttempt(ctx, home, history.Task, *historical, attest)
			if err != nil {
				result.Outcome = reconcileOutcomeBlocked
				return result, err
			}
			if decision.Action == reconciliationActionNeedsRepair {
				if err := r.recordRepair(home, history.Task, *historical, decision, &result); err != nil {
					return result, err
				}
				return result, nil
			}
			if progress {
				recordHistoricalProgress(&result, decision)
				continue
			}
		}
		if cleared, err := clearResolvedTerminalRepair(home, history); err != nil {
			return result, err
		} else if cleared {
			result.Outcome = reconcileOutcomeRecovered
			continue
		}
		if history.ActiveAttempt == nil {
			if reportExistingRepair(&result, history.Task) {
				return result, nil
			}
			result.Outcome = reconcileOutcomeHealthy
			return result, nil
		}
		attempt := *history.ActiveAttempt
		result.AttemptID = attempt.ID
		result.Harness, result.Model, result.Effort = attempt.Harness, attempt.Model, attempt.Effort
		result.ExecutionClass, result.Profile = attempt.ExecutionClass, attempt.RequestedProfile
		result.PlannedAgainst, result.RoutingSource = attempt.PlannedAgainst, attempt.RoutingSource
		if attest.NeverStarted && attempt.TeardownTerminalAttempt == "" {
			decision, err := r.attestAttemptNeverStarted(home, history.Task, attempt)
			if err != nil {
				return result, err
			}
			result.Action, result.Outcome, result.Detail = string(decision.Action), reconcileOutcomeRecovered, decision.Detail
			continue
		}
		if attempt.TeardownTerminalAttempt != "" {
			progress, decision, err := r.reconcileHistoricalAttempt(ctx, home, history.Task, attempt, attest)
			if err != nil {
				result.Outcome = reconcileOutcomeBlocked
				return result, err
			}
			if decision.Action == reconciliationActionNeedsRepair {
				if err := r.recordRepair(home, history.Task, attempt, decision, &result); err != nil {
					return result, err
				}
				return result, nil
			}
			if progress {
				recordHistoricalProgress(&result, decision)
				continue
			}
		}
		observation, err := r.observeAttempt(home, history.Task, attempt)
		if err != nil {
			result.Outcome = reconcileOutcomeBlocked
			return result, err
		}
		if terminalConvergenceCandidate(attempt, observation) {
			observation.Landing = r.observeLanding(ctx, home, history.Task, mergeObserved)
			result.Landing = string(observation.Landing)
		}
		decision := decideReconciliation(history.Task, attempt, observation)
		if attempt.Lifecycle == state.AttemptRunning && observation.Herdr.State == herdrOwnershipExact && observation.Herdr.Agent == attempt.Harness {
			liveness, err := r.recordAttemptLiveness(home, history.Task, attempt, observation.Herdr.AgentStatus)
			if err != nil {
				return result, err
			}
			result.Liveness = liveness
		}
		result.Action = string(decision.Action)
		switch decision.Action {
		case reconciliationActionNeedsRepair:
			if err := r.recordRepair(home, history.Task, attempt, decision, &result); err != nil {
				return result, err
			}
			return result, nil
		case reconciliationActionBlocked:
			result.Outcome = reconcileOutcomeBlocked
			return result, fmt.Errorf("reconciliation observation was incomplete")
		case reconciliationActionConvergeTerminal:
			if err := r.convergeTerminalLifecycle(home, history.Task, attempt, decision); err != nil {
				return result, err
			}
			result.Outcome, result.Detail = reconcileOutcomeRecovered, decision.Detail
			continue
		case reconciliationActionUnwindProvisioning:
			if err := r.unwindFailedProvisioning(home, history.Task, attempt, decision); err != nil {
				return result, err
			}
			result.Outcome, result.Detail = reconcileOutcomeRecovered, decision.Detail
			continue
		}
		if shouldClearRepair(history.Task, attempt, observation) {
			if err := state.ClearTaskRepair(home, id, history.Task.RepairCode); err != nil {
				return result, err
			}
			result.Outcome = reconcileOutcomeRecovered
			continue
		}
		if reportExistingRepair(&result, history.Task) {
			return result, nil
		}
		if decision.Action == reconciliationActionKeep {
			result.Outcome = reconcileOutcomeHealthy
			return result, nil
		}
		fingerprint := fmt.Sprintf("%d:%s:%s:%s:%s:%s", attempt.ID, attempt.Lifecycle, attempt.Worktree, attempt.Herdr.PaneID, attempt.LaunchSubmittedAt, attempt.LaunchConfirmedAt)
		if fingerprint == previousFingerprint {
			if reportExistingRepair(&result, history.Task) {
				return result, nil
			}
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

func recordHistoricalProgress(result *ReconcileResult, decision reconciliationDecision) {
	result.Action = string(reconciliationActionCleanupTerminal)
	if decision.Action != "" {
		result.Action = string(decision.Action)
	}
	if decision.Detail != "" {
		result.Detail = decision.Detail
	}
	result.Outcome = reconcileOutcomeRecovered
}

func reportExistingRepair(result *ReconcileResult, task state.Task) bool {
	if task.RepairCode == "" {
		return false
	}
	result.Outcome = reconcileOutcomeRepair
	result.RepairCode = task.RepairCode
	result.RepairReason = task.RepairReason
	return true
}

func (r *Runtime) observeMerge(ctx context.Context, home string, history state.TaskHistory) (bool, bool, string, error) {
	task := history.Task
	if !task.MergeExecuted && !task.MergeAnnounced {
		return false, false, "", nil
	}
	if task.PR != "" {
		prMerged := r.deps.prMerged
		if prMerged == nil {
			prMerged = ghutil.ObserveMergeState
		}
		observation := prMerged(ctx, task.PR)
		if !observation.Found() {
			return false, false, "github-pr", fmt.Errorf("observe merged PR %s: %s", task.PR, observation.Reason())
		}
		return observation.Merged, !observation.Merged, "github-pr", nil
	}
	projectInfo, found, err := project.FindReadOnly(home, task.Project)
	if err != nil {
		return false, false, "local-git", fmt.Errorf("observe local merge project %q: %w", task.Project, err)
	}
	if !found {
		return false, false, "local-git", fmt.Errorf("observe local merge project %q: project is not registered", task.Project)
	}
	if projectInfo.Mode != project.ModeLocalOnly {
		return false, false, "local-git", fmt.Errorf("observe local merge for task %q: project mode %q has no local merge evidence", task.ID, projectInfo.Mode)
	}
	releaseProject, err := state.Lock(home, "project:"+task.Project)
	if err != nil {
		return false, false, "local-git", fmt.Errorf("lock project %q for local merge observation: %w", task.Project, err)
	}
	defer releaseProject()
	attempt, err := mergeWorktreeAttempt(history)
	if err != nil {
		return false, false, "local-git", err
	}
	releaseWorktree, err := state.Lock(home, "worktree:"+attempt.Worktree)
	if err != nil {
		return false, false, "local-git", fmt.Errorf("lock worktree %q for local merge observation: %w", attempt.Worktree, err)
	}
	defer releaseWorktree()
	lease := r.observeWorktreeLease(attempt.Worktree, attempt.LeaseID)
	if lease.State != worktree.LeaseExact {
		unproven := &worktree.UnprovenLeaseError{WorktreePath: attempt.Worktree, ExpectedLeaseID: attempt.LeaseID, Observation: lease}
		return false, false, "local-git", fmt.Errorf("observe local merge for task %q Attempt %d: %w", task.ID, attempt.ID, unproven)
	}
	branchMerged := r.deps.branchMerged
	if branchMerged == nil {
		branchMerged = branchIsMerged
	}
	merged, err := branchMerged(filepath.Join(home, "projects", task.Project), attempt.Worktree)
	if err != nil {
		return false, false, "local-git", fmt.Errorf("observe local merged branch for task %q: %w", task.ID, err)
	}
	return merged, !merged, "local-git", nil
}

func (r *Runtime) observeLanding(ctx context.Context, home string, task state.Task, mergeObserved bool) landingState {
	if mergeObserved || task.DeliveredAt != "" {
		return landingLanded
	}
	if task.Kind == state.KindScout {
		if _, err := os.Stat(filepath.Join(home, "data", task.ID, "report.md")); err != nil {
			if os.IsNotExist(err) {
				return landingUnlanded
			}
			return landingUnknown
		}
		return landingLanded
	}
	if task.PR == "" {
		return landingUnlanded
	}
	prMerged := r.deps.prMerged
	if prMerged == nil {
		prMerged = ghutil.ObserveMergeState
	}
	observation := prMerged(ctx, task.PR)
	if !observation.Found() {
		return landingUnknown
	}
	if observation.Merged {
		return landingLanded
	}
	return landingUnlanded
}

func terminalConvergenceCandidate(attempt state.Attempt, observation reconciliationObservation) bool {
	if attempt.Lifecycle != state.AttemptRunning || attempt.TeardownTerminalAttempt != "" || attempt.Herdr.PaneID == "" {
		return false
	}
	if observation.Herdr.State == herdrOwnershipAbsent {
		return true
	}
	return observation.Herdr.State == herdrOwnershipExact &&
		observation.Herdr.AgentStatus == herdr.StatusDone &&
		(observation.ReportState == "" || observation.ReportState == state.ReportWorking)
}

func runningPaneMissingReason(landing landingState) string {
	if landing == landingUnknown {
		return "persisted running Attempt has no matching Herdr pane and its landing evidence is unknown"
	}
	return "persisted running Attempt has no matching Herdr pane"
}

func decideTerminalConvergence(attempt state.Attempt, observation reconciliationObservation) (state.AttemptLifecycle, string, bool) {
	if !terminalConvergenceCandidate(attempt, observation) {
		return "", "", false
	}
	switch observation.Landing {
	case landingLanded:
		return state.AttemptCompleted, state.TeardownDispositionCompleted, true
	case landingUnlanded:
		return state.AttemptInterrupted, state.TeardownDispositionWorkerExitedUnlanded, true
	}
	return "", "", false
}

func (r *Runtime) convergeTerminalLifecycle(home string, task state.Task, attempt state.Attempt, decision reconciliationDecision) error {
	return r.terminalizeWithoutRelease(home, task, attempt, decision, "converged")
}

func (r *Runtime) unwindFailedProvisioning(home string, task state.Task, attempt state.Attempt, decision reconciliationDecision) error {
	if !unwindableProvisioning(attempt) {
		return Precondition(fmt.Errorf("refusing to unwind attempt %d: it records lifecycle %q, worktree %q, lease %s, Herdr identity (%s) and teardown lifecycle %q, so this is not a launch that left nothing behind",
			attempt.ID, attempt.Lifecycle, attempt.Worktree, leaseOrNone(attempt.LeaseID),
			herdrIdentityText(attempt.Herdr), attempt.TeardownTerminalAttempt))
	}
	return r.terminalizeWithoutRelease(home, task, attempt, decision, "unwound")
}

func (r *Runtime) terminalizeWithoutRelease(home string, task state.Task, attempt state.Attempt, decision reconciliationDecision, label string) error {
	if err := state.SetAttemptTeardownDecision(home, task.ID, attempt.ID, decision.TerminalAttempt, decision.Disposition); err != nil {
		return fmt.Errorf("record %s teardown decision for attempt %d: %w", label, attempt.ID, err)
	}
	if err := state.SetAttemptTeardownCompletionState(home, task.ID, attempt.ID, attempt.Lifecycle, state.TeardownCompletionPending); err != nil {
		return fmt.Errorf("record %s completion phase for attempt %d: %w", label, attempt.ID, err)
	}
	record := completionFor(task, decision.Disposition, true, attempt.LastReportState, attempt.LastReportNote)
	record.AttemptID, record.AttemptLifecycle = attempt.ID, string(decision.TerminalAttempt)
	record.TornDownAt = r.deps.now().Format(time.RFC3339)
	if err := r.deps.appendCompletion(home, record); err != nil {
		return fmt.Errorf("append %s completion for attempt %d: %w", label, attempt.ID, err)
	}
	if err := state.SetAttemptTeardownCompletionState(home, task.ID, attempt.ID, attempt.Lifecycle, state.TeardownCompletionAppended); err != nil {
		return fmt.Errorf("record %s completion evidence for attempt %d: %w", label, attempt.ID, err)
	}
	if err := terminalizeAndClearLimitHold(home, task.ID, attempt.ID, attempt.Lifecycle, decision.TerminalAttempt); err != nil {
		return fmt.Errorf("%s attempt %d to %s: %w", label, attempt.ID, decision.TerminalAttempt, err)
	}
	return nil
}

func terminalizeAndClearLimitHold(home, taskID string, attemptID int64, attemptFrom, attemptTo state.AttemptLifecycle) error {
	if err := state.TerminalizeTaskAndAttempt(home, taskID, attemptID, attemptFrom, attemptTo); err != nil {
		return err
	}
	if err := state.ClearHoldIfKind(home, taskID, state.HoldKindLimit); err != nil {
		return fmt.Errorf("clear usage-limit hold: %w", err)
	}
	return nil
}

func mergeWorktreeAttempt(history state.TaskHistory) (*state.Attempt, error) {
	task := history.Task
	var attempt *state.Attempt
	if history.ActiveAttempt != nil {
		attempt = history.ActiveAttempt
	} else {
		for i := len(history.Attempts) - 1; i >= 0; i-- {
			candidate := &history.Attempts[i]
			if candidate.Worktree != "" && !worktreeCleanupSettled(candidate.TeardownWorktreeState) {
				attempt = candidate
				break
			}
		}
	}
	if attempt == nil || attempt.Worktree == "" {
		return nil, fmt.Errorf("observe local merge for task %q: no Attempt worktree with current ownership is available", task.ID)
	}
	if attempt.LeaseID == "" {
		return nil, fmt.Errorf("observe local merge for task %q: Attempt %d has no exact Treehouse lease identity", task.ID, attempt.ID)
	}
	return attempt, nil
}

func (r *Runtime) observeHerdrOrphans(home string) ([]ReconcileAnomaly, error) {
	fleetID, err := state.FleetID(home)
	if err != nil {
		return nil, err
	}
	client := r.herdrClient(herdr.SessionName(fleetID))
	ownerships, err := state.ListHerdrOwnerships(home)
	if err != nil {
		return nil, err
	}
	ownershipCandidates := make(map[string][]string, len(ownerships))
	legacyWorkspaces := make(map[string]bool)
	for _, ownership := range ownerships {
		if ownership.WorkspaceID != "" && ownership.TabID != "" {
			key := herdrIdentityKey(herdrSession(ownership.Session), ownership.WorkspaceID, ownership.TabID)
			if !containsString(ownershipCandidates[key], ownership.TaskID) {
				ownershipCandidates[key] = append(ownershipCandidates[key], ownership.TaskID)
			}
		}
		if (ownership.Session == "" || ownership.Session == "default") && ownership.WorkspaceID != "" {
			legacyWorkspaces[herdrIdentityKey("default", ownership.WorkspaceID, "")] = true
		}
	}
	workspaces, err := client.WorkspaceList()
	if err != nil {
		return nil, fmt.Errorf("list Herdr workspaces: %w", err)
	}
	var anomalies []ReconcileAnomaly
	currentSession := herdr.SessionName(fleetID)
	if r.deps.herdrFor == nil {
		currentSession = "default"
	}
	currentSkip := legacyWorkspaces
	if currentSession == "default" {
		currentSkip = nil
	}
	inspect := func(inventory herdrClient, session string, candidates []herdr.Workspace, skip map[string]bool) error {
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].Label != candidates[j].Label {
				return candidates[i].Label < candidates[j].Label
			}
			return candidates[i].WorkspaceID < candidates[j].WorkspaceID
		})
		for _, workspace := range candidates {
			if !strings.HasPrefix(workspace.Label, "hand:") || skip[herdrIdentityKey(session, workspace.WorkspaceID, "")] {
				continue
			}
			tabs, err := inventory.TabList(workspace.WorkspaceID)
			if err != nil {
				return fmt.Errorf("list Herdr tabs for workspace %s: %w", workspace.WorkspaceID, err)
			}
			sort.Slice(tabs, func(i, j int) bool {
				if tabs[i].TabID != tabs[j].TabID {
					return tabs[i].TabID < tabs[j].TabID
				}
				return tabs[i].Label < tabs[j].Label
			})
			for _, tab := range tabs {
				anomaly, err := r.classifyHerdrTab(home, inventory, session, workspace, tab, ownershipCandidates)
				if err != nil {
					return err
				}
				if anomaly != nil {
					anomalies = append(anomalies, *anomaly)
				}
			}
		}
		return nil
	}
	if err := inspect(client, currentSession, workspaces, currentSkip); err != nil {
		return nil, err
	}
	if len(legacyWorkspaces) > 0 && r.deps.herdrFor != nil {
		legacyClient := r.herdrClient("default")
		workspaces, err := legacyClient.WorkspaceList()
		if err != nil {
			return nil, fmt.Errorf("list legacy Herdr workspaces: %w", err)
		}
		selected := make([]herdr.Workspace, 0, len(workspaces))
		for _, workspace := range workspaces {
			if legacyWorkspaces[herdrIdentityKey("default", workspace.WorkspaceID, "")] {
				selected = append(selected, workspace)
			}
		}
		if err := inspect(legacyClient, "default", selected, nil); err != nil {
			return nil, err
		}
	}
	return anomalies, nil
}

func herdrSession(session string) string {
	if session == "" {
		return "default"
	}
	return session
}

func herdrIdentityKey(session, workspaceID, tabID string) string {
	return herdrSession(session) + "\x00" + workspaceID + "\x00" + tabID
}

func (r *Runtime) classifyHerdrTab(home string, client herdrClient, session string, workspace herdr.Workspace, tab herdr.Tab, ownershipCandidates map[string][]string) (*ReconcileAnomaly, error) {
	candidates := append([]string(nil), ownershipCandidates[herdrIdentityKey(session, workspace.WorkspaceID, tab.TabID)]...)
	if state.ValidateID(tab.Label) == nil && !containsString(candidates, tab.Label) {
		candidates = append(candidates, tab.Label)
	}
	sort.Strings(candidates)
	for _, taskID := range candidates {
		release, err := state.Lock(home, "task:"+taskID)
		if err != nil {
			return nil, fmt.Errorf("lock Herdr candidate task %q: %w", taskID, err)
		}
		history, err := state.ReadHistory(home, taskID)
		if err != nil {
			release()
			if errors.Is(err, state.ErrTaskNotFound) {
				continue
			}
			return nil, err
		}
		freshTab, found, err := findHerdrTab(client, workspace.WorkspaceID, tab.TabID)
		if err != nil {
			release()
			return nil, err
		}
		if !found {
			release()
			return nil, nil
		}
		anomaly, matched, err := classifyLockedHerdrHistory(client, workspace, freshTab, history)
		release()
		if err != nil {
			return nil, err
		}
		if matched {
			return anomaly, nil
		}
	}
	return unattributedHerdrAnomaly(workspace, tab), nil
}

func findHerdrTab(client herdrClient, workspaceID, tabID string) (herdr.Tab, bool, error) {
	tabs, err := client.TabList(workspaceID)
	if err != nil {
		return herdr.Tab{}, false, fmt.Errorf("re-observe Herdr tabs for workspace %s: %w", workspaceID, err)
	}
	for _, tab := range tabs {
		if tab.TabID == tabID {
			return tab, true, nil
		}
	}
	return herdr.Tab{}, false, nil
}

func classifyLockedHerdrHistory(client herdrClient, workspace herdr.Workspace, tab herdr.Tab, history state.TaskHistory) (*ReconcileAnomaly, bool, error) {
	var matches []state.Attempt
	for _, attempt := range history.Attempts {
		if attempt.Herdr.WorkspaceID == workspace.WorkspaceID && attempt.Herdr.TabID == tab.TabID {
			matches = append(matches, attempt)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		iActive := matches[i].Lifecycle == state.AttemptProvisioning || matches[i].Lifecycle == state.AttemptRunning
		jActive := matches[j].Lifecycle == state.AttemptProvisioning || matches[j].Lifecycle == state.AttemptRunning
		if iActive != jActive {
			return iActive
		}
		return matches[i].ID > matches[j].ID
	})
	if len(matches) == 0 {
		return nil, false, nil
	}
	for _, attempt := range matches {
		if attempt.TeardownHerdrState == state.TeardownResourceReleased {
			return &ReconcileAnomaly{
				Kind: "released-herdr-resource", WorkspaceID: workspace.WorkspaceID, WorkspaceLabel: workspace.Label,
				TabID: tab.TabID, TabLabel: tab.Label, OwnerAttemptID: attempt.ID,
				Reason: "live Herdr tab matches an Attempt whose Herdr teardown is durably released; possible recreation or ID reuse; refusing to close",
			}, true, nil
		}
	}
	attempt := matches[0]
	if attempt.Lifecycle != state.AttemptProvisioning && attempt.Lifecycle != state.AttemptRunning {
		return classifyTerminalHerdrMatch(client, workspace, tab, history, attempt)
	}
	observation, err := observeHerdrOwnership(client, attempt.Herdr, history.Task.ID, history.Task.Project)
	if err != nil {
		return nil, false, err
	}
	if observation.State == herdrOwnershipExact {
		return nil, true, nil
	}
	return &ReconcileAnomaly{
		Kind: "herdr-ownership-inconsistency", WorkspaceID: workspace.WorkspaceID, WorkspaceLabel: workspace.Label,
		TabID: tab.TabID, TabLabel: tab.Label, OwnerAttemptID: attempt.ID,
		Reason: "live Herdr tab matches durable workspace/tab identity but current pane ownership is not exact; refusing to close",
	}, true, nil
}

func classifyTerminalHerdrMatch(client herdrClient, workspace herdr.Workspace, tab herdr.Tab, history state.TaskHistory, attempt state.Attempt) (*ReconcileAnomaly, bool, error) {
	if !hasHerdrIdentity(attempt.Herdr) {
		return &ReconcileAnomaly{
			Kind: "herdr-ownership-inconsistency", WorkspaceID: workspace.WorkspaceID, WorkspaceLabel: workspace.Label,
			TabID: tab.TabID, TabLabel: tab.Label, OwnerAttemptID: attempt.ID,
			Reason: "live Herdr tab matches a terminal Attempt with incomplete durable identity; refusing to close",
		}, true, nil
	}
	observation, err := observeHerdrOwnership(client, attempt.Herdr, history.Task.ID, history.Task.Project)
	if err != nil {
		return nil, false, err
	}
	if observation.State == herdrOwnershipExact {
		return nil, true, nil
	}
	return &ReconcileAnomaly{
		Kind: "herdr-ownership-inconsistency", WorkspaceID: workspace.WorkspaceID, WorkspaceLabel: workspace.Label,
		TabID: tab.TabID, TabLabel: tab.Label, OwnerAttemptID: attempt.ID,
		Reason: "live Herdr tab matches a terminal Attempt but current ownership is not exact; refusing to close",
	}, true, nil
}

func unattributedHerdrAnomaly(workspace herdr.Workspace, tab herdr.Tab) *ReconcileAnomaly {
	return &ReconcileAnomaly{
		Kind: "unattributed-herdr-tab", WorkspaceID: workspace.WorkspaceID, WorkspaceLabel: workspace.Label,
		TabID: tab.TabID, TabLabel: tab.Label, Reason: "Hand namespace tab has no exact durable Attempt owner",
	}
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
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
		if hasHerdrIdentity(attempt.Herdr) && !herdrCleanupSettled(attempt.TeardownHerdrState) {
			return attempt, nil
		}
		if attempt.Worktree != "" && !worktreeCleanupSettled(attempt.TeardownWorktreeState) {
			return attempt, nil
		}
		if attempt.TeardownCompletionState != "" {
			return attempt, nil
		}
	}
	return nil, nil
}

func (r *Runtime) reconcileHistoricalAttempt(ctx context.Context, home string, task state.Task, attempt state.Attempt, attest reconcileAttestations) (bool, reconciliationDecision, error) {
	if hasHerdrIdentity(attempt.Herdr) && !herdrCleanupSettled(attempt.TeardownHerdrState) {
		observation, err := observeHerdrOwnership(r.herdrClient(attempt.Herdr.Session), attempt.Herdr, task.ID, task.Project)
		if err != nil {
			return false, reconciliationDecision{}, err
		}
		switch observation.State {
		case herdrOwnershipIncomplete, herdrOwnershipMismatch:
			if attest.Pane {
				return r.abandonHistoricalPane(home, task, attempt, observation)
			}
			return false, repairDecision(reconciliationDecision{}, repairCodeTeardownResourceAmbiguous, "historical Herdr resource ownership cannot be proven safely"), nil
		case herdrOwnershipAbsent:
			if cleared, err := clearHistoricalRepair(home, task, attempt, repairCodeTeardownResourceAmbiguous, repairCodeHerdrOwnershipMismatch); err != nil {
				return false, reconciliationDecision{}, err
			} else if cleared {
				return true, reconciliationDecision{}, nil
			}
			if attempt.TeardownHerdrState == "" {
				if err := state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "herdr", state.TeardownResourceReleasing); err != nil {
					return false, reconciliationDecision{}, err
				}
			}
			if err := state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "herdr", state.TeardownResourceReleased); err != nil {
				return false, reconciliationDecision{}, err
			}
			return true, reconciliationDecision{}, nil
		case herdrOwnershipExact:
			if cleared, err := clearHistoricalRepair(home, task, attempt, repairCodeTeardownResourceAmbiguous, repairCodeHerdrOwnershipMismatch); err != nil {
				return false, reconciliationDecision{}, err
			} else if cleared {
				return true, reconciliationDecision{}, nil
			}
			if attempt.TeardownHerdrState == "" {
				if err := state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "herdr", state.TeardownResourceReleasing); err != nil {
					return false, reconciliationDecision{}, err
				}
			}
			if err := closeTaskTab(r.herdrClient(attempt.Herdr.Session), attempt.Herdr.WorkspaceID, attempt.Herdr.TabID); err != nil {
				_ = state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "herdr", state.TeardownResourceAmbiguous)
				return false, reconciliationDecision{}, fmt.Errorf("close historical Herdr resource: %w", err)
			}
			if err := state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "herdr", state.TeardownResourceReleased); err != nil {
				return false, reconciliationDecision{}, err
			}
			return true, reconciliationDecision{}, nil
		}
	}

	if attempt.Worktree != "" && !worktreeCleanupSettled(attempt.TeardownWorktreeState) {
		lease := r.observeWorktreeLease(attempt.Worktree, attempt.LeaseID)
		switch lease.State {
		case worktree.LeaseUnknown, worktree.LeaseUnprovable:
			if attest.Worktree {
				return r.abandonHistoricalWorktree(home, task, attempt, lease)
			}
			if lease.State == worktree.LeaseUnprovable {
				return false, repairDecision(reconciliationDecision{}, repairCodeLegacyWorktreeUnprovable, "historical worktree has no exact lease identity"), nil
			}
			return false, repairDecision(reconciliationDecision{}, repairCodeWorktreeUnobservable, unobservableWorktreeReason(attempt, lease.Probe)), nil
		case worktree.LeaseMismatch:
			if cleared, err := clearHistoricalRepair(home, task, attempt, repairCodeWorktreeOwnershipMismatch, repairCodeWorktreeUnobservable, repairCodeLegacyWorktreeUnprovable); err != nil {
				return false, reconciliationDecision{}, err
			} else if cleared {
				return true, reconciliationDecision{}, nil
			}
			return r.relinquishHistoricalWorktree(home, task, attempt, lease)
		case worktree.LeaseAbsent:
			if cleared, err := clearHistoricalRepair(home, task, attempt, repairCodeWorktreeOwnershipMismatch, repairCodeWorktreeUnobservable, repairCodeWorktreeLocalCommits, repairCodeWorktreeCommitSafetyUnknown); err != nil {
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
			safety := r.resolveWorktreeCommitSafety(ctx, task, attempt)
			if safety.State != commitSafetyProvenDurable {
				return false, repairDecision(reconciliationDecision{}, commitSafetyRepairCode(safety.State), safety.Reason), nil
			}
			if cleared, err := clearHistoricalRepair(home, task, attempt, repairCodeWorktreeDirty, repairCodeWorktreeOwnershipMismatch, repairCodeWorktreeUnobservable, repairCodeLegacyWorktreeUnprovable, repairCodeWorktreeLocalCommits, repairCodeWorktreeCommitSafetyUnknown); err != nil {
				return false, reconciliationDecision{}, err
			} else if cleared {
				return true, reconciliationDecision{}, nil
			}
			if attempt.TeardownWorktreeState == "" || attempt.TeardownWorktreeState == state.TeardownResourceAmbiguous {
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
			return false, repairDecision(reconciliationDecision{}, repairCodeCompletionEvidenceMismatch, "durable completion state says appended but no exact completion record exists"), nil
		}
		if cleared, err := clearHistoricalRepair(home, task, attempt, repairCodeCompletionEvidenceMismatch); err != nil {
			return false, reconciliationDecision{}, err
		} else if cleared {
			return true, reconciliationDecision{}, nil
		}
		if attempt.TeardownTerminalAttempt != "" {
			if attempt.Lifecycle == attempt.TeardownTerminalAttempt {
				if err := state.ClearHoldIfKind(home, task.ID, state.HoldKindLimit); err != nil {
					return false, reconciliationDecision{}, fmt.Errorf("clear usage-limit hold for already-settled attempt %d: %w", attempt.ID, err)
				}
				return false, reconciliationDecision{}, nil
			}
			if err := terminalizeAndClearLimitHold(home, task.ID, attempt.ID, attempt.Lifecycle, attempt.TeardownTerminalAttempt); err != nil {
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
			record = completionFor(task, attempt.TeardownDisposition, launched, attempt.LastReportState, attempt.LastReportNote)
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

func (r *Runtime) abandonHistoricalWorktree(home string, task state.Task, attempt state.Attempt, lease worktree.LeaseObservation) (bool, reconciliationDecision, error) {
	if lease.State != worktree.LeaseUnknown && lease.State != worktree.LeaseUnprovable {
		return false, reconciliationDecision{}, Precondition(fmt.Errorf("refusing to abandon worktree %s of attempt %d: ownership observed as %s, which an observation can prove or disprove; abandonment is only for ownership neither observation can settle", attempt.Worktree, attempt.ID, lease.State))
	}
	if err := recordAbandonedResource(home, task.ID, attempt, "worktree", attempt.TeardownWorktreeState); err != nil {
		return false, reconciliationDecision{}, fmt.Errorf("record abandoned worktree ownership: %w", err)
	}
	detail := fmt.Sprintf("attempt %d relinquished worktree %s with recorded lease %s on operator attestation; %s", attempt.ID, attempt.Worktree, leaseOrNone(attempt.LeaseID), abandonedWorktreeReason(attempt, lease))
	return true, reconciliationDecision{Action: reconciliationActionAbandonWorktree, Detail: detail}, nil
}

func (r *Runtime) abandonHistoricalPane(home string, task state.Task, attempt state.Attempt, observation herdrObservation) (bool, reconciliationDecision, error) {
	if observation.State != herdrOwnershipIncomplete && observation.State != herdrOwnershipMismatch {
		return false, reconciliationDecision{}, Precondition(fmt.Errorf("refusing to abandon the Herdr pane of attempt %d: ownership observed as %s, which an observation can prove or disprove and an ordinary reconcile settles; abandonment is only for ownership neither observation can settle", attempt.ID, observation.State))
	}
	if err := recordAbandonedResource(home, task.ID, attempt, "herdr", attempt.TeardownHerdrState); err != nil {
		return false, reconciliationDecision{}, fmt.Errorf("record abandoned Herdr ownership: %w", err)
	}
	detail := fmt.Sprintf("attempt %d relinquished its Herdr identity (%s) on operator attestation; ownership observed as %s, and no pane, tab or workspace was closed",
		attempt.ID, herdrIdentityText(attempt.Herdr), observation.State)
	return true, reconciliationDecision{Action: reconciliationActionAbandonPane, Detail: detail}, nil
}

func (r *Runtime) attestAttemptNeverStarted(home string, task state.Task, attempt state.Attempt) (reconciliationDecision, error) {
	if attempt.Lifecycle != state.AttemptRunning {
		return reconciliationDecision{}, Precondition(fmt.Errorf("refusing to attest that attempt %d never started: it records lifecycle %q rather than %q, and reconcile treats every other lifecycle on its own evidence",
			attempt.ID, attempt.Lifecycle, state.AttemptRunning))
	}
	if evidence, found, err := startedWorkEvidence(home, task, attempt); err != nil {
		return reconciliationDecision{}, err
	} else if found {
		return reconciliationDecision{}, Precondition(fmt.Errorf("refusing to attest that attempt %d never started: %s, which disproves it; end this attempt with `hand teardown %s` instead", attempt.ID, evidence, task.ID))
	}
	if attempt.Worktree != "" {
		observeClean := r.deps.worktree.observeClean
		if observeClean == nil {
			observeClean = worktree.ObserveCleanliness
		}
		clean, err := observeClean(attempt.Worktree)
		if err != nil {
			return reconciliationDecision{}, fmt.Errorf("observe worktree cleanliness for attempt %d: %w", attempt.ID, err)
		}
		if clean == worktree.Dirty {
			return reconciliationDecision{}, Precondition(fmt.Errorf("refusing to attest that attempt %d never started: worktree %s holds uncommitted changes, which disproves it; end this attempt with `hand teardown %s` instead", attempt.ID, attempt.Worktree, task.ID))
		}
		observeCommits := r.deps.worktree.observeCommits
		if observeCommits == nil {
			observeCommits = worktree.ObserveCommitSafety
		}
		if commits := observeCommits(attempt.Worktree); commits.State == worktree.CommitSafetyLocalOnly {
			return reconciliationDecision{}, Precondition(fmt.Errorf("refusing to attest that attempt %d never started: %d commit(s) in %s are reachable from no remote-tracking ref, which disproves it; end this attempt with `hand teardown %s` instead",
				attempt.ID, commits.Probe.LocalOnly, commits.Probe.WorkingDir, task.ID))
		}
	}
	if err := state.SetAttemptTeardownDecision(home, task.ID, attempt.ID, state.AttemptInterrupted, state.TeardownDispositionWorkerNeverStarted); err != nil {
		return reconciliationDecision{}, fmt.Errorf("record unstarted teardown decision for attempt %d: %w", attempt.ID, err)
	}
	detail := fmt.Sprintf("attempt %d recorded interrupted on operator attestation that its worker never started; no outcome about work was claimed, and its pane and worktree are released by the ordinary path under their own guards", attempt.ID)
	return reconciliationDecision{Action: reconciliationActionAttestNeverStarted, Detail: detail}, nil
}

func startedWorkEvidence(home string, task state.Task, attempt state.Attempt) (string, bool, error) {
	lines, err := state.ReadReportLines(home, task.ID)
	if err != nil {
		return "", false, fmt.Errorf("read reported state for task %q: %w", task.ID, err)
	}
	if len(lines) > 0 {
		return fmt.Sprintf("%s holds %d reported line(s)", state.ReportPath(home, task.ID), len(lines)), true, nil
	}
	switch {
	case task.PR != "":
		return "pull request " + task.PR + " is recorded for this task", true, nil
	case task.MergeExecuted || task.MergeAnnounced:
		return "a merge is recorded for this task", true, nil
	case task.DeliveredAt != "":
		return "this task is recorded as delivered at " + task.DeliveredAt, true, nil
	case attempt.LastReportState != "":
		return fmt.Sprintf("attempt %d last reported state %q", attempt.ID, attempt.LastReportState), true, nil
	case attempt.DoneVerified:
		return fmt.Sprintf("attempt %d is recorded as verified done", attempt.ID), true, nil
	}
	return "", false, nil
}

func (r *Runtime) relinquishHistoricalWorktree(home string, task state.Task, attempt state.Attempt, lease worktree.LeaseObservation) (bool, reconciliationDecision, error) {
	if err := recordAbandonedResource(home, task.ID, attempt, "worktree", attempt.TeardownWorktreeState); err != nil {
		return false, reconciliationDecision{}, fmt.Errorf("record relinquished worktree claim: %w", err)
	}
	detail := fmt.Sprintf("attempt %d relinquished worktree %s with recorded lease %s, which Treehouse now reports under lease %s; nothing was returned, pruned or deleted",
		attempt.ID, attempt.Worktree, leaseOrNone(attempt.LeaseID), leaseOrNone(lease.LeaseID))
	return true, reconciliationDecision{Action: reconciliationActionRelinquishWorktree, Detail: detail}, nil
}

func recordAbandonedResource(home, taskID string, attempt state.Attempt, resource, current string) error {
	if current == state.TeardownResourceReleasing {
		if err := state.SetAttemptTeardownResourceState(home, taskID, attempt.ID, attempt.Lifecycle, resource, state.TeardownResourceAmbiguous); err != nil {
			return err
		}
	}
	return state.SetAttemptTeardownResourceState(home, taskID, attempt.ID, attempt.Lifecycle, resource, state.TeardownResourceAbandoned)
}

func abandonedWorktreeReason(attempt state.Attempt, lease worktree.LeaseObservation) string {
	if lease.State == worktree.LeaseUnprovable {
		return fmt.Sprintf("recorded worktree %s has no exact lease identity, so ownership is neither proven nor disproven; nothing was returned, pruned or deleted", attempt.Worktree)
	}
	return unobservableWorktreeReason(attempt, lease.Probe)
}

func herdrIdentityText(ownership state.Herdr) string {
	return fmt.Sprintf("workspace=%q, tab=%q, pane=%q", ownership.WorkspaceID, ownership.TabID, ownership.PaneID)
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

func clearResolvedTerminalRepair(home string, history state.TaskHistory) (bool, error) {
	task := history.Task
	if task.RepairCode == "" || task.RepairAttemptID == 0 || !terminalRepairCanBeResolved(task.RepairCode) {
		return false, nil
	}
	var attempt *state.Attempt
	for i := range history.Attempts {
		if history.Attempts[i].ID == task.RepairAttemptID {
			attempt = &history.Attempts[i]
			break
		}
	}
	if attempt == nil || attempt.Lifecycle == state.AttemptProvisioning || attempt.Lifecycle == state.AttemptRunning {
		return false, nil
	}
	if !terminalRepairEvidenceResolved(task.RepairCode, *attempt) {
		return false, nil
	}
	// An attempt that reached its terminal lifecycle without ever recording a teardown decision never
	// gets a completion record to wait for; the resolved resource evidence above is the whole answer.
	if attempt.TeardownTerminalAttempt != "" {
		if attempt.TeardownCompletionState != state.TeardownCompletionAppended {
			return false, nil
		}
		if _, found, err := completion.FindAttempt(home, attempt.ID); err != nil {
			return false, err
		} else if !found {
			return false, nil
		}
	}
	if err := state.ClearTaskRepair(home, task.ID, task.RepairCode); err != nil {
		return false, err
	}
	return true, nil
}

func terminalRepairCanBeResolved(code string) bool {
	switch code {
	case repairCodeProvisioningLaunchAmbiguous, repairCodeProvisioningPaneMissing, repairCodeLaunchSubmittedPaneMissing,
		repairCodeLaunchAgentMismatch, repairCodeRunningPaneMissing, repairCodeRunningPaneIdentityMismatch,
		repairCodeHerdrOwnershipIncomplete, repairCodeHerdrOwnershipMismatch, repairCodeWorktreeDirty,
		repairCodeWorktreeOwnershipMismatch, repairCodeLegacyWorktreeUnprovable, repairCodeWorktreeUnobservable,
		repairCodeWorktreeLocalCommits, repairCodeWorktreeCommitSafetyUnknown,
		repairCodeTeardownResourceAmbiguous:
		return true
	default:
		return false
	}
}

func terminalRepairEvidenceResolved(code string, attempt state.Attempt) bool {
	switch code {
	case repairCodeProvisioningLaunchAmbiguous, repairCodeProvisioningPaneMissing, repairCodeLaunchSubmittedPaneMissing,
		repairCodeLaunchAgentMismatch, repairCodeRunningPaneMissing, repairCodeRunningPaneIdentityMismatch,
		repairCodeHerdrOwnershipIncomplete, repairCodeHerdrOwnershipMismatch, repairCodeTeardownResourceAmbiguous:
		return !hasHerdrIdentity(attempt.Herdr) || herdrCleanupSettled(attempt.TeardownHerdrState)
	case repairCodeWorktreeDirty, repairCodeWorktreeOwnershipMismatch, repairCodeLegacyWorktreeUnprovable, repairCodeWorktreeUnobservable,
		repairCodeWorktreeLocalCommits, repairCodeWorktreeCommitSafetyUnknown:
		return attempt.Worktree == "" || worktreeCleanupSettled(attempt.TeardownWorktreeState)
	default:
		return false
	}
}

func (r *Runtime) observeAttempt(home string, task state.Task, attempt state.Attempt) (reconciliationObservation, error) {
	observation := reconciliationObservation{}
	reports, err := state.ReadReportLines(home, task.ID)
	if err != nil {
		return observation, fmt.Errorf("read worker report: %w", err)
	}
	if report, ok := state.LastReportedState(reports); ok {
		observation.ReportState = report.State
	}
	skipHerdrObservation := attempt.Lifecycle == state.AttemptProvisioning && attempt.Herdr.WorkspaceID != "" && attempt.Herdr.TabID != "" && attempt.Herdr.PaneID != "" && attempt.LaunchSubmittedAt == ""
	if attempt.Worktree != "" {
		lease := r.observeWorktreeLease(attempt.Worktree, attempt.LeaseID)
		observation.Treehouse.State = treehouseLeaseState(lease.State)
		observation.Treehouse.Probe = lease.Probe
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
	if hasHerdrIdentity(attempt.Herdr) && !skipHerdrObservation {
		var err error
		observation.Herdr, err = observeHerdrOwnership(r.herdrClient(attempt.Herdr.Session), attempt.Herdr, task.ID, task.Project)
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
		if observation.Herdr.State == herdrOwnershipExact &&
			harness.IsOneShot(attempt.Harness) &&
			attempt.LaunchConfirmedAt != "" {
			pane, normalizeErr := workerobs.Normalize(attempt, observation.Herdr.Pane, r.herdrClient(attempt.Herdr.Session))
			if normalizeErr != nil {
				return observation, fmt.Errorf("observe one-shot worker liveness: %w", normalizeErr)
			}
			observation.Herdr.Agent, observation.Herdr.AgentStatus = pane.Agent, pane.AgentStatus
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
		briefPath := filepath.Join(home, task.Brief)
		_, hasFrontMatter, err := brief.Parse(briefPath)
		if err != nil {
			return fmt.Errorf("parse persisted brief for launch confirmation: %w", err)
		}
		reportPath, err := absoluteReportPath(home, task.ID)
		if err != nil {
			return fmt.Errorf("resolve persisted report path: %w", err)
		}
		spec, err := r.deps.buildHarness(attempt.Harness, harness.Options{
			Worktree: attempt.Worktree, Brief: briefPath, ReportPath: reportPath, Model: attempt.Model, Effort: attempt.Effort,
			ExecutionClass: brief.ExecutionClass(attempt.ExecutionClass), BriefHasFrontMatter: hasFrontMatter,
		})
		if err != nil {
			return fmt.Errorf("build persisted launch evidence: %w", err)
		}
		if err := r.deps.confirmLaunch(r.herdrClient(attempt.Herdr.Session), attempt.Herdr.PaneID, attempt.Harness, spec); err != nil {
			return fmt.Errorf("confirm persisted launch: %w", err)
		}
		return state.MarkLaunchConfirmed(home, task.ID, attempt.ID, r.deps.now().Format(time.RFC3339))
	case reconciliationActionMarkRunning:
		return state.MarkAttemptRunning(home, task.ID, attempt.ID)
	default:
		return nil
	}
}

func (r *Runtime) recordRepair(home string, task state.Task, attempt state.Attempt, decision reconciliationDecision, result *ReconcileResult) error {
	reason := repairReasonWithTreatment(task.ID, decision.RepairCode, decision.RepairReason)
	result.Outcome, result.RepairCode, result.RepairReason = reconcileOutcomeRepair, decision.RepairCode, reason
	if task.RepairCode == decision.RepairCode && task.RepairReason == reason && task.RepairAttemptID == attempt.ID {
		return nil
	}
	return state.SetTaskRepair(home, task.ID, decision.RepairCode, reason, attempt.ID, r.deps.now().Format(time.RFC3339))
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
	if observation.Treehouse.State == treehouseLeaseExact {
		switch task.RepairCode {
		case repairCodeWorktreeOwnershipMismatch, repairCodeWorktreeUnobservable, repairCodeLegacyWorktreeUnprovable:
			return attempt.Lifecycle == state.AttemptRunning || observation.Worktree.State == worktreeClean
		case repairCodeWorktreeDirty:
			return observation.Worktree.State == worktreeClean
		}
	}
	if task.RepairCode == repairCodeWorktreeUnobservable && worktreeCleanupSettled(attempt.TeardownWorktreeState) {
		return true
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
	if terminal, disposition, converge := decideTerminalConvergence(attempt, observation); converge {
		decision.Action = reconciliationActionConvergeTerminal
		decision.TerminalAttempt, decision.Disposition = terminal, disposition
		decision.Detail = fmt.Sprintf("attempt %d converged to %s on observed evidence", attempt.ID, terminal)
		return decision
	}
	if attempt.Worktree != "" {
		switch observation.Treehouse.State {
		case treehouseLeaseMismatch:
			return repairDecision(decision, repairCodeWorktreeOwnershipMismatch, "recorded worktree path is leased under a different Treehouse lease ID")
		case treehouseLeaseUnknown:
			return repairDecision(decision, repairCodeWorktreeUnobservable, unobservableWorktreeReason(attempt, observation.Treehouse.Probe))
		case treehouseLeaseUnprovable:
			return repairDecision(decision, repairCodeLegacyWorktreeUnprovable, "recorded worktree ownership has no provable Treehouse lease identity")
		case treehouseLeaseAbsent:
			return repairDecision(decision, repairCodeWorktreeOwnershipMismatch, "recorded Treehouse lease is absent")
		case treehouseLeaseUnobserved:
			return repairDecision(decision, repairCodeWorktreeUnobservable, "recorded Treehouse lease was not observed")
		}
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
			return repairDecision(decision, repairCodeRunningPaneMissing, runningPaneMissingReason(observation.Landing))
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
	if hasHerdrIdentity(attempt.Herdr) && incompleteHerdrOwnership(attempt.Herdr) != nil {
		return repairDecision(decision, repairCodeHerdrOwnershipIncomplete, "persisted Herdr ownership identity is incomplete")
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
		if harness.IsOneShot(attempt.Harness) {
			if observation.Herdr.Agent != "" && observation.Herdr.Agent != attempt.Harness {
				return repairDecision(decision, repairCodeLaunchAgentMismatch, "the recorded pane contains a different live harness")
			}
			if attempt.LaunchConfirmedAt != "" {
				decision.Action = reconciliationActionMarkRunning
				return decision
			}
			decision.Action = reconciliationActionConfirmLaunch
			return decision
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
		if unwindableProvisioning(attempt) {
			decision.Action = reconciliationActionUnwindProvisioning
			decision.TerminalAttempt, decision.Disposition = state.AttemptInterrupted, state.TeardownDispositionProvisioningUnwound
			decision.Detail = fmt.Sprintf("attempt %d unwound: launch evidence exists but no worktree lease and no Herdr identity were ever persisted, so nothing was released and no ownership was claimed", attempt.ID)
			return decision
		}
		return repairDecision(decision, repairCodeProvisioningPaneMissing, "launch evidence exists but no Herdr pane identity was persisted")
	}

	if attempt.Worktree != "" {
		switch observation.Treehouse.State {
		case treehouseLeaseMismatch:
			return repairDecision(decision, repairCodeWorktreeOwnershipMismatch, "recorded worktree path is leased under a different Treehouse lease ID")
		case treehouseLeaseUnknown:
			return repairDecision(decision, repairCodeWorktreeUnobservable, unobservableWorktreeReason(attempt, observation.Treehouse.Probe))
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

func unwindableProvisioning(attempt state.Attempt) bool {
	return attempt.Lifecycle == state.AttemptProvisioning && !hasHerdrIdentity(attempt.Herdr) &&
		attempt.Worktree == "" && attempt.LeaseID == "" && attempt.TeardownTerminalAttempt == ""
}

func leaseOrNone(leaseID string) string {
	if leaseID == "" {
		return "(none recorded)"
	}
	return leaseID
}

func unobservableWorktreeReason(attempt state.Attempt, probe worktree.LeaseProbe) string {
	lease := leaseOrNone(attempt.LeaseID)
	if probe.Command == "" {
		return fmt.Sprintf("recorded worktree %s could not be observed, so recorded lease %s is neither proven nor disproven; destructive cleanup refused because ownership could not be proven, not because a lease mismatched", attempt.Worktree, lease)
	}
	return fmt.Sprintf(
		"recorded worktree ownership could not be observed: %s; observed by running %q with working directory %s, which is what selects the pool; recorded lease %s is neither proven nor disproven; destructive cleanup refused because ownership could not be proven, not because a lease mismatched",
		probe.Reason, probe.Command, probe.WorkingDir, lease)
}

type commitSafety string

const (
	commitSafetyProvenDurable   commitSafety = "proven-durable"
	commitSafetyLocalWorkAtRisk commitSafety = "local-work-at-risk"
	commitSafetyUnprovable      commitSafety = "unprovable"
)

type commitSafetyDecision struct {
	State  commitSafety
	Reason string
}

func (r *Runtime) resolveWorktreeCommitSafety(ctx context.Context, task state.Task, attempt state.Attempt) commitSafetyDecision {
	observeCommits := r.deps.worktree.observeCommits
	if observeCommits == nil {
		observeCommits = worktree.ObserveCommitSafety
	}
	observation := observeCommits(attempt.Worktree)
	if observation.State == worktree.CommitSafetyRemoteObserved {
		return commitSafetyDecision{
			State:  commitSafetyProvenDurable,
			Reason: fmt.Sprintf("every commit reachable from %s in %s is also reachable from one of the %d remote-tracking refs this clone holds, so returning the worktree discards nothing", shortCommit(observation.Probe.Head), observation.Probe.WorkingDir, observation.Probe.RemoteRefs),
		}
	}
	if task.PR == "" {
		return withheldCommitSafety(attempt, observation, "no pull request is recorded for this task, so no pushed head can hold them either")
	}
	prHead := r.deps.prHead
	if prHead == nil {
		prHead = ghutil.ObserveHeadCommit
	}
	pushed := prHead(ctx, task.PR)
	if !pushed.Found() {
		return commitSafetyDecision{
			State:  commitSafetyUnprovable,
			Reason: unprovableCommitSafetyReason(attempt, observation, fmt.Sprintf("the head commit of pull request %s could not be read, so whether GitHub holds them is unobserved rather than answered: %s", task.PR, pushed.Reason())),
		}
	}
	if observation.Probe.Head != "" && pushed.Head == observation.Probe.Head {
		return commitSafetyDecision{
			State:  commitSafetyProvenDurable,
			Reason: fmt.Sprintf("the remote-tracking refs in %s do not prove %s durable, and GitHub records that exact commit as the head of pull request %s, which holds it independently of this worktree", observation.Probe.WorkingDir, shortCommit(observation.Probe.Head), task.PR),
		}
	}
	return withheldCommitSafety(attempt, observation, fmt.Sprintf("pull request %s records head commit %s instead, so GitHub was never observed holding %s", task.PR, shortCommit(pushed.Head), shortCommit(observation.Probe.Head)))
}

func withheldCommitSafety(attempt state.Attempt, observation worktree.CommitSafetyObservation, unheldBecause string) commitSafetyDecision {
	if observation.State == worktree.CommitSafetyLocalOnly {
		return commitSafetyDecision{State: commitSafetyLocalWorkAtRisk, Reason: localOnlyCommitsReason(observation, unheldBecause)}
	}
	return commitSafetyDecision{State: commitSafetyUnprovable, Reason: unprovableCommitSafetyReason(attempt, observation, unheldBecause)}
}

func commitSafetyRepairCode(safety commitSafety) string {
	if safety == commitSafetyLocalWorkAtRisk {
		return repairCodeWorktreeLocalCommits
	}
	return repairCodeWorktreeCommitSafetyUnknown
}

func commitSafetyFinding(observation worktree.CommitSafetyObservation) string {
	if observation.State == worktree.CommitSafetyLocalOnly {
		return fmt.Sprintf("%d commit(s) in %s are reachable from none of the %d remote-tracking refs this clone holds",
			observation.Probe.LocalOnly, observation.Probe.WorkingDir, observation.Probe.RemoteRefs)
	}
	return observation.Probe.Reason
}

func localOnlyCommitsReason(observation worktree.CommitSafetyObservation, unheldBecause string) string {
	return fmt.Sprintf(
		"%s, and %s; observed by running %q with working directory %s; automatic reconciliation will not discard work no other copy is known to hold",
		commitSafetyFinding(observation), unheldBecause, observation.Probe.Command, observation.Probe.WorkingDir)
}

func unprovableCommitSafetyReason(attempt state.Attempt, observation worktree.CommitSafetyObservation, unheldBecause string) string {
	return fmt.Sprintf(
		"whether worktree %s still holds the only copy of any commit could not be observed: %s, and %s; observed by running %q with working directory %s; the return is withheld because the question went unanswered, not because work was found at risk",
		attempt.Worktree, commitSafetyFinding(observation), unheldBecause, observation.Probe.Command, observation.Probe.WorkingDir)
}

func shortCommit(commit string) string {
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}

func repairDecision(decision reconciliationDecision, code, reason string) reconciliationDecision {
	decision.Action = reconciliationActionNeedsRepair
	decision.RepairCode = code
	decision.RepairReason = reason
	return decision
}
