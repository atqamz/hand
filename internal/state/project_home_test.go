package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectInsideTheHomeFollowsAMove(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	home := filepath.Join(dir, "a")
	if err := os.MkdirAll(filepath.Join(home, "projects", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(home, "hand.db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "elsewhere")
	if _, err := s.AddProject(ctx, "app", filepath.Join(home, "projects", "app")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProject(ctx, "ext", outside); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT repo FROM project WHERE name = 'app'`).Scan(&raw); err != nil || raw != filepath.Join("projects", "app") {
		t.Fatalf("stored repo = %q, %v", raw, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(dir, "b")
	if err := os.Rename(home, moved); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filepath.Join(moved, "hand.db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if p, err := s.Project(ctx, "app"); err != nil || p.Repo != filepath.Join(moved, "projects", "app") {
		t.Fatalf("app = %+v, %v", p, err)
	}
	ps, err := s.Projects(ctx)
	if err != nil || len(ps) != 2 || ps[0].Repo != filepath.Join(moved, "projects", "app") || ps[1].Repo != outside {
		t.Fatalf("projects = %+v, %v", ps, err)
	}
}

func TestUncleanedAttemptsSkipsCleanedOnes(t *testing.T) {
	s, _ := openTest(t)
	task := activeTask(t, s)
	ctx := context.Background()
	spec := claudeSpec
	spec.TaskID = task.ID
	first, err := s.AddAttempt(ctx, spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndAttempt(ctx, first.ID, AttemptFailed, "no terminal"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanAttempt(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := s.AddAttempt(ctx, spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.UncleanedAttempts(ctx)
	if err != nil || len(got) != 1 || got[0].ID != second.ID {
		t.Fatalf("uncleaned = %+v, %v", got, err)
	}
}

func TestProjectAddedThroughASymlinkedHomeIsStoredRelative(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(home, "hand.db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.AddProject(ctx, "app", filepath.Join(alias, "projects", "app")); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT repo FROM project WHERE name = 'app'`).Scan(&raw); err != nil || raw != filepath.Join("projects", "app") {
		t.Fatalf("stored repo = %q, %v", raw, err)
	}
}

func TestRebaseProjectsMakesPathsUnderTheOldHomeRelative(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	old := filepath.Join(t.TempDir(), "old")
	for name, repo := range map[string]string{"inner": filepath.Join(old, "projects", "inner"), "outer": "/srv/outer"} {
		if _, err := s.db.Exec(`INSERT INTO project(name, repo, created_at) VALUES (?, ?, 'x')`, name, repo); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RebaseProjects(ctx, old); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Project(ctx, "inner"); err != nil || p.Repo != filepath.Join(s.home, "projects", "inner") {
		t.Fatalf("inner = %+v, %v", p, err)
	}
	if p, err := s.Project(ctx, "outer"); err != nil || p.Repo != "/srv/outer" {
		t.Fatalf("outer = %+v, %v", p, err)
	}
	events, err := s.RecentEvents(ctx, 1)
	if err != nil || len(events) != 1 || events[0].Kind != "project.rebased" || events[0].Detail != "inner" {
		t.Fatalf("events = %+v, %v", events, err)
	}
}
