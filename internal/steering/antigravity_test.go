package steering

import (
	"errors"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/store"
)

func TestExecuteRefusesOneShotWorkerBeforeAnySendOrPaneMutation(t *testing.T) {
	home := t.TempDir()
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(state.Task{ID: "task-1", Lifecycle: state.TaskOpen}); err != nil {
		t.Fatal(err)
	}
	attempt, err := db.CreateAttempt(state.Attempt{
		TaskID:    "task-1",
		Lifecycle: state.AttemptRunning,
		Harness:   harness.Antigravity,
		Herdr:     state.Herdr{Session: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1", PaneID: "pane-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	pane := &testPane{pane: herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "workspace-1", AgentStatus: herdr.StatusIdle}}
	_, err = Execute(Request{Home: home, TaskID: "task-1", Message: "echo this must never reach a shell", Origin: state.SendOriginOperator, Client: pane})
	var sendErr *Error
	if err == nil || !errors.As(err, &sendErr) || !sendErr.Precondition || !strings.Contains(err.Error(), "one-shot worker execution") {
		t.Fatalf("err = %v, want one-shot precondition refusal", err)
	}
	if len(pane.textCalls) != 0 || len(pane.keyCalls) != 0 {
		t.Fatalf("pane mutations = text %v keys %v, want none", pane.textCalls, pane.keyCalls)
	}
	sends, readErr := state.ListSends(home, "task-1")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(sends) != 0 {
		t.Fatalf("sends = %+v, want none before refusal", sends)
	}
	_ = attempt
}
