package state

import (
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/filelock"
	"github.com/atqamz/hand/internal/store"
)

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	task := Task{
		ID: "fix-login", Project: "nsr", Kind: KindShip, Brief: "data/fix-login/brief.md",
		CreatedAt: "2026-07-24T10:00:00Z",
	}
	attempt := Attempt{
		TaskID: task.ID, Lifecycle: AttemptRunning, Harness: "claude", Model: "sonnet", Effort: "low", Worktree: "/tmp/wt",
		Herdr: Herdr{Session: "default", WorkspaceID: "wA", TabID: "wA:tB", PaneID: "wA:pC"},
	}

	if err := CreateTask(dir, task); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateAttempt(dir, attempt); err != nil {
		t.Fatal(err)
	}

	history, err := ReadHistory(dir, "fix-login")
	if err != nil {
		t.Fatal(err)
	}
	got := history.Task
	if got.ID != task.ID || got.Project != task.Project || got.Kind != task.Kind || got.Brief != task.Brief || got.CreatedAt != task.CreatedAt {
		t.Fatalf("task got %+v, want %+v", got, task)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Harness != attempt.Harness || history.ActiveAttempt.Model != attempt.Model || history.ActiveAttempt.Effort != attempt.Effort || history.ActiveAttempt.Worktree != attempt.Worktree || history.ActiveAttempt.Herdr != attempt.Herdr {
		t.Fatalf("attempt got %+v, want %+v", history.ActiveAttempt, attempt)
	}
}

func TestRepairMarkerRequiresMatchingEvidenceToClear(t *testing.T) {
	home := t.TempDir()
	if _, err := CreateTaskWithAttempt(home, Task{ID: "task-1", Project: "demo", Kind: KindShip, Brief: "data/task-1/brief.md"}, Attempt{
		TaskID: "task-1", Lifecycle: AttemptProvisioning, Harness: "claude",
	}); err != nil {
		t.Fatal(err)
	}
	if err := SetTaskRepair(home, "task-1", "running-pane-missing", "persisted running Attempt has no matching Herdr pane", 1, "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := ClearTaskRepair(home, "task-1", "different-code"); err == nil {
		t.Fatal("ClearTaskRepair with stale code succeeded")
	}
	history, err := ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != "running-pane-missing" || history.Task.RepairAttemptID != 1 {
		t.Fatalf("stale clear changed repair marker: %+v", history.Task)
	}
	if err := ClearTaskRepair(home, "task-1", "running-pane-missing"); err != nil {
		t.Fatal(err)
	}
	history, err = ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != "" || history.Task.RepairReason != "" || history.Task.RepairAttemptID != 0 || history.Task.RepairObservedAt != "" {
		t.Fatalf("matching clear left repair marker: %+v", history.Task)
	}
}

