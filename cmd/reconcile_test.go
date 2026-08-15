package cmd

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/runtime"
	"github.com/atqamz/hand/internal/state"
)

func TestReconcileCommandRendersHealthyTerminalTask(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HAND_HOME", home)
	if err := os.MkdirAll(state.Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.CreateTask(home, state.Task{ID: "task-1", Lifecycle: state.TaskTerminal}); err != nil {
		t.Fatal(err)
	}
	cmd := newReconcileCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !bytes.Contains([]byte(got), []byte("result: healthy")) || !bytes.Contains([]byte(got), []byte("id: task-1")) {
		t.Fatalf("reconcile output = %q, want healthy structured result", got)
	}
}

func TestReconcileCommandRendersNeedsRepairAndReturnsPrecondition(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HAND_HOME", home)
	if err := os.MkdirAll(state.Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.CreateTask(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.CreateAttempt(home, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptRunning, Harness: "claude", Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}}); err != nil {
		t.Fatal(err)
	}
	faketool.Herdr{Responses: []faketool.HerdrResponse{{Command: "workspace list", Stdout: `{"id":"cli:1","result":{"workspaces":[]}}`}}}.Install(t, faketool.Bin(t))
	cmd := newReconcileCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("reconcile error = %v, want precondition exit 3", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("repair_code: running-pane-missing")) || !bytes.Contains(out.Bytes(), []byte("result: needs-repair")) {
		t.Fatalf("reconcile output = %q, want repair result", out.String())
	}
}

func TestReconcileCommandPreservesRepairAndObservationErrors(t *testing.T) {
	report := runtime.ReconcileReport{Results: []runtime.ReconcileResult{
		{ID: "task-a", Outcome: "needs-repair", RepairCode: "running-pane-missing"},
		{ID: "task-b", Outcome: "blocked", Error: "Herdr service unavailable"},
	}}
	err := reconcileReportError(report, errors.New("task-b: Herdr service unavailable"))
	if err == nil || !strings.Contains(err.Error(), "running-pane-missing") || !strings.Contains(err.Error(), "Herdr service unavailable") {
		t.Fatalf("reconcile error = %v, want repair and observation diagnostics", err)
	}
}
