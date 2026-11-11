package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestAVersion22DatabaseMigratesToTwentyThreeWithNoSocketOnItsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range append(append([]string{schema}, migrations[:6]...),
		`PRAGMA user_version = 22`,
		`INSERT INTO project(name, repo, created_at) VALUES ('hand', '/home/me/hand', '2026-09-26T00:00:00Z')`,
		`INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'hand', 'one', '', 'active', '2026-09-26T00:00:00Z', '2026-09-26T00:00:00Z')`,
		`INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, server_generation, terminal_id, pid, start_marker, created_at) VALUES (1, 1, 'claude', '', '', '[]', '/w/1', 'b1', 'running', 'g', 't', 7, 'm', '2026-09-26T00:00:00Z')`,
	) {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
		t.Fatalf("user_version = %d, %v", v, err)
	}
	a, err := s.Attempt(t.Context(), 1)
	if err != nil || a.Socket != "" || a.PID != 7 {
		t.Fatalf("attempt = %+v, %v", a, err)
	}
	if _, err := s.AttemptSwitched(t.Context(), 1, Terminal{ServerGeneration: "g2", Socket: "/l.sock"}, "claude", "", "", false); err != nil {
		t.Fatal(err)
	}
	if a, err := s.Attempt(t.Context(), 1); err != nil || a.Socket != "/l.sock" {
		t.Fatalf("attempt after switch = %+v, %v", a, err)
	}
}

func TestStampingTheSocketKeepsTheAttemptsLatestSignal(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	a := runningAttempt(t, s)
	if err := s.NoteAttempt(ctx, a.ID, "blocked", "Esc to cancel"); err != nil {
		t.Fatal(err)
	}
	if err := s.StampAttemptSocket(ctx, a.ID, "/l.sock"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Attempt(ctx, a.ID)
	if err != nil || got.Socket != "/l.sock" {
		t.Fatalf("attempt = %+v, %v", got, err)
	}
	if kind, err := s.LastAttemptEvent(ctx, a.ID); err != nil || kind != "attempt.blocked" {
		t.Fatalf("last event = %q, %v", kind, err)
	}
	if e, ok, err := s.AttemptSignal(ctx, a.ID); err != nil || !ok || e.Kind != "attempt.blocked" {
		t.Fatalf("signal = %+v, %v, %v", e, ok, err)
	}
}
