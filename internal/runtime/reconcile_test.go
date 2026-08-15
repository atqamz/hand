package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/routing"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

func TestDecideReconciliationUsesPersistedAttemptIdentity(t *testing.T) {
	attempt := state.Attempt{
		ID: 17, TaskID: "task-1", Lifecycle: state.AttemptProvisioning,
		Harness: "claude", Model: "opus", Effort: "high",
		ExecutionClass: "mechanical", PlannedAgainst: "base-1",
		RequestedProfile: "profile-a", RoutingSource: "route-a",
		LaunchConfirmedAt: "2026-08-15T00:00:00Z",
		LaunchSubmittedAt: "2026-08-15T00:00:00Z",
		Herdr:             state.Herdr{WorkspaceID: "ws", TabID: "tab", PaneID: "pane"},
	}
	decision := decideReconciliation(state.Task{ID: "task-1"}, attempt, reconciliationObservation{
		Herdr: herdrObservation{State: herdrOwnershipExact, Agent: "claude"},
	})
	if decision.Action != reconciliationActionMarkRunning {
		t.Fatalf("action = %q, want %q", decision.Action, reconciliationActionMarkRunning)
	}
	if decision.Harness != attempt.Harness || decision.Model != attempt.Model || decision.Effort != attempt.Effort || decision.Profile != attempt.RequestedProfile || decision.PlannedAgainst != attempt.PlannedAgainst {
		t.Fatalf("decision identity = %+v, want persisted attempt identity", decision)
	}
}

func TestDecideReconciliationProvisioningMatrix(t *testing.T) {
	tests := []struct {
		name        string
		attempt     state.Attempt
		observation reconciliationObservation
		wantAction  reconciliationAction
		wantCode    string
	}{
		{
			name:       "attempt created",
			attempt:    state.Attempt{Lifecycle: state.AttemptProvisioning},
			wantAction: reconciliationActionContinueProvisioning,
		},
		{
			name:    "worktree recorded clean exact lease",
			attempt: state.Attempt{Lifecycle: state.AttemptProvisioning, Worktree: "/pool/1", LeaseID: "lease-1"},
			observation: reconciliationObservation{
				Treehouse: treehouseObservation{State: treehouseLeaseExact},
				Worktree:  worktreeObservation{State: worktreeClean},
			},
			wantAction: reconciliationActionContinueProvisioning,
		},
		{
			name:    "worktree recorded dirty",
			attempt: state.Attempt{Lifecycle: state.AttemptProvisioning, Worktree: "/pool/1", LeaseID: "lease-1"},
			observation: reconciliationObservation{
				Treehouse: treehouseObservation{State: treehouseLeaseExact},
				Worktree:  worktreeObservation{State: worktreeDirty},
			},
			wantAction: reconciliationActionNeedsRepair,
			wantCode:   repairCodeWorktreeDirty,
		},
		{
			name:    "worktree lease mismatch",
			attempt: state.Attempt{Lifecycle: state.AttemptProvisioning, Worktree: "/pool/1", LeaseID: "lease-1"},
			observation: reconciliationObservation{
				Treehouse: treehouseObservation{State: treehouseLeaseMismatch},
				Worktree:  worktreeObservation{State: worktreeClean},
			},
			wantAction: reconciliationActionNeedsRepair,
			wantCode:   repairCodeWorktreeOwnershipMismatch,
		},
		{
			name:        "pane recorded without launch evidence",
			attempt:     state.Attempt{Lifecycle: state.AttemptProvisioning, Herdr: state.Herdr{WorkspaceID: "ws", TabID: "tab", PaneID: "pane"}},
			observation: reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipExact, Agent: "claude"}},
			wantAction:  reconciliationActionNeedsRepair,
			wantCode:    repairCodeProvisioningLaunchAmbiguous,
		},
		{
			name:        "submitted pane healthy",
			attempt:     state.Attempt{Lifecycle: state.AttemptProvisioning, Harness: "claude", LaunchSubmittedAt: "2026-08-15T00:00:00Z", Herdr: state.Herdr{WorkspaceID: "ws", TabID: "tab", PaneID: "pane"}},
			observation: reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipExact, Agent: "claude"}},
			wantAction:  reconciliationActionConfirmLaunch,
		},
		{
			name:        "submitted pane missing",
			attempt:     state.Attempt{Lifecycle: state.AttemptProvisioning, Harness: "claude", LaunchSubmittedAt: "2026-08-15T00:00:00Z", Herdr: state.Herdr{WorkspaceID: "ws", TabID: "tab", PaneID: "pane"}},
			observation: reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipAbsent}},
			wantAction:  reconciliationActionNeedsRepair,
			wantCode:    repairCodeLaunchSubmittedPaneMissing,
		},
		{
			name:        "confirmed pane healthy",
			attempt:     state.Attempt{Lifecycle: state.AttemptProvisioning, Harness: "claude", LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z", Herdr: state.Herdr{WorkspaceID: "ws", TabID: "tab", PaneID: "pane"}},
			observation: reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipExact, Agent: "claude"}},
			wantAction:  reconciliationActionMarkRunning,
		},
		{
			name:        "running pane missing",
			attempt:     state.Attempt{Lifecycle: state.AttemptRunning, Harness: "claude", Herdr: state.Herdr{WorkspaceID: "ws", TabID: "tab", PaneID: "pane"}},
			observation: reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipAbsent}},
			wantAction:  reconciliationActionNeedsRepair,
			wantCode:    repairCodeRunningPaneMissing,
		},
		{
			name:    "running absent lease",
			attempt: state.Attempt{Lifecycle: state.AttemptRunning, Worktree: "/pool/1", LeaseID: "lease-1", Herdr: state.Herdr{WorkspaceID: "ws", TabID: "tab", PaneID: "pane"}},
			observation: reconciliationObservation{
				Treehouse: treehouseObservation{State: treehouseLeaseAbsent},
				Herdr:     herdrObservation{State: herdrOwnershipExact, Agent: "claude"},
			},
			wantAction: reconciliationActionNeedsRepair,
			wantCode:   repairCodeWorktreeOwnershipMismatch,
		},
		{
			name:    "running unprovable lease",
			attempt: state.Attempt{Lifecycle: state.AttemptRunning, Worktree: "/pool/1", LeaseID: "lease-1", Herdr: state.Herdr{WorkspaceID: "ws", TabID: "tab", PaneID: "pane"}},
			observation: reconciliationObservation{
				Treehouse: treehouseObservation{State: treehouseLeaseUnprovable},
				Herdr:     herdrObservation{State: herdrOwnershipExact, Agent: "claude"},
			},
			wantAction: reconciliationActionNeedsRepair,
			wantCode:   repairCodeLegacyWorktreeUnprovable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := decideReconciliation(state.Task{ID: "task-1"}, tt.attempt, tt.observation)
			if decision.Action != tt.wantAction {
				t.Fatalf("action = %q, want %q (%+v)", decision.Action, tt.wantAction, decision)
			}
			if decision.RepairCode != tt.wantCode {
				t.Fatalf("repair code = %q, want %q", decision.RepairCode, tt.wantCode)
			}
		})
	}
}

