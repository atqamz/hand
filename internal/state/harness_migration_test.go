package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestAVersion4HomeLearnsNewHarnesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:4] {
		if _, err := db.Exec(m); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`PRAGMA user_version = 4`,
		`INSERT INTO fleet(only, id, name) VALUES (1, 'f0123456789ab', 'old')`,
		`INSERT INTO project(name, repo, created_at) VALUES ('hand', '/r', 'x')`,
		`INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'hand', 'One', '', 'active', 'x', 'x'), (2, 'hand', 'Two', '', 'active', 'x', 'x')`,
		`INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES (1, 1, 'claude', 'sonnet', 'low', '["/bin/claude","x"]', '/w/t1-a1', 'hand/t1-a1', 'exited', 'x')`,
		`INSERT INTO report(attempt_id, task_id, status, body, created_at) VALUES (1, 1, 'done', 'kept', 'x')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
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
	ctx := context.Background()
	if a, err := s.AddAttempt(ctx, AttemptSpec{TaskID: 2, Harness: "opencode", Argv: []string{"/usr/bin/opencode", "x"}}, t.TempDir()); err != nil || a.ID != 2 {
		t.Fatalf("opencode attempt on a migrated home = %+v, %v", a, err)
	}
	if old, err := s.Attempt(ctx, 1); err != nil || old.Harness != "claude" || old.Branch != "hand/t1-a1" {
		t.Fatalf("old attempt = %+v, %v", old, err)
	}
	if r, err := s.Report(ctx, 1); err != nil || r.Body != "kept" || r.AttemptID != 1 {
		t.Fatalf("old report = %+v, %v", r, err)
	}
	var sqlText string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'report'`).Scan(&sqlText); err != nil || !strings.Contains(sqlText, "REFERENCES attempt(id)") {
		t.Fatalf("report schema = %q, %v", sqlText, err)
	}
	var indexes int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name IN ('attempt_task', 'attempt_live')`).Scan(&indexes); err != nil || indexes != 2 {
		t.Fatalf("attempt indexes = %d, %v", indexes, err)
	}
	if _, err := s.AddAttempt(ctx, AttemptSpec{TaskID: 2, Harness: "claude", Argv: []string{"/bin/claude", "x"}}, t.TempDir()); err == nil {
		t.Fatal("a second live attempt was accepted after the rebuild")
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("the rebuild left foreign key violations")
	}
}
