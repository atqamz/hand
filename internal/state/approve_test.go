package state

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnAttemptRecordsHowItAsksForApproval(t *testing.T) {
	s, _ := openTest(t)
	task := activeTask(t, s)
	ctx := context.Background()
	spec := claudeSpec
	spec.TaskID = task.ID
	spec.Approve = ApproveAsk
	a, err := s.AddAttempt(ctx, spec, t.TempDir())
	if err != nil || a.Approve != ApproveAsk {
		t.Fatalf("add = %+v, %v", a, err)
	}
	if got, err := s.Attempt(ctx, a.ID); err != nil || got.Approve != ApproveAsk {
		t.Fatalf("read = %+v, %v", got, err)
	}
	if _, err := s.AttemptRunning(ctx, a.ID, Terminal{TerminalID: "t", PaneID: "p"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndAttempt(ctx, a.ID, AttemptStopped, "x"); err != nil {
		t.Fatal(err)
	}
	spec.Approve = ""
	b, err := s.AddAttempt(ctx, spec, t.TempDir())
	if err != nil || b.Approve != ApproveNever {
		t.Fatalf("default = %+v, %v", b, err)
	}
	spec.Approve = "sometimes"
	if _, err := s.AddAttempt(ctx, spec, t.TempDir()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad approve err = %v", err)
	}
}

func TestGrantsAreStoredPerProjectAndHarnessOnce(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	g, added, err := s.AddGrant(ctx, "hand", "claude", " git push ")
	if err != nil || !added || g.Pattern != "git push" || g.ID != 1 {
		t.Fatalf("add = %+v, %v, %v", g, added, err)
	}
	again, added, err := s.AddGrant(ctx, "hand", "claude", "git push")
	if err != nil || added || again.ID != g.ID {
		t.Fatalf("again = %+v, %v, %v", again, added, err)
	}
	if _, _, err := s.AddGrant(ctx, "hand", "claude", "go test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddGrant(ctx, "nope", "claude", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project err = %v", err)
	}
	if _, _, err := s.AddGrant(ctx, "hand", "claude", " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty pattern err = %v", err)
	}
	list, err := s.Grants(ctx, "hand", "claude")
	if err != nil || len(list) != 2 || list[0].Pattern != "git push" || list[1].Pattern != "go test" {
		t.Fatalf("grants = %+v, %v", list, err)
	}
	if other, err := s.Grants(ctx, "hand", "codex"); err != nil || len(other) != 0 {
		t.Fatalf("other harness grants = %+v, %v", other, err)
	}
	if _, err := s.RemoveGrant(ctx, "hand", 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove unknown err = %v", err)
	}
	if got, err := s.RemoveGrant(ctx, "hand", g.ID); err != nil || got.Pattern != "git push" {
		t.Fatalf("remove = %+v, %v", got, err)
	}
	if list, _ := s.Grants(ctx, "hand", ""); len(list) != 1 || list[0].Pattern != "go test" {
		t.Fatalf("after remove = %+v", list)
	}
	events, err := s.RecentEventsOf(ctx, []string{"grant.added", "grant.removed"}, 10)
	if err != nil || len(events) != 3 || !strings.HasPrefix(events[0].Detail, "hand: claude git push") || events[2].Kind != "grant.removed" {
		t.Fatalf("events = %+v, %v", events, err)
	}
}

func TestAVersion14HomeKeepsItsAttemptsAsNever(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:14] {
		if _, err := db.Exec(m); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`PRAGMA user_version = 14`,
		`INSERT INTO fleet(only, id, name) VALUES (1, 'f0123456789ab', 'old')`,
		`INSERT INTO project(name, repo, created_at) VALUES ('hand', '/r', 'x')`,
		`INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'hand', 'One', '', 'active', 'x', 'x')`,
		`INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES (1, 1, 'claude', 'sonnet', 'low', '["/bin/claude","x"]', '/w/t1-a1', 'b', 'running', 'x')`,
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
	if a, err := s.Attempt(ctx, 1); err != nil || a.Approve != ApproveNever {
		t.Fatalf("attempt after the migration = %+v, %v", a, err)
	}
	if g, added, err := s.AddGrant(ctx, "hand", "claude", "git push"); err != nil || !added || g.ID != 1 {
		t.Fatalf("grant on a migrated home = %+v, %v, %v", g, added, err)
	}
	if _, err := s.db.Exec(`UPDATE attempt SET approve = 'sometimes'`); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("approve outside the set = %v", err)
	}
}
