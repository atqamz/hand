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
	if history.Task.Project != "nsr" || history.Task.Kind != "ship" || history.Task.Brief != "data/legacy/brief.md" || history.Task.ReportOffset != 41 || history.Task.ReportDigest != "digest-before-upgrade" || history.Task.PR != "https://github.com/o/nsr/pull/7" || !history.Task.MergeExecuted || history.Task.MergeExecutedAt != "2026-07-24T12:00:00Z" || !history.Task.MergeAnnounced || history.Task.DeliveredAt != "2026-07-24T13:00:00Z" || history.Task.DeliveredReason != "merged" || history.Task.CreatedAt != "2026-07-24T10:00:00Z" {
		t.Fatalf("task-owned state was not preserved: %+v", history.Task)
	}
	if len(history.Attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(history.Attempts))
	}
	attempt := history.Attempts[0]
	if attempt.Ordinal != 1 || attempt.Lifecycle != AttemptRunning || attempt.Harness != "claude" || attempt.Model != "opus" || attempt.Effort != "high" || attempt.Worktree != "/w/nsr" || attempt.LeaseID != "lease-7" || attempt.CreatedAt != "2026-07-24T10:00:00Z" || attempt.PaneStartedAt != "2026-07-24T10:30:00Z" || attempt.StatusChangedAt != "2026-07-24T11:00:00Z" || attempt.StatusChangedFor != "working" || !attempt.DoneVerified {
		t.Fatalf("execution state was not preserved: %+v", attempt)
	}
	if attempt.Herdr.Session != "default" || attempt.Herdr.WorkspaceID != "wA" || attempt.Herdr.TabID != "wA:tB" || attempt.Herdr.PaneID != "wA:pC" || attempt.LastReportState != "working" || attempt.LastReportNote != "still working" || attempt.UsageLimitRetryAt != "2026-07-24T15:00:00Z" || attempt.UsageLimitAttempts != 2 || attempt.SendUndeliveredMessage != "stop" || attempt.SendUndeliveredAt != "2026-07-24T13:30:00Z" || attempt.ParkedFiredFor != "2026-07-24T11:30:00Z" {
		t.Fatalf("attempt bookkeeping was not preserved: %+v", attempt)
	}
	sends, err := migrated.ListSends("legacy")
	if err != nil || len(sends) != 1 || sends[0].State != SendUncertain || sends[0].Message != "stop" || sends[0].Origin != SendOriginLegacyUndelivered {
		t.Fatalf("migrated send history = %+v, err=%v, want one uncertain legacy send", sends, err)
	}
	wantTask, wantAttempt, wantSend := history.Task, attempt, sends[0]
	var teardownColumns int
	if err := migrated.sql.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('attempt') WHERE name IN ('teardown_terminal_attempt', 'teardown_disposition', 'teardown_herdr_state', 'teardown_worktree_state', 'teardown_completion_state')`).Scan(&teardownColumns); err != nil {
		t.Fatal(err)
	}
	if teardownColumns != 5 {
		t.Fatalf("teardown evidence columns after upgrade = %d, want 5", teardownColumns)
	}
	if _, err := migrated.sql.Query(`SELECT harness FROM task`); err == nil {
		t.Fatal("old execution columns remain authoritative on task")
	}
	if err := migrated.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(home)
	if err != nil {
		t.Fatalf("reopen migrated 0.4.0 database: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	reopenedHistory, found, err := reopened.ReadTaskHistory("legacy")
	if err != nil || !found || len(reopenedHistory.Attempts) != 1 {
		t.Fatalf("reopened history = %+v, found=%t, err=%v", reopenedHistory, found, err)
	}
	if reopenedHistory.Task != wantTask || reopenedHistory.Attempts[0] != wantAttempt {
		t.Fatalf("reopened history changed: task=%+v attempt=%+v, want task=%+v attempt=%+v", reopenedHistory.Task, reopenedHistory.Attempts[0], wantTask, wantAttempt)
	}
	reopenedSends, err := reopened.ListSends("legacy")
	if err != nil || len(reopenedSends) != 1 || reopenedSends[0] != wantSend {
		t.Fatalf("reopened send history = %+v, err=%v, want %+v", reopenedSends, err, wantSend)
	}
}

// The fleet home that predates user_version entirely: sqlite's default 0 with none of the
// columns the registered migrations add. Every one of them has to replay before the split,
// and version 0 also being what an unstamped current database reports must not stop that.
func TestPreVersioningDatabaseReplaysEveryMigrationThroughTheSplit(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	db := &DB{sql: sqlDB, home: home}
	if _, err := db.sql.Exec(preVersioningSchema); err != nil {
		t.Fatal(err)
	}
	values := []any{
		"baseline", "nsr", "ship", "data/baseline/brief.md", "https://github.com/o/nsr/pull/3", true,
		"2026-07-24T12:00:00Z", 17, true, "claude", "opus", "high", "/w/nsr", "default", "wA", "wA:tB",
		"wA:pC", "2026-07-24T10:00:00Z", "2026-07-24T11:00:00Z", "working", true, "working", "on it",
	}
	if _, err := db.sql.Exec(`INSERT INTO task (
		id, project, kind, brief, pr, merge_executed, merge_executed_at, report_offset, merge_announced,
		harness, model, effort, worktree, herdr_session, herdr_workspace_id, herdr_tab_id, herdr_pane_id,
		created_at, status_changed_at, status_changed_for, done_verified, last_report_state,
		last_report_note) VALUES (`+placeholders(len(values))+`)`, values...); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`INSERT INTO project (name, url, mode, position) VALUES ('nsr', 'git@github.com:o/nsr.git', 'direct-pr', 0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(home)
	if err != nil {
		t.Fatalf("open a pre-versioning database: %v", err)
	}
	defer func() { _ = migrated.Close() }()
	if version, err := migrated.schemaVersion(); err != nil || version != len(migrations) {
		t.Fatalf("schemaVersion = %d, %v, want %d", version, err, len(migrations))
	}
	history, found, err := migrated.ReadTaskHistory("baseline")
	if err != nil || !found {
		t.Fatalf("ReadTaskHistory = %v, %v", found, err)
	}
	if history.Task.Lifecycle != TaskOpen || history.Task.ReportOffset != 17 || history.Task.PR != "https://github.com/o/nsr/pull/3" {
		t.Fatalf("task-owned state was not preserved: %+v", history.Task)
	}
	if history.ActiveAttempt == nil || len(history.Attempts) != 1 {
		t.Fatalf("attempts = %+v, want one active attempt", history.Attempts)
	}
	attempt := *history.ActiveAttempt
	if attempt.Ordinal != 1 || attempt.Lifecycle != AttemptRunning || attempt.Harness != "claude" || attempt.Worktree != "/w/nsr" || attempt.Herdr.PaneID != "wA:pC" {
		t.Fatalf("execution state was not preserved: %+v", attempt)
	}
	// Migration 5's backfill, carried through the split: only a real replay can have set it,
	// since the column it reads did not exist on the row this home was written with.
	if attempt.PaneStartedAt != "2026-07-24T11:00:00Z" {
		t.Fatalf("pane start = %q, want the backfill from status_changed_at", attempt.PaneStartedAt)
	}
	var upstream string
	if err := migrated.sql.QueryRow(`SELECT upstream FROM project WHERE name = 'nsr'`).Scan(&upstream); err != nil {
		t.Fatalf("project.upstream did not migrate: %v", err)
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

// legacy040Schema minus every column a registered migration adds, which is what the
// version-0 baseline `schema` built.
const preVersioningSchema = `
CREATE TABLE task (
 id TEXT PRIMARY KEY, project TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '', brief TEXT NOT NULL DEFAULT '',
 pr TEXT NOT NULL DEFAULT '', merge_executed INTEGER NOT NULL DEFAULT 0, merge_executed_at TEXT NOT NULL DEFAULT '',
 report_offset INTEGER NOT NULL DEFAULT 0, merge_announced INTEGER NOT NULL DEFAULT 0, harness TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL DEFAULT '', effort TEXT NOT NULL DEFAULT '', worktree TEXT NOT NULL DEFAULT '',
 herdr_session TEXT NOT NULL DEFAULT '', herdr_workspace_id TEXT NOT NULL DEFAULT '', herdr_tab_id TEXT NOT NULL DEFAULT '',
 herdr_pane_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT '', status_changed_at TEXT NOT NULL DEFAULT '',
 status_changed_for TEXT NOT NULL DEFAULT '', done_verified INTEGER NOT NULL DEFAULT 0,
 last_report_state TEXT NOT NULL DEFAULT '', last_report_note TEXT NOT NULL DEFAULT ''
);
CREATE TABLE project (name TEXT PRIMARY KEY, url TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL DEFAULT '', position INTEGER NOT NULL DEFAULT 0);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE hold (id TEXT PRIMARY KEY, kind TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', blocked_on TEXT NOT NULL DEFAULT '', set_at TEXT NOT NULL DEFAULT '');`
