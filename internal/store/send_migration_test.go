package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationV13AddsSendHardeningColumnsAndIndexes(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	oldSchema := strings.NewReplacer(
		"\tusage_limit_episode INTEGER NOT NULL DEFAULT 0,\n", "",
		"\tusage_limit_stuck_episode INTEGER NOT NULL DEFAULT 0,\n", "",
	).Replace(schema)
	oldSendSchema := strings.NewReplacer(
		"\tfinalized_at TEXT NOT NULL DEFAULT '',\n\tusage_limit_episode INTEGER NOT NULL DEFAULT 0\n", "\tfinalized_at TEXT NOT NULL DEFAULT ''\n",
		"\tusage_limit_episode INTEGER NOT NULL DEFAULT 0\n", "",
		"CREATE INDEX IF NOT EXISTS send_attempt_latest\nON send_attempt(task_id, attempt_id, origin, id DESC);\n", "",
		"CREATE INDEX IF NOT EXISTS send_attempt_pending_lookup\nON send_attempt(task_id, attempt_id, state);\n", "",
	).Replace(sendSchema)
	if _, err := sqlDB.Exec(oldSchema + oldSendSchema); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("create reviewed v13 schema: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO task (id, lifecycle, created_at) VALUES ('legacy-v13', 'open', '2026-08-16T00:00:00Z')`); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO attempt (task_id, ordinal, lifecycle, created_at) VALUES ('legacy-v13', 1, 'running', '2026-08-16T00:00:00Z')`); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`UPDATE task SET active_attempt_id = 1 WHERE id = 'legacy-v13'`); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO send_attempt (task_id, attempt_id, origin, message, state, created_at) VALUES ('legacy-v13', 1, 'operator', 'preserve me', 'submitted', '2026-08-16T00:00:01Z')`); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`PRAGMA user_version = 13`); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	readOnly, found, err := ReadTaskHistoryReadOnly(home, "legacy-v13")
	if err != nil || !found || readOnly.ActiveAttempt == nil {
		t.Fatalf("read-only v13 history = %+v, err=%v", readOnly, err)
	}
	metadata, found, err := LatestSendMetadataReadOnly(home, "legacy-v13", 1)
	if err != nil || !found || metadata.State != SendSubmitted || metadata.UsageLimitEpisode != 0 {
		t.Fatalf("read-only v13 metadata = %+v, found=%t, err=%v", metadata, found, err)
	}
	histories, err := ListReconciliationHistoriesReadOnly(home)
	if err != nil || len(histories) != 1 || histories[0].ActiveAttempt == nil {
		t.Fatalf("read-only v13 reconciliation histories = %+v, err=%v", histories, err)
	}

	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	version, err := db.schemaVersion()
	if err != nil || version != len(migrations) {
		t.Fatalf("schema version = %d, err=%v, want %d", version, err, len(migrations))
	}
	for _, table := range []string{"attempt", "send_attempt"} {
		var count int
		if err := db.sql.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = 'usage_limit_episode'`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s usage_limit_episode columns = %d, want 1", table, count)
		}
	}
	var stuck int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('attempt') WHERE name = 'usage_limit_stuck_episode'`).Scan(&stuck); err != nil {
		t.Fatal(err)
	}
	if stuck != 1 {
		t.Fatalf("attempt usage_limit_stuck_episode columns = %d, want 1", stuck)
	}
	for _, index := range []string{"send_attempt_latest", "send_attempt_latest_any", "send_attempt_pending_lookup"} {
		var count int
		if err := db.sql.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("index %s count = %d, want 1", index, count)
		}
	}
	send, found, err := db.LatestSendMetadata("legacy-v13", 1)
	if err != nil || !found || send.Message != "" || send.State != SendSubmitted || send.UsageLimitEpisode != 0 {
		t.Fatalf("send metadata = %+v, found=%t, err=%v", send, found, err)
	}
}

