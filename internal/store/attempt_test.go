package store

import (
	"errors"
	"testing"
)

func TestTaskAndAttemptHaveSeparateIdentityAndExecutionOwnership(t *testing.T) {
	db, _ := openTemp(t)

	task := Task{ID: "task-1", Project: "demo", Kind: KindScout, Brief: "data/task-1/brief.md", Lifecycle: TaskOpen}
	if err := db.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	attempt, err := db.CreateAttempt(Attempt{
		TaskID: task.ID, Lifecycle: AttemptRunning, Harness: "claude", Model: "opus", Effort: "high",
		Worktree: "/tmp/wt-1", LeaseID: "lease-1", Herdr: Herdr{PaneID: "pane-1"}, CreatedAt: "2026-08-13T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempt.ID == 0 || attempt.Ordinal != 1 {
		t.Fatalf("attempt identity = %+v, want an SQLite ID and ordinal 1", attempt)
	}

	gotTask, found, err := db.ReadTask(task.ID)
	if err != nil || !found {
		t.Fatalf("ReadTask = %+v, %v, want task", gotTask, err)
	}
	if gotTask.ActiveAttemptID != attempt.ID {
		t.Fatalf("active attempt = %d, want %d", gotTask.ActiveAttemptID, attempt.ID)
	}
	if gotTask.ReportOffset != 0 || gotTask.ReportDigest != "" {
		t.Fatalf("task report state changed while creating attempt: %+v", gotTask)
	}
	gotAttempt, found, err := db.ReadAttempt(attempt.ID)
	if err != nil || !found {
		t.Fatalf("ReadAttempt = %+v, %v, want attempt", gotAttempt, err)
	}
	if gotAttempt.TaskID != task.ID || gotAttempt.Harness != "claude" || gotAttempt.Worktree != "/tmp/wt-1" {
		t.Fatalf("attempt lost execution identity: %+v", gotAttempt)
	}
}

func TestAttemptOrdinalIncrementsPerTaskAndHistoryIsRetained(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.CreateTask(Task{ID: "task-1", Lifecycle: TaskOpen}); err != nil {
		t.Fatal(err)
	}
	first, err := db.CreateAttempt(Attempt{TaskID: "task-1", Lifecycle: AttemptRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAttempt(first.ID, AttemptRunning, AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateAttempt(Attempt{TaskID: "task-1", Lifecycle: AttemptRunning})
	if err != nil {
		t.Fatal(err)
	}
	if second.Ordinal != 2 {
		t.Fatalf("second ordinal = %d, want 2", second.Ordinal)
	}
	attempts, err := db.ListAttempts("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].ID != first.ID || attempts[1].ID != second.ID {
		t.Fatalf("attempt history = %+v, want both attempts in ordinal order", attempts)
	}
}

func TestPartialUniqueIndexRejectsSecondActiveAttempt(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.CreateTask(Task{ID: "task-1", Lifecycle: TaskOpen}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAttempt(Attempt{TaskID: "task-1", Lifecycle: AttemptRunning}); err != nil {
		t.Fatal(err)
	}
	_, err := db.sql.Exec(`INSERT INTO attempt (task_id, ordinal, lifecycle) VALUES ('task-1', 99, 'provisioning')`)
	if err == nil {
		t.Fatal("raw insert accepted a second active attempt")
	}
}

func TestTaskAndAttemptTransitionsRejectIllegalStates(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.CreateTask(Task{ID: "task-1", Lifecycle: TaskOpen}); err != nil {
		t.Fatal(err)
	}
	attempt, err := db.CreateAttempt(Attempt{TaskID: "task-1", Lifecycle: AttemptProvisioning})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAttempt(attempt.ID, AttemptProvisioning, AttemptRunning); err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAttempt(attempt.ID, AttemptRunning, AttemptProvisioning); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("running -> provisioning = %v, want ErrInvalidTransition", err)
	}
	if err := db.TransitionAttempt(attempt.ID, AttemptRunning, AttemptFailed); err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAttempt(attempt.ID, AttemptFailed, AttemptRunning); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("failed -> running = %v, want ErrInvalidTransition", err)
	}
	if err := db.TransitionTask("task-1", TaskOpen, TaskTerminal); err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionTask("task-1", TaskTerminal, TaskTerminal); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal -> terminal = %v, want ErrInvalidTransition", err)
	}
	if err := db.TransitionTask("task-1", TaskTerminal, TaskOpen); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal -> open = %v, want ErrInvalidTransition", err)
	}
}

func TestAttemptTerminalStateCannotBecomeActiveAgain(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.CreateTask(Task{ID: "task-1", Lifecycle: TaskOpen}); err != nil {
		t.Fatal(err)
	}
	attempt, err := db.CreateAttempt(Attempt{TaskID: "task-1", Lifecycle: AttemptRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAttempt(attempt.ID, AttemptRunning, AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	if err := db.SetActiveAttempt("task-1", attempt.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("reactivating completed attempt = %v, want ErrInvalidTransition", err)
	}
}

func TestReopenTaskCreatesANewAttemptWithoutResurrectingHistory(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.CreateTask(Task{ID: "task-1", Lifecycle: TaskOpen}); err != nil {
		t.Fatal(err)
	}
	first, err := db.CreateAttempt(Attempt{TaskID: "task-1", Lifecycle: AttemptRunning, Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAttempt(first.ID, AttemptRunning, AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionTask("task-1", TaskOpen, TaskTerminal); err != nil {
		t.Fatal(err)
	}
	second, err := db.ReopenTask("task-1", Attempt{Lifecycle: AttemptRunning, Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Ordinal != 2 || second.Lifecycle != AttemptRunning || second.Harness != "codex" {
		t.Fatalf("reopened attempt = %+v, want ordinal 2 running codex", second)
	}
	history, found, err := db.ReadTaskHistory("task-1")
	if err != nil || !found || history.Task.Lifecycle != TaskOpen || history.ActiveAttempt == nil || history.ActiveAttempt.ID != second.ID {
		t.Fatalf("reopened history = %+v, %v, want task open with second active", history, err)
	}
	if len(history.Attempts) != 2 || history.Attempts[0].Lifecycle != AttemptCompleted {
		t.Fatalf("attempt history = %+v, want completed first attempt and running second", history.Attempts)
	}
}