func TestListReconciliationHistoriesIsSortedAndIncludesRepairTasks(t *testing.T) {
	home := t.TempDir()
	for _, id := range []string{"zebra", "apple"} {
		if _, err := CreateTaskWithAttempt(home, Task{ID: id, Project: "demo", Kind: KindShip, Brief: "data/" + id + "/brief.md"}, Attempt{
			TaskID: id, Lifecycle: AttemptProvisioning, Harness: "claude",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetTaskRepair(home, "zebra", "worktree-dirty", "worktree is dirty", 2, "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	histories, err := ListReconciliationHistories(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(histories) != 2 || histories[0].Task.ID != "apple" || histories[1].Task.ID != "zebra" {
		t.Fatalf("candidate order = %+v", histories)
	}
}

func TestListHerdrOwnershipsIncludesLifecycleAndTeardownMetadata(t *testing.T) {
	home := t.TempDir()
	attempt, err := CreateTaskWithAttempt(home, Task{ID: "task-1", Project: "demo", Kind: KindShip, Brief: "data/task-1/brief.md"}, Attempt{
		TaskID: "task-1", Lifecycle: AttemptProvisioning, Harness: "claude", Herdr: Herdr{Session: "default", WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, AttemptProvisioning, AttemptInterrupted); err != nil {
		t.Fatal(err)
	}
	if err := SetAttemptTeardownResourceState(home, "task-1", attempt.ID, AttemptInterrupted, "herdr", TeardownResourceReleasing); err != nil {
		t.Fatal(err)
	}
	if err := SetAttemptTeardownResourceState(home, "task-1", attempt.ID, AttemptInterrupted, "herdr", TeardownResourceReleased); err != nil {
		t.Fatal(err)
	}
	ownerships, err := ListHerdrOwnerships(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerships) != 1 {
		t.Fatalf("ownerships = %+v, want one row", ownerships)
	}
	got := ownerships[0]
	if got.AttemptID != attempt.ID || got.TaskID != "task-1" || got.Lifecycle != AttemptInterrupted || got.TeardownHerdrState != TeardownResourceReleased || got.WorkspaceID != "ws-1" || got.TabID != "tab-1" || got.PaneID != "pane-1" {
		t.Fatalf("ownership = %+v, want durable lifecycle and teardown metadata", got)
	}
}

// A latch has to be leavable in both directions: forward to a release once ownership is proven, and
// sideways to abandonment when an operator attests, for either resource (atqamz/hand#254).
func TestSetAttemptTeardownResourceStateLeavesAmbiguousBothWays(t *testing.T) {
	for _, test := range []struct {
		name     string
		resource string
		next     string
		wantErr  error
	}{
		{name: "worktree resumes releasing", resource: "worktree", next: TeardownResourceReleasing},
		{name: "worktree is abandonable", resource: "worktree", next: TeardownResourceAbandoned},
		{name: "worktree cannot skip to released", resource: "worktree", next: TeardownResourceReleased, wantErr: store.ErrLifecycleConflict},
		{name: "herdr resumes releasing", resource: "herdr", next: TeardownResourceReleasing},
		{name: "herdr is abandonable", resource: "herdr", next: TeardownResourceAbandoned},
		{name: "herdr cannot skip to released", resource: "herdr", next: TeardownResourceReleased, wantErr: store.ErrLifecycleConflict},
		{name: "neither resource takes an unknown state", resource: "herdr", next: "relinquished", wantErr: store.ErrInvalidTransition},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			attempt, err := CreateTaskWithAttempt(home, Task{ID: "task-1", Project: "demo", Kind: KindShip, Brief: "data/task-1/brief.md"}, Attempt{
				TaskID: "task-1", Lifecycle: AttemptProvisioning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1",
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := SetAttemptTeardownResourceState(home, "task-1", attempt.ID, AttemptProvisioning, test.resource, TeardownResourceAmbiguous); err != nil {
				t.Fatal(err)
			}
			err = SetAttemptTeardownResourceState(home, "task-1", attempt.ID, AttemptProvisioning, test.resource, test.next)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("SetAttemptTeardownResourceState() = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("SetAttemptTeardownResourceState() = %v, want the latch left behind", err)
			}
		})
	}
}

func TestReadHistoryReadOnlySupportsSchemaV10(t *testing.T) {
	home := t.TempDir()
	if _, err := CreateTaskWithAttempt(home, Task{ID: "task-1", Project: "demo", Kind: KindShip, Brief: "data/task-1/brief.md"}, Attempt{
		TaskID: "task-1", Lifecycle: AttemptProvisioning, Harness: "claude", Model: "old-model", Effort: "high",
	}); err != nil {
		t.Fatal(err)
	}
	history, err := ReadHistory(home, "task-1")
	if err != nil || history.ActiveAttempt == nil {
		t.Fatalf("initial history = %+v, %v", history, err)
	}
	if err := MarkLaunchSubmitted(home, "task-1", history.ActiveAttempt.ID, "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := MarkLaunchConfirmed(home, "task-1", history.ActiveAttempt.ID, "2026-08-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := MarkAttemptRunning(home, "task-1", history.ActiveAttempt.ID); err != nil {
		t.Fatal(err)
	}
	if err := TransitionAttempt(home, history.ActiveAttempt.ID, AttemptRunning, AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	if err := TransitionTask(home, "task-1", TaskOpen, TaskTerminal); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", store.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"execution_class", "planned_against", "requested_profile", "routing_source"} {
		if _, err := db.Exec("ALTER TABLE attempt DROP COLUMN " + column); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version = 10"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := ReadHistoryReadOnly(home, "task-1")
	if err != nil {
		t.Fatalf("ReadHistoryReadOnly() = %v, want schema v10 compatibility", err)
	}
	if got.Task.Lifecycle != TaskTerminal || len(got.Attempts) != 1 || got.Attempts[0].Harness != "claude" || got.Attempts[0].Model != "old-model" || got.Attempts[0].Effort != "high" {
		t.Fatalf("history = %+v, want the v10 task and attempt identity", got)
	}
}

func TestReadMissingTask(t *testing.T) {
	dir := t.TempDir()
	if _, err := Read(dir, "missing"); err == nil {
		t.Fatal("expected error for missing task")
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	if exists, err := Exists(dir, "fix-login"); err != nil || exists {
		t.Fatalf("Exists = %v, %v, want false, nil", exists, err)
	}

	if err := Write(dir, Task{ID: "fix-login"}); err != nil {
		t.Fatal(err)
	}
	if exists, err := Exists(dir, "fix-login"); err != nil || !exists {
		t.Fatalf("Exists = %v, %v, want true, nil", exists, err)
	}
}

func TestListSortedAndEmpty(t *testing.T) {
	dir := t.TempDir()
	tasks, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if tasks != nil {
		t.Fatalf("got %+v, want nil for missing state dir", tasks)
	}

	for _, id := range []string{"zebra", "apple", "mango"} {
		if err := Write(dir, Task{ID: id}); err != nil {
			t.Fatal(err)
		}
	}

	tasks, err = List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Fatalf("got %d tasks, want 3", len(tasks))
	}
	want := []string{"apple", "mango", "zebra"}
	for i, id := range want {
		if tasks[i].ID != id {
			t.Errorf("tasks[%d].ID = %q, want %q", i, tasks[i].ID, id)
		}
	}
}

func TestListIgnoresNonJSONFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(dir), "events.log"), []byte("noise"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, Task{ID: "fix-login"}); err != nil {
		t.Fatal(err)
	}

	tasks, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "fix-login" {
		t.Fatalf("got %+v", tasks)
	}
}

func TestListFailsClosedOnMalformedState(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(dir), "broken.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := List(dir); err == nil {
		t.Fatal("expected malformed state file to fail closed")
	}
}

func TestRejectsUnsafeIDs(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"../escape", "nested/task", "", "."} {
		if err := Write(dir, Task{ID: id}); err == nil {
			t.Errorf("Write accepted unsafe ID %q", id)
		}
		if _, err := Read(dir, id); err == nil {
			t.Errorf("Read accepted unsafe ID %q", id)
		}
	}
}

func TestClaimIsExclusive(t *testing.T) {
	dir := t.TempDir()
	release, err := Claim(dir, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := Claim(dir, "task-1"); err == nil {
		t.Fatal("second claim succeeded")
	}
}

func TestLockIsExclusive(t *testing.T) {
	dir := t.TempDir()
	release, err := Lock(dir, "project:myproj")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lock(dir, "project:myproj", true); err != filelock.ErrBusy {
		t.Fatalf("second lock error = %v, want filelock.ErrBusy", err)
	}
	release()
	second, err := Lock(dir, "project:myproj")
	if err != nil {
		t.Fatal(err)
	}
	second()
}

func TestTryLockReportsBusyInsteadOfWaiting(t *testing.T) {
	dir := t.TempDir()
	release, err := Lock(dir, "task:task-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(dir, "task:task-1"); !errors.Is(err, ErrLockBusy) {
		t.Fatalf("TryLock error = %v, want ErrLockBusy", err)
	}
	release()

	second, err := TryLock(dir, "task:task-1")
	if err != nil {
		t.Fatal(err)
	}
	second()
}

const (
	taskLockHelperEnv = "HAND_STATE_LOCK_TEST_HELPER"
	taskLockHomeEnv   = "HAND_STATE_LOCK_TEST_HOME"
	taskLockKeyEnv    = "HAND_STATE_LOCK_TEST_KEY"
)

// Extends TestLockIsExclusive and TestTryLockReportsBusyInsteadOfWaiting from
// same-process to cross-process: the refusal both wrappers give is API for
// state.ErrTaskActive and steering.ErrOwnershipConflict.
func TestNonblockingRefusalIsPreservedCrossProcess(t *testing.T) {
	if os.Getenv(taskLockHelperEnv) == "hold-forever" {
		runTaskLockHoldForeverHelper(t)
		return
	}

	home := t.TempDir()
	const key = "task:cross-process-busy"
	child := exec.Command(os.Args[0], "-test.run=^TestNonblockingRefusalIsPreservedCrossProcess$")
	child.Env = append(os.Environ(), taskLockHelperEnv+"=hold-forever", taskLockHomeEnv+"="+home, taskLockKeyEnv+"="+key)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})

	if !waitForHolderReady(t, stdout) {
		t.Fatal("holder never reported acquiring the lock")
	}

	if _, err := store.Lock(home, key, true); err != filelock.ErrBusy {
		t.Fatalf("store.Lock got %v, want filelock.ErrBusy", err)
	}
	if _, err := TryLock(home, key); !errors.Is(err, ErrLockBusy) {
		t.Fatalf("TryLock got %v, want ErrLockBusy", err)
	}

	if err := child.Process.Kill(); err != nil {
		t.Fatalf("kill holder: %v", err)
	}
	_ = child.Wait()
}

func runTaskLockHoldForeverHelper(t *testing.T) {
	_, err := store.Lock(os.Getenv(taskLockHomeEnv), os.Getenv(taskLockKeyEnv), false)
	if err != nil {
		t.Fatalf("holder Lock: %v", err)
	}
	fmt.Println("acquired")
	for {
		time.Sleep(time.Hour)
	}
}

func waitForHolderReady(t *testing.T, r io.Reader) bool {
	t.Helper()
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		if scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	select {
	case line := <-lines:
		return line == "acquired"
	case <-time.After(10 * time.Second):
		return false
	}
}

func TestAttemptSendBookkeepingDoesNotNestTaskLock(t *testing.T) {
	dir := t.TempDir()
	attempt, err := CreateTaskWithAttempt(dir, Task{ID: "task-1", Lifecycle: TaskOpen}, Attempt{TaskID: "task-1", Lifecycle: AttemptProvisioning})
	if err != nil {
		t.Fatal(err)
	}
	release, err := Lock(dir, "task:task-1")
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- SetAttemptSendTrace(dir, "task-1", attempt.ID, AttemptProvisioning, "message", "2026-08-14T00:00:00Z")
	}()
	select {
	case err := <-result:
		release()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		release()
		t.Fatal("attempt bookkeeping waited on the task lock")
	}
}

func TestWriteOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Task{ID: "fix-login", PR: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, Task{ID: "fix-login", PR: "new"}); err != nil {
		t.Fatal(err)
	}

	got, err := Read(dir, "fix-login")
	if err != nil {
		t.Fatal(err)
	}
	if got.PR != "new" {
		t.Fatalf("got PR %q, want new", got.PR)
	}
}

