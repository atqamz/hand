package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/store"
)

func TestObserveLegacyV18CutoverHerdrRejectsNilGuard(t *testing.T) {
	if _, err := observeLegacyV18CutoverHerdr(context.Background(), nil); !errors.Is(err, store.ErrLegacyV18CutoverGuardClosed) {
		t.Fatalf("nil guard observation = %v, want %v", err, store.ErrLegacyV18CutoverGuardClosed)
	}
}

func TestObserveLegacyV18CutoverHerdrPlanAllowsStoppedCurrentAndLegacySessions(t *testing.T) {
	plan, deps := legacyV18CutoverHerdrFixture()
	plan.Herdr = []store.LegacyV18CutoverHerdrObservation{{
		TaskID: "task-1", AttemptID: 7, ProjectID: "project-1", ProjectName: "demo",
		Session: "hand-f_self", WorkspaceID: "workspace-1", TabID: "tab-1", PaneID: "pane-1", TeardownState: "released",
	}}

	evidence, err := observeLegacyV18CutoverHerdrPlan(context.Background(), plan, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Sessions) != 2 || evidence.Sessions[0].State != herdr.SessionStopped || evidence.Sessions[1].State != herdr.SessionStopped {
		t.Fatalf("evidence = %#v, want two stopped sessions", evidence)
	}
}

func TestObserveLegacyV18CutoverHerdrPlanAllowsUnrelatedResources(t *testing.T) {
	plan, deps := legacyV18CutoverHerdrFixture()
	current := herdr.SessionName(plan.FleetID)
	deps.observeSession = runningLegacyV18CutoverHerdrSession
	deps.inventoryFor = legacyV18CutoverHerdrInventories(map[string]*fakeLegacyV18CutoverHerdrInventory{
		current: {
			workspaces: []herdr.Workspace{{WorkspaceID: "user-workspace", Label: "scratch"}},
			tabs:       map[string][]herdr.Tab{"user-workspace": nil},
		},
		"default": {
			workspaces: []herdr.Workspace{{WorkspaceID: "legacy-user-workspace", Label: "personal"}},
			tabs:       map[string][]herdr.Tab{"legacy-user-workspace": nil},
		},
	})

	if _, err := observeLegacyV18CutoverHerdrPlan(context.Background(), plan, deps); err != nil {
		t.Fatalf("unrelated Herdr resources blocked cutover: %v", err)
	}
}

func TestObserveLegacyV18CutoverHerdrPlanBlocksCurrentHandWorkspace(t *testing.T) {
	plan, deps := legacyV18CutoverHerdrFixture()
	current := herdr.SessionName(plan.FleetID)
	deps.observeSession = runningLegacyV18CutoverHerdrSession
	deps.inventoryFor = legacyV18CutoverHerdrInventories(map[string]*fakeLegacyV18CutoverHerdrInventory{
		current: {
			workspaces: []herdr.Workspace{{WorkspaceID: "workspace-1", Label: "hand:f_self:demo"}},
			tabs:       map[string][]herdr.Tab{"workspace-1": nil},
		},
		"default": {},
	})

	requireLegacyV18CutoverHerdrBlocker(t, plan, deps, "herdr-current-hand-resource-live")
}

func TestObserveLegacyV18CutoverHerdrPlanBlocksLegacyHandWorkspace(t *testing.T) {
	plan, deps := legacyV18CutoverHerdrFixture()
	current := herdr.SessionName(plan.FleetID)
	deps.observeSession = runningLegacyV18CutoverHerdrSession
	deps.inventoryFor = legacyV18CutoverHerdrInventories(map[string]*fakeLegacyV18CutoverHerdrInventory{
		current: {},
		"default": {
			workspaces: []herdr.Workspace{{WorkspaceID: "workspace-1", Label: "hand:demo"}},
			tabs:       map[string][]herdr.Tab{"workspace-1": nil},
		},
	})

	requireLegacyV18CutoverHerdrBlocker(t, plan, deps, "herdr-legacy-hand-resource-live")
}

func TestObserveLegacyV18CutoverHerdrPlanBlocksRecordedWorkspaceAfterRename(t *testing.T) {
	plan, deps := legacyV18CutoverHerdrFixture()
	current := herdr.SessionName(plan.FleetID)
	plan.Herdr = []store.LegacyV18CutoverHerdrObservation{{
		TaskID: "task-1", AttemptID: 7, ProjectID: "project-1", ProjectName: "demo",
		Session: current, WorkspaceID: "workspace-1", TabID: "tab-1", PaneID: "pane-1", TeardownState: "released",
	}}
	deps.observeSession = runningLegacyV18CutoverHerdrSession
	deps.inventoryFor = legacyV18CutoverHerdrInventories(map[string]*fakeLegacyV18CutoverHerdrInventory{
		current: {
			workspaces: []herdr.Workspace{{WorkspaceID: "workspace-1", Label: "renamed"}},
			tabs:       map[string][]herdr.Tab{"workspace-1": nil},
		},
		"default": {},
	})

	requireLegacyV18CutoverHerdrBlocker(t, plan, deps, "herdr-recorded-workspace-live")
}

