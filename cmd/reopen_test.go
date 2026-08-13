package cmd

import (
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
)

func TestReopenCreatesANewAttemptWithoutResurrectingTheOldOne(t *testing.T) {
	herdr := defaultSpawnHerdr(harness.Claude)
	herdr.TabCreates = []faketool.HerdrTab{
		{ID: "wA:tB", Label: "task-1", Pane: "wA:pC"},
		{ID: "wA:tC", Label: "task-1", Pane: "wA:pD"},
	}
	home := setupSpawnHome(t, t.TempDir()+"/wt", herdr)

	spawn := newSpawnCmd()
	spawn.SetArgs([]string{"task-1", "myproj", "--harness", harness.Claude})
	if err := spawn.Execute(); err != nil {
		t.Fatal(err)
	}
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	task.PR = "https://github.com/o/r/pull/1"
	task.MergeAnnounced = true
	task.DeliveredAt = "2026-08-03T00:00:00Z"
	task.DeliveredReason = "offered upstream"
	if err := state.UpdateTask(home, task); err != nil {
		t.Fatal(err)
	}
	teardown := newTeardownCmd()
	teardown.SetArgs([]string{"task-1", "--force"})
	if err := teardown.Execute(); err != nil {
		t.Fatal(err)
	}

	reopen := newReopenCmd()
	reopen.SetArgs([]string{"task-1", "--harness", harness.Codex, "--model", "new-model", "--effort", "high"})
	if err := reopen.Execute(); err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != state.TaskOpen || len(history.Attempts) != 2 || history.Attempts[0].Lifecycle != state.AttemptInterrupted || history.Attempts[1].Lifecycle != state.AttemptRunning {
		t.Fatalf("reopened history = %+v", history)
	}
	if history.Attempts[1].Harness != harness.Codex || history.Attempts[1].Model != "new-model" || history.Attempts[1].Ordinal != 2 {
		t.Fatalf("new attempt = %+v", history.Attempts[1])
	}
	if history.Task.PR != task.PR || !history.Task.MergeAnnounced || history.Task.DeliveredAt != task.DeliveredAt || history.Task.DeliveredReason != task.DeliveredReason {
		t.Fatalf("reopen erased task history: %+v", history.Task)
	}
}

func TestReopenRefusesAnOpenTask(t *testing.T) {
	home := setupSpawnHome(t, t.TempDir()+"/wt", defaultSpawnHerdr(harness.Claude))
	if err := state.Write(home, state.Task{ID: "task-1", Lifecycle: state.TaskOpen}); err != nil {
		t.Fatal(err)
	}
	cmd := newReopenCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("reopen accepted an open task")
	}
}

func TestReopenRefusesAnOpenHold(t *testing.T) {
	home := setupSpawnHome(t, t.TempDir()+"/wt", defaultSpawnHerdr(harness.Claude))
	if err := state.Write(home, state.Task{ID: "task-1", Lifecycle: state.TaskOpen}); err != nil {
		t.Fatal(err)
	}
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TransitionAttempt(home, task.ActiveAttemptID, state.AttemptRunning, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	if err := state.TransitionTask(home, "task-1", state.TaskOpen, state.TaskTerminal); err != nil {
		t.Fatal(err)
	}
	if err := state.SetHold(home, state.Hold{ID: "task-1", Kind: state.HoldKindOperator, Reason: "wait for review"}); err != nil {
		t.Fatal(err)
	}

	cmd := newReopenCmd()
	cmd.SetArgs([]string{"task-1", "--harness", harness.Claude})
	if err := cmd.Execute(); err == nil {
		t.Fatal("reopen bypassed an open hold")
	}
}
