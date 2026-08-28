package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/faketool"
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

func TestObserveHerdrOwnershipRejectsEachIncompleteIdentity(t *testing.T) {
	client := &reconcileHerdrClient{}
	for name, ownership := range map[string]state.Herdr{
		"missing workspace": {TabID: "tab-1", PaneID: "pane-1"},
		"missing tab":       {WorkspaceID: "ws-1", PaneID: "pane-1"},
		"missing pane":      {WorkspaceID: "ws-1", TabID: "tab-1"},
	} {
		t.Run(name, func(t *testing.T) {
			observation, err := observeHerdrOwnership(client, ownership, "task-1", "demo")
			if err != nil {
				t.Fatal(err)
			}
			if observation.State != herdrOwnershipIncomplete {
				t.Fatalf("observation = %+v, want incomplete ownership", observation)
			}
		})
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
func (f *reconcileHerdrClient) WorkspaceCreate(string, map[string]string, string) (herdr.Workspace, herdr.Tab, herdr.Pane, error) {
	return herdr.Workspace{}, herdr.Tab{}, herdr.Pane{}, errors.New("unused")
}
func (f *reconcileHerdrClient) WorkspaceClose(string) error         { return errors.New("unused") }
func (f *reconcileHerdrClient) TabList(string) ([]herdr.Tab, error) { return f.tabs, nil }
func (f *reconcileHerdrClient) TabCreate(string, string, map[string]string, string) (herdr.Tab, herdr.Pane, error) {
	return herdr.Tab{}, herdr.Pane{}, errors.New("unused")
}
func (f *reconcileHerdrClient) TabRename(string, string) error     { return errors.New("unused") }
func (f *reconcileHerdrClient) TabClose(string) error              { return errors.New("unused") }
func (f *reconcileHerdrClient) PaneGet(string) (herdr.Pane, error) { return f.pane, nil }
func (f *reconcileHerdrClient) PaneRun(string, string) error       { return errors.New("unused") }
func (f *reconcileHerdrClient) PaneProcessInfo(string) (herdr.ProcessInfo, error) {
	return herdr.ProcessInfo{ShellPID: 1, ForegroundProcesses: []herdr.Process{{PID: 1, Name: "bash"}}}, nil
}
func (f *reconcileHerdrClient) PaneRunSpec(string, launchSpec) error { return errors.New("unused") }
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
	r := reconcileRuntime(client, func(path, _ string) (worktree.Lease, error) {
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
	r.deps.buildHarness = func(name string, options harness.Options) (launchSpec, error) {
		gotHarness, got = name, options
		return launchSpec{Executable: "launch"}, nil
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

func TestReconcileConfirmLaunchCarriesTaskKind(t *testing.T) {
	home := reconcileFixture(t)
	if _, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindScout, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", LaunchSubmittedAt: "2026-08-15T00:00:00Z", Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
	}); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	var got harness.Options
	r.deps.buildHarness = func(_ string, options harness.Options) (launchSpec, error) {
		got = options
		return launchSpec{Executable: "launch"}, nil
	}
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	if got.Kind != state.KindScout {
		t.Fatalf("Options.Kind = %q, want %q", got.Kind, state.KindScout)
	}
}

// The confirm-launch arm only reconstructs already-persisted launch evidence; for a grok
// attempt that must not repeat the provisioning-time brief append, since an observation must
// never mutate what it observes (atqamz/hand#418, INV-REC-3).
func TestReconcileConfirmLaunchDoesNotModifyBriefFile(t *testing.T) {
	home := reconcileFixture(t)
	briefPath := filepath.Join(home, "data", "task-1", "brief.md")
	before, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: harness.Grok, LaunchSubmittedAt: "2026-08-15T00:00:00Z", Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
	}); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&grokReconcileHerdr{}, nil)
	// The real builder, not the fixture's static fake: this is what would catch AppendPromptToBrief
	// being called from inside Build again instead of only from the provisioning path.
	r.deps.buildHarness = harness.Build
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning || history.ActiveAttempt.LaunchConfirmedAt == "" {
		t.Fatalf("history=%+v, want the grok attempt confirmed running", history)
	}
	after, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("confirm-launch modified the brief file:\nbefore: %q\nafter:  %q", before, after)
	}
}