func TestObserveLegacyV18CutoverHerdrPlanBlocksRecordedTabAfterLabelRename(t *testing.T) {
	plan, deps := legacyV18CutoverHerdrFixture()
	current := herdr.SessionName(plan.FleetID)
	plan.Herdr = []store.LegacyV18CutoverHerdrObservation{{
		TaskID: "task-1", AttemptID: 7, ProjectID: "project-1", ProjectName: "demo",
		Session: current, WorkspaceID: "workspace-1", TabID: "tab-1", PaneID: "pane-1", TeardownState: "abandoned",
	}}
	deps.observeSession = runningLegacyV18CutoverHerdrSession
	deps.inventoryFor = legacyV18CutoverHerdrInventories(map[string]*fakeLegacyV18CutoverHerdrInventory{
		current: {
			workspaces: []herdr.Workspace{{WorkspaceID: "workspace-1", Label: "renamed"}},
			tabs: map[string][]herdr.Tab{
				"workspace-1": {{TabID: "tab-1", WorkspaceID: "workspace-1", Label: "renamed-tab"}},
			},
		},
		"default": {},
	})

	requireLegacyV18CutoverHerdrBlocker(t, plan, deps, "herdr-recorded-tab-live")
}

func TestObserveLegacyV18CutoverHerdrPlanBlocksRecordedPaneAfterRename(t *testing.T) {
	plan, deps := legacyV18CutoverHerdrFixture()
	current := herdr.SessionName(plan.FleetID)
	plan.Herdr = []store.LegacyV18CutoverHerdrObservation{{
		TaskID: "task-1", AttemptID: 7, ProjectID: "project-1", ProjectName: "demo",
		Session: current, WorkspaceID: "old-workspace", TabID: "old-tab", PaneID: "pane-1", TeardownState: "released",
	}}
	deps.observeSession = runningLegacyV18CutoverHerdrSession
	deps.inventoryFor = legacyV18CutoverHerdrInventories(map[string]*fakeLegacyV18CutoverHerdrInventory{
		current: {
			panes: map[string]herdr.Pane{
				"pane-1": {PaneID: "pane-1", TabID: "new-tab", WorkspaceID: "new-workspace"},
			},
		},
		"default": {},
	})

	requireLegacyV18CutoverHerdrBlocker(t, plan, deps, "herdr-recorded-pane-live")
}

func TestObserveLegacyV18CutoverHerdrPlanAllowsAbsentRecordedIdentityInRunningSession(t *testing.T) {
	plan, deps := legacyV18CutoverHerdrFixture()
	current := herdr.SessionName(plan.FleetID)
	plan.Herdr = []store.LegacyV18CutoverHerdrObservation{{
		TaskID: "task-1", AttemptID: 7, ProjectID: "project-1", ProjectName: "demo",
		Session: current, WorkspaceID: "workspace-1", TabID: "tab-1", PaneID: "pane-1", TeardownState: "released",
	}}
	deps.observeSession = runningLegacyV18CutoverHerdrSession
	deps.inventoryFor = legacyV18CutoverHerdrInventories(map[string]*fakeLegacyV18CutoverHerdrInventory{
		current:   {},
		"default": {},
	})

	if _, err := observeLegacyV18CutoverHerdrPlan(context.Background(), plan, deps); err != nil {
		t.Fatalf("absent recorded Herdr identity blocked cutover: %v", err)
	}
}

func TestObserveLegacyV18CutoverHerdrPlanBlocksUnknownSession(t *testing.T) {
	plan, deps := legacyV18CutoverHerdrFixture()
	current := herdr.SessionName(plan.FleetID)
	deps.observeSession = func(_ context.Context, session string) herdr.SessionObservation {
		if session == current {
			return herdr.SessionObservation{Name: session, State: herdr.SessionUnknown, Reason: "provider unavailable"}
		}
		return herdr.SessionObservation{Name: session, State: herdr.SessionStopped}
	}

	requireLegacyV18CutoverHerdrBlocker(t, plan, deps, "herdr-session-unobservable")
}

func TestObserveLegacyV18CutoverHerdrPlanBlocksUnrecognizedRecordedSession(t *testing.T) {
	plan, deps := legacyV18CutoverHerdrFixture()
	plan.Herdr = []store.LegacyV18CutoverHerdrObservation{{
		TaskID: "task-1", AttemptID: 7, ProjectID: "project-1", ProjectName: "demo",
		Session: "hand-f_other", WorkspaceID: "workspace-1", TabID: "tab-1", PaneID: "pane-1", TeardownState: "released",
	}}

	requireLegacyV18CutoverHerdrBlocker(t, plan, deps, "herdr-recorded-session-unrecognized")
}

