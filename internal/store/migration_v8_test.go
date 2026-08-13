package store

import "testing"

func TestMigrationV8ConvertsReal040Database(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	db := &DB{sql: sqlDB, home: home}
	if _, err := db.sql.Exec(legacy040Schema); err != nil {
		t.Fatal(err)
	}
	values := []any{
		"legacy", "nsr", "ship", "data/legacy/brief.md", "https://github.com/o/nsr/pull/7", true,
		"2026-07-24T12:00:00Z", 41, "digest-before-upgrade", true, "2026-07-24T13:00:00Z", "merged",
		"claude", "opus", "high", "/w/nsr", "lease-7", "default", "wA", "wA:tB", "wA:pC",
		"2026-07-24T10:00:00Z", "2026-07-24T10:30:00Z", "2026-07-24T11:00:00Z", "working", true,
		"working", "still working", "stop", "2026-07-24T13:30:00Z", "2026-07-24T11:30:00Z",
		"2026-07-24T15:00:00Z", 2,
	}
	if _, err := db.sql.Exec(`INSERT INTO task (
		id, project, kind, brief, pr, merge_executed, merge_executed_at, report_offset, report_digest,
		merge_announced, delivered_at, delivered_reason, harness, model, effort, worktree, lease_id,
		herdr_session, herdr_workspace_id, herdr_tab_id, herdr_pane_id, created_at, pane_started_at,
		status_changed_at, status_changed_for, done_verified, last_report_state, last_report_note,
		send_undelivered_message, send_undelivered_at, parked_fired_for, usage_limit_retry_at,
		usage_limit_attempts) VALUES (`+placeholders(len(values))+`)`, values...); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`PRAGMA user_version = 7`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(home)
	if err != nil {
		t.Fatalf("open 0.4.0 database: %v", err)
	}
	defer func() { _ = migrated.Close() }()
	history, found, err := migrated.ReadTaskHistory("legacy")
	if err != nil || !found {
		t.Fatalf("ReadTaskHistory = %v, %v", found, err)
	}
	if history.Task.Lifecycle != TaskOpen || history.Task.ActiveAttemptID == 0 {
		t.Fatalf("task lifecycle/active attempt = %q/%d", history.Task.Lifecycle, history.Task.ActiveAttemptID)
	}
	if history.Task.ReportOffset != 41 || history.Task.ReportDigest != "digest-before-upgrade" || history.Task.PR != "https://github.com/o/nsr/pull/7" || history.Task.DeliveredAt != "2026-07-24T13:00:00Z" {
		t.Fatalf("task-owned state was not preserved: %+v", history.Task)
	}
	if len(history.Attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(history.Attempts))
	}
	attempt := history.Attempts[0]
	if attempt.Ordinal != 1 || attempt.Lifecycle != AttemptRunning || attempt.Harness != "claude" || attempt.Model != "opus" || attempt.Effort != "high" || attempt.Worktree != "/w/nsr" || attempt.LeaseID != "lease-7" {
		t.Fatalf("execution state was not preserved: %+v", attempt)
	}
	if attempt.Herdr.PaneID != "wA:pC" || attempt.LastReportState != "working" || attempt.UsageLimitAttempts != 2 || attempt.SendUndeliveredMessage != "stop" {
		t.Fatalf("attempt bookkeeping was not preserved: %+v", attempt)
	}
	if _, err := migrated.sql.Query(`SELECT harness FROM task`); err == nil {
		t.Fatal("old execution columns remain authoritative on task")
	}
}

const legacy040Schema = `
CREATE TABLE task (
 id TEXT PRIMARY KEY, project TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '', brief TEXT NOT NULL DEFAULT '',
 pr TEXT NOT NULL DEFAULT '', merge_executed INTEGER NOT NULL DEFAULT 0, merge_executed_at TEXT NOT NULL DEFAULT '',
 report_offset INTEGER NOT NULL DEFAULT 0, report_digest TEXT NOT NULL DEFAULT '', merge_announced INTEGER NOT NULL DEFAULT 0,
 delivered_at TEXT NOT NULL DEFAULT '', delivered_reason TEXT NOT NULL DEFAULT '', harness TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL DEFAULT '', effort TEXT NOT NULL DEFAULT '', worktree TEXT NOT NULL DEFAULT '', lease_id TEXT NOT NULL DEFAULT '',
 herdr_session TEXT NOT NULL DEFAULT '', herdr_workspace_id TEXT NOT NULL DEFAULT '', herdr_tab_id TEXT NOT NULL DEFAULT '',
 herdr_pane_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT '', pane_started_at TEXT NOT NULL DEFAULT '',
 status_changed_at TEXT NOT NULL DEFAULT '', status_changed_for TEXT NOT NULL DEFAULT '', done_verified INTEGER NOT NULL DEFAULT 0,
 last_report_state TEXT NOT NULL DEFAULT '', last_report_note TEXT NOT NULL DEFAULT '', send_undelivered_message TEXT NOT NULL DEFAULT '',
 send_undelivered_at TEXT NOT NULL DEFAULT '', parked_fired_for TEXT NOT NULL DEFAULT '', usage_limit_retry_at TEXT NOT NULL DEFAULT '',
 usage_limit_attempts INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE project (name TEXT PRIMARY KEY, url TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL DEFAULT '', position INTEGER NOT NULL DEFAULT 0, upstream TEXT NOT NULL DEFAULT '');
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE hold (id TEXT PRIMARY KEY, kind TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', blocked_on TEXT NOT NULL DEFAULT '', set_at TEXT NOT NULL DEFAULT '');`