func TestDecideReconciliationObservationFailureDoesNotBecomeRepair(t *testing.T) {
	decision := decideReconciliation(state.Task{ID: "task-1"}, state.Attempt{
		Lifecycle: state.AttemptRunning, Harness: "claude", Herdr: state.Herdr{WorkspaceID: "ws", TabID: "tab", PaneID: "pane"},
	}, reconciliationObservation{ObservationError: true})
	if decision.Action != reconciliationActionBlocked {
		t.Fatalf("action = %q, want blocked", decision.Action)
	}
	if decision.RepairCode != "" {
		t.Fatalf("repair code = %q, want empty on observation failure", decision.RepairCode)
	}
}

func TestObserveHerdrOwnershipRequiresAllDurableIdentityEdges(t *testing.T) {
	client := &reconcileHerdrClient{
		workspace: herdr.Workspace{WorkspaceID: "ws-1", Label: "hand:demo"},
		tabs:      []herdr.Tab{{TabID: "tab-1", WorkspaceID: "ws-1", Label: "task-1"}},
		pane:      herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1", Agent: "claude"},
	}
	observation, err := observeHerdrOwnership(client, state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, "task-1", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != herdrOwnershipExact || observation.Agent != "claude" {
		t.Fatalf("observation = %+v, want exact claude ownership", observation)
	}

	client.pane.PaneID = "pane-new"
	observation, err = observeHerdrOwnership(client, state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, "task-1", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != herdrOwnershipMismatch {
		t.Fatalf("reused pane observation = %+v, want mismatch", observation)
	}
}

func TestObserveHerdrOwnershipPreservesObservationFailures(t *testing.T) {
	client := &reconcileHerdrClient{err: errors.New("herdr service unavailable")}
	_, err := observeHerdrOwnership(client, state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, "task-1", "demo")
	if err == nil || err.Error() != "find Herdr workspace: herdr service unavailable" {
		t.Fatalf("observeHerdrOwnership() = %v, want service error", err)
	}
}

type reconcileHerdrClient struct {
	workspace herdr.Workspace
	tabs      []herdr.Tab
	pane      herdr.Pane
	err       error
}

func (f *reconcileHerdrClient) FindWorkspaceByLabel(string) (herdr.Workspace, bool, error) {
	if f.err != nil {
		return herdr.Workspace{}, false, f.err
	}
	return f.workspace, f.workspace.WorkspaceID != "", nil
}
func (f *reconcileHerdrClient) WorkspaceList() ([]herdr.Workspace, error) { return nil, nil }
func (f *reconcileHerdrClient) WorkspaceCreate(string, string) (herdr.Workspace, herdr.Tab, herdr.Pane, error) {
	return herdr.Workspace{}, herdr.Tab{}, herdr.Pane{}, errors.New("unused")
}
func (f *reconcileHerdrClient) WorkspaceClose(string) error         { return errors.New("unused") }
func (f *reconcileHerdrClient) TabList(string) ([]herdr.Tab, error) { return f.tabs, nil }
func (f *reconcileHerdrClient) TabCreate(string, string, string) (herdr.Tab, herdr.Pane, error) {
	return herdr.Tab{}, herdr.Pane{}, errors.New("unused")
}
func (f *reconcileHerdrClient) TabRename(string, string) error       { return errors.New("unused") }
func (f *reconcileHerdrClient) TabClose(string) error                { return errors.New("unused") }
func (f *reconcileHerdrClient) PaneGet(string) (herdr.Pane, error)   { return f.pane, nil }
func (f *reconcileHerdrClient) PaneRun(string, string) error         { return errors.New("unused") }
func (f *reconcileHerdrClient) PaneSendKeys(string, ...string) error { return errors.New("unused") }
func (f *reconcileHerdrClient) PaneRead(string, int) (string, error) { return "", errors.New("unused") }

func TestReconcileAttemptCreatedContinuesTheSameAttempt(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Model: "opus", Effort: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &healthyReconcileHerdr{}
	gets := 0
	r := reconcileRuntime(client, func(path, holder string) (worktree.Lease, error) {
		gets++
		return worktree.Lease{Path: filepath.Join(home, "leased"), ID: "lease-1"}, nil
	})
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.ID != attempt.ID || history.ActiveAttempt.Lifecycle != state.AttemptRunning {
		t.Fatalf("history after reconcile = %+v, want same running Attempt %d", history, attempt.ID)
	}
	if gets != 1 || client.runs != 1 || len(report.Results) != 1 || report.Results[0].Outcome != reconcileOutcomeHealthy {
		t.Fatalf("reconcile effects = gets %d runs %d report %+v", gets, client.runs, report)
	}
	second, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Results[0].Outcome != reconcileOutcomeHealthy || gets != 1 || client.runs != 1 {
		t.Fatalf("second reconcile = %+v, gets=%d runs=%d, want no new effects", second, gets, client.runs)
	}
}

func TestReconcileNeverReroutesAnExistingAttempt(t *testing.T) {
	home := reconcileFixture(t)
	if err := routing.WriteProfile(home, routing.Profile{Name: "profile-a", Harness: "claude", Model: "model-a", Effort: "low"}); err != nil {
		t.Fatal(err)
	}
	if err := routing.WriteProfile(home, routing.Profile{Name: "profile-b", Harness: "codex", Model: "model-b", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Model: "model-a", Effort: "low", ExecutionClass: "deep", PlannedAgainst: "base-a", RequestedProfile: "profile-a", RoutingSource: "route-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := routing.WriteRoute(home, routing.Route{Kind: routing.TaskKindShip, ExecutionClass: routing.ExecutionClassDeep, Profile: "profile-b"}); err != nil {
		t.Fatal(err)
	}
	var gotHarness string
	var got harness.Options
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.buildHarness = func(name string, options harness.Options) (string, error) {
		gotHarness, got = name, options
		return "launch", nil
	}
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.ID != attempt.ID || gotHarness != "claude" || got.Model != "model-a" || got.Effort != "low" || got.ExecutionClass != brief.ExecutionClass("deep") {
		t.Fatalf("history=%+v launch=%q %+v, want persisted profile-a execution", history, gotHarness, got)
	}
}

func TestReconcileRecordedWorktreeNeverAcquiresAnotherLease(t *testing.T) {
	home := reconcileFixture(t)
	path := filepath.Join(home, "leased")
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: path, LeaseID: "lease-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	gets := 0
	r := reconcileRuntime(&healthyReconcileHerdr{}, func(string, string) (worktree.Lease, error) {
		gets++
		return worktree.Lease{}, errors.New("unexpected second treehouse get")
	})
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.ID != attempt.ID || gets != 0 {
		t.Fatalf("history=%+v gets=%d, want same Attempt and no get", history, gets)
	}
	if report.Results[0].Outcome != reconcileOutcomeHealthy {
		t.Fatalf("report = %+v, want healthy", report)
	}
}

func TestReconcileDirtyWorktreeRecordsRepairWithoutTouchingIt(t *testing.T) {
	home := reconcileFixture(t)
	path := filepath.Join(home, "leased")
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: path, LeaseID: "lease-1",
	}); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.worktree.observeClean = func(string) (worktree.Cleanliness, error) { return worktree.Dirty, nil }
	result, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != repairCodeWorktreeDirty || result.Results[0].Outcome != reconcileOutcomeRepair {
		t.Fatalf("repair result = %+v history=%+v", result, history.Task)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "keep" {
		t.Fatalf("dirty worktree changed: %q %v", got, err)
	}
}

func TestReconcileRecordedPaneWithoutLaunchEvidenceRefusesToRelaunch(t *testing.T) {
	home := reconcileFixture(t)
	client := &healthyReconcileHerdr{}
	if _, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
	}); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(client, nil)
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Outcome != reconcileOutcomeRepair || history.Task.RepairCode != repairCodeProvisioningLaunchAmbiguous || client.runs != 0 {
		t.Fatalf("result=%+v history=%+v runs=%d, want durable ambiguity and no relaunch", report, history.Task, client.runs)
	}
}

