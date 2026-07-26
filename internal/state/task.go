package state

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/atqamz/secondhand/internal/atomicfile"
)

// ErrTaskNotFound is wrapped into errors returned by Read and Delete when no
// state file exists for the given task ID, rendering as `task "<id>" not found`.
var ErrTaskNotFound = errors.New("not found")

// ErrTaskActive is wrapped into errors returned by Claim when the task is
// already claimed by another running command, rendering as
// `task "<id>" already active`.
var ErrTaskActive = errors.New("already active")

// ErrLockBusy is returned by TryLock when another process holds the lock.
var ErrLockBusy = errors.New("lock held by another process")

func Dir(homeDir string) string {
	return filepath.Join(homeDir, "state")
}

func Path(homeDir, id string) string {
	if err := ValidateID(id); err != nil {
		return ""
	}
	return filepath.Join(Dir(homeDir), id+".json")
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
	release, err := lock(homeDir, "task:"+id, true)
	if err != nil {
		if err == syscall.EWOULDBLOCK {
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
		release()
		return nil, fmt.Errorf("task %q %w", id, ErrTaskActive)
	}
	return release, nil
}

func Lock(homeDir, name string) (func(), error) {
	return lock(homeDir, name, false)
}

// TryLock is Lock for callers that must never wait - a poll loop, or anything
// holding no claim of its own on the work the lock protects. It reports
// ErrLockBusy instead of blocking behind a holder that may be mid-network-call.
func TryLock(homeDir, name string) (func(), error) {
	release, err := lock(homeDir, name, true)
	if err == syscall.EWOULDBLOCK {
		return nil, ErrLockBusy
	}
	return release, err
}

func lock(homeDir, name string, nonblock bool) (func(), error) {
	if err := os.MkdirAll(Dir(homeDir), 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	lockName := fmt.Sprintf(".%x.lock", sha256.Sum256([]byte(name)))
	file, err := os.OpenFile(filepath.Join(Dir(homeDir), lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	flags := syscall.LOCK_EX
	if nonblock {
		flags |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), flags); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

// Exists reports whether a task state file exists for id.
func Exists(homeDir, id string) (bool, error) {
	if err := ValidateID(id); err != nil {
		return false, err
	}
	_, err := os.Stat(Path(homeDir, id))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("stat task state %q: %w", id, err)
}

// Read loads the state file for id.
func Read(homeDir, id string) (Task, error) {
	if err := ValidateID(id); err != nil {
		return Task{}, err
	}
	data, err := os.ReadFile(Path(homeDir, id))
	if os.IsNotExist(err) {
		return Task{}, fmt.Errorf("task %q %w", id, ErrTaskNotFound)
	}
	if err != nil {
		return Task{}, fmt.Errorf("read task state %q: %w", id, err)
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return Task{}, fmt.Errorf("parse task state %q: %w", id, err)
	}
	if t.ID != id {
		return Task{}, fmt.Errorf("task state %q has mismatched ID %q", id, t.ID)
	}
	return t, nil
}

// Write persists t atomically (write to temp file, then rename).
func Write(homeDir string, t Task) error {
	if err := ValidateID(t.ID); err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(homeDir), 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("encode task state %q: %w", t.ID, err)
	}
	data = append(data, '\n')

	if err := atomicfile.Write(Path(homeDir, t.ID), "."+t.ID+".json-", data, 0o644); err != nil {
		return fmt.Errorf("write task state %q: %w", t.ID, err)
	}
	return nil
}

// List returns all active tasks, sorted by ID. Returns nil if the state directory doesn't exist.
func List(homeDir string) ([]Task, error) {
	entries, err := os.ReadDir(Dir(homeDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state directory: %w", err)
	}

	var tasks []Task
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		t, err := Read(homeDir, id)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks, nil
}

// Delete removes the state file for id, along with the task's report channel at
// state/<id>.status. The report file is the volatile wake log, not a
// deliverable: a task respawned under a used ID starts at report_offset 0, so a
// surviving log would replay the previous run's lines as if they were new -
// re-raising resolved decisions, absorbing a genuine unexplained stop, and
// auto-recording a PR URL out of an old done line onto a task nobody recorded it
// for. The durable deliverables (data/<id>/) survive teardown as before.
// Delete removes the report channel before the task state file, not after: the
// report removal is the one that can fail on a permissions or I/O fault, and
// doing it first means that fault leaves nothing durable gone yet, so the whole
// command is simply retryable. Removing the state file first would let a
// report-removal failure strand the caller with the state already gone and no
// way to retry (see cmd/teardown.go's guarded path).
func Delete(homeDir, id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	if err := os.Remove(ReportPath(homeDir, id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove report channel %q: %w", id, err)
	}
	if err := os.Remove(Path(homeDir, id)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("task %q %w", id, ErrTaskNotFound)
		}
		return fmt.Errorf("remove task state %q: %w", id, err)
	}
	return nil
}
