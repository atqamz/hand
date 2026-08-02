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

// Reading the JSON directly is the point: recovering an existing fleet must
// not depend on the binary that wrote it still working. A second run is a
// no-op, because it finds no JSON left to import.
func (db *DB) migrateLegacy() error {
	entries, err := os.ReadDir(Dir(db.home))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state directory: %w", err)
	}

	var imported []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(Dir(db.home), name)
		t, err := readLegacyTask(path, strings.TrimSuffix(name, ".json"))
		if err != nil {
			return err
		}
		// An id already in the database wins: it is what hand has been writing
		// since the import, and the JSON file is a snapshot from before it.
		if _, err := db.sql.Exec(`INSERT OR IGNORE INTO task (`+taskColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.Project, t.Kind, t.Harness, t.Model, t.Effort, t.Worktree, t.Brief,
			t.Herdr.Session, t.Herdr.WorkspaceID, t.Herdr.TabID, t.Herdr.PaneID, t.PR,
			t.MergeExecuted, t.MergeExecutedAt, t.ReportOffset, t.MergeAnnounced, t.DoneVerified,
			t.CreatedAt, t.StatusChangedAt, t.StatusChangedFor, t.LastReportState, t.LastReportNote); err != nil {
			return fmt.Errorf("import task %q: %w", t.ID, err)
		}
		imported = append(imported, name)
	}
	if len(imported) == 0 {
		return nil
	}

	if err := os.MkdirAll(LegacyDir(db.home), 0o755); err != nil {
		return fmt.Errorf("create migrated directory: %w", err)
	}
	for _, name := range imported {
		from := filepath.Join(Dir(db.home), name)
		// A concurrent hand that imported the same file first has already moved
		// it, which is the same end state, not a fault.
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