func TestReconcileSubmittedLaunchConfirmsExistingPaneWithoutRelaunch(t *testing.T) {
	home := reconcileFixture(t)
	client := &healthyReconcileHerdr{}
	if _, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", LaunchSubmittedAt: "2026-08-15T00:00:00Z", Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
	}); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(client, nil)
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning || history.ActiveAttempt.LaunchConfirmedAt == "" || client.runs != 0 {
		t.Fatalf("history=%+v runs=%d, want confirmed same Attempt without PaneRun", history, client.runs)
	}
}

func TestReconcileRunningMissingPaneRecordsRepairWithoutReplacement(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z", Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", attempt.ID); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&missingReconcileHerdr{}, nil)
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != repairCodeRunningPaneMissing || history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning {
		t.Fatalf("history=%+v, want running Attempt and repair marker", history)
	}
}

func TestReconcileRunningWorktreeLeaseMismatchWinsOverHealthyPane(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/1", LeaseID: "old-lease",
		Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", attempt.ID); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.worktree.observeLease = func(string, string) (worktree.LeaseObservation, error) {
		return worktree.LeaseObservation{State: worktree.LeaseMismatch, LeaseID: "new-lease"}, nil
	}
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != repairCodeWorktreeOwnershipMismatch || history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning {
		t.Fatalf("history = %+v, want lease repair and unchanged running Attempt", history)
	}
}

