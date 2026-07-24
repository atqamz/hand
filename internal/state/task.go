package state

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

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
	if err := os.MkdirAll(Dir(homeDir), 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(Dir(homeDir), "."+id+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open task lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, fmt.Errorf("task %q already active", id)
		}
		return nil, fmt.Errorf("lock task: %w", err)
	}
	active, err := Exists(homeDir, id)
	if err != nil {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
		return nil, err
	}
	if active {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
		return nil, fmt.Errorf("task %q already active", id)
	}
	return func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
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
		return Task{}, fmt.Errorf("task %q not found", id)
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

	tmp, err := os.CreateTemp(Dir(homeDir), "."+t.ID+".json-")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := func() { _ = os.Remove(tmpName) }

	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		removeTemp()
		return fmt.Errorf("chmod temp state file: %w", err)
	}
	n, err := tmp.Write(data)
	if err != nil {
		_ = tmp.Close()
		removeTemp()
		return fmt.Errorf("write temp state file: %w", err)
	}
	if n != len(data) {
		_ = tmp.Close()
		removeTemp()
		return io.ErrShortWrite
	}
	if err := tmp.Close(); err != nil {
		removeTemp()
		return fmt.Errorf("close temp state file: %w", err)
	}
	if err := os.Rename(tmpName, Path(homeDir, t.ID)); err != nil {
		removeTemp()
		return fmt.Errorf("rename temp state file: %w", err)
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

// Delete removes the state file for id.
func Delete(homeDir, id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	if err := os.Remove(Path(homeDir, id)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("task %q not found", id)
		}
		return fmt.Errorf("remove task state %q: %w", id, err)
	}
	return nil
}
