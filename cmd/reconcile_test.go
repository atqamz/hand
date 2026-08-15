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
	}, Anomalies: []runtime.ReconcileAnomaly{{Kind: "released-herdr-resource", WorkspaceID: "ws-1", TabID: "tab-1", OwnerAttemptID: 7, Reason: "refusing to close"}}}
	var out bytes.Buffer
	cmd := newReconcileCmd()
	cmd.SetOut(&out)
	if err := renderReconcileReport(cmd, report, false); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("error: Herdr service unavailable")) {
		t.Fatalf("rendered report = %q, want per-result observation error", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("owner_attempt=7")) || !bytes.Contains(out.Bytes(), []byte("reason=refusing to close")) {
		t.Fatalf("rendered report = %q, want attributed anomaly details", out.String())
	}
	err := reconcileReportError(report, errors.New("task-b: Herdr service unavailable"))
	if err == nil || !strings.Contains(err.Error(), "running-pane-missing") || !strings.Contains(err.Error(), "Herdr service unavailable") {
		t.Fatalf("reconcile error = %v, want repair and observation diagnostics", err)
	}
	if !strings.Contains(err.Error(), "released-herdr-resource") || !strings.Contains(err.Error(), "attempt 7") {
		t.Fatalf("reconcile error = %v, want the attributed anomaly classification", err)
	}
	if strings.Contains(err.Error(), "unattributed Herdr resource") {
		t.Fatalf("reconcile error = %v, must not misclassify an attributed anomaly", err)
	}
}

func TestReconcileCommandReportsUnknownRepairAsFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HAND_HOME", home)
	if err := os.MkdirAll(state.Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.CreateTask(home, state.Task{ID: "task-1", Lifecycle: state.TaskTerminal}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTaskRepair(home, "task-1", "operator-review-required", "unknown contradiction", 0, "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	cmd := newReconcileCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("reconcile error = %v, want precondition exit 3", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("result: needs-repair")) || !bytes.Contains(out.Bytes(), []byte("repair_code: operator-review-required")) {
		t.Fatalf("reconcile output = %q, want unknown repair result", out.String())
	}
}
