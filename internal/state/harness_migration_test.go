package state

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
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

func TestAVersion6HomeLearnsTheSupervisorSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:6] {
		if _, err := db.Exec(m); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`PRAGMA user_version = 6`,
		`INSERT INTO fleet(only, id, name) VALUES (1, 'f0123456789ab', 'old')`,
		`INSERT INTO supervisor(harness, model, effort, argv, status, created_at) VALUES ('claude', 'sonnet', 'low', '["/bin/claude"]', 'stopped', 'x')`,
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
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion || SchemaVersion != 8 {
		t.Fatalf("user_version = %d (SchemaVersion %d), %v", v, SchemaVersion, err)
	}
	if sup, ok, err := s.LatestSupervisor(context.Background()); err != nil || !ok || sup.Switching() || sup.Model != "sonnet" {
		t.Fatalf("migrated supervisor = %+v, %v, %v", sup, ok, err)
	}
}

func TestAForeignNewerDatabaseIsRefusedUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hand.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE legacy (x INTEGER); PRAGMA user_version = 21`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, clock); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "schema 21") {
		t.Fatalf("open = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the refused database changed: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("open left %d entries beside the database", len(entries))
	}
}

func TestAVersion7HomeLearnsWhichAttemptContinues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:7] {
		if _, err := db.Exec(m); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`PRAGMA user_version = 7`,
		`INSERT INTO fleet(only, id, name) VALUES (1, 'f0123456789ab', 'old')`,
		`INSERT INTO project(name, repo, created_at) VALUES ('hand', '/r', 'x')`,
		`INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'hand', 'One', '', 'active', 'x', 'x')`,
		`INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, cleaned_at, created_at) VALUES (1, 1, 'claude', 'sonnet', 'low', '["/bin/claude","x"]', '/w/t1-a1', 'hand/f0123456789ab/t1-a1', 'stopped', 'x', 'x')`,
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
	if old, err := s.Attempt(ctx, 1); err != nil || old.Continues != 0 || old.Branch != "hand/f0123456789ab/t1-a1" {
		t.Fatalf("old attempt = %+v, %v", old, err)
	}
	next, err := s.AddAttempt(ctx, AttemptSpec{TaskID: 1, Harness: "claude", Argv: []string{"/bin/claude", "x"}, Continues: 1}, t.TempDir())
	if err != nil || next.Continues != 1 || next.Branch != "hand/f0123456789ab/t1-a1" {
		t.Fatalf("continuing attempt = %+v, %v", next, err)
	}
	if got, err := s.Attempt(ctx, next.ID); err != nil || got.Continues != 1 {
		t.Fatalf("stored attempt = %+v, %v", got, err)
	}
}