func TestDecideRunningKeepsDirtyWorktreeWhenLeaseAndPaneAreExact(t *testing.T) {
	decision := decideReconciliation(state.Task{ID: "task-1"}, state.Attempt{
		Lifecycle: state.AttemptRunning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1",
		Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
	}, reconciliationObservation{
		Treehouse: treehouseObservation{State: treehouseLeaseExact},
		Worktree:  worktreeObservation{State: worktreeDirty},
		Herdr:     herdrObservation{State: herdrOwnershipExact, Agent: "claude"},
	})
	if decision.Action != reconciliationActionKeep {
		t.Fatalf("decision = %+v, want keep despite worker edits", decision)
	}
}

func TestReconcileClearsRepairAfterTeardownAndReopenResolvesOldAttempt(t *testing.T) {
	home := reconcileFixture(t)
	oldAttempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude",
		Herdr:             state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
		LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", oldAttempt.ID); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&missingReconcileHerdr{}, nil)
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil || history.Task.RepairCode != repairCodeRunningPaneMissing {
		t.Fatalf("repair history = %+v, err=%v", history, err)
	}

	if err := state.SetAttemptTeardownDecision(home, "task-1", oldAttempt.ID, state.AttemptInterrupted, state.TeardownDispositionForced); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownResourceState(home, "task-1", oldAttempt.ID, state.AttemptRunning, "herdr", state.TeardownResourceReleasing); err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", oldAttempt.ID, state.AttemptRunning, state.AttemptInterrupted); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownResourceState(home, "task-1", oldAttempt.ID, state.AttemptInterrupted, "herdr", state.TeardownResourceReleased); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownCompletionState(home, "task-1", oldAttempt.ID, state.AttemptInterrupted, state.TeardownCompletionPending); err != nil {
		t.Fatal(err)
	}
	if err := completion.Append(home, completion.Record{ID: "task-1", Project: "demo", Outcome: "torn-down", Detail: "forced", AttemptID: oldAttempt.ID, AttemptLifecycle: string(state.AttemptInterrupted)}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownCompletionState(home, "task-1", oldAttempt.ID, state.AttemptInterrupted, state.TeardownCompletionAppended); err != nil {
		t.Fatal(err)
	}

	newAttempt, err := state.ReopenTask(home, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	newWorktree := filepath.Join(home, "reopened-worktree")
	if err := state.RecordAttemptWorktree(home, "task-1", newAttempt.ID, newWorktree, "lease-new"); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordAttemptHerdr(home, "task-1", newAttempt.ID, state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, "2026-08-15T00:00:02Z"); err != nil {
		t.Fatal(err)
	}
	if err := state.MarkLaunchSubmitted(home, "task-1", newAttempt.ID, "2026-08-15T00:00:03Z"); err != nil {
		t.Fatal(err)
	}
	if err := state.MarkLaunchConfirmed(home, "task-1", newAttempt.ID, "2026-08-15T00:00:04Z"); err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", newAttempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileRuntime(&healthyReconcileHerdr{}, nil).Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != "" || history.Task.RepairAttemptID != 0 {
		t.Fatalf("stale repair marker after resolved teardown and reopen: %+v", history.Task)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.ID != newAttempt.ID {
		t.Fatalf("active attempt = %+v, want reopened attempt %d", history.ActiveAttempt, newAttempt.ID)
	}
}

func TestReconcileDoesNotClearUnknownRepairAfterTerminalTeardown(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownDecision(home, "task-1", attempt.ID, state.AttemptInterrupted, state.TeardownDispositionForced); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownResourceState(home, "task-1", attempt.ID, state.AttemptProvisioning, "herdr", state.TeardownResourceReleasing); err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptProvisioning, state.AttemptInterrupted); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownResourceState(home, "task-1", attempt.ID, state.AttemptInterrupted, "herdr", state.TeardownResourceReleased); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownCompletionState(home, "task-1", attempt.ID, state.AttemptInterrupted, state.TeardownCompletionPending); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownCompletionState(home, "task-1", attempt.ID, state.AttemptInterrupted, state.TeardownCompletionAppended); err != nil {
		t.Fatal(err)
	}
	if err := completion.Append(home, completion.Record{ID: "task-1", Project: "demo", Outcome: "torn-down", Detail: "forced", AttemptID: attempt.ID, AttemptLifecycle: string(state.AttemptInterrupted)}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskRepair(home, "task-1", "operator-review-required", "unknown contradiction", attempt.ID, "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileRuntime(&healthyReconcileHerdr{}, nil).Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != "operator-review-required" {
		t.Fatalf("repair code = %q, want unknown marker retained", history.Task.RepairCode)
	}
}

func TestReconcileTerminalOwnedResourcesCleansEachResourceOnce(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1", Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", attempt.ID); err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptRunning, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	client := &healthyReconcileHerdr{}
	r := reconcileRuntime(client, nil)
	returns := 0
	r.deps.worktree.returnWithID = func(path, leaseID string, force bool) error {
		returns++
		if path != "/pool/1" || leaseID != "lease-1" || force {
			t.Fatalf("returnWithID(%q, %q, %t), want exact clean lease", path, leaseID, force)
		}
		return nil
	}
	first, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Results[0].Outcome != reconcileOutcomeHealthy || returns != 1 || client.closed != 1 || history.Attempts[0].TeardownHerdrState != state.TeardownResourceReleased || history.Attempts[0].TeardownWorktreeState != state.TeardownResourceReleased {
		t.Fatalf("first=%+v returns=%d closes=%d attempt=%+v", first, returns, client.closed, history.Attempts[0])
	}
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	if returns != 1 || client.closed != 1 {
		t.Fatalf("second reconcile repeated cleanup: returns=%d closes=%d", returns, client.closed)
	}
}

