package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/atqamz/hand/internal/filelock"
	"github.com/atqamz/hand/internal/store"
)

// ErrTaskNotFound is wrapped into errors returned by the task and history readers
// and by Delete when no task row exists for the given ID, rendering as
// `task "<id>" not found`.
var ErrTaskNotFound = store.ErrTaskNotFound

// ErrTaskActive is wrapped into errors returned by Claim when the task is
// already claimed by another running command, rendering as
// `task "<id>" already active`.
var ErrTaskActive = errors.New("already active")

var ErrNoActiveAttempt = errors.New("no active attempt")

// ErrLockBusy is returned by TryLock when another process holds the lock.
var ErrLockBusy = errors.New("lock held by another process")

func Dir(homeDir string) string {
	return store.Dir(homeDir)
}

func ValidateID(id string) error {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
		return fmt.Errorf("invalid task ID %q", id)
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("invalid task ID %q", id)
	}
	return nil
}

func Claim(homeDir, id string) (func(), error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	release, err := store.Lock(homeDir, "task:"+id, true)
	if err != nil {
		if err == filelock.ErrBusy {
			return nil, fmt.Errorf("task %q %w", id, ErrTaskActive)
		}
		return nil, fmt.Errorf("lock task: %w", err)
	}
	active, err := Exists(homeDir, id)
	if err != nil {
		release()
		return nil, err
	}
	if active {
		task, readErr := Read(homeDir, id)
		release()
		if readErr == nil && task.Lifecycle == TaskTerminal {
			return nil, fmt.Errorf("task %q already exists; use hand reopen %s: %w", id, id, ErrTaskActive)
		}
		return nil, fmt.Errorf("task %q %w", id, ErrTaskActive)
	}
	return release, nil
}

func Lock(homeDir, name string) (func(), error) {
	// Lifecycle commands acquire task, then project, then worktree. Send and watcher resume acquire send,
	// then task only for the short external-send boundary; attempt writes remain ID-targeted.
	return store.Lock(homeDir, name, false)
}

// TryLock is Lock for callers that must never wait - a poll loop, or anything
// holding no claim of its own on the work the lock protects. It reports
// ErrLockBusy instead of blocking behind a holder that may be mid-network-call.
func TryLock(homeDir, name string) (func(), error) {
	release, err := store.Lock(homeDir, name, true)
	if err == filelock.ErrBusy {
		return nil, ErrLockBusy
	}
	return release, err
}

func Exists(homeDir, id string) (bool, error) {
	if err := ValidateID(id); err != nil {
		return false, err
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return false, err
	}
	defer func() { _ = db.Close() }()
	return db.TaskExists(id)
}

func Read(homeDir, id string) (Task, error) {
	if err := ValidateID(id); err != nil {
		return Task{}, err
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = db.Close() }()

	t, ok, err := db.ReadTask(id)
	if err != nil {
		return Task{}, err
	}
	if !ok {
		return Task{}, fmt.Errorf("task %q %w", id, ErrTaskNotFound)
	}
	return t, nil
}

func ReadHistory(homeDir, id string) (TaskHistory, error) {
	return readHistory(homeDir, id, false)
}

// A read-only history read avoids schema migration and legacy import.
func ReadHistoryReadOnly(homeDir, id string) (TaskHistory, error) {
	return readHistory(homeDir, id, true)
}

func readHistory(homeDir, id string, readOnly bool) (TaskHistory, error) {
	if err := ValidateID(id); err != nil {
		return TaskHistory{}, err
	}
	if readOnly {
		history, found, err := store.ReadTaskHistoryReadOnly(homeDir, id)
		if err != nil {
			return TaskHistory{}, err
		}
		if !found {
			return TaskHistory{}, fmt.Errorf("task %q %w", id, ErrTaskNotFound)
		}
		return history, nil
	}
	open := store.Open
	db, err := open(homeDir)
	if err != nil {
		return TaskHistory{}, err
	}
	defer func() { _ = db.Close() }()
	history, found, err := db.ReadTaskHistory(id)
	if err != nil {
		return TaskHistory{}, fmt.Errorf("read task history %q: %w", id, err)
	}
	if !found {
		return TaskHistory{}, fmt.Errorf("task %q %w", id, ErrTaskNotFound)
	}
	return history, nil
}

