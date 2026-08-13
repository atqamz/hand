package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/filelock"
	"github.com/atqamz/hand/internal/store"
)

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	task := Task{
		ID: "fix-login", Project: "nsr", Kind: KindShip, Harness: "claude",
		Model: "sonnet", Effort: "low", Worktree: "/tmp/wt", Brief: "data/fix-login/brief.md",
		Herdr:     Herdr{Session: "default", WorkspaceID: "wA", TabID: "wA:tB", PaneID: "wA:pC"},
		CreatedAt: "2026-07-24T10:00:00Z",
	}

	if err := Write(dir, task); err != nil {
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
	if history.ActiveAttempt == nil || history.ActiveAttempt.Harness != task.Harness || history.ActiveAttempt.Model != task.Model || history.ActiveAttempt.Effort != task.Effort || history.ActiveAttempt.Worktree != task.Worktree || history.ActiveAttempt.Herdr != task.Herdr {
		t.Fatalf("attempt got %+v, want execution fields from %+v", history.ActiveAttempt, task)
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

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Task{ID: "fix-login"}); err != nil {
		t.Fatal(err)
	}
	if err := Delete(dir, "fix-login"); err != nil {
		t.Fatal(err)
	}
	if exists, err := Exists(dir, "fix-login"); err != nil || exists {
		t.Fatalf("Exists after delete = %v, %v, want false, nil", exists, err)
	}
}

// Pins the cleanup a respawn depends on: a new task under a used ID starts at
// report_offset 0, so a surviving wake log replays as this run's - re-raising resolved
// decisions and auto-recording a PR URL out of the previous run's done line.
func TestDeleteRemovesTheReportChannel(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Task{ID: "fix-login"}); err != nil {
		t.Fatal(err)
	}
	report := ReportPath(dir, "fix-login")
	if err := os.WriteFile(report, []byte("done: PR https://github.com/a/b/pull/1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Delete(dir, "fix-login"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(report); !os.IsNotExist(err) {
		t.Fatalf("stat report after delete = %v, want it gone", err)
	}
}

func TestDeleteMissingTask(t *testing.T) {
	dir := t.TempDir()
	if err := Delete(dir, "missing"); err == nil {
		t.Fatal("expected error for missing task")
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

func TestWriteOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Task{ID: "fix-login", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, Task{ID: "fix-login", Harness: "codex"}); err != nil {
		t.Fatal(err)
	}

	got, err := Read(dir, "fix-login")
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != "codex" {
		t.Fatalf("got harness %q, want codex", got.Harness)
	}
}

// The read-only fleet view is what `hand session start` renders, and a torn-down task
// belongs to history: it stays inspectable by id without crowding the fleet overview.
func TestListReadOnlyShowsOpenTasksOnly(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"open-task", "torn-down"} {
		if err := Write(dir, Task{ID: id, Harness: "claude"}); err != nil {
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

	tasks, err := ListReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "open-task" {
		t.Fatalf("ListReadOnly = %+v, want the open task only", tasks)
	}
	if tasks[0].Harness != "claude" {
		t.Fatalf("active attempt was not projected onto the row: %+v", tasks[0])
	}
	history, err := ReadHistoryReadOnly(dir, "open-task")
	if err != nil || history.ActiveAttempt == nil {
		t.Fatalf("ReadHistoryReadOnly = %+v, %v", history, err)
	}
}