func TestReconcileTerminalDirtyWorktreeRefusesCleanup(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Worktree: "/pool/1", LeaseID: "lease-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", attempt.ID); err == nil {
		t.Fatal("MarkAttemptRunning succeeded without launch evidence")
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptProvisioning, state.AttemptInterrupted); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	returns := 0
	r.deps.worktree.observeClean = func(string) (worktree.Cleanliness, error) { return worktree.Dirty, nil }
	r.deps.worktree.returnWithID = func(string, string, bool) error { returns++; return nil }
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != repairCodeWorktreeDirty || returns != 0 || history.Attempts[0].TeardownWorktreeState == state.TeardownResourceReleased {
		t.Fatalf("history=%+v returns=%d, want dirty repair without return", history, returns)
	}
}

func TestReconcileTerminalIncompleteHerdrIdentityNeedsRepair(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Herdr: state.Herdr{WorkspaceID: "ws-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptProvisioning, state.AttemptInterrupted); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != "teardown-resource-ambiguous" {
		t.Fatalf("repair code = %q, want incomplete Herdr ownership repair", history.Task.RepairCode)
	}
}

func TestReconcileReusedTerminalLeaseDoesNotReturnNewOwner(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Worktree: "/pool/1", LeaseID: "old-lease",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptProvisioning, state.AttemptInterrupted); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.worktree.observeLease = func(string, string) (worktree.LeaseObservation, error) {
		return worktree.LeaseObservation{State: worktree.LeaseMismatch, LeaseID: "new-lease"}, nil
	}
	returns := 0
	r.deps.worktree.returnWithID = func(string, string, bool) error { returns++; return nil }
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != "worktree-ownership-mismatch" || returns != 0 {
		t.Fatalf("history=%+v returns=%d, want mismatch repair without touching new lease", history, returns)
	}
}

