package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestStatusInspectsTerminalTaskAndAttemptHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HAND_HOME", home)
	if err := initLayout(home); err != nil {
		t.Fatal(err)
	}
	if err := state.CreateTask(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Lifecycle: state.TaskOpen}); err != nil {
		t.Fatal(err)
	}
	attempt, err := state.CreateAttempt(home, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptRunning, Harness: "claude", Worktree: "/tmp/wt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TransitionAttempt(home, attempt.ID, state.AttemptRunning, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	if err := state.TransitionTask(home, "task-1", state.TaskOpen, state.TaskTerminal); err != nil {
		t.Fatal(err)
	}

	cmd := newStatusCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got struct {
		TaskLifecycle    string `json:"task_lifecycle"`
		AttemptLifecycle string `json:"attempt_lifecycle"`
		Attempts         []struct {
			Ordinal int `json:"ordinal"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if got.TaskLifecycle != string(state.TaskTerminal) || got.AttemptLifecycle != string(state.AttemptCompleted) || len(got.Attempts) != 1 || got.Attempts[0].Ordinal != 1 {
		t.Fatalf("status = %+v", got)
	}
}
