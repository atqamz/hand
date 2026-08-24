package runtime

import (
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
)

func installOneShotPhysicalObservation(t *testing.T) *herdr.Client {
	t.Helper()
	bin := faketool.Bin(t)
	faketool.Herdr{
		Workspaces: []faketool.HerdrWorkspace{{
			ID:    "wA",
			Label: herdr.LegacyWorkspaceLabel("project"),
			Tabs:  []faketool.HerdrTab{{ID: "tA", Label: "task-1", Pane: "pA"}},
		}},
		PaneStatus:  "unknown",
		PaneReadOut: "{\"event\":\"init\",\"conversation_id\":\"stale\",\"init\":{\"cwd\":\"/tmp/old\"}}\n",
	}.Install(t, bin)
	return herdr.NewClient()
}

func TestObserveHerdrOwnershipDoesNotInferWorkerIdentityFromScrollback(t *testing.T) {
	client := installOneShotPhysicalObservation(t)
	observation, err := observeHerdrOwnership(client, state.Herdr{
		Session: "default", WorkspaceID: "wA", TabID: "tA", PaneID: "pA",
	}, "task-1", "project")
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != herdrOwnershipExact {
		t.Fatalf("observation = %+v, want exact physical ownership", observation)
	}
	if observation.Agent != "" || observation.AgentStatus != herdr.StatusUnknown {
		t.Fatalf("observation = %+v, pane scrollback must not fabricate worker identity/liveness", observation)
	}
}

func TestDecideProvisioningOneShotUsesDedicatedLaunchConfirmation(t *testing.T) {
	attempt := state.Attempt{
		Lifecycle: state.AttemptProvisioning,
		Harness:   harness.Antigravity,
		Herdr: state.Herdr{
			Session: "default", WorkspaceID: "wA", TabID: "tA", PaneID: "pA",
		},
		LaunchSubmittedAt: "2026-08-24T00:00:00Z",
	}
	observation := reconciliationObservation{Herdr: herdrObservation{
		State: herdrOwnershipExact,
		Pane:  herdr.Pane{PaneID: "pA", TabID: "tA", WorkspaceID: "wA"},
	}}
	decision := decideReconciliation(state.Task{}, attempt, observation)
	if decision.Action != reconciliationActionConfirmLaunch {
		t.Fatalf("decision = %+v, want dedicated one-shot launch confirmation", decision)
	}
}

func TestDecideProvisioningOneShotRejectsDifferentLiveHarness(t *testing.T) {
	attempt := state.Attempt{
		Lifecycle: state.AttemptProvisioning,
		Harness:   harness.Antigravity,
		Herdr: state.Herdr{
			Session: "default", WorkspaceID: "wA", TabID: "tA", PaneID: "pA",
		},
		LaunchSubmittedAt: "2026-08-24T00:00:00Z",
	}
	observation := reconciliationObservation{Herdr: herdrObservation{
		State: herdrOwnershipExact, Agent: harness.Claude,
	}}
	decision := decideReconciliation(state.Task{}, attempt, observation)
	if decision.Action != reconciliationActionNeedsRepair || decision.RepairCode != repairCodeLaunchAgentMismatch {
		t.Fatalf("decision = %+v, want different live harness rejected", decision)
	}
}