func TestReconcilePromotionCrashCleansScoutBeforeShipLaunch(t *testing.T) {
	home := reconcileFixture(t)
	scout, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindScout, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/scout", LeaseID: "scout-lease", Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", scout.ID); err != nil {
		t.Fatal(err)
	}
	ship, err := state.PromoteTask(home, "task-1", scout.ID, state.AttemptRunning, state.Attempt{
		Lifecycle: state.AttemptProvisioning, Harness: "codex", Model: "gpt-5.6-codex", Effort: "high", ExecutionClass: "deep", PlannedAgainst: "base-2", RequestedProfile: "ship-profile", RoutingSource: "route",
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &healthyReconcileHerdr{}
	r := reconcileRuntime(client, nil)
	returns := 0
	r.deps.worktree.returnWithID = func(path, leaseID string, force bool) error {
		returns++
		if path != "/scout" || leaseID != "scout-lease" || force {
			t.Fatalf("scout return = %q %q %t", path, leaseID, force)
		}
		return nil
	}
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.ID != ship.ID || history.ActiveAttempt.Lifecycle != state.AttemptRunning || history.Attempts[0].TeardownHerdrState != state.TeardownResourceReleased || history.Attempts[0].TeardownWorktreeState != state.TeardownResourceReleased || returns != 1 || client.runs != 1 {
		t.Fatalf("history=%+v returns=%d runs=%d, want cleaned scout and running same ship Attempt %d", history, returns, client.runs, ship.ID)
	}
	if history.ActiveAttempt.Harness != "codex" || history.ActiveAttempt.Model != "gpt-5.6-codex" || history.ActiveAttempt.RequestedProfile != "ship-profile" {
		t.Fatalf("ship routing changed during recovery: %+v", history.ActiveAttempt)
	}
}

func TestReconcilePromotionDirtyScoutBlocksShipLaunch(t *testing.T) {
	home := reconcileFixture(t)
	scout, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindScout, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/scout", LeaseID: "scout-lease", LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", scout.ID); err != nil {
		t.Fatal(err)
	}
	ship, err := state.PromoteTask(home, "task-1", scout.ID, state.AttemptRunning, state.Attempt{Lifecycle: state.AttemptProvisioning, Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.worktree.observeClean = func(string) (worktree.Cleanliness, error) { return worktree.Dirty, nil }
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != repairCodeWorktreeDirty || history.ActiveAttempt == nil || history.ActiveAttempt.ID != ship.ID || history.ActiveAttempt.Lifecycle != state.AttemptProvisioning {
		t.Fatalf("history=%+v, want scout repair and unlaunched ship Attempt %d", history, ship.ID)
	}
}

func TestReconcilePendingTeardownCompletionResumesDurableDecision(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownDecision(home, "task-1", attempt.ID, state.AttemptInterrupted, state.TeardownDispositionForced); err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptProvisioning, state.AttemptInterrupted); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownCompletionState(home, "task-1", attempt.ID, state.AttemptInterrupted, state.TeardownCompletionPending); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Attempts[0].TeardownDisposition != state.TeardownDispositionForced || history.Attempts[0].TeardownCompletionState != state.TeardownCompletionAppended {
		t.Fatalf("teardown journal changed: %+v", history.Attempts[0])
	}
	record, found, err := completion.FindAttempt(home, attempt.ID)
	if err != nil || !found || record.Detail != "attempt never launched" {
		t.Fatalf("completion = %+v found=%t err=%v", record, found, err)
	}
	before, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	after, err := completion.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("second reconcile appended completion: before=%d after=%d", len(before), len(after))
	}
}

