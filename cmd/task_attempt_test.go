package cmd

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/project"
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

// Teardown writes the permanent completion record from the state the task had then, so a delivery
// recorded afterwards would sit on the row unread. hand pr is the one exception (atqamz/hand#424):
// see TestPRRecordsOnATerminalTaskWithoutReopening.
func TestDeliverRefusesATerminalTask(t *testing.T) {
	home := terminalTaskHome(t)
	cmd := newDeliverCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"task-1", "--reason", "handed to upstream"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("hand deliver accepted a terminal task")
	}
	if !strings.Contains(err.Error(), "hand reopen task-1") {
		t.Fatalf("error = %v, want it to name the remedy", err)
	}
	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.DeliveredAt != "" {
		t.Fatalf("terminal task was written to: %+v", got)
	}
}

// atqamz/hand#424 ask 1: recording where a torn-down task's work landed is not resuming it, so hand
// pr is the one write allowed past teardown's completion record. Lifecycle must stay terminal, and
// taskFlags/needsAttention (the fleet's one attention definition) must never key off task.PR.
func TestPRRecordsOnATerminalTaskWithoutReopening(t *testing.T) {
	home := terminalTaskHome(t)
	registerTerminalTaskProject(t, home)
	const pr = "https://github.com/owner/repo/pull/7"
	faketool.GH{PRs: []faketool.GHPR{{Number: 7, URL: pr, Repo: "owner/repo", State: "OPEN"}}}.Install(t, faketool.Bin(t))

	cmd := newPRCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"task-1", pr})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("hand pr refused a terminal task: %v", err)
	}
	if strings.Contains(out.String(), "hand merge") {
		t.Fatalf("output = %q, want no hand merge suggestion: the task has no active attempt for it to act on", out.String())
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PR != pr {
		t.Fatalf("task.PR = %q, want %q recorded", got.PR, pr)
	}
	if got.Lifecycle != state.TaskTerminal {
		t.Fatalf("task.Lifecycle = %q, want it to stay terminal - hand pr must not reopen the task", got.Lifecycle)
	}

	// Write-once still holds: a second, different URL is refused exactly like an open task's would be.
	again := newPRCmd()
	again.SetOut(io.Discard)
	again.SetErr(io.Discard)
	again.SetArgs([]string{"task-1", "https://github.com/owner/repo/pull/8"})
	if err := again.Execute(); err == nil {
		t.Fatal("hand pr accepted a second, different PR on a task that already has one recorded")
	}

	view := taskView{task: got}
	if needsAttention(view) {
		t.Fatalf("needsAttention(%+v) = true, want a terminal task with a recorded PR to gain no liveness", view)
	}
	if flags := taskFlags(view); len(flags) != 0 {
		t.Fatalf("taskFlags(%+v) = %v, want none: a recorded-but-unmerged PR renders no flag", view, flags)
	}
}

func registerTerminalTaskProject(t *testing.T, home string) {
	t.Helper()
	clonePath := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	runGitIn(t, clonePath, "remote", "add", "origin", "https://github.com/owner/repo.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/owner/repo.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
}
