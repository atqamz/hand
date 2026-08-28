package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/store"
)

// atqamz/hand#417, criterion 1: a satisfied blocked hold reaches hand orient (actionable) and hand
// status (marked), each naming the blocker. The blocker is terminal but cleanly torn down, absent from
// ListReconciliationHistories - this must resolve via a direct store read, not the fleet view.
func TestOrientAndStatusReportASatisfiedBlockedHold(t *testing.T) {
	home := setupSessionHome(t)
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddProject(store.Project{Name: "demo", URL: "local", Mode: "local-only"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(store.Task{ID: "held-task", Project: "demo", Kind: store.KindShip}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(store.Task{ID: "blocker-task", Project: "demo", Kind: store.KindShip, Lifecycle: store.TaskTerminal}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.SetHold(home, state.Hold{ID: "held-task", Kind: state.HoldKindBlocked,
		BlockedOn: "blocker-task", Reason: "waiting on blocker-task", SetAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	orientOut, _, err := executeRootForTest(t, devBuild("test"), nil, "orient")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(orientOut, "next_action_kind: hold-satisfied\n") || !strings.Contains(orientOut, "next_action_task: held-task\n") {
		t.Fatalf("orient = %q, want the satisfied hold to lead next_action", orientOut)
	}
	if !strings.Contains(orientOut, "orientation_actionable[1]{target_id,kind,reason,provenance}:") {
		t.Fatalf("orient = %q, want exactly one actionable item", orientOut)
	}
	// The row's target_id is blank rather than held-task: like every other actionable subject on a task
	// with no running attempt (e.g. needs-repair), it has no monitor target to resolve against. The task
	// is still named unambiguously by next_action_task above and by the reason naming its blocker here.
	if !strings.Contains(orientOut, ",hold-satisfied,blocker-task is terminal; this hold can be cleared,hold\n") {
		t.Fatalf("orient = %q, want the actionable item naming blocker-task with hold provenance", orientOut)
	}

	statusCmd := newStatusCmd()
	var statusOut bytes.Buffer
	statusCmd.SetOut(&statusOut)
	statusCmd.SetArgs(nil)
	if err := statusCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statusOut.String(), `held-task,blocked,"satisfied: blocker-task is terminal; this hold can be cleared"`) {
		t.Fatalf("status = %q, want the hold row naming its terminal blocker as satisfied", statusOut.String())
	}

	single := newStatusCmd()
	var singleOut bytes.Buffer
	single.SetOut(&singleOut)
	single.SetArgs([]string{"held-task"})
	if err := single.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := detailField(t, singleOut.String(), "held"); got != "satisfied: blocker-task is terminal; this hold can be cleared" {
		t.Fatalf("held = %q, want the satisfied blocker named", got)
	}

	// Criterion 4: reporting a satisfied hold is not clearing it - only `hand hold clear` does that.
	if _, found, err := state.ReadHold(home, "held-task"); err != nil || !found {
		t.Fatalf("ReadHold = %v, %v, want the satisfied hold to survive being reported", found, err)
	}
}

// Criterion 2: nothing changes from atqamz/hand#414's behaviour while the blocker is still open - the
// hold stays deferred, not actionable, and hand status keeps showing the plain "waiting on" text.
func TestOrientAndStatusLeaveABlockedHoldUnchangedWhileItsBlockerRuns(t *testing.T) {
	home := setupSessionHome(t)
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddProject(store.Project{Name: "demo", URL: "local", Mode: "local-only"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(store.Task{ID: "held-task", Project: "demo", Kind: store.KindShip}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(store.Task{ID: "blocker-task", Project: "demo", Kind: store.KindShip}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.SetHold(home, state.Hold{ID: "held-task", Kind: state.HoldKindBlocked,
		BlockedOn: "blocker-task", Reason: "waiting on blocker-task", SetAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	orientOut, _, err := executeRootForTest(t, devBuild("test"), nil, "orient")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(orientOut, "hold-satisfied") {
		t.Fatalf("orient = %q, want no hold-satisfied subject while the blocker is still open", orientOut)
	}
	if !strings.Contains(orientOut, "orientation_actionable[0]") {
		t.Fatalf("orient = %q, want nothing actionable for a still-blocked hold", orientOut)
	}
	if strings.Contains(orientOut, "next_action_task: held-task\n") {
		t.Fatalf("orient = %q, want held-task excluded from next_action", orientOut)
	}

	statusCmd := newStatusCmd()
	var statusOut bytes.Buffer
	statusCmd.SetOut(&statusOut)
	statusCmd.SetArgs(nil)
	if err := statusCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statusOut.String(), `held-task,blocked,"waiting on blocker-task: waiting on blocker-task"`) {
		t.Fatalf("status = %q, want the unchanged waiting-on text", statusOut.String())
	}
}

// Criterion 3: a blocked_on naming an id the store has never heard of is inconsistency, not
// satisfaction - "not found" must never read as "done". blocker-task is never created.
func TestStatusFlagsAnUnknownBlockedOnAsInconsistentNotSatisfied(t *testing.T) {
	home := setupSessionHome(t)
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddProject(store.Project{Name: "demo", URL: "local", Mode: "local-only"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(store.Task{ID: "held-task", Project: "demo", Kind: store.KindShip}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.SetHold(home, state.Hold{ID: "held-task", Kind: state.HoldKindBlocked,
		BlockedOn: "blocker-task", Reason: "waiting on blocker-task", SetAt: "2026-07-24T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	statusCmd := newStatusCmd()
	var statusOut bytes.Buffer
	statusCmd.SetOut(&statusOut)
	statusCmd.SetArgs(nil)
	if err := statusCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := statusOut.String()
	if !strings.Contains(got, `held-task,blocked,"inconsistent: blocked hold waits on unknown task \"blocker-task\""`) {
		t.Fatalf("status = %q, want the unknown blocker flagged inconsistent", got)
	}
	if strings.Contains(got, "satisfied") {
		t.Fatalf("status = %q, want an unknown blocker never reported as satisfied", got)
	}

	orientOut, _, err := executeRootForTest(t, devBuild("test"), nil, "orient")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(orientOut, "hold-satisfied") || !strings.Contains(orientOut, "orientation_actionable[0]") {
		t.Fatalf("orient = %q, want an unknown blocker to stay non-actionable", orientOut)
	}
}