func TestReconcileCompletionStateWithoutRecordNeedsRepair(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownCompletionState(home, "task-1", attempt.ID, state.AttemptProvisioning, state.TeardownCompletionPending); err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptProvisioning, state.AttemptInterrupted); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownCompletionState(home, "task-1", attempt.ID, state.AttemptInterrupted, state.TeardownCompletionAppended); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != "completion-evidence-mismatch" {
		t.Fatalf("repair code = %q, want completion mismatch", history.Task.RepairCode)
	}
}

func TestReconcileMergeMismatchNeedsRepairWithoutRewritingHistory(t *testing.T) {
	home := reconcileFixture(t)
	if _, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskPR(home, "task-1", "https://github.com/example/repo/pull/7"); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskMerge(home, "task-1", "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.prMerged = func(context.Context, string) (bool, error) { return false, nil }
	first, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	observedAt := history.Task.RepairObservedAt
	if first.Results[0].Outcome != reconcileOutcomeRepair || history.Task.RepairCode != "merge-fact-mismatch" || !history.Task.MergeExecuted {
		t.Fatalf("first=%+v task=%+v, want repair without history rewrite", first, history.Task)
	}
	second, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if second.Results[0].Outcome != reconcileOutcomeRepair || history.Task.RepairObservedAt != observedAt {
		t.Fatalf("repair marker churned: first=%+v second=%+v task=%+v", first, second, history.Task)
	}
}

func TestReconcileMergeObservationFailureLeavesRepairMarkerUnchanged(t *testing.T) {
	home := reconcileFixture(t)
	if _, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskRepair(home, "task-1", "old-code", "old reason", 1, "old-time"); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskPR(home, "task-1", "https://github.com/example/repo/pull/7"); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskMerge(home, "task-1", "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.prMerged = func(context.Context, string) (bool, error) { return false, errors.New("GitHub unavailable") }
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err == nil {
		t.Fatal("Reconcile succeeded through GitHub observation failure")
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != "old-code" || history.Task.RepairReason != "old reason" || history.Task.RepairObservedAt != "old-time" {
		t.Fatalf("observation failure changed repair marker: %+v", history.Task)
	}
}

func TestReconcileLocalMergeMismatchNeedsRepairWithoutRewritingHistory(t *testing.T) {
	home := reconcileFixture(t)
	if _, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskMerge(home, "task-1", "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.branchMerged = func(string, string) (bool, error) { return false, nil }
	result, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Results[0].Outcome != reconcileOutcomeRepair || history.Task.RepairCode != "merge-fact-mismatch" || !history.Task.MergeExecuted {
		t.Fatalf("result=%+v task=%+v, want local merge contradiction repair", result, history.Task)
	}
}

func TestReconcileLocalMergeObservationFailureDoesNotCreateRepair(t *testing.T) {
	home := reconcileFixture(t)
	if _, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskMerge(home, "task-1", "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.branchMerged = func(string, string) (bool, error) { return false, errors.New("git unavailable") }
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err == nil {
		t.Fatal("Reconcile succeeded through local Git observation failure")
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != "" {
		t.Fatalf("local Git observation failure created repair marker: %+v", history.Task)
	}
}

func TestReconcileResumesPartialTeardownWithoutChangingDisposition(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1", Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", attempt.ID); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownDecision(home, "task-1", attempt.ID, state.AttemptCompleted, state.TeardownDispositionCompleted); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownResourceState(home, "task-1", attempt.ID, state.AttemptRunning, "herdr", state.TeardownResourceReleasing); err != nil {
		t.Fatal(err)
	}
	client := &healthyReconcileHerdr{}
	r := reconcileRuntime(client, nil)
	returns := 0
	r.deps.worktree.returnWithID = func(string, string, bool) error { returns++; return nil }
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != state.TaskTerminal || history.ActiveAttempt != nil || history.Attempts[0].TeardownDisposition != state.TeardownDispositionCompleted || history.Attempts[0].TeardownHerdrState != state.TeardownResourceReleased || history.Attempts[0].TeardownWorktreeState != state.TeardownResourceReleased || history.Attempts[0].TeardownCompletionState != state.TeardownCompletionAppended || returns != 1 || client.closed != 1 {
		t.Fatalf("history=%+v returns=%d closes=%d, want resumed terminal teardown", history, returns, client.closed)
	}
}

func TestReconcileFleetReportsUnattributedHandTabWithoutClaimingOwnership(t *testing.T) {
	home := reconcileFixture(t)
	client := &inventoryReconcileHerdr{}
	r := reconcileRuntime(client, nil)
	report, err := r.Reconcile(ReconcileRequest{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Anomalies) != 1 {
		t.Fatalf("anomalies = %+v, want one unattributed Herdr tab", report.Anomalies)
	}
	anomaly := report.Anomalies[0]
	if anomaly.Kind != "unattributed-herdr-tab" || anomaly.WorkspaceID != "ws-orphan" || anomaly.TabID != "tab-orphan" || anomaly.OwnerAttemptID != 0 {
		t.Fatalf("anomaly = %+v, want unattributed identity without owner", anomaly)
	}
}

func reconcileFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "brief.md"), []byte("brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://example.com/demo.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	return home
}

func reconcileRuntime(client herdrClient, get func(string, string) (worktree.Lease, error)) *Runtime {
	if get == nil {
		get = func(path, holder string) (worktree.Lease, error) {
			return worktree.Lease{Path: filepath.Join(path, "leased"), ID: "lease-1"}, nil
		}
	}
	return &Runtime{deps: dependencies{
		now:   func() time.Time { return time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC) },
		herdr: func() herdrClient { return client },
		worktree: worktreeDependencies{
			get: get,
			observeLease: func(string, string) (worktree.LeaseObservation, error) {
				return worktree.LeaseObservation{State: worktree.LeaseExact}, nil
			},
			observeClean: func(path string) (worktree.Cleanliness, error) {
				return worktree.Clean, nil
			},
			checkCollision: func(string, worktree.Lease, string) (string, error) { return "", nil },
			returnWorktree: func(string, bool) error { return nil },
			returnWithID:   func(string, string, bool) error { return nil },
		},
		buildHarness:     func(string, harness.Options) (string, error) { return "launch", nil },
		confirmLaunch:    func(herdrClient, string, string) error { return nil },
		appendCompletion: completion.Append,
		phase:            func(lifecyclePhase) error { return nil },
	}}
}

type healthyReconcileHerdr struct {
	runs   int
	closed int
}

type missingReconcileHerdr struct{ healthyReconcileHerdr }

type inventoryReconcileHerdr struct{ healthyReconcileHerdr }

func (*inventoryReconcileHerdr) WorkspaceList() ([]herdr.Workspace, error) {
	return []herdr.Workspace{{WorkspaceID: "ws-orphan", Label: "hand:demo"}}, nil
}

func (*inventoryReconcileHerdr) TabList(string) ([]herdr.Tab, error) {
	return []herdr.Tab{{TabID: "tab-orphan", WorkspaceID: "ws-orphan", Label: "orphan-task"}}, nil
}

func (*missingReconcileHerdr) FindWorkspaceByLabel(string) (herdr.Workspace, bool, error) {
	return herdr.Workspace{}, false, nil
}

func (f *healthyReconcileHerdr) FindWorkspaceByLabel(string) (herdr.Workspace, bool, error) {
	return herdr.Workspace{WorkspaceID: "ws-1", Label: "hand:demo"}, true, nil
}
func (f *healthyReconcileHerdr) WorkspaceList() ([]herdr.Workspace, error) { return nil, nil }
func (f *healthyReconcileHerdr) WorkspaceCreate(string, string) (herdr.Workspace, herdr.Tab, herdr.Pane, error) {
	return herdr.Workspace{}, herdr.Tab{}, herdr.Pane{}, errors.New("unused")
}
func (f *healthyReconcileHerdr) WorkspaceClose(string) error { f.closed++; return nil }
func (f *healthyReconcileHerdr) TabList(string) ([]herdr.Tab, error) {
	return []herdr.Tab{{TabID: "tab-1", WorkspaceID: "ws-1", Label: "task-1"}}, nil
}
func (f *healthyReconcileHerdr) TabCreate(string, string, string) (herdr.Tab, herdr.Pane, error) {
	return herdr.Tab{TabID: "tab-1", WorkspaceID: "ws-1", Label: "task-1"}, herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1"}, nil
}
func (f *healthyReconcileHerdr) TabRename(string, string) error { return nil }
func (f *healthyReconcileHerdr) TabClose(string) error          { return nil }
func (f *healthyReconcileHerdr) PaneGet(string) (herdr.Pane, error) {
	return herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1", Agent: "claude"}, nil
}
func (f *healthyReconcileHerdr) PaneRun(string, string) error         { f.runs++; return nil }
func (f *healthyReconcileHerdr) PaneSendKeys(string, ...string) error { return nil }
func (f *healthyReconcileHerdr) PaneRead(string, int) (string, error) { return "ready", nil }
