//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/state"
)

func TestReconcileReportsReleasedHerdrResourceThroughStatefulFake(t *testing.T) {
	home := newHome(t)
	oldAttempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude",
		Herdr: state.Herdr{Session: "default", WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
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
	faketool.Herdr{Workspaces: []faketool.HerdrWorkspace{{ID: "ws-1", Label: "hand:demo", Tabs: []faketool.HerdrTab{{ID: "tab-1", Label: "task-1", Pane: "pane-1"}}}}}.Install(t, binDir(t))

	got := runHand(t, home, "reconcile")
	if got.code != 3 {
		t.Fatalf("reconcile: exit %d, stdout %q, stderr %q, want precondition anomaly", got.code, got.stdout, got.stderr)
	}
	for _, want := range []string{"released-herdr-resource", fmt.Sprintf("owner_attempt=%d", oldAttempt.ID), "refusing to close"} {
		if !strings.Contains(got.stdout, want) {
			t.Fatalf("reconcile stdout = %q, want %q", got.stdout, want)
		}
	}
}