func TestObserveLegacyV18CutoverHerdrPlanBlocksInventoryFailure(t *testing.T) {
	plan, deps := legacyV18CutoverHerdrFixture()
	current := herdr.SessionName(plan.FleetID)
	deps.observeSession = runningLegacyV18CutoverHerdrSession
	deps.inventoryFor = legacyV18CutoverHerdrInventories(map[string]*fakeLegacyV18CutoverHerdrInventory{
		current:   {workspaceErr: errors.New("malformed workspace response")},
		"default": {},
	})

	requireLegacyV18CutoverHerdrBlocker(t, plan, deps, "herdr-workspace-inventory-unobservable")
}

func TestObserveLegacyV18CutoverHerdrPlanBlocksRecordedPaneObservationFailure(t *testing.T) {
	plan, deps := legacyV18CutoverHerdrFixture()
	current := herdr.SessionName(plan.FleetID)
	plan.Herdr = []store.LegacyV18CutoverHerdrObservation{{
		TaskID: "task-1", AttemptID: 7, ProjectID: "project-1", ProjectName: "demo",
		Session: current, WorkspaceID: "workspace-1", TabID: "tab-1", PaneID: "pane-1", TeardownState: "released",
	}}
	deps.observeSession = runningLegacyV18CutoverHerdrSession
	deps.inventoryFor = legacyV18CutoverHerdrInventories(map[string]*fakeLegacyV18CutoverHerdrInventory{
		current:   {paneErrors: map[string]error{"pane-1": errors.New("provider unavailable")}},
		"default": {},
	})

	requireLegacyV18CutoverHerdrBlocker(t, plan, deps, "herdr-recorded-pane-unobservable")
}

func legacyV18CutoverHerdrFixture() (store.LegacyV18CutoverObservationPlan, legacyV18CutoverHerdrDeps) {
	plan := store.LegacyV18CutoverObservationPlan{FleetID: "f_self"}
	deps := legacyV18CutoverHerdrDeps{
		observeSession: func(_ context.Context, session string) herdr.SessionObservation {
			return herdr.SessionObservation{Name: session, State: herdr.SessionStopped}
		},
		inventoryFor: func(string) legacyV18CutoverHerdrInventory {
			return nil
		},
	}
	return plan, deps
}

func runningLegacyV18CutoverHerdrSession(_ context.Context, session string) herdr.SessionObservation {
	return herdr.SessionObservation{Name: session, State: herdr.SessionRunningCompatible}
}

func legacyV18CutoverHerdrInventories(inventories map[string]*fakeLegacyV18CutoverHerdrInventory) func(string) legacyV18CutoverHerdrInventory {
	return func(session string) legacyV18CutoverHerdrInventory {
		return inventories[session]
	}
}

type fakeLegacyV18CutoverHerdrInventory struct {
	workspaces   []herdr.Workspace
	workspaceErr error
	tabs         map[string][]herdr.Tab
	tabErrors    map[string]error
	panes        map[string]herdr.Pane
	paneErrors   map[string]error
}

func (f *fakeLegacyV18CutoverHerdrInventory) WorkspaceList() ([]herdr.Workspace, error) {
	if f == nil {
		return nil, errors.New("nil Herdr inventory")
	}
	return append([]herdr.Workspace(nil), f.workspaces...), f.workspaceErr
}

func (f *fakeLegacyV18CutoverHerdrInventory) TabList(workspaceID string) ([]herdr.Tab, error) {
	if err := f.tabErrors[workspaceID]; err != nil {
		return nil, err
	}
	return append([]herdr.Tab(nil), f.tabs[workspaceID]...), nil
}

func (f *fakeLegacyV18CutoverHerdrInventory) PaneGet(paneID string) (herdr.Pane, error) {
	if err := f.paneErrors[paneID]; err != nil {
		return herdr.Pane{}, err
	}
	if pane, ok := f.panes[paneID]; ok {
		return pane, nil
	}
	return herdr.Pane{}, fmt.Errorf("pane %s: %w", paneID, herdr.ErrNotFound)
}

func requireLegacyV18CutoverHerdrBlocker(t *testing.T, plan store.LegacyV18CutoverObservationPlan, deps legacyV18CutoverHerdrDeps, code string) {
	t.Helper()
	_, err := observeLegacyV18CutoverHerdrPlan(context.Background(), plan, deps)
	if err == nil {
		t.Fatalf("want blocker %q, got nil", code)
	}
	if !errors.Is(err, errLegacyV18CutoverHerdrUnsafe) {
		t.Fatalf("error = %v, want %v", err, errLegacyV18CutoverHerdrUnsafe)
	}
	var blocked *legacyV18CutoverHerdrBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("error = %T %v, want *legacyV18CutoverHerdrBlockedError", err, err)
	}
	for _, blocker := range blocked.Blockers {
		if blocker.Code == code {
			return
		}
	}
	t.Fatalf("blockers = %#v, want code %q", blocked.Blockers, code)
}