func ActiveAttempt(homeDir, id string) (Attempt, error) {
	if err := ValidateID(id); err != nil {
		return Attempt{}, err
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return Attempt{}, err
	}
	defer func() { _ = db.Close() }()
	attempt, found, err := db.ReadActiveAttempt(id)
	if err != nil {
		return Attempt{}, err
	}
	if !found {
		if _, taskFound, err := db.ReadTask(id); err != nil {
			return Attempt{}, err
		} else if !taskFound {
			return Attempt{}, fmt.Errorf("task %q %w", id, ErrTaskNotFound)
		}
		return Attempt{}, fmt.Errorf("task %q %w", id, ErrNoActiveAttempt)
	}
	return attempt, nil
}

func ReadAttempt(homeDir string, attemptID int64) (Attempt, bool, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return Attempt{}, false, err
	}
	defer func() { _ = db.Close() }()
	return db.ReadAttempt(attemptID)
}

func Write(homeDir string, t Task) error {
	if err := ValidateID(t.ID); err != nil {
		return err
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	found, err := db.TaskExists(t.ID)
	if err != nil {
		return err
	}
	if found {
		return db.UpdateTask(t)
	}
	return db.CreateTask(t)
}

func CreateTask(homeDir string, t Task) error {
	if err := ValidateID(t.ID); err != nil {
		return err
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.CreateTask(t)
}

func CreateTaskWithAttempt(homeDir string, t Task, a Attempt) (Attempt, error) {
	if err := ValidateID(t.ID); err != nil {
		return Attempt{}, err
	}
	if a.TaskID != "" {
		if err := ValidateID(a.TaskID); err != nil {
			return Attempt{}, err
		}
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return Attempt{}, err
	}
	defer func() { _ = db.Close() }()
	return db.CreateTaskWithAttempt(t, a)
}

func UpdateTask(homeDir string, t Task) error {
	if err := ValidateID(t.ID); err != nil {
		return err
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.UpdateTask(t)
}

func CreateAttempt(homeDir string, a Attempt) (Attempt, error) {
	if err := ValidateID(a.TaskID); err != nil {
		return Attempt{}, err
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return Attempt{}, err
	}
	defer func() { _ = db.Close() }()
	return db.CreateAttempt(a)
}

func UpdateAttempt(homeDir string, a Attempt) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.UpdateAttempt(a)
}

func TransitionAttempt(homeDir string, id int64, from, to AttemptLifecycle) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.TransitionAttempt(id, from, to)
}

func TransitionTask(homeDir, id string, from, to TaskLifecycle) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.TransitionTask(id, from, to)
}

func PromoteTask(homeDir, id string, scoutAttemptID int64, scoutFrom AttemptLifecycle, ship Attempt) (Attempt, error) {
	if err := ValidateID(id); err != nil {
		return Attempt{}, err
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return Attempt{}, err
	}
	defer func() { _ = db.Close() }()
	return db.PromoteTask(id, scoutAttemptID, scoutFrom, ship)
}

func TerminalizeTaskAndAttempt(homeDir, taskID string, attemptID int64, attemptFrom, attemptTo AttemptLifecycle) error {
	if err := ValidateID(taskID); err != nil {
		return err
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.TerminalizeTaskAndAttempt(taskID, attemptID, attemptFrom, attemptTo)
}

func SetTaskPR(homeDir, id, pr string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.SetTaskPR(id, pr)
}

func SetTaskKind(homeDir, id, kind string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.SetTaskKind(id, kind)
}

func SetTaskMergeAnnounced(homeDir, id string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.SetTaskMergeAnnounced(id)
}

func SetTaskDelivery(homeDir, id, deliveredAt, reason string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.SetTaskDelivery(id, deliveredAt, reason)
}

func SetTaskMerge(homeDir, id, mergedAt string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.SetTaskMerge(id, mergedAt)
}

func SetTaskReportState(homeDir, id string, offset int64, digest string, mergeAnnounced bool) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.SetTaskReportState(id, offset, digest, mergeAnnounced)
}

