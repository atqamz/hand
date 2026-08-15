package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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
	history, found, err := migrated.ReadTaskHistory("legacy")
	if err != nil || !found || len(history.Sends) != 1 {
		t.Fatalf("history = %+v found=%v err=%v, want one send", history, found, err)
	}
	send := history.Sends[0]
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
