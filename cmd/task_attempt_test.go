package cmd

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
	"github.com/spf13/cobra"
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

func terminalTaskHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HAND_HOME", home)
	if err := initLayout(home); err != nil {
		t.Fatal(err)
	}
	if err := state.CreateTask(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Lifecycle: state.TaskOpen}); err != nil {
		t.Fatal(err)
	}
	attempt, err := state.CreateAttempt(home, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TransitionAttempt(home, attempt.ID, state.AttemptRunning, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	if err := state.TransitionTask(home, "task-1", state.TaskOpen, state.TaskTerminal); err != nil {
		t.Fatal(err)
	}
	return home
}

// Teardown writes the permanent completion record from the state the task had then, so a
// delivery or a PR recorded afterwards would sit on the row unread. Both name hand reopen.
func TestDeliverAndPRRefuseATerminalTask(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  func() *cobra.Command
		args []string
	}{
		{"deliver", newDeliverCmd, []string{"task-1", "--reason", "handed to upstream"}},
		{"pr", newPRCmd, []string{"task-1", "https://github.com/o/nsr/pull/7"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := terminalTaskHome(t)
			cmd := tc.cmd()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("hand %s accepted a terminal task", tc.name)
			}
			if !strings.Contains(err.Error(), "hand reopen task-1") {
				t.Fatalf("error = %v, want it to name the remedy", err)
			}
			got, err := state.Read(home, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if got.DeliveredAt != "" || got.PR != "" {
				t.Fatalf("terminal task was written to: %+v", got)
			}
		})
	}
}
