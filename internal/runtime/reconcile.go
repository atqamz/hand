package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/herdr"
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
	reconciliationActionAbandonWorktree      reconciliationAction = "abandon-worktree"
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
	repairCodeWorktreeUnobservable        = "worktree-ownership-unobservable"
	repairCodeTeardownResourceAmbiguous   = "teardown-resource-ambiguous"
	repairCodeCompletionEvidenceMismatch  = "completion-evidence-mismatch"
	repairCodeMergeFactMismatch           = "merge-fact-mismatch"
)

type treehouseObservation struct {
	State treehouseLeaseState
	Probe worktree.LeaseProbe
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
	Detail         string
}

// AbandonWorktree carries an operator attestation and demands an explicit ID, so it can never
// relinquish more than the one recorded lease the operator named.
type ReconcileRequest struct {
	Context         context.Context
	Home            string
	ID              string
	AbandonWorktree bool
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
	Detail         string `json:"detail,omitempty"`
	Error          string `json:"error,omitempty"`
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
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if req.ID != "" {
		result, err := r.reconcileTask(ctx, req.Home, req.ID, req.AbandonWorktree)
		if err != nil {
			result.Error = err.Error()
		}
		return ReconcileReport{Results: []ReconcileResult{result}}, err
	}
	if req.AbandonWorktree {
		return ReconcileReport{}, Precondition(fmt.Errorf("abandoning a worktree lease needs an explicit task ID"))
	}
	histories, err := state.ListReconciliationHistories(req.Home)
	if err != nil {
		return ReconcileReport{}, err
	}
	report := ReconcileReport{Results: make([]ReconcileResult, 0, len(histories))}
	var errs []error
	for _, history := range histories {
		result, err := r.reconcileTask(ctx, req.Home, history.Task.ID, false)
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

func (r *Runtime) reconcileTask(ctx context.Context, home, id string, abandonWorktree bool) (ReconcileResult, error) {
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
			if err := r.recordRepair(home, history.Task, state.Attempt{ID: attemptID}, decision); err != nil {
				return result, err
			}
			result.Outcome, result.RepairCode, result.RepairReason = reconcileOutcomeRepair, decision.RepairCode, decision.RepairReason
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
			progress, decision, err := r.reconcileHistoricalAttempt(home, history.Task, *historical, abandonWorktree)
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
		if attempt.TeardownTerminalAttempt != "" {
			progress, decision, err := r.reconcileHistoricalAttempt(home, history.Task, attempt, abandonWorktree)
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
				recordHistoricalProgress(&result, decision)
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
			prMerged = ghutil.PRIsMerged
		}
		merged, err := prMerged(ctx, task.PR)
		if err != nil {
			return false, false, "github-pr", fmt.Errorf("observe merged PR %s: %w", task.PR, err)
		}
		return merged, !merged, "github-pr", nil
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
	client := r.deps.herdr()
	ownerships, err := state.ListHerdrOwnerships(home)
	if err != nil {
		return nil, err
	}
	ownershipCandidates := make(map[string][]string, len(ownerships))
	for _, ownership := range ownerships {
		if ownership.WorkspaceID != "" && ownership.TabID != "" {
			key := ownership.WorkspaceID + "\x00" + ownership.TabID
			if !containsString(ownershipCandidates[key], ownership.TaskID) {
				ownershipCandidates[key] = append(ownershipCandidates[key], ownership.TaskID)
			}
		}
	}
	workspaces, err := client.WorkspaceList()
	if err != nil {
		return nil, fmt.Errorf("list Herdr workspaces: %w", err)
	}
	sort.Slice(workspaces, func(i, j int) bool {
		if workspaces[i].Label != workspaces[j].Label {
			return workspaces[i].Label < workspaces[j].Label
		}
		return workspaces[i].WorkspaceID < workspaces[j].WorkspaceID
	})
	var anomalies []ReconcileAnomaly
	for _, workspace := range workspaces {
		if !strings.HasPrefix(workspace.Label, "hand:") {
			continue
		}
		tabs, err := client.TabList(workspace.WorkspaceID)
		if err != nil {
			return nil, fmt.Errorf("list Herdr tabs for workspace %s: %w", workspace.WorkspaceID, err)
		}
		sort.Slice(tabs, func(i, j int) bool {
			if tabs[i].TabID != tabs[j].TabID {
				return tabs[i].TabID < tabs[j].TabID
			}
			return tabs[i].Label < tabs[j].Label
		})
		for _, tab := range tabs {
			anomaly, err := r.classifyHerdrTab(home, client, workspace, tab, ownershipCandidates)
			if err != nil {
				return nil, err
			}
			if anomaly != nil {
				anomalies = append(anomalies, *anomaly)
			}
		}
	}
	return anomalies, nil
}

func (r *Runtime) classifyHerdrTab(home string, client herdrClient, workspace herdr.Workspace, tab herdr.Tab, ownershipCandidates map[string][]string) (*ReconcileAnomaly, error) {
	candidates := append([]string(nil), ownershipCandidates[workspace.WorkspaceID+"\x00"+tab.TabID]...)
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
		if hasHerdrIdentity(attempt.Herdr) && attempt.TeardownHerdrState != state.TeardownResourceReleased {
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

func (r *Runtime) reconcileHistoricalAttempt(home string, task state.Task, attempt state.Attempt, abandonWorktree bool) (bool, reconciliationDecision, error) {
	if hasHerdrIdentity(attempt.Herdr) && attempt.TeardownHerdrState != state.TeardownResourceReleased {
		observation, err := observeHerdrOwnership(r.deps.herdr(), attempt.Herdr, task.ID, task.Project)
		if err != nil {
			return false, reconciliationDecision{}, err
		}
		switch observation.State {
		case herdrOwnershipIncomplete, herdrOwnershipMismatch:
			return false, repairDecision(reconciliationDecision{}, repairCodeTeardownResourceAmbiguous, "historical Herdr resource ownership cannot be proven safely"), nil
		case herdrOwnershipAbsent:
			if cleared, err := clearHistoricalRepair(home, task, attempt, repairCodeTeardownResourceAmbiguous, repairCodeHerdrOwnershipMismatch); err != nil {
				return false, reconciliationDecision{}, err
			} else if cleared {
				return true, reconciliationDecision{}, nil
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

	if attempt.Worktree != "" && !worktreeCleanupSettled(attempt.TeardownWorktreeState) {
		lease := r.observeWorktreeLease(attempt.Worktree, attempt.LeaseID)
		switch lease.State {
		case worktree.LeaseUnknown:
			if abandonWorktree {
				return r.abandonHistoricalWorktree(home, task, attempt, lease)
			}
			return false, repairDecision(reconciliationDecision{}, repairCodeWorktreeUnobservable, unobservableWorktreeReason(attempt, lease.Probe)), nil
		case worktree.LeaseUnprovable:
			return false, repairDecision(reconciliationDecision{}, repairCodeLegacyWorktreeUnprovable, "historical worktree has no exact lease identity"), nil
		case worktree.LeaseMismatch:
			if attempt.TeardownWorktreeState == state.TeardownResourceReleasing {
				if err := state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "worktree", state.TeardownResourceReleased); err != nil {
					return false, reconciliationDecision{}, err
				}
				return true, reconciliationDecision{}, nil
			}
			return false, repairDecision(reconciliationDecision{}, repairCodeWorktreeOwnershipMismatch, "historical worktree path is held by a different Treehouse lease"), nil
		case worktree.LeaseAbsent:
			if cleared, err := clearHistoricalRepair(home, task, attempt, repairCodeWorktreeOwnershipMismatch, repairCodeWorktreeUnobservable); err != nil {
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
			if cleared, err := clearHistoricalRepair(home, task, attempt, repairCodeWorktreeDirty, repairCodeWorktreeOwnershipMismatch, repairCodeWorktreeUnobservable, repairCodeLegacyWorktreeUnprovable); err != nil {
				return false, reconciliationDecision{}, err
			} else if cleared {
				return true, reconciliationDecision{}, nil
			}
			// Proven ownership is what clears the ambiguous latch: the same observation that would
			// have refused the release is the one allowed to resume it.
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

// Records an operator's attestation that Hand relinquishes a lease no observation can reach. It
// runs no destructive command and refuses any state an observation could still prove or disprove.
func (r *Runtime) abandonHistoricalWorktree(home string, task state.Task, attempt state.Attempt, lease worktree.LeaseObservation) (bool, reconciliationDecision, error) {
	if lease.State != worktree.LeaseUnknown {
		return false, reconciliationDecision{}, Precondition(fmt.Errorf("refusing to abandon worktree %s of attempt %d: ownership observed as %s, which is provable evidence; abandonment is only for an unobservable pool", attempt.Worktree, attempt.ID, lease.State))
	}
	if err := state.SetAttemptTeardownResourceState(home, task.ID, attempt.ID, attempt.Lifecycle, "worktree", state.TeardownResourceAbandoned); err != nil {
		return false, reconciliationDecision{}, fmt.Errorf("record abandoned worktree ownership: %w", err)
	}
	detail := fmt.Sprintf("attempt %d relinquished worktree %s with recorded lease %s on operator attestation; %s", attempt.ID, attempt.Worktree, leaseOrNone(attempt.LeaseID), unobservableWorktreeReason(attempt, lease.Probe))
	return true, reconciliationDecision{Action: reconciliationActionAbandonWorktree, Detail: detail}, nil
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
	if attempt.TeardownCompletionState != state.TeardownCompletionAppended {
		return false, nil
	}
	if _, found, err := completion.FindAttempt(home, attempt.ID); err != nil {
		return false, err
	} else if !found {
		return false, nil
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
		return !hasHerdrIdentity(attempt.Herdr) || attempt.TeardownHerdrState == state.TeardownResourceReleased
	case repairCodeWorktreeDirty, repairCodeWorktreeOwnershipMismatch, repairCodeLegacyWorktreeUnprovable, repairCodeWorktreeUnobservable:
		return attempt.Worktree == "" || worktreeCleanupSettled(attempt.TeardownWorktreeState)
	default:
		return false
	}
}

func (r *Runtime) observeAttempt(_ string, task state.Task, attempt state.Attempt) (reconciliationObservation, error) {
	observation := reconciliationObservation{}
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

func repairDecision(decision reconciliationDecision, code, reason string) reconciliationDecision {
	decision.Action = reconciliationActionNeedsRepair
	decision.RepairCode = code
	decision.RepairReason = reason
	return decision
}