func TestReconcileRunningMissingPaneConvergesWithoutReplacement(t *testing.T) {
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
	if history.ActiveAttempt != nil || len(history.Attempts) != 1 {
		t.Fatalf("history=%+v, want the same Attempt converged without a replacement", history)
	}
	if history.Task.Lifecycle != state.TaskTerminal || history.Attempts[0].Lifecycle != state.AttemptInterrupted {
		t.Fatalf("history=%+v, want a terminal task and interrupted Attempt", history)
	}
	if history.Task.RepairCode != "" {
		t.Fatalf("task=%+v, want no repair marker for an Attempt that converged", history.Task)
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
	r.deps.worktree.observeLease = func(string, string, string) worktree.LeaseObservation {
		return worktree.LeaseObservation{State: worktree.LeaseMismatch, LeaseID: "new-lease"}
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
	if err := state.SetTaskRepair(home, "task-1", repairCodeRunningPaneIdentityMismatch, "pane belongs to another Attempt", oldAttempt.ID, "2026-08-15T00:00:02Z"); err != nil {
		t.Fatal(err)
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
	if err := state.RecordAttemptWorktree(home, "task-1", newAttempt.ID, newWorktree, "", "lease-new"); err != nil {
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
	history, err := state.ReadHistory(home, "task-1")
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

// A retry after a terminalization whose lifecycle write committed but whose hold clear did not still has
// to converge: the historical attempt already sits at its recorded terminal lifecycle, so reconcile must
// clear a surviving usage-limit hold on that no-progress path too, not only on the transition that sets it.
func TestReconcileClearsUsageLimitHoldOnAlreadySettledHistoricalAttempt(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownDecision(home, "task-1", attempt.ID, state.AttemptInterrupted, state.TeardownDispositionWorkerNeverStarted); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownCompletionState(home, "task-1", attempt.ID, state.AttemptProvisioning, state.TeardownCompletionPending); err != nil {
		t.Fatal(err)
	}
	if err := completion.Append(home, completion.Record{ID: "task-1", Project: "demo", Outcome: "torn-down", Detail: "worker never started", AttemptID: attempt.ID, AttemptLifecycle: string(state.AttemptInterrupted)}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownCompletionState(home, "task-1", attempt.ID, state.AttemptProvisioning, state.TeardownCompletionAppended); err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptProvisioning, state.AttemptInterrupted); err != nil {
		t.Fatal(err)
	}
	if err := state.SetHold(home, state.Hold{ID: "task-1", Kind: state.HoldKindLimit, Reason: "usage limit", SetAt: "2026-08-15T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	if _, err := reconcileRuntime(&healthyReconcileHerdr{}, nil).Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	if _, hasHold, err := state.ReadHold(home, "task-1"); err != nil {
		t.Fatal(err)
	} else if hasHold {
		t.Fatal("usage-limit hold survived reconcile on an already-settled historical attempt, want reopen to stay reachable")
	}
}

func convergenceTask(t *testing.T, home string, task state.Task) state.Attempt {
	t.Helper()
	attempt, err := state.CreateTaskWithAttempt(home, task, state.Attempt{
		TaskID: task.ID, Lifecycle: state.AttemptProvisioning, Harness: "claude",
		LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
		Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, task.ID, attempt.ID); err != nil {
		t.Fatal(err)
	}
	return attempt
}

func TestDecideTerminalConvergenceMatrix(t *testing.T) {
	running := state.Attempt{Lifecycle: state.AttemptRunning, Harness: "claude", Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}}
	withPR := state.Task{ID: "task-1", PR: "https://github.com/example/repo/pull/7"}
	tests := []struct {
		name            string
		task            state.Task
		attempt         state.Attempt
		observation     reconciliationObservation
		wantConverge    bool
		wantLifecycle   state.AttemptLifecycle
		wantDisposition string
	}{
		{
			name:            "pane gone and work landed",
			attempt:         running,
			observation:     reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipAbsent}, Landing: landingLanded},
			wantConverge:    true,
			wantLifecycle:   state.AttemptCompleted,
			wantDisposition: state.TeardownDispositionCompleted,
		},
		{
			name:            "pane gone and nothing landed",
			attempt:         running,
			observation:     reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipAbsent}, Landing: landingUnlanded},
			wantConverge:    true,
			wantLifecycle:   state.AttemptInterrupted,
			wantDisposition: state.TeardownDispositionWorkerExitedUnlanded,
		},
		{
			name:        "pane gone and landing unknown",
			attempt:     running,
			observation: reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipAbsent}, Landing: landingUnknown},
		},
		{
			name:        "pane gone and landing unobserved",
			attempt:     running,
			observation: reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipAbsent}},
		},
		{
			name:        "pane still present, no PR recorded",
			attempt:     running,
			observation: reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipExact, Agent: "claude"}, Landing: landingLanded},
		},
		{
			name:        "pane identity never persisted",
			attempt:     state.Attempt{Lifecycle: state.AttemptRunning, Harness: "claude"},
			observation: reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipAbsent}, Landing: landingLanded},
		},
		{
			name:        "teardown already decided the terminal value",
			attempt:     state.Attempt{Lifecycle: state.AttemptRunning, Harness: "claude", Herdr: running.Herdr, TeardownTerminalAttempt: state.AttemptInterrupted},
			observation: reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipAbsent}, Landing: landingLanded},
		},
		{
			// atqamz/hand#422 ask 2: a recorded PR GitHub reports merged completes the Attempt whether
			// or not its pane is still alive, because a live pane is not evidence of anything.
			name:            "recorded PR merged with pane still present",
			task:            withPR,
			attempt:         running,
			observation:     reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipExact, Agent: "claude"}, Landing: landingLanded},
			wantConverge:    true,
			wantLifecycle:   state.AttemptCompleted,
			wantDisposition: state.TeardownDispositionCompleted,
		},
		{
			// The other half of locked decision 3: opening the gate does not make the pane evidence of
			// anything, so unlanded evidence still may not interrupt a worker whose pane is alive.
			name:        "recorded PR unmerged with pane still present stays unchanged",
			task:        withPR,
			attempt:     running,
			observation: reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipExact, Agent: "claude"}, Landing: landingUnlanded},
		},
		{
			name:        "recorded PR landing unknown with pane still present stays unchanged",
			task:        withPR,
			attempt:     running,
			observation: reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipExact, Agent: "claude"}, Landing: landingUnknown},
		},
		{
			name:            "recorded PR merged with pane gone",
			task:            withPR,
			attempt:         running,
			observation:     reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipAbsent}, Landing: landingLanded},
			wantConverge:    true,
			wantLifecycle:   state.AttemptCompleted,
			wantDisposition: state.TeardownDispositionCompleted,
		},
		{
			name:        "recorded PR merged but teardown already decided",
			task:        withPR,
			attempt:     state.Attempt{Lifecycle: state.AttemptRunning, Harness: "claude", Herdr: running.Herdr, TeardownTerminalAttempt: state.AttemptInterrupted},
			observation: reconciliationObservation{Herdr: herdrObservation{State: herdrOwnershipExact, Agent: "claude"}, Landing: landingLanded},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lifecycle, disposition, converge := decideTerminalConvergence(tc.task, tc.attempt, tc.observation)
			if converge != tc.wantConverge || lifecycle != tc.wantLifecycle || disposition != tc.wantDisposition {
				t.Fatalf("converge=%v lifecycle=%q disposition=%q, want %v/%q/%q", converge, lifecycle, disposition, tc.wantConverge, tc.wantLifecycle, tc.wantDisposition)
			}
		})
	}
}

