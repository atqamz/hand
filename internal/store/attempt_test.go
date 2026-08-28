package store

import (
	"errors"
	"sync"
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
		ExecutionClass: "standard", PlannedAgainst: "2026-08-14T00:00:00Z", RequestedProfile: "daily", RoutingSource: "route",
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
	if gotAttempt.TaskID != task.ID || gotAttempt.Harness != "claude" || gotAttempt.ExecutionClass != "standard" || gotAttempt.PlannedAgainst != "2026-08-14T00:00:00Z" || gotAttempt.RequestedProfile != "daily" || gotAttempt.RoutingSource != "route" || gotAttempt.Worktree != "/tmp/wt-1" {
		t.Fatalf("attempt lost execution identity: %+v", gotAttempt)
	}
	attempts, err := db.ListAttempts(task.ID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("ListAttempts = %+v, %v", attempts, err)
	}
	if attempts[0].ExecutionClass != "standard" || attempts[0].PlannedAgainst != "2026-08-14T00:00:00Z" || attempts[0].RequestedProfile != "daily" || attempts[0].RoutingSource != "route" {
		t.Fatalf("listed attempt lost routing snapshot: %+v", attempts[0])
	}
	history, found, err := db.ReadTaskHistory(task.ID)
	if err != nil || !found || len(history.Attempts) != 1 {
		t.Fatalf("ReadTaskHistory = %+v, %v, %v", history, found, err)
	}
	if history.Attempts[0].ExecutionClass != "standard" || history.Attempts[0].PlannedAgainst != "2026-08-14T00:00:00Z" || history.Attempts[0].RequestedProfile != "daily" || history.Attempts[0].RoutingSource != "route" {
		t.Fatalf("history attempt lost routing snapshot: %+v", history.Attempts[0])
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
	if _, err := db.sql.Exec(`UPDATE attempt SET launch_submitted_at = 'submitted', launch_confirmed_at = 'confirmed' WHERE id = ?`, attempt.ID); err != nil {
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
	second, err := db.ReopenTask("task-1", Attempt{Lifecycle: AttemptProvisioning, Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Ordinal != 2 || second.Lifecycle != AttemptProvisioning || second.Harness != "codex" {
		t.Fatalf("reopened attempt = %+v, want ordinal 2 provisioning codex", second)
	}
	history, found, err := db.ReadTaskHistory("task-1")
	if err != nil || !found || history.Task.Lifecycle != TaskOpen || history.ActiveAttempt == nil || history.ActiveAttempt.ID != second.ID {
		t.Fatalf("reopened history = %+v, %v, want task open with second active", history, err)
	}
	if len(history.Attempts) != 2 || history.Attempts[0].Lifecycle != AttemptCompleted {
		t.Fatalf("attempt history = %+v, want completed first attempt and provisioning second", history.Attempts)
	}
}

func TestProvisioningEvidenceIsIncrementalAndRequiredBeforeRunning(t *testing.T) {
	db, _ := openTemp(t)
	created, err := db.CreateTaskWithAttempt(Task{ID: "task-1", Lifecycle: TaskOpen}, Attempt{TaskID: "task-1", Lifecycle: AttemptProvisioning})
	if err != nil {
		t.Fatal(err)
	}
	read := func() Attempt {
		attempt, found, err := db.ReadAttempt(created.ID)
		if err != nil || !found {
			t.Fatalf("ReadAttempt = %+v, %v, %v", attempt, found, err)
		}
		return attempt
	}
	if got := read(); got.Worktree != "" || got.Herdr.PaneID != "" || got.LaunchSubmittedAt != "" || got.LaunchConfirmedAt != "" {
		t.Fatalf("initial provisioning evidence = %+v, want empty boundaries", got)
	}
	if err := db.RecordAttemptWorktree("task-1", created.ID, "/tmp/wt", "", "lease-1"); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordAttemptHerdr("task-1", created.ID, Herdr{PaneID: "pane-1"}, "2026-08-14T00:00:01Z"); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkLaunchSubmitted("task-1", created.ID, "2026-08-14T00:00:02Z"); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkAttemptRunning("task-1", created.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("running before confirmation = %v, want invalid transition", err)
	}
	if err := db.MarkLaunchConfirmed("task-1", created.ID, "2026-08-14T00:00:03Z"); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkAttemptRunning("task-1", created.ID); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got.Lifecycle != AttemptRunning || got.Worktree != "/tmp/wt" || got.LeaseID != "lease-1" || got.Herdr.PaneID != "pane-1" || got.LaunchSubmittedAt == "" || got.LaunchConfirmedAt == "" {
		t.Fatalf("final provisioning evidence = %+v, want every boundary and running", got)
	}
}

func TestReleasedProvisioningResourcesDoNotLeaveStaleOwnershipEvidence(t *testing.T) {
	db, _ := openTemp(t)
	created, err := db.CreateTaskWithAttempt(Task{ID: "task-1", Lifecycle: TaskOpen}, Attempt{TaskID: "task-1", Lifecycle: AttemptProvisioning})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RecordAttemptWorktree("task-1", created.ID, "/tmp/wt", "", "lease-1"); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordAttemptHerdr("task-1", created.ID, Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, "2026-08-14T00:00:01Z"); err != nil {
		t.Fatal(err)
	}
	if err := db.ClearAttemptWorktree("task-1", created.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.ClearAttemptHerdr("task-1", created.ID); err != nil {
		t.Fatal(err)
	}
	got, found, err := db.ReadAttempt(created.ID)
	if err != nil || !found {
		t.Fatalf("ReadAttempt = %+v, %v, %v", got, found, err)
	}
	if got.Worktree != "" || got.LeaseID != "" || got.Herdr != (Herdr{}) || got.PaneStartedAt != "" {
		t.Fatalf("released provisioning evidence = %+v, want no current resource ownership", got)
	}
}

func TestTerminalTaskRejectsOrdinaryAttemptCreation(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.CreateTask(Task{ID: "task-1", Lifecycle: TaskTerminal}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAttemptIfOpenAndInactive(Attempt{TaskID: "task-1", Lifecycle: AttemptProvisioning}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("attempt on terminal task = %v, want invalid transition", err)
	}
}

func TestStaleTaskTransitionLosesWithoutChangingNewerLifecycle(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.CreateTask(Task{ID: "task-1", Lifecycle: TaskOpen}); err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionTask("task-1", TaskOpen, TaskTerminal); err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionTask("task-1", TaskOpen, TaskTerminal); !errors.Is(err, ErrLifecycleConflict) {
		t.Fatalf("stale task transition = %v, want lifecycle conflict", err)
	}
	got, found, err := db.ReadTask("task-1")
	if err != nil || !found || got.Lifecycle != TaskTerminal {
		t.Fatalf("task after stale transition = %+v, %v, %v", got, found, err)
	}
}

func TestConcurrentReopenHasOneWinnerAndOneFreshProvisioningAttempt(t *testing.T) {
	db, home := openTemp(t)
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
	if err := db.TransitionTask("task-1", TaskOpen, TaskTerminal); err != nil {
		t.Fatal(err)
	}
	other, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, writer := range []*DB{db, other} {
		wg.Add(1)
		go func(writer *DB) {
			defer wg.Done()
			<-start
			_, err := writer.ReopenTask("task-1", Attempt{Lifecycle: AttemptProvisioning, Harness: "codex"})
			results <- err
		}(writer)
	}
	close(start)
	wg.Wait()
	close(results)

	winners := 0
	for err := range results {
		if err == nil {
			winners++
			continue
		}
		if !errors.Is(err, ErrLifecycleConflict) {
			t.Fatalf("losing reopen error = %v, want lifecycle conflict", err)
		}
	}
	if winners != 1 {
		t.Fatalf("reopen winners = %d, want 1", winners)
	}
	history, found, err := db.ReadTaskHistory("task-1")
	if err != nil || !found || history.Task.Lifecycle != TaskOpen || history.ActiveAttempt == nil {
		t.Fatalf("reopened history = %+v, %v, %v", history, found, err)
	}
	if len(history.Attempts) != 2 || history.ActiveAttempt.Ordinal != 2 || history.ActiveAttempt.Lifecycle != AttemptProvisioning {
		t.Fatalf("reopened attempts = %+v, want one historical and one active provisioning", history.Attempts)
	}
}

func TestPromotePreservesScoutAndAtomicallyOwnsShipProvisioningAttempt(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.CreateTask(Task{ID: "task-1", Kind: KindScout, Lifecycle: TaskOpen, DeliveredAt: "old", DeliveredReason: "old"}); err != nil {
		t.Fatal(err)
	}
	scout, err := db.CreateAttempt(Attempt{TaskID: "task-1", Lifecycle: AttemptRunning, Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	ship, err := db.PromoteTask("task-1", scout.ID, AttemptRunning, Attempt{Lifecycle: AttemptProvisioning, Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	history, found, err := db.ReadTaskHistory("task-1")
	if err != nil || !found || history.Task.Kind != KindShip || history.Task.ActiveAttemptID != ship.ID || history.ActiveAttempt == nil {
		t.Fatalf("promoted history = %+v, %v, %v", history, found, err)
	}
	if history.ActiveAttempt.ID != ship.ID || history.ActiveAttempt.Lifecycle != AttemptProvisioning || history.ActiveAttempt.Harness != "codex" {
		t.Fatalf("ship attempt = %+v, want fresh provisioning attempt", history.ActiveAttempt)
	}
	if history.Attempts[0].ID != scout.ID || history.Attempts[0].Lifecycle != AttemptCompleted {
		t.Fatalf("scout history = %+v, want completed scout preserved", history.Attempts)
	}
	if history.Task.DeliveredAt != "" || history.Task.DeliveredReason != "" {
		t.Fatalf("delivery facts = %q/%q, want cleared for ship", history.Task.DeliveredAt, history.Task.DeliveredReason)
	}
}

func TestConcurrentActiveAttemptCreationHasOneWinnerAndOneActiveRelation(t *testing.T) {
	db, home := openTemp(t)
	if err := db.CreateTask(Task{ID: "task-1", Lifecycle: TaskOpen}); err != nil {
		t.Fatal(err)
	}
	other, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()

	start := make(chan struct{})
	results := make(chan struct {
		attempt Attempt
		err     error
	}, 2)
	var wg sync.WaitGroup
	for _, writer := range []*DB{db, other} {
		wg.Add(1)
		go func(writer *DB) {
			defer wg.Done()
			<-start
			attempt, err := writer.CreateAttemptIfOpenAndInactive(Attempt{TaskID: "task-1", Lifecycle: AttemptProvisioning})
			results <- struct {
				attempt Attempt
				err     error
			}{attempt: attempt, err: err}
		}(writer)
	}
	close(start)
	wg.Wait()
	close(results)

	winners := 0
	for result := range results {
		if result.err == nil {
			winners++
			continue
		}
		if !errors.Is(result.err, ErrLifecycleConflict) {
			t.Fatalf("losing creation error = %v, want lifecycle conflict", result.err)
		}
	}
	if winners != 1 {
		t.Fatalf("creation winners = %d, want 1", winners)
	}
	history, found, err := db.ReadTaskHistory("task-1")
	if err != nil || !found || history.ActiveAttempt == nil {
		t.Fatalf("final history = %+v, %v, %v", history, found, err)
	}
	if len(history.Attempts) != 1 || !isActiveAttempt(history.Attempts[0].Lifecycle) || history.Task.ActiveAttemptID != history.ActiveAttempt.ID {
		t.Fatalf("final attempt ownership = %+v, task=%+v", history.Attempts, history.Task)
	}
}

func TestStaleAttemptTransitionsLoseWithoutChangingNewerLifecycle(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.CreateTask(Task{ID: "task-1", Lifecycle: TaskOpen}); err != nil {
		t.Fatal(err)
	}
	attempt, err := db.CreateAttempt(Attempt{TaskID: "task-1", Lifecycle: AttemptProvisioning})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`UPDATE attempt SET launch_submitted_at = 'submitted', launch_confirmed_at = 'confirmed' WHERE id = ?`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAttempt(attempt.ID, AttemptProvisioning, AttemptRunning); err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAttempt(attempt.ID, AttemptProvisioning, AttemptFailed); !errors.Is(err, ErrLifecycleConflict) {
		t.Fatalf("stale provisioning transition = %v, want lifecycle conflict", err)
	}
	if err := db.TransitionAttempt(attempt.ID, AttemptRunning, AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAttempt(attempt.ID, AttemptRunning, AttemptInterrupted); !errors.Is(err, ErrLifecycleConflict) {
		t.Fatalf("stale running transition = %v, want lifecycle conflict", err)
	}
	got, found, err := db.ReadAttempt(attempt.ID)
	if err != nil || !found || got.Lifecycle != AttemptCompleted {
		t.Fatalf("attempt after stale transitions = %+v, %v, %v", got, found, err)
	}
}

func TestAttemptBookkeepingRequiresCurrentActiveOwnership(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.CreateTask(Task{ID: "task-1", Lifecycle: TaskOpen}); err != nil {
		t.Fatal(err)
	}
	first, err := db.CreateAttempt(Attempt{TaskID: "task-1", Lifecycle: AttemptRunning, Worktree: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAttempt(first.ID, AttemptRunning, AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateAttempt(Attempt{TaskID: "task-1", Lifecycle: AttemptRunning, Worktree: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RecordAttemptWorktree("task-1", first.ID, "stale", "", "stale-lease"); !errors.Is(err, ErrOwnershipConflict) {
		t.Fatalf("historical bookkeeping = %v, want ownership conflict", err)
	}
	got, found, err := db.ReadAttempt(second.ID)
	if err != nil || !found || got.Worktree != "new" {
		t.Fatalf("replacement attempt = %+v, %v, %v", got, found, err)
	}
	got, found, err = db.ReadAttempt(first.ID)
	if err != nil || !found || got.Worktree != "old" {
		t.Fatalf("historical attempt = %+v, %v, %v", got, found, err)
	}
}

func TestTerminalizeTaskAndAttemptClearsOwnershipTogether(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.CreateTask(Task{ID: "task-1", Lifecycle: TaskOpen}); err != nil {
		t.Fatal(err)
	}
	attempt, err := db.CreateAttempt(Attempt{TaskID: "task-1", Lifecycle: AttemptRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TerminalizeTaskAndAttempt("task-1", attempt.ID, AttemptRunning, AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	history, found, err := db.ReadTaskHistory("task-1")
	if err != nil || !found {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != TaskTerminal || history.Task.ActiveAttemptID != 0 || history.Attempts[0].Lifecycle != AttemptCompleted {
		t.Fatalf("terminalized history = %+v, want coherent closure", history)
	}
}

func TestTerminalizeTaskAndAttemptRollsBackOnStaleTask(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.CreateTask(Task{ID: "task-1", Lifecycle: TaskOpen}); err != nil {
		t.Fatal(err)
	}
	attempt, err := db.CreateAttempt(Attempt{TaskID: "task-1", Lifecycle: AttemptRunning})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`UPDATE task SET lifecycle = 'terminal' WHERE id = 'task-1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.TerminalizeTaskAndAttempt("task-1", attempt.ID, AttemptRunning, AttemptCompleted); !errors.Is(err, ErrLifecycleConflict) {
		t.Fatalf("stale terminalization = %v, want lifecycle conflict", err)
	}
	gotAttempt, found, err := db.ReadAttempt(attempt.ID)
	if err != nil || !found || gotAttempt.Lifecycle != AttemptRunning {
		t.Fatalf("attempt after rollback = %+v, %v, %v", gotAttempt, found, err)
	}
	gotTask, found, err := db.ReadTask("task-1")
	if err != nil || !found || gotTask.ActiveAttemptID != attempt.ID {
		t.Fatalf("task after rollback = %+v, %v, %v", gotTask, found, err)
	}
}
