package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// An imported state/<id>.json is kept rather than deleted so an operator can
// still read it, and moved rather than left in place so state/ never holds a
// second file that looks authoritative.
func LegacyDir(homeDir string) string {
	return filepath.Join(Dir(homeDir), "migrated")
}

func legacyTaskFiles(homeDir string) ([]string, error) {
	entries, err := os.ReadDir(Dir(homeDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// Reading the JSON directly is the point: recovering an existing fleet must
// not depend on the binary that wrote it still working. A second run is a
// no-op, because it finds no JSON left to import.
func (db *DB) migrateLegacy() error {
	names, err := legacyTaskFiles(db.home)
	if err != nil || len(names) == 0 {
		return err
	}
	// Unlocked, a process that parsed a file before another one imported it and
	// deleted the row lands its own insert afterwards and resurrects the task.
	unlock, err := Lock(db.home, MigrationLock, false)
	if err != nil {
		return fmt.Errorf("lock migration: %w", err)
	}
	defer unlock()

	names, err = legacyTaskFiles(db.home)
	if err != nil || len(names) == 0 {
		return err
	}

	for _, name := range names {
		path := filepath.Join(Dir(db.home), name)
		t, err := readLegacyTask(path, strings.TrimSuffix(name, ".json"))
		if err != nil {
			return err
		}
		// An id already in the database wins: it is what hand has been writing
		// since the import, and the JSON file is a snapshot from before it.
		if _, err := db.sql.Exec(`INSERT OR IGNORE INTO task (`+taskColumns+`)
			VALUES (`+taskPlaceholders+`)`, taskValues(t)...); err != nil {
			return fmt.Errorf("import task %q: %w", t.ID, err)
		}
	}

	if err := os.MkdirAll(LegacyDir(db.home), 0o755); err != nil {
		return fmt.Errorf("create migrated directory: %w", err)
	}
	for _, name := range names {
		from := filepath.Join(Dir(db.home), name)
		// A file that went missing from under the import is already in the end
		// state this loop wants, not a fault.
		if err := os.Rename(from, filepath.Join(LegacyDir(db.home), name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("archive imported task state %s: %w", from, err)
		}
	}
	return nil
}

func readLegacyTask(path, id string) (Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Task{}, fmt.Errorf("read legacy task state %s: %w", path, err)
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return Task{}, fmt.Errorf("parse legacy task state %s (move it aside to continue): %w", path, err)
	}
	if t.ID != id {
		return Task{}, fmt.Errorf("legacy task state %s has mismatched ID %q (move it aside to continue)", path, t.ID)
	}
	// A file predating the pane-start column carries no such key, so the import lands as an
	// INSERT the backfill never sees. Same CASE as that backfill, so a task promoted before
	// either existed gets its own pane's start, not atqamz/hand#128's false one.
	if t.PaneStartedAt == "" {
		t.PaneStartedAt = t.StatusChangedAt
		if t.PaneStartedAt == "" {
			t.PaneStartedAt = t.CreatedAt
		}
	}
	return t, nil
}

// For a caller that owns a legacy file's format and runs its own one-time
// import: a source that stays on disk afterwards cannot use its own absence as
// the done marker.
func (db *DB) Migrated(key string) (bool, error) {
	value, err := db.meta("migrated:" + key)
	return value != "", err
}

func (db *DB) MarkMigrated(key string) error {
	return db.setMeta("migrated:"+key, "done")
}
