package state

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func Dir(homeDir string) string {
	return filepath.Join(homeDir, "state")
}

func Path(homeDir, id string) string {
	return filepath.Join(Dir(homeDir), id+".json")
}

// Exists reports whether a task state file exists for id.
func Exists(homeDir, id string) (bool, error) {
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
	return t, nil
}

// Write persists t atomically (write to temp file, then rename).
func Write(homeDir string, t Task) error {
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
	if err := os.Remove(Path(homeDir, id)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("task %q not found", id)
		}
		return fmt.Errorf("remove task state %q: %w", id, err)
	}
	return nil
}