func TestReconcileConvergesExitedWorkerWhoseMergedPRLanded(t *testing.T) {
	home := reconcileFixture(t)
	attempt := convergenceTask(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"})
	if err := state.SetTaskPR(home, "task-1", "https://github.com/example/repo/pull/7"); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskMerge(home, "task-1", "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	writeReport(t, home, "task-1", "done: shipped\n")
	r := reconcileRuntime(&missingReconcileHerdr{}, nil)
	r.deps.prMerged = observedMergedPR(true)
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Landing != string(landingLanded) {
		t.Fatalf("landing = %q, want landed", report.Results[0].Landing)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != state.TaskTerminal || history.ActiveAttempt != nil {
		t.Fatalf("task = %+v, want a terminal task with no active Attempt", history.Task)
	}
	converged := history.Attempts[0]
	if converged.Lifecycle != state.AttemptCompleted || converged.TeardownDisposition != state.TeardownDispositionCompleted {
		t.Fatalf("attempt = %+v, want completed by convergence", converged)
	}
	if converged.TeardownCompletionState != state.TeardownCompletionAppended {
		t.Fatalf("attempt = %+v, want appended completion evidence", converged)
	}
	record, found, err := completion.FindAttempt(home, attempt.ID)
	if err != nil || !found || record.Outcome != "merged" {
		t.Fatalf("record=%+v found=%v err=%v, want a merged completion record", record, found, err)
	}
	lines, err := state.ReadReportLines(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if last, ok := state.LastReportedState(lines); !ok || last.State != state.ReportDone {
		t.Fatalf("last report = %+v ok=%v, want the done report still readable beside a terminal Attempt", last, ok)
	}
}

func TestReconcileDiscoversMergedPRForShipWithoutRecordedPR(t *testing.T) {
	home := reconcileFixture(t)
	clonePath := filepath.Join(home, "projects", "demo")
	runRuntimeGit(t, clonePath, "init", "-q")
	runRuntimeGit(t, clonePath, "remote", "add", "origin", "https://github.com/example/demo.git")
	prURL := "https://github.com/example/demo/pull/7"
	faketool.GH{PRs: []faketool.GHPR{{Number: 7, URL: prURL, Branch: "topic", State: "MERGED", Repo: "example/demo"}}}.Install(t, faketool.Bin(t))
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Branch: "topic",
		LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
		Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", attempt.ID); err != nil {
		t.Fatal(err)
	}
	report, err := reconcileRuntime(&missingReconcileHerdr{}, nil).Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Landing != string(landingLanded) || history.Task.PR != prURL || !history.Task.MergeAnnounced || history.Task.Lifecycle != state.TaskTerminal {
		t.Fatalf("report=%+v task=%+v, want discovered merged PR and terminal task", report, history.Task)
	}
	if record, found, err := completion.FindAttempt(home, history.Attempts[0].ID); err != nil || !found || record.Outcome != "merged" {
		t.Fatalf("completion=%+v found=%v err=%v, want merged completion", record, found, err)
	}
}

func TestReconcileKeepsKilledWorkerUnknownWithoutDiscoverablePR(t *testing.T) {
	home := reconcileFixture(t)
	convergenceTask(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"})
	report, err := reconcileRuntime(&missingReconcileHerdr{}, nil).Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Landing != string(landingUnknown) {
		t.Fatalf("landing = %q, want unknown", report.Results[0].Landing)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != state.TaskOpen || history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning {
		t.Fatalf("history = %+v, want an open task and running Attempt", history)
	}
	if history.Task.RepairCode != repairCodeRunningPaneMissing {
		t.Fatalf("repair code = %q, want running pane repair", history.Task.RepairCode)
	}
}

// atqamz/hand#422 ask 2: a merged PR is landing evidence regardless of the worker's pane. Before the
// fix this task stayed "healthy / keep" forever despite GitHub reporting it merged - the reproduction
// on task 408-orient-respects-holds cited in the issue.
func TestReconcileConvergesLiveWorkerWhoseMergedPRLanded(t *testing.T) {
	home := reconcileFixture(t)
	convergenceTask(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"})
	if err := state.SetTaskPR(home, "task-1", "https://github.com/example/repo/pull/7"); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskMerge(home, "task-1", "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	writeReport(t, home, "task-1", "done: shipped and merged\n")
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.prMerged = observedMergedPR(true)
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Landing != string(landingLanded) {
		t.Fatalf("landing = %q, want landed", report.Results[0].Landing)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != state.TaskTerminal || history.ActiveAttempt != nil {
		t.Fatalf("history = %+v, want a terminal task with no active Attempt despite the live pane", history)
	}
	converged := history.Attempts[0]
	if converged.Lifecycle != state.AttemptCompleted || converged.TeardownDisposition != state.TeardownDispositionCompleted {
		t.Fatalf("attempt = %+v, want completed by convergence", converged)
	}
	if _, found, err := completion.FindAttempt(home, converged.ID); err != nil || !found {
		t.Fatalf("found=%v err=%v, want a completion record despite the live pane", found, err)
	}
}

// The other half of the fix: an unmerged PR must not become grounds to interrupt a worker whose pane
// is still alive. Ask 2 opens the gate on observing landing, it does not turn "not merged yet" into
// evidence the pane's aliveness cannot already answer.
func TestReconcileKeepsLiveWorkerWithUnmergedRecordedPR(t *testing.T) {
	home := reconcileFixture(t)
	convergenceTask(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"})
	if err := state.SetTaskPR(home, "task-1", "https://github.com/example/repo/pull/7"); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.prMerged = observedMergedPR(false)
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Landing != string(landingUnlanded) {
		t.Fatalf("landing = %q, want unlanded", report.Results[0].Landing)
	}
	if report.Results[0].Action != string(reconciliationActionKeep) {
		t.Fatalf("action = %q, want keep: an open PR must not interrupt a worker whose pane is alive", report.Results[0].Action)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning || history.Task.Lifecycle != state.TaskOpen {
		t.Fatalf("history = %+v, want the running Attempt untouched while its PR is still open", history)
	}
}

// A recorded PR GitHub cannot be asked about still refuses to converge, live pane or not: an
// unreachable GitHub is unknown, never a positive answer either way.
func TestReconcileKeepsLiveWorkerWhenPRStateUnobservable(t *testing.T) {
	home := reconcileFixture(t)
	convergenceTask(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"})
	if err := state.SetTaskPR(home, "task-1", "https://github.com/example/repo/pull/7"); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.prMerged = unobservedPR("GitHub unavailable")
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Landing != string(landingUnknown) {
		t.Fatalf("landing = %q, want unknown", report.Results[0].Landing)
	}
	if report.Results[0].Action != string(reconciliationActionKeep) {
		t.Fatalf("action = %q, want keep while GitHub cannot be asked", report.Results[0].Action)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning {
		t.Fatalf("history = %+v, want no terminal value invented from an unknown landing", history)
	}
}

func TestReconcileIgnoresMalformedWorkerProse(t *testing.T) {
	home := reconcileFixture(t)
	convergenceTask(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"})
	writeReport(t, home, "task-1", "finished the thing\ndone shipped\n")
	report, err := reconcileRuntime(&healthyReconcileHerdr{}, nil).Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Action != string(reconciliationActionKeep) {
		t.Fatalf("action = %q, want keep through malformed prose", report.Results[0].Action)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning || history.Task.Lifecycle != state.TaskOpen {
		t.Fatalf("history = %+v, want no lifecycle transition from malformed prose", history)
	}
	lines, err := state.ReadReportLines(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || !lines[0].Malformed || !lines[1].Malformed {
		t.Fatalf("lines = %+v, want both prose lines recorded as malformed", lines)
	}
}

func TestReconcileRecordsUnknownLandingWhenGitHubIsUnreachable(t *testing.T) {
	home := reconcileFixture(t)
	convergenceTask(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"})
	if err := state.SetTaskPR(home, "task-1", "https://github.com/example/repo/pull/7"); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&missingReconcileHerdr{}, nil)
	r.deps.prMerged = unobservedPR("GitHub unavailable")
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Landing != string(landingUnknown) || report.Results[0].Outcome != reconcileOutcomeRepair {
		t.Fatalf("result = %+v, want an unknown landing recorded as needs-repair", report.Results[0])
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning {
		t.Fatalf("history = %+v, want no terminal value invented from an unknown landing", history)
	}
	if history.Task.RepairCode != repairCodeRunningPaneMissing || !strings.Contains(history.Task.RepairReason, "unknown") {
		t.Fatalf("task = %+v, want the unknown landing recorded as the repair condition", history.Task)
	}
	if _, found, err := completion.FindAttempt(home, history.ActiveAttempt.ID); err != nil || found {
		t.Fatalf("found=%v err=%v, want no completion record for an unknown landing", found, err)
	}
}

// A recorded PR GitHub will not resolve is not evidence the work did not land either: unlanded here
// interrupts the attempt and records that the worker exited without landing, which is a durable claim
// about the operator's work built on a query that answered nothing.
func TestReconcileRecordsUnknownLandingForAPRGitHubReportsAsAbsent(t *testing.T) {
	home := reconcileFixture(t)
	convergenceTask(t, home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"})
	if err := state.SetTaskPR(home, "task-1", "https://github.com/example/repo/pull/7"); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&missingReconcileHerdr{}, nil)
	r.deps.prMerged = absentPRObservation()
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Landing != string(landingUnknown) {
		t.Fatalf("result = %+v, want an unknown landing rather than an unlanded one", report.Results[0])
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning {
		t.Fatalf("history = %+v, want the attempt left alone rather than interrupted as unlanded", history)
	}
}

func TestReconcileConvergesLifecycleWhileWorktreeStillNeedsRepair(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/1",
		LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
		Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", attempt.ID); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskPR(home, "task-1", "https://github.com/example/repo/pull/7"); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&missingReconcileHerdr{}, nil)
	r.deps.prMerged = observedMergedPR(true)
	r.deps.worktree.observeLease = func(string, string, string) worktree.LeaseObservation {
		return worktree.LeaseObservation{State: worktree.LeaseUnprovable}
	}
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].RepairCode != repairCodeLegacyWorktreeUnprovable {
		t.Fatalf("repair code = %q, want the unprovable worktree lease", report.Results[0].RepairCode)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != state.TaskTerminal || history.ActiveAttempt != nil {
		t.Fatalf("task = %+v, want the lifecycle converged independently of the worktree", history.Task)
	}
	if history.Attempts[0].Lifecycle != state.AttemptCompleted {
		t.Fatalf("attempt = %+v, want completed despite the worktree repair", history.Attempts[0])
	}
	if history.Task.RepairCode != repairCodeLegacyWorktreeUnprovable {
		t.Fatalf("task = %+v, want the worktree repair still recorded", history.Task)
	}
}

func TestReconcileConvergesLifecycleDespiteRecordedRepairMarker(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/1",
		LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
		Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", attempt.ID); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskRepair(home, "task-1", repairCodeLegacyWorktreeUnprovable, "historical worktree has no exact lease identity", attempt.ID, "2026-08-15T00:00:02Z"); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskPR(home, "task-1", "https://github.com/example/repo/pull/7"); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&missingReconcileHerdr{}, nil)
	r.deps.prMerged = observedMergedPR(true)
	r.deps.worktree.observeLease = func(string, string, string) worktree.LeaseObservation {
		return worktree.LeaseObservation{State: worktree.LeaseUnprovable}
	}
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != state.TaskTerminal || history.Attempts[0].Lifecycle != state.AttemptCompleted {
		t.Fatalf("history = %+v, want convergence through a repair marker recorded for another resource", history)
	}
	if history.Task.RepairCode != repairCodeLegacyWorktreeUnprovable {
		t.Fatalf("task = %+v, want the unrelated repair marker left standing", history.Task)
	}
}

func writeReport(t *testing.T, home, id, body string) {
	t.Helper()
	if err := os.MkdirAll(state.Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.ReportPath(home, id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
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
	report, err := reconcileRuntime(&healthyReconcileHerdr{}, nil).Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].Outcome != reconcileOutcomeRepair || report.Results[0].RepairCode != "operator-review-required" || history.Task.RepairCode != "operator-review-required" {
		t.Fatalf("repair code = %q, want unknown marker retained", history.Task.RepairCode)
	}
}

func TestClearResolvedTerminalRepairRequiresRepairEvidenceResolution(t *testing.T) {
	for _, test := range []struct {
		name       string
		code       string
		attempt    state.Attempt
		completion bool
	}{
		{
			name: "herdr repair retains recorded identity",
			code: repairCodeRunningPaneMissing,
			attempt: state.Attempt{Lifecycle: state.AttemptProvisioning, Herdr: state.Herdr{
				WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1",
			}},
			completion: true,
		},
		{
			name:       "worktree repair retains recorded ownership",
			code:       repairCodeWorktreeDirty,
			attempt:    state.Attempt{Lifecycle: state.AttemptProvisioning, Worktree: "/pool/1", LeaseID: "lease-1"},
			completion: true,
		},
		{
			name:       "completion mismatch never clears here",
			code:       repairCodeCompletionEvidenceMismatch,
			attempt:    state.Attempt{Lifecycle: state.AttemptProvisioning},
			completion: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := reconcileFixture(t)
			test.attempt.TaskID = "task-1"
			attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, test.attempt)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.SetAttemptTeardownDecision(home, "task-1", attempt.ID, state.AttemptInterrupted, state.TeardownDispositionForced); err != nil {
				t.Fatal(err)
			}
			if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptProvisioning, state.AttemptInterrupted); err != nil {
				t.Fatal(err)
			}
			if test.completion {
				if err := state.SetAttemptTeardownCompletionState(home, "task-1", attempt.ID, state.AttemptInterrupted, state.TeardownCompletionPending); err != nil {
					t.Fatal(err)
				}
				if err := state.SetAttemptTeardownCompletionState(home, "task-1", attempt.ID, state.AttemptInterrupted, state.TeardownCompletionAppended); err != nil {
					t.Fatal(err)
				}
				if err := completion.Append(home, completion.Record{ID: "task-1", Project: "demo", Outcome: "torn-down", AttemptID: attempt.ID, AttemptLifecycle: string(state.AttemptInterrupted)}); err != nil {
					t.Fatal(err)
				}
			}
			if err := state.SetTaskRepair(home, "task-1", test.code, "unresolved evidence", attempt.ID, "2026-08-15T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
			history, err := state.ReadHistory(home, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			cleared, err := clearResolvedTerminalRepair(home, history)
			if err != nil {
				t.Fatal(err)
			}
			if cleared {
				t.Fatal("clearResolvedTerminalRepair cleared unresolved repair evidence")
			}
			history, err = state.ReadHistory(home, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if history.Task.RepairCode != test.code {
				t.Fatalf("repair code = %q, want %q retained", history.Task.RepairCode, test.code)
			}
		})
	}
}

func TestReconcileRunningOwnershipRepairClearsWithDirtyExactLease(t *testing.T) {
	task := state.Task{ID: "task-1", RepairCode: repairCodeWorktreeOwnershipMismatch, RepairAttemptID: 7}
	attempt := state.Attempt{ID: 7, Lifecycle: state.AttemptRunning, Worktree: "/pool/1", LeaseID: "lease-1"}
	observation := reconciliationObservation{
		Treehouse: treehouseObservation{State: treehouseLeaseExact},
		Worktree:  worktreeObservation{State: worktreeDirty},
	}
	if !shouldClearRepair(task, attempt, observation) {
		t.Fatal("shouldClearRepair() = false, want exact running lease to clear ownership-only repair despite dirt")
	}
}

func TestReconcileClearsRunningOwnershipRepairWithDirtyWorktree(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1",
		Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", attempt.ID); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskRepair(home, "task-1", repairCodeWorktreeOwnershipMismatch, "previous lease observation was mismatched", attempt.ID, "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.worktree.observeClean = func(string) (worktree.Cleanliness, error) { return worktree.Dirty, nil }
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Outcome != reconcileOutcomeHealthy || history.Task.RepairCode != "" || history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning {
		t.Fatalf("report=%+v history=%+v, want healthy running Attempt with ownership repair cleared", report, history)
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
	r.deps.worktree.returnWithID = func(_, path, leaseID string, force bool) error {
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
	r.deps.worktree.returnWithID = func(string, string, string, bool) error { returns++; return nil }
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
	r.deps.worktree.observeLease = func(string, string, string) worktree.LeaseObservation {
		return worktree.LeaseObservation{State: worktree.LeaseMismatch, LeaseID: "new-lease"}
	}
	returns := 0
	r.deps.worktree.returnWithID = func(string, string, string, bool) error { returns++; return nil }
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Attempts[0].TeardownWorktreeState != state.TeardownResourceAbandoned || returns != 0 {
		t.Fatalf("history=%+v returns=%d, want the disproven claim relinquished without touching the new lease", history, returns)
	}
	if history.Task.RepairCode != "" {
		t.Fatalf("repair code = %q, want a disproven claim to end rather than persist a diagnosis", history.Task.RepairCode)
	}
}

// The shape atqamz/hand#245 reports: a teardown decision is recorded, the worktree is still owned,
// and the pool the lease was acquired from can no longer be observed from that worktree.
func unobservableTeardownFixture(t *testing.T, latch string) (string, state.Attempt) {
	t.Helper()
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1",
		LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
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
	if latch != "" {
		if err := state.SetAttemptTeardownResourceState(home, "task-1", attempt.ID, state.AttemptRunning, "worktree", latch); err != nil {
			t.Fatal(err)
		}
	}
	return home, attempt
}

func unobservableReconcileRuntime(t *testing.T, returns *int) *Runtime {
	t.Helper()
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.worktree.observeLease = func(_, path, leaseID string) worktree.LeaseObservation {
		return worktree.LeaseObservation{State: worktree.LeaseUnknown, Probe: worktree.LeaseProbe{
			Command: "treehouse status --json", WorkingDir: path, Reason: "treehouse reported no pool entries",
		}}
	}
	r.deps.worktree.returnWithID = func(string, string, string, bool) error { *returns++; return nil }
	r.deps.worktree.returnWorktree = func(string, string, bool) error { *returns++; return nil }
	return r
}

func TestReconcileUnobservableWorktreeNeedsRepairInsteadOfMismatch(t *testing.T) {
	home, _ := unobservableTeardownFixture(t, state.TeardownResourceAmbiguous)
	returns := 0
	r := unobservableReconcileRuntime(t, &returns)
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].Outcome != reconcileOutcomeRepair {
		t.Fatalf("report = %+v, want one needs-repair result", report)
	}
	if report.Results[0].RepairCode != repairCodeWorktreeUnobservable {
		t.Fatalf("repair code = %q, want %q", report.Results[0].RepairCode, repairCodeWorktreeUnobservable)
	}
	reason := report.Results[0].RepairReason
	for _, want := range []string{"could not be observed", "treehouse status --json", "/pool/1", "lease-1", "neither proven nor disproven", "destructive cleanup refused because ownership could not be proven, not because a lease mismatched"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("repair reason = %q, want it to contain %q", reason, want)
		}
	}
	if strings.Contains(reason, "held by a different Treehouse lease") || strings.Contains(reason, "no exact lease identity") {
		t.Fatalf("repair reason = %q, want no claim about a different or missing identity", reason)
	}
	if returns != 0 {
		t.Fatalf("worktree return count = %d, want no destructive cleanup", returns)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.TeardownWorktreeState != state.TeardownResourceAmbiguous {
		t.Fatalf("worktree state = %+v, want the latch unchanged by an observation that failed", history.ActiveAttempt)
	}
}

// The supported convergence path for a pool that can never be observed again: an operator attests
// that Hand relinquishes the recorded lease, and the task finishes its teardown from there.
func TestReconcileAbandonWorktreeConvergesAnUnobservableLease(t *testing.T) {
	home, attempt := unobservableTeardownFixture(t, state.TeardownResourceAmbiguous)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.ID != attempt.ID || history.ActiveAttempt.Lifecycle != state.AttemptRunning || history.ActiveAttempt.TeardownTerminalAttempt != state.AttemptCompleted {
		t.Fatalf("eligible attempt = %+v, want an active attempt with a recorded teardown decision", history.ActiveAttempt)
	}
	returns := 0
	r := unobservableReconcileRuntime(t, &returns)
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1", AbandonWorktree: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].Outcome != reconcileOutcomeHealthy {
		t.Fatalf("report = %+v, want one converged result", report)
	}
	detail := report.Results[0].Detail
	for _, want := range []string{"operator attestation", "/pool/1", "lease-1", "treehouse status --json"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail = %q, want it to contain %q", detail, want)
		}
	}
	if returns != 0 {
		t.Fatalf("worktree return count = %d, want abandonment to run no destructive command", returns)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != state.TaskTerminal || history.ActiveAttempt != nil {
		t.Fatalf("task after abandonment = %+v, want a converged terminal task", history.Task)
	}
	if history.Attempts[0].TeardownWorktreeState != state.TeardownResourceAbandoned {
		t.Fatalf("worktree state = %q, want abandoned", history.Attempts[0].TeardownWorktreeState)
	}
	if history.Task.RepairCode != "" {
		t.Fatalf("repair code = %q, want the unobservable repair cleared", history.Task.RepairCode)
	}
	if _, found, err := completion.FindAttempt(home, attempt.ID); err != nil || !found {
		t.Fatalf("completion for attempt %d found=%t err=%v, want the teardown finished", attempt.ID, found, err)
	}
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("reconcile after convergence = %v, want a healthy no-op", err)
	}
}

// Abandonment is only for what cannot be observed. A lease another owner provably holds and a lease
// proven to be ours both keep their ordinary handling even when the operator passes the attestation.
func TestReconcileAbandonWorktreeRefusesProvableOwnership(t *testing.T) {
	for _, test := range []struct {
		name        string
		observation worktree.LeaseObservation
		wantState   string
		wantReturns int
		wantRepair  string
	}{
		{
			name:        "another owner",
			observation: worktree.LeaseObservation{State: worktree.LeaseMismatch, LeaseID: "lease-2"},
			wantState:   state.TeardownResourceAbandoned,
		},
		{
			name:        "proven ours",
			observation: worktree.LeaseObservation{State: worktree.LeaseExact, LeaseID: "lease-1"},
			wantState:   state.TeardownResourceReleased,
			wantReturns: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, _ := unobservableTeardownFixture(t, state.TeardownResourceAmbiguous)
			returns := 0
			r := unobservableReconcileRuntime(t, &returns)
			r.deps.worktree.observeLease = func(string, string, string) worktree.LeaseObservation { return test.observation }
			if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1", AbandonWorktree: true}); err != nil {
				t.Fatal(err)
			}
			history, err := state.ReadHistory(home, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if history.Attempts[0].TeardownWorktreeState != test.wantState {
				t.Fatalf("worktree state = %q, want %q", history.Attempts[0].TeardownWorktreeState, test.wantState)
			}
			if returns != test.wantReturns {
				t.Fatalf("worktree return count = %d, want %d", returns, test.wantReturns)
			}
			if history.Task.RepairCode != test.wantRepair {
				t.Fatalf("repair code = %q, want %q", history.Task.RepairCode, test.wantRepair)
			}
		})
	}
}

// Unknown and unprovable are the two states no observation settles; every other observation proves or
// disproves the lease, so the attestation has nothing to add and refuses.
func TestAbandonHistoricalWorktreeRefusesOwnershipAnObservationSettles(t *testing.T) {
	home, attempt := unobservableTeardownFixture(t, "")
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	task := state.Task{ID: "task-1"}
	for _, observed := range []worktree.LeaseObservationState{worktree.LeaseExact, worktree.LeaseMismatch, worktree.LeaseAbsent} {
		_, _, err := r.abandonHistoricalWorktree(home, task, attempt, worktree.LeaseObservation{State: observed})
		if err == nil {
			t.Fatalf("abandonHistoricalWorktree() accepted a %s observation", observed)
		}
		var classified *Error
		if !errors.As(err, &classified) || classified.Kind != ErrorPrecondition {
			t.Fatalf("abandonHistoricalWorktree() = %v, want a precondition refusal", err)
		}
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.TeardownWorktreeState != "" {
		t.Fatalf("worktree state = %+v, want nothing recorded by a refused abandonment", history.ActiveAttempt)
	}
}

// Abandonment relinquishes a lease nothing is still using; a worker's live worktree is never that,
// so the attestation cannot reach an attempt that has not decided its teardown.
func TestReconcileAbandonWorktreeNeverTouchesARunningAttempt(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1",
		Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", attempt.ID); err != nil {
		t.Fatal(err)
	}
	returns := 0
	r := unobservableReconcileRuntime(t, &returns)
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1", AbandonWorktree: true}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning {
		t.Fatalf("attempt after abandonment attempt = %+v, want the running worker untouched", history.ActiveAttempt)
	}
	if history.ActiveAttempt.TeardownTerminalAttempt != "" || history.ActiveAttempt.TeardownWorktreeState != "" || returns != 0 {
		t.Fatalf("worktree state = %q returns = %d, want no teardown of a running attempt", history.ActiveAttempt.TeardownWorktreeState, returns)
	}
	if history.Task.RepairCode != repairCodeWorktreeUnobservable {
		t.Fatalf("repair code = %q, want %q", history.Task.RepairCode, repairCodeWorktreeUnobservable)
	}
}

func TestReconcileAbandonWorktreeSettlesATerminalAttemptWithoutATeardownDecision(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1",
		LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
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
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt != nil || history.Attempts[0].Lifecycle != state.AttemptCompleted {
		t.Fatalf("eligible attempt = %+v, want a terminal attempt that is no longer active", history.Attempts[0])
	}
	if history.Attempts[0].TeardownTerminalAttempt != "" || history.Attempts[0].TeardownWorktreeState != "" {
		t.Fatalf("eligible attempt = %+v, want an unsettled worktree and no recorded teardown decision", history.Attempts[0])
	}
	returns := 0
	r := unobservableReconcileRuntime(t, &returns)
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1", AbandonWorktree: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].RepairCode != "" {
		t.Fatalf("report = %+v, want one result that needs no repair", report)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Attempts[0].TeardownWorktreeState != state.TeardownResourceAbandoned || returns != 0 {
		t.Fatalf("worktree state = %q returns = %d, want abandonment without a destructive command", history.Attempts[0].TeardownWorktreeState, returns)
	}
}

// atqamz/hand#263: a repair code recorded before an attestation must not survive the attestation it
// names as the fix, even when the attempt it repairs reached its terminal lifecycle without ever
// recording a teardown decision, so its completion state and record never get appended.
func TestReconcileAbandonPaneClearsAPreviouslyRecordedRepairCode(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude",
		Herdr:             state.Herdr{Session: "default", WorkspaceID: "ws-mismatch", TabID: "tab-mismatch", PaneID: "pane-mismatch"},
		LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
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
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt != nil || history.Attempts[0].TeardownTerminalAttempt != "" || history.Attempts[0].TeardownHerdrState != "" {
		t.Fatalf("eligible attempt = %+v, want a terminal attempt with no teardown decision and no settled Herdr state", history.Attempts[0])
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	firstReport, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(firstReport.Results) != 1 || firstReport.Results[0].RepairCode != repairCodeTeardownResourceAmbiguous {
		t.Fatalf("first report = %+v, want %q recorded before any attestation", firstReport, repairCodeTeardownResourceAmbiguous)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != repairCodeTeardownResourceAmbiguous {
		t.Fatalf("durable repair code = %q, want %q persisted", history.Task.RepairCode, repairCodeTeardownResourceAmbiguous)
	}
	secondReport, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1", AbandonPane: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(secondReport.Results) != 1 || secondReport.Results[0].RepairCode != "" {
		t.Fatalf("second report = %+v, want the recorded repair code cleared once the pane is abandoned", secondReport)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != "" {
		t.Fatalf("durable repair code = %q, want cleared", history.Task.RepairCode)
	}
	if history.Attempts[0].TeardownHerdrState != state.TeardownResourceAbandoned {
		t.Fatalf("Herdr teardown state = %q, want abandoned", history.Attempts[0].TeardownHerdrState)
	}
}

// A latch is a refusal to guess, not a verdict: the observation that would have refused the release
// is the one that resumes it once the pool answers again.
func TestReconcileConvergesALatchedWorktreeOnceOwnershipIsProven(t *testing.T) {
	home, _ := unobservableTeardownFixture(t, state.TeardownResourceAmbiguous)
	returns := 0
	r := unobservableReconcileRuntime(t, &returns)
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	r.deps.worktree.observeLease = func(string, string, string) worktree.LeaseObservation {
		return worktree.LeaseObservation{State: worktree.LeaseExact, LeaseID: "lease-1"}
	}
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != state.TaskTerminal || history.ActiveAttempt != nil {
		t.Fatalf("task after proven ownership = %+v, want a converged terminal task", history.Task)
	}
	if history.Attempts[0].TeardownWorktreeState != state.TeardownResourceReleased || returns != 1 {
		t.Fatalf("worktree state = %q returns = %d, want one proven return", history.Attempts[0].TeardownWorktreeState, returns)
	}
	if history.Task.RepairCode != "" {
		t.Fatalf("repair code = %q, want the unobservable repair cleared", history.Task.RepairCode)
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
	r.deps.worktree.returnWithID = func(_, path, leaseID string, force bool) error {
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
	r.deps.prMerged = observedMergedPR(false)
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
	r.deps.prMerged = unobservedPR("GitHub unavailable")
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

func TestReconcileLocalMergeDoesNotInspectReusedTreehousePath(t *testing.T) {
	home := reconcileFixture(t)
	_, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/shared", LeaseID: "lease-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskMerge(home, "task-1", "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-2", Project: "demo", Kind: state.KindShip, Brief: "data/task-2/brief.md"}, state.Attempt{
		TaskID: "task-2", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/shared", LeaseID: "lease-b",
	}); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	observed := 0
	r.deps.worktree.observeLease = func(_, path, leaseID string) worktree.LeaseObservation {
		observed++
		if path != "/pool/shared" || leaseID != "lease-a" {
			t.Fatalf("observeLease(%q, %q), want Task 1 lease", path, leaseID)
		}
		return worktree.LeaseObservation{State: worktree.LeaseMismatch, LeaseID: "lease-b"}
	}
	branchChecks := 0
	r.deps.branchMerged = func(string, string) (bool, error) {
		branchChecks++
		return false, nil
	}
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err == nil {
		t.Fatal("Reconcile succeeded without proving the historical lease")
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed != 1 || branchChecks != 0 || history.Task.RepairCode == repairCodeMergeFactMismatch || report.Results[0].Outcome != reconcileOutcomeBlocked {
		t.Fatalf("report=%+v history=%+v observed=%d branchChecks=%d, want blocked ABA-safe observation", report, history.Task, observed, branchChecks)
	}
}

func TestReconcileLocalMergeHoldsProjectAndWorktreeLocks(t *testing.T) {
	home := reconcileFixture(t)
	if _, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/shared", LeaseID: "lease-a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskMerge(home, "task-1", "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.branchMerged = func(string, string) (bool, error) {
		for _, lockName := range []string{"project:demo", "worktree:/pool/shared"} {
			release, err := state.TryLock(home, lockName)
			if release != nil {
				release()
			}
			if !errors.Is(err, state.ErrLockBusy) {
				return false, fmt.Errorf("%s was not held during local Git observation", lockName)
			}
		}
		return true, nil
	}
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
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
	r.deps.worktree.returnWithID = func(string, string, string, bool) error { returns++; return nil }
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

func TestObserveHerdrOrphansRechecksOwnershipAfterTaskLock(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &inventoryRaceReconcileHerdr{}
	client.beforeWorkspaceList = func() error {
		release, err := state.Lock(home, "task:task-1")
		if err != nil {
			return err
		}
		defer release()
		return state.RecordAttemptHerdr(home, "task-1", attempt.ID, state.Herdr{Session: "default", WorkspaceID: "ws-race", TabID: "tab-race", PaneID: "pane-race"}, "2026-08-15T00:00:00Z")
	}
	r := reconcileRuntime(client, nil)
	anomalies, err := r.observeHerdrOrphans(home)
	if err != nil {
		t.Fatal(err)
	}
	if client.beforeWorkspaceListCalls != 1 || client.tabListCalls < 2 || len(anomalies) != 0 {
		t.Fatalf("calls=%d tabs=%d anomalies=%+v, want locked fresh ownership with no anomaly", client.beforeWorkspaceListCalls, client.tabListCalls, anomalies)
	}
}

func TestObserveHerdrOrphansReportsReleasedHistoricalResource(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Herdr: state.Herdr{Session: "default", WorkspaceID: "ws-released", TabID: "tab-released", PaneID: "pane-released"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptProvisioning, state.AttemptInterrupted); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownResourceState(home, "task-1", attempt.ID, state.AttemptInterrupted, "herdr", state.TeardownResourceReleasing); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownResourceState(home, "task-1", attempt.ID, state.AttemptInterrupted, "herdr", state.TeardownResourceReleased); err != nil {
		t.Fatal(err)
	}
	client := &releasedInventoryReconcileHerdr{}
	anomalies, err := reconcileRuntime(client, nil).observeHerdrOrphans(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(anomalies) != 1 || anomalies[0].Kind != "released-herdr-resource" || anomalies[0].OwnerAttemptID != attempt.ID {
		t.Fatalf("anomalies=%+v, want attributed released-resource anomaly for Attempt %d", anomalies, attempt.ID)
	}
}

func TestObserveHerdrOrphansReportsReleasedIdentityAlongsideReopenedAttempt(t *testing.T) {
	home := reconcileFixture(t)
	identity := state.Herdr{Session: "default", WorkspaceID: "ws-reused", TabID: "tab-reused", PaneID: "pane-reused"}
	oldAttempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Herdr: identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", oldAttempt.ID, state.AttemptProvisioning, state.AttemptInterrupted); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownResourceState(home, "task-1", oldAttempt.ID, state.AttemptInterrupted, "herdr", state.TeardownResourceReleasing); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAttemptTeardownResourceState(home, "task-1", oldAttempt.ID, state.AttemptInterrupted, "herdr", state.TeardownResourceReleased); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReopenTask(home, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Herdr: identity}); err != nil {
		t.Fatal(err)
	}
	client := &releasedReopenInventoryReconcileHerdr{}
	anomalies, err := reconcileRuntime(client, nil).observeHerdrOrphans(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(anomalies) != 1 || anomalies[0].Kind != "released-herdr-resource" || anomalies[0].OwnerAttemptID != oldAttempt.ID {
		t.Fatalf("anomalies=%+v, want released historical Attempt %d anomaly despite active reuse", anomalies, oldAttempt.ID)
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

func TestReconcileNormalizesPendingSendWithoutSteering(t *testing.T) {
	home := reconcileFixture(t)
	if err := state.CreateTask(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Lifecycle: state.TaskOpen}); err != nil {
		t.Fatal(err)
	}
	attempt, err := state.CreateAttempt(home, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptRunning,
		Herdr: state.Herdr{Session: "default", WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.BeginSend(home, "task-1", attempt.ID, attempt.Herdr, state.SendOriginOperator, "hello", "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileRuntime(&healthyReconcileHerdr{}, nil).Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	sends, err := state.ListSends(home, "task-1")
	if err != nil || len(sends) != 1 || sends[0].State != state.SendUncertain || sends[0].ReasonCode != "reconcile-stale-pending" {
		t.Fatalf("sends=%+v err=%v, want stale pending uncertain", sends, err)
	}
}

func reconcileRuntime(client herdrClient, get func(string, string) (worktree.Lease, error)) *Runtime {
	if get == nil {
		get = func(path, _ string) (worktree.Lease, error) {
			return worktree.Lease{Path: filepath.Join(path, "leased"), ID: "lease-1"}, nil
		}
	}
	return &Runtime{deps: dependencies{
		now:   func() time.Time { return time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC) },
		herdr: func() herdrClient { return client },
		worktree: worktreeDependencies{
			get: get,
			observeLease: func(string, string, string) worktree.LeaseObservation {
				return worktree.LeaseObservation{State: worktree.LeaseExact}
			},
			observeClean: func(path string) (worktree.Cleanliness, error) {
				return worktree.Clean, nil
			},
			observeCommits: func(path string) worktree.CommitSafetyObservation {
				return worktree.CommitSafetyObservation{
					State: worktree.CommitSafetyRemoteObserved,
					Probe: worktree.CommitSafetyProbe{Command: "git rev-list --count HEAD --not --remotes", WorkingDir: path, Head: "1111111111111111111111111111111111111111", RemoteRefs: 1},
				}
			},
			checkCollision: func(string, worktree.Lease, string) (string, error) { return "", nil },
			returnWorktree: func(string, string, bool) error { return nil },
			returnWithID:   func(string, string, string, bool) error { return nil },
		},
		buildHarness:     func(string, harness.Options) (launchSpec, error) { return launchSpec{Executable: "launch"}, nil },
		confirmLaunch:    func(herdrClient, string, string, launchSpec) error { return nil },
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

type inventoryRaceReconcileHerdr struct {
	healthyReconcileHerdr
	beforeWorkspaceList      func() error
	beforeWorkspaceListCalls int
	tabListCalls             int
}

type releasedInventoryReconcileHerdr struct{ healthyReconcileHerdr }

type releasedReopenInventoryReconcileHerdr struct{ healthyReconcileHerdr }

// Reports the pane agent a grok attempt's confirm-launch arm expects, so reconcile reaches
// reconciliationActionConfirmLaunch instead of diagnosing launch-agent-mismatch.
type grokReconcileHerdr struct{ healthyReconcileHerdr }

func (f *grokReconcileHerdr) PaneGet(string) (herdr.Pane, error) {
	return herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1", Agent: "grok"}, nil
}

func (*inventoryReconcileHerdr) WorkspaceList() ([]herdr.Workspace, error) {
	return []herdr.Workspace{{WorkspaceID: "ws-orphan", Label: "hand:demo"}}, nil
}

func (*inventoryReconcileHerdr) TabList(string) ([]herdr.Tab, error) {
	return []herdr.Tab{{TabID: "tab-orphan", WorkspaceID: "ws-orphan", Label: "orphan-task"}}, nil
}

func (f *inventoryRaceReconcileHerdr) WorkspaceList() ([]herdr.Workspace, error) {
	f.beforeWorkspaceListCalls++
	if f.beforeWorkspaceList != nil {
		if err := f.beforeWorkspaceList(); err != nil {
			return nil, err
		}
		f.beforeWorkspaceList = nil
	}
	return []herdr.Workspace{{WorkspaceID: "ws-race", Label: "hand:demo"}}, nil
}

func (f *inventoryRaceReconcileHerdr) TabList(string) ([]herdr.Tab, error) {
	f.tabListCalls++
	return []herdr.Tab{{TabID: "tab-race", WorkspaceID: "ws-race", Label: "task-1"}}, nil
}

func (f *inventoryRaceReconcileHerdr) FindWorkspaceByLabel(string) (herdr.Workspace, bool, error) {
	return herdr.Workspace{WorkspaceID: "ws-race", Label: "hand:demo"}, true, nil
}

func (f *inventoryRaceReconcileHerdr) PaneGet(string) (herdr.Pane, error) {
	return herdr.Pane{PaneID: "pane-race", TabID: "tab-race", WorkspaceID: "ws-race", Agent: "claude"}, nil
}

func (*releasedInventoryReconcileHerdr) WorkspaceList() ([]herdr.Workspace, error) {
	return []herdr.Workspace{{WorkspaceID: "ws-released", Label: "hand:demo"}}, nil
}

func (*releasedInventoryReconcileHerdr) TabList(string) ([]herdr.Tab, error) {
	return []herdr.Tab{{TabID: "tab-released", WorkspaceID: "ws-released", Label: "malformed label"}}, nil
}

func (*releasedInventoryReconcileHerdr) FindWorkspaceByLabel(string) (herdr.Workspace, bool, error) {
	return herdr.Workspace{WorkspaceID: "ws-released", Label: "hand:demo"}, true, nil
}

func (*releasedInventoryReconcileHerdr) PaneGet(string) (herdr.Pane, error) {
	return herdr.Pane{PaneID: "pane-released", TabID: "tab-released", WorkspaceID: "ws-released", Agent: "claude"}, nil
}

func (*releasedReopenInventoryReconcileHerdr) WorkspaceList() ([]herdr.Workspace, error) {
	return []herdr.Workspace{{WorkspaceID: "ws-reused", Label: "hand:demo"}}, nil
}

func (*releasedReopenInventoryReconcileHerdr) TabList(string) ([]herdr.Tab, error) {
	return []herdr.Tab{{TabID: "tab-reused", WorkspaceID: "ws-reused", Label: "task-1"}}, nil
}

func (*missingReconcileHerdr) FindWorkspaceByLabel(string) (herdr.Workspace, bool, error) {
	return herdr.Workspace{}, false, nil
}

func (f *healthyReconcileHerdr) FindWorkspaceByLabel(label string) (herdr.Workspace, bool, error) {
	return herdr.Workspace{WorkspaceID: "ws-1", Label: label}, true, nil
}
func (f *healthyReconcileHerdr) WorkspaceList() ([]herdr.Workspace, error) { return nil, nil }
func (f *healthyReconcileHerdr) WorkspaceCreate(string, map[string]string, string) (herdr.Workspace, herdr.Tab, herdr.Pane, error) {
	return herdr.Workspace{}, herdr.Tab{}, herdr.Pane{}, errors.New("unused")
}
func (f *healthyReconcileHerdr) WorkspaceClose(string) error { f.closed++; return nil }
func (f *healthyReconcileHerdr) TabList(string) ([]herdr.Tab, error) {
	return []herdr.Tab{{TabID: "tab-1", WorkspaceID: "ws-1", Label: "task-1"}}, nil
}
func (f *healthyReconcileHerdr) TabCreate(string, string, map[string]string, string) (herdr.Tab, herdr.Pane, error) {
	return herdr.Tab{TabID: "tab-1", WorkspaceID: "ws-1", Label: "task-1"}, herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1"}, nil
}
func (f *healthyReconcileHerdr) TabRename(string, string) error { return nil }
func (f *healthyReconcileHerdr) TabClose(string) error          { return nil }
func (f *healthyReconcileHerdr) PaneGet(string) (herdr.Pane, error) {
	return herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1", Agent: "claude"}, nil
}
func (f *healthyReconcileHerdr) PaneProcessInfo(string) (herdr.ProcessInfo, error) {
	return herdr.ProcessInfo{ShellPID: 1, ForegroundProcesses: []herdr.Process{{PID: 1, Name: "bash"}}}, nil
}
func (f *healthyReconcileHerdr) PaneRunSpec(string, launchSpec) error { f.runs++; return nil }
func (f *healthyReconcileHerdr) PaneRun(string, string) error         { f.runs++; return nil }
func (f *healthyReconcileHerdr) PaneSendKeys(string, ...string) error { return nil }
func (f *healthyReconcileHerdr) PaneRead(string, int) (string, error) { return "ready", nil }