func SetTaskRepair(homeDir, id, code, reason string, attemptID int64, observedAt string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.SetTaskRepair(id, code, reason, attemptID, observedAt)
}

func ClearTaskRepair(homeDir, id, expectedCode string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.ClearTaskRepair(id, expectedCode)
}

func RecordAttemptWorktree(homeDir, taskID string, attemptID int64, worktree, branch, leaseID string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.RecordAttemptWorktree(taskID, attemptID, worktree, branch, leaseID)
}

func ClearAttemptWorktree(homeDir, taskID string, attemptID int64) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.ClearAttemptWorktree(taskID, attemptID)
}

func RecordAttemptHerdr(homeDir, taskID string, attemptID int64, herdr Herdr, paneStartedAt string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.RecordAttemptHerdr(taskID, attemptID, herdr, paneStartedAt)
}

func ClearAttemptHerdr(homeDir, taskID string, attemptID int64) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.ClearAttemptHerdr(taskID, attemptID)
}

func MarkLaunchSubmitted(homeDir, taskID string, attemptID int64, at string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.MarkLaunchSubmitted(taskID, attemptID, at)
}

func MarkLaunchConfirmed(homeDir, taskID string, attemptID int64, at string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.MarkLaunchConfirmed(taskID, attemptID, at)
}

func MarkAttemptRunning(homeDir, taskID string, attemptID int64) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.MarkAttemptRunning(taskID, attemptID)
}

func SetAttemptTeardownDecision(homeDir, taskID string, attemptID int64, terminal AttemptLifecycle, disposition string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.SetAttemptTeardownDecision(taskID, attemptID, terminal, disposition)
}

func SetAttemptTeardownResourceState(homeDir, taskID string, attemptID int64, expected AttemptLifecycle, resource, next string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.SetAttemptTeardownResourceState(taskID, attemptID, expected, resource, next)
}

func SetAttemptTeardownCompletionState(homeDir, taskID string, attemptID int64, expected AttemptLifecycle, next string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.SetAttemptTeardownCompletionState(taskID, attemptID, expected, next)
}

func SetAttemptSendTrace(homeDir, taskID string, attemptID int64, expected AttemptLifecycle, message, at string) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.SetAttemptSendTrace(taskID, attemptID, expected, message, at)
}

func BeginSend(homeDir, taskID string, attemptID int64, ownership Herdr, origin SendOrigin, message, createdAt string, usageLimitEpisode ...int64) (SendAttempt, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return SendAttempt{}, err
	}
	defer func() { _ = db.Close() }()
	return db.BeginSend(taskID, attemptID, ownership, origin, message, createdAt, usageLimitEpisode...)
}

func FinalizeSend(homeDir string, id int64, taskID string, attemptID int64, next SendState, reasonCode, finalizedAt string) (SendAttempt, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return SendAttempt{}, err
	}
	defer func() { _ = db.Close() }()
	return db.FinalizeSend(id, taskID, attemptID, next, reasonCode, finalizedAt)
}

func ReadSend(homeDir string, id int64) (SendAttempt, bool, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return SendAttempt{}, false, err
	}
	defer func() { _ = db.Close() }()
	return db.ReadSend(id)
}

func ReadSendMetadata(homeDir string, id int64) (SendAttempt, bool, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return SendAttempt{}, false, err
	}
	defer func() { _ = db.Close() }()
	return db.ReadSendMetadata(id)
}

func ListSends(homeDir, taskID string) ([]SendAttempt, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return db.ListSends(taskID)
}

func LatestSend(homeDir string, taskID string, attemptID int64, origins ...SendOrigin) (SendAttempt, bool, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return SendAttempt{}, false, err
	}
	defer func() { _ = db.Close() }()
	return db.LatestSend(taskID, attemptID, origins...)
}

func PendingSend(homeDir, taskID string, attemptID int64) (SendAttempt, bool, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return SendAttempt{}, false, err
	}
	defer func() { _ = db.Close() }()
	return db.PendingSend(taskID, attemptID)
}

