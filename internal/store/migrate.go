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
func (db *DB) migrateLegacy(archive bool) error {
	names, err := legacyTaskFiles(db.home)
	if err != nil || len(names) == 0 {
		return err
	}
	// Unlocked, a process that parsed a file before another one imported it and
	// deleted the row lands its own insert afterwards and resurrects the task.
	var unlock func()
	if archive {
		unlock, err = Lock(db.home, MigrationLock, false)
		if err != nil {
			return fmt.Errorf("lock migration: %w", err)
		}
		defer unlock()
	}

	names, err = legacyTaskFiles(db.home)
	if err != nil || len(names) == 0 {
		return err
	}

	for _, name := range names {
		path := filepath.Join(Dir(db.home), name)
		legacy, err := readLegacyTask(path, strings.TrimSuffix(name, ".json"))
		if err != nil {
			return err
		}
		// An id already in the database wins: it is what hand has been writing
		// since the import, and the JSON file is a snapshot from before it.
		task := Task{
			ID: legacy.ID, Project: legacy.Project, Kind: legacy.Kind, Brief: legacy.Brief,
			Lifecycle: TaskOpen, PR: legacy.PR, MergeExecuted: legacy.MergeExecuted,
			MergeExecutedAt: legacy.MergeExecutedAt, ReportOffset: legacy.ReportOffset,
			ReportDigest: legacy.ReportDigest, MergeAnnounced: legacy.MergeAnnounced,
			DeliveredAt: legacy.DeliveredAt, DeliveredReason: legacy.DeliveredReason,
			CreatedAt: legacy.CreatedAt,
		}
		if _, err := db.sql.Exec(`INSERT OR IGNORE INTO task (`+taskColumns+`)
			VALUES (`+placeholders(len(taskColumnNames))+`)`, taskValues(task)...); err != nil {
			return fmt.Errorf("import task %q: %w", task.ID, err)
		}
		var lifecycle TaskLifecycle
		var attempts int
		if err := db.sql.QueryRow(`SELECT lifecycle, (SELECT COUNT(*) FROM attempt WHERE task_id = task.id)
			FROM task WHERE id = ?`, task.ID).Scan(&lifecycle, &attempts); err != nil {
			return fmt.Errorf("read imported task %q: %w", task.ID, err)
		}
		// An id the database already owns attempts for keeps them, for the same reason the row
		// itself wins: they postdate the snapshot. Attempting one anyway collides on the ordinal,
		// or is refused outright once the task is terminal, and fails every command on this home.
		if lifecycle == TaskOpen && attempts == 0 {
			if _, err := db.CreateAttempt(Attempt{
				TaskID: legacy.ID, Ordinal: 1, Lifecycle: AttemptRunning,
				Harness: legacy.Harness, Model: legacy.Model, Effort: legacy.Effort,
				Worktree: legacy.Worktree, LeaseID: legacy.LeaseID, Herdr: legacy.Herdr,
				CreatedAt: legacy.CreatedAt, PaneStartedAt: legacy.PaneStartedAt,
				StatusChangedAt: legacy.StatusChangedAt, StatusChangedFor: legacy.StatusChangedFor,
				DoneVerified: legacy.DoneVerified, LastReportState: legacy.LastReportState,
				LastReportNote: legacy.LastReportNote, SendUndeliveredMessage: legacy.SendUndeliveredMessage,
				SendUndeliveredAt: legacy.SendUndeliveredAt, ParkedFiredFor: legacy.ParkedFiredFor,
				UsageLimitRetryAt: legacy.UsageLimitRetryAt, UsageLimitAttempts: legacy.UsageLimitAttempts,
			}); err != nil {
				return fmt.Errorf("import attempt for task %q: %w", task.ID, err)
			}
		}
	}

	if !archive {
		return nil
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

type legacyTask struct {
	ID                     string `json:"id"`
	Project                string `json:"project"`
	Kind                   string `json:"kind"`
	Harness                string `json:"harness"`
	Model                  string `json:"model"`
	Effort                 string `json:"effort"`
	Worktree               string `json:"worktree"`
	Brief                  string `json:"brief"`
	Herdr                  Herdr  `json:"herdr"`
	PR                     string `json:"pr"`
	MergeExecuted          bool   `json:"merged"`
	MergeExecutedAt        string `json:"merged_at"`
	ReportOffset           int64  `json:"report_offset"`
	ReportDigest           string `json:"report_digest"`
	MergeAnnounced         bool   `json:"pr_merged_observed"`
	DoneVerified           bool   `json:"done_verified"`
	CreatedAt              string `json:"created_at"`
	StatusChangedAt        string `json:"status_changed_at"`
	StatusChangedFor       string `json:"status_changed_for"`
	LastReportState        string `json:"last_report_state"`
	LastReportNote         string `json:"last_report_note"`
	SendUndeliveredMessage string `json:"send_undelivered_message"`
	SendUndeliveredAt      string `json:"send_undelivered_at"`
	LeaseID                string `json:"lease_id"`
	DeliveredAt            string `json:"delivered_at"`
	DeliveredReason        string `json:"delivered_reason"`
	PaneStartedAt          string `json:"pane_started_at"`
	ParkedFiredFor         string `json:"parked_fired_for"`
	UsageLimitRetryAt      string `json:"usage_limit_retry_at"`
	UsageLimitAttempts     int    `json:"usage_limit_attempts"`
}

func readLegacyTask(path, id string) (legacyTask, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return legacyTask{}, fmt.Errorf("read legacy task state %s: %w", path, err)
	}
	var t legacyTask
	if err := json.Unmarshal(data, &t); err != nil {
		return legacyTask{}, fmt.Errorf("parse legacy task state %s (move it aside to continue): %w", path, err)
	}
	if t.ID != id {
		return legacyTask{}, fmt.Errorf("legacy task state %s has mismatched ID %q (move it aside to continue)", path, t.ID)
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
	if db.empty {
		return false, nil
	}
	value, err := db.meta("migrated:" + key)
	return value != "", err
}

func (db *DB) MarkMigrated(key string) error {
	return db.setMeta("migrated:"+key, "done")
}