func TestMigrationV12BackfillsLegacySendAsUncertain(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(Task{ID: "legacy", Lifecycle: TaskOpen}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAttempt(Attempt{TaskID: "legacy", Lifecycle: AttemptRunning, SendUndeliveredMessage: "old text", SendUndeliveredAt: "2026-08-15T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`DROP INDEX send_attempt_one_pending; DROP TABLE send_attempt; PRAGMA user_version = 12`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migrated.Close() }()
	if version, err := migrated.schemaVersion(); err != nil || version != len(migrations) {
		t.Fatalf("version = %d, err=%v, want %d", version, err, len(migrations))
	}
	if _, found, err := migrated.ReadTaskHistory("legacy"); err != nil || !found {
		t.Fatalf("ReadTaskHistory found=%v err=%v", found, err)
	}
	sends, err := migrated.ListSends("legacy")
	if err != nil || len(sends) != 1 {
		t.Fatalf("sends = %+v err=%v, want one send", sends, err)
	}
	send := sends[0]
	if send.State != SendUncertain || send.Origin != SendOriginLegacyUndelivered || send.Message != "old text" || send.ReasonCode != "legacy-undelivered-trace" {
		t.Fatalf("send = %+v, want conservative legacy uncertainty", send)
	}
}

func TestMigrationDoesNotInventSendForEmptyLegacyTrace(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(Task{ID: "empty", Lifecycle: TaskOpen}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAttempt(Attempt{TaskID: "empty", Lifecycle: AttemptRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`DROP INDEX send_attempt_one_pending; DROP TABLE send_attempt; PRAGMA user_version = 12`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migrated.Close() }()
	sends, err := migrated.ListSends("empty")
	if err != nil || len(sends) != 0 {
		t.Fatalf("sends=%+v err=%v, want no invented history", sends, err)
	}
}

func TestLegacyJSONSendEvidenceIsIdempotentBeforeArchive(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	legacy := map[string]any{
		"id": "json-task", "project": "demo", "kind": "ship", "harness": "claude", "model": "opus", "effort": "high",
		"created_at": "2026-08-15T11:00:00Z", "send_undelivered_message": "from json", "send_undelivered_at": "2026-08-15T11:01:00Z",
		"herdr": map[string]string{"session": "s", "workspace_id": "w", "tab_id": "t", "pane_id": "p"},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(home), "json-task.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.migrateLegacy(false); err != nil {
		t.Fatal(err)
	}
	if err := db.migrateLegacy(false); err != nil {
		t.Fatal(err)
	}
	sends, err := db.ListSends("json-task")
	if err != nil || len(sends) != 1 || sends[0].State != SendUncertain {
		t.Fatalf("sends=%+v err=%v, want one uncertain imported row", sends, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if _, err := os.Stat(filepath.Join(LegacyDir(home), "json-task.json")); err != nil {
		t.Fatalf("legacy JSON was not archived: %v", err)
	}
	sends, err = reopened.ListSends("json-task")
	if err != nil || len(sends) != 1 {
		t.Fatalf("reopened sends=%+v err=%v, want one row", sends, err)
	}
}

func TestLegacyJSONSendEvidenceDoesNotAttachToReopenedTask(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(Task{ID: "reopened", Lifecycle: TaskOpen, CreatedAt: "2026-08-15T11:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAttempt(Attempt{TaskID: "reopened", Ordinal: 1, Lifecycle: AttemptCompleted, Harness: "claude", Model: "opus", Effort: "high", CreatedAt: "2026-08-15T11:00:00Z", Herdr: Herdr{Session: "s1", WorkspaceID: "w1", TabID: "t1", PaneID: "p1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAttempt(Attempt{TaskID: "reopened", Ordinal: 2, Lifecycle: AttemptRunning, CreatedAt: "2026-08-15T12:00:00Z", Herdr: Herdr{Session: "s2", WorkspaceID: "w2", TabID: "t2", PaneID: "p2"}}); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]any{
		"id": "reopened", "created_at": "2026-08-15T11:00:00Z", "harness": "claude", "model": "opus", "effort": "high",
		"send_undelivered_message": "old pane", "send_undelivered_at": "2026-08-15T11:01:00Z",
		"herdr": map[string]string{"session": "s1", "workspace_id": "w1", "tab_id": "t1", "pane_id": "p1"},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(home), "reopened.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.migrateLegacy(false); err != nil {
		t.Fatal(err)
	}
	sends, err := db.ListSends("reopened")
	if err != nil || len(sends) != 0 {
		t.Fatalf("sends=%+v err=%v, want no evidence attached without exact legacy Attempt identity", sends, err)
	}
	_ = db.Close()
}