// The read-only fleet view is what `hand session start` renders, and a torn-down task
// belongs to history: it stays inspectable by id without crowding the fleet overview.
func TestListOpenHistoriesReadOnlyShowsOpenTasksOnly(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"open-task", "torn-down"} {
		if err := CreateTask(dir, Task{ID: id}); err != nil {
			t.Fatal(err)
		}
		if _, err := CreateAttempt(dir, Attempt{TaskID: id, Lifecycle: AttemptRunning, Harness: "claude"}); err != nil {
			t.Fatal(err)
		}
	}
	done, err := Read(dir, "torn-down")
	if err != nil {
		t.Fatal(err)
	}
	if err := TransitionAttempt(dir, done.ActiveAttemptID, AttemptRunning, AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	if err := TransitionTask(dir, "torn-down", TaskOpen, TaskTerminal); err != nil {
		t.Fatal(err)
	}

	histories, err := ListOpenHistoriesReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(histories) != 1 || histories[0].Task.ID != "open-task" {
		t.Fatalf("ListOpenHistoriesReadOnly = %+v, want the open task only", histories)
	}
	if histories[0].ActiveAttempt == nil || histories[0].ActiveAttempt.Harness != "claude" {
		t.Fatalf("active attempt was not carried with the open task: %+v", histories[0])
	}
}