func LatestSendMetadata(homeDir string, taskID string, attemptID int64, origins ...SendOrigin) (SendAttempt, bool, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return SendAttempt{}, false, err
	}
	defer func() { _ = db.Close() }()
	return db.LatestSendMetadata(taskID, attemptID, origins...)
}

func LatestSendMetadataReadOnly(homeDir string, taskID string, attemptID int64, origins ...SendOrigin) (SendAttempt, bool, error) {
	return store.LatestSendMetadataReadOnly(homeDir, taskID, attemptID, origins...)
}

func NormalizePendingSends(homeDir, taskID, reasonCode, finalizedAt string) (int64, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	return db.NormalizePendingSends(taskID, reasonCode, finalizedAt)
}

func NormalizePendingSend(homeDir string, id int64, taskID string, attemptID int64, reasonCode, finalizedAt string) (SendAttempt, bool, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return SendAttempt{}, false, err
	}
	defer func() { _ = db.Close() }()
	return db.NormalizePendingSend(id, taskID, attemptID, reasonCode, finalizedAt)
}

func UpdateAttemptObservation(homeDir, taskID string, attemptID int64, expected AttemptLifecycle, statusChangedAt, statusChangedFor string, doneVerified bool, lastReportState, lastReportNote, parkedFiredFor, usageLimitRetryAt string, usageLimitAttempts int, usageLimitEpisode, usageLimitStuckEpisode int64) error {
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.UpdateAttemptObservation(taskID, attemptID, expected, statusChangedAt, statusChangedFor, doneVerified, lastReportState, lastReportNote, parkedFiredFor, usageLimitRetryAt, usageLimitAttempts, usageLimitEpisode, usageLimitStuckEpisode)
}

func ReopenTask(homeDir string, a Attempt) (Attempt, error) {
	if err := ValidateID(a.TaskID); err != nil {
		return Attempt{}, err
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return Attempt{}, err
	}
	defer func() { _ = db.Close() }()
	return db.ReopenTask(a.TaskID, a)
}

func List(homeDir string) ([]Task, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return db.ListTasks()
}

func ListOpen(homeDir string) ([]Task, error) {
	histories, err := ListOpenHistories(homeDir)
	if err != nil {
		return nil, err
	}
	tasks := make([]Task, 0, len(histories))
	for _, history := range histories {
		tasks = append(tasks, history.Task)
	}
	return tasks, nil
}

func ListOpenHistories(homeDir string) ([]TaskHistory, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return db.ListOpenTaskHistories()
}

func ListReconciliationHistories(homeDir string) ([]TaskHistory, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return db.ListReconciliationHistories()
}

func ListReconciliationHistoriesReadOnly(homeDir string) ([]TaskHistory, error) {
	return store.ListReconciliationHistoriesReadOnly(homeDir)
}

func ListHerdrOwnerships(homeDir string) ([]HerdrOwnership, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return db.ListHerdrOwnerships()
}

// ListOpenHistoriesReadOnly is ListOpenHistories for a presentation reader: same open-only fleet, off
// a handle that cannot create schema or import legacy state. A torn-down task is history, not fleet.
func ListOpenHistoriesReadOnly(homeDir string) ([]TaskHistory, error) {
	db, err := store.OpenReadOnly(homeDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return db.ListOpenTaskHistories()
}

// Delete removes a task's row along with its report channel at state/<id>.status, leaving
// the durable deliverables in data/<id>/. That file is the volatile wake log: a respawn
// under a used ID starts at report_offset 0, so a surviving log replays as new lines.
func Delete(homeDir, id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	// A replayed log re-raises resolved decisions, absorbs a genuine unexplained stop, and
	// auto-records a PR URL out of an old done line onto a task nobody recorded it for.
	if err := os.Remove(ReportPath(homeDir, id)); err != nil && !os.IsNotExist(err) {
		// Failing here leaves nothing durable gone yet, so the whole command is retryable.
		// Removing the row first would strand the caller with the state gone and no way to
		// retry (see internal/runtime/teardown.go's guarded path).
		return fmt.Errorf("remove report channel %q: %w", id, err)
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.DeleteTask(id)
}
