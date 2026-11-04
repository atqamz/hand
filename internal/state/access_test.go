package state

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAnAttemptRecordsItsAccess(t *testing.T) {
	s, _ := openTest(t)
	task := activeTask(t, s)
	ctx := context.Background()
	spec := claudeSpec
	spec.TaskID = task.ID
	spec.Access = AccessReadonly
	a, err := s.AddAttempt(ctx, spec, t.TempDir())
	if err != nil || a.Access != AccessReadonly {
		t.Fatalf("add = %+v, %v", a, err)
	}
	if got, err := s.Attempt(ctx, a.ID); err != nil || got.Access != AccessReadonly {
		t.Fatalf("read = %+v, %v", got, err)
	}
	if _, err := s.AttemptRunning(ctx, a.ID, Terminal{TerminalID: "t", PaneID: "p"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndAttempt(ctx, a.ID, AttemptStopped, "x"); err != nil {
		t.Fatal(err)
	}
	spec.Access = ""
	b, err := s.AddAttempt(ctx, spec, t.TempDir())
	if err != nil || b.Access != AccessOpen {
		t.Fatalf("default = %+v, %v", b, err)
	}
	if _, err := s.AttemptRunning(ctx, b.ID, Terminal{TerminalID: "t2", PaneID: "p2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndAttempt(ctx, b.ID, AttemptStopped, "x"); err != nil {
		t.Fatal(err)
	}
	spec.Access = "write"
	if _, err := s.AddAttempt(ctx, spec, t.TempDir()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad access err = %v", err)
	}
}

func TestAVersion15HomeKeepsItsAttemptsAndLearnsAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:15] {
		if _, err := db.Exec(m); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`PRAGMA user_version = 15`,
		`INSERT INTO fleet(only, id, name) VALUES (1, 'f0123456789ab', 'old')`,
		`INSERT INTO project(name, repo, created_at) VALUES ('hand', '/r', 'x')`,
		`INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'hand', 'One', '', 'active', 'x', 'x'), (2, 'hand', 'Two', '', 'active', 'x', 'x')`,
		`INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, approve, created_at) VALUES (1, 1, 'claude', 'sonnet', 'low', '["/bin/claude","x"]', '/w/t1-a1', 'b1', 'running', 'never', 'x'), (2, 1, 'claude', 'sonnet', 'low', '["/bin/claude","y"]', '/w/t1-a2', 'b2', 'exited', 'ask', 'x'), (3, 2, 'codex', 'gpt-6-luna', 'low', '["/bin/codex","z"]', '/w/t2-a3', 'b3', 'launching', 'never', 'x')`,
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
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
		t.Fatalf("user_version = %d, %v", v, err)
	}
	for id, approve := range map[int64]string{1: ApproveNever, 2: ApproveAsk, 3: ApproveNever} {
		if a, err := s.Attempt(ctx, id); err != nil || a.Access != AccessOpen || a.Approve != approve || a.Branch != "b"+strconv.FormatInt(id, 10) {
			t.Fatalf("attempt %d after the migration = %+v, %v", id, a, err)
		}
	}
	if _, err := s.db.Exec(`UPDATE attempt SET access = 'write'`); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("access outside the set = %v", err)
	}
	var check string
	if err := s.db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity_check = %q, %v", check, err)
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("the migration left foreign key violations")
	}
}
