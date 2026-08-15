package store

import (
	"errors"
	"testing"
)

const sendTestTime = "2026-08-15T12:00:00.123456789Z"

func newSendTestDB(t *testing.T) (*DB, Task, Attempt) {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	task := Task{ID: "task-1", Lifecycle: TaskOpen}
	if err := db.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	attempt, err := db.CreateAttempt(Attempt{
		TaskID: "task-1", Lifecycle: AttemptRunning,
		Herdr: Herdr{Session: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1", PaneID: "pane-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, task, attempt
}

func TestBeginSendRequiresExactOpenRunningAttemptOwnership(t *testing.T) {
	db, task, attempt := newSendTestDB(t)
	got, err := db.BeginSend(task.ID, attempt.ID, attempt.Herdr, SendOriginOperator, "hello", sendTestTime)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == 0 || got.TaskID != task.ID || got.AttemptID != attempt.ID || got.Message != "hello" || got.Origin != SendOriginOperator || got.State != SendPending || got.CreatedAt != sendTestTime {
		t.Fatalf("send = %+v, want immutable pending facts", got)
	}

	if _, err := db.BeginSend(task.ID, attempt.ID, attempt.Herdr, SendOriginOperator, "second", sendTestTime); !errors.Is(err, ErrSendInFlight) {
		t.Fatalf("duplicate pending error = %v, want ErrSendInFlight", err)
	}

	terminalDB, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminalDB.Close() })
	terminalTask := Task{ID: "terminal-task", Lifecycle: TaskOpen}
	if err := terminalDB.CreateTask(terminalTask); err != nil {
		t.Fatal(err)
	}
	if err := terminalDB.TransitionTask(terminalTask.ID, TaskOpen, TaskTerminal); err != nil {
		t.Fatal(err)
	}
	if _, err := terminalDB.BeginSend(terminalTask.ID, 1, Herdr{PaneID: "pane-1"}, SendOriginOperator, "hello", sendTestTime); !errors.Is(err, ErrSendOwnershipConflict) {
		t.Fatalf("terminal task error = %v, want ErrSendOwnershipConflict", err)
	}
}

func TestBeginSendRejectsHistoricalAttemptAndChangedPane(t *testing.T) {
	db, task, first := newSendTestDB(t)
	if err := db.TransitionAttempt(first.ID, AttemptRunning, AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateAttempt(Attempt{TaskID: task.ID, Lifecycle: AttemptRunning, Herdr: Herdr{WorkspaceID: "workspace-2", TabID: "tab-2", PaneID: "pane-2"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.BeginSend(task.ID, first.ID, first.Herdr, SendOriginOperator, "old", sendTestTime); !errors.Is(err, ErrSendOwnershipConflict) {
		t.Fatalf("historical attempt error = %v, want ErrSendOwnershipConflict", err)
	}
	if _, err := db.BeginSend(task.ID, second.ID, first.Herdr, SendOriginOperator, "wrong pane", sendTestTime); !errors.Is(err, ErrSendOwnershipConflict) {
		t.Fatalf("changed pane error = %v, want ErrSendOwnershipConflict", err)
	}
}

func TestSendFinalizationAllowsOnlyPendingTerminalTransitions(t *testing.T) {
	db, task, attempt := newSendTestDB(t)
	pending, err := db.BeginSend(task.ID, attempt.ID, attempt.Herdr, SendOriginOperator, "hello", sendTestTime)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []SendState{SendNotSubmitted, SendSubmitted, SendUncertain} {
		if _, err := db.FinalizeSend(pending.ID, task.ID, attempt.ID, state, "reason-"+string(state), sendTestTime); err != nil {
			t.Fatalf("finalize %s: %v", state, err)
		}
		got, found, err := db.ReadSend(pending.ID)
		if err != nil || !found {
			t.Fatalf("read %s: found=%v err=%v", state, found, err)
		}
		if got.State != state || got.ReasonCode != "reason-"+string(state) || got.FinalizedAt != sendTestTime {
			t.Fatalf("send = %+v, want finalized %s", got, state)
		}
		if _, err := db.FinalizeSend(pending.ID, task.ID, attempt.ID, state, "other", "later"); err != nil {
			t.Fatalf("idempotent finalize %s: %v", state, err)
		}
		if _, err := db.FinalizeSend(pending.ID, task.ID, attempt.ID, SendPending, "regress", "later"); !errors.Is(err, ErrInvalidSendTransition) {
			t.Fatalf("regressive finalize %s error = %v, want ErrInvalidSendTransition", state, err)
		}
		if state != SendUncertain {
			pending, err = db.BeginSend(task.ID, attempt.ID, attempt.Herdr, SendOriginOperator, "next", sendTestTime)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestNormalizePendingSendsMakesOnlyPendingUncertain(t *testing.T) {
	db, task, attempt := newSendTestDB(t)
	pending, err := db.BeginSend(task.ID, attempt.ID, attempt.Herdr, SendOriginOperator, "pending", sendTestTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.FinalizeSend(pending.ID, task.ID, attempt.ID, SendSubmitted, "submitted", sendTestTime); err != nil {
		t.Fatal(err)
	}
	uncertain, err := db.BeginSend(task.ID, attempt.ID, attempt.Herdr, SendOriginOperator, "uncertain", sendTestTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.FinalizeSend(uncertain.ID, task.ID, attempt.ID, SendUncertain, "uncertain", sendTestTime); err != nil {
		t.Fatal(err)
	}
	pending, err = db.BeginSend(task.ID, attempt.ID, attempt.Herdr, SendOriginOperator, "pending-again", sendTestTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.NormalizePendingSends(task.ID, "recovered", "2026-08-15T12:01:00Z"); err != nil {
		t.Fatal(err)
	}
	got, found, err := db.ReadSend(pending.ID)
	if err != nil || !found {
		t.Fatalf("read normalized send: found=%v err=%v", found, err)
	}
	if got.State != SendUncertain || got.ReasonCode != "recovered" {
		t.Fatalf("normalized send = %+v, want uncertain/recovered", got)
	}
	sends, err := db.ListSends(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sends) != 3 {
		t.Fatalf("sends = %d, want 3", len(sends))
	}
}
