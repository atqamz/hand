package state

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func repoPaths(t *testing.T) (app, bare string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "app"), filepath.Join(dir, "bare")
}

func projectEvents(t *testing.T, s *Store) []string {
	t.Helper()
	events, err := s.EventsAfter(context.Background(), 0, []string{"project.added", "project.adopted", "project.trusted"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Kind + " " + e.Detail
	}
	return out
}

func TestAProjectKeepsTheSettingsItWasRegisteredWith(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	appRepo, bareRepo := repoPaths(t)
	if _, err := s.AddProject(ctx, "app", appRepo, "v1\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProject(ctx, "bare", bareRepo, ""); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"app": "v1\n", "bare": ""} {
		if p, err := s.Project(ctx, name); err != nil || p.Settings != want {
			t.Fatalf("%s = %+v, %v", name, p, err)
		}
	}
	if ps, err := s.Projects(ctx); err != nil || ps[0].Settings != "v1\n" || ps[1].Settings != "" {
		t.Fatalf("Projects = %+v, %v", ps, err)
	}
	if got := strings.Join(projectEvents(t, s), "|"); got != "project.added app|project.added bare" {
		t.Fatalf("events = %q", got)
	}
}

func TestAdoptingRecordsOnceAndTheFirstWriterWins(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	appRepo, _ := repoPaths(t)
	if _, err := s.AddProject(ctx, "app", appRepo, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AdoptProject(ctx, "app", "v1\nfirst\n"); err != nil || got != "v1\nfirst\n" {
		t.Fatalf("first adopt = %q, %v", got, err)
	}
	if got, err := s.AdoptProject(ctx, "app", "v1\nsecond\n"); err != nil || got != "v1\nfirst\n" {
		t.Fatalf("second adopt = %q, %v", got, err)
	}
	if p, _ := s.Project(ctx, "app"); p.Settings != "v1\nfirst\n" {
		t.Fatalf("stored = %q", p.Settings)
	}
	if got := strings.Join(projectEvents(t, s), "|"); got != "project.added app|project.adopted app" {
		t.Fatalf("events = %q", got)
	}
	if _, err := s.AdoptProject(ctx, "nope", "v1\n"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project = %v", err)
	}
}

func TestTrustingReplacesTheSettingsAndRecordsAnEvent(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	appRepo, _ := repoPaths(t)
	if _, err := s.AddProject(ctx, "app", appRepo, "v1\nold\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.TrustProject(ctx, "app", "v1\nnew\n"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Project(ctx, "app"); p.Settings != "v1\nnew\n" {
		t.Fatalf("stored = %q", p.Settings)
	}
	if err := s.TrustProject(ctx, "app", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty settings = %v", err)
	}
	if err := s.TrustProject(ctx, "nope", "v1\n"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project = %v", err)
	}
	if got := strings.Join(projectEvents(t, s), "|"); got != "project.added app|project.trusted app" {
		t.Fatalf("events = %q", got)
	}
}

func TestAVersion12HomeKeepsItsProjectsUnrecordedAndAdoptsLater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	appRepo, _ := repoPaths(t)
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:12] {
		if _, err := db.Exec(m); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`PRAGMA user_version = 12`,
		`INSERT INTO fleet(only, id, name) VALUES (1, 'f0123456789ab', 'old')`,
		`INSERT INTO project(name, repo, created_at) VALUES ('app', '` + appRepo + `', '2026-10-22T05:00:00Z')`,
		`INSERT INTO task(project, title, goal, status, created_at, updated_at) VALUES ('app', 'Fix', 'g', 'inbox', 'x', 'x')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	backup := filepath.Join(t.TempDir(), "hand.db.bak")
	if _, err := db.Exec(`VACUUM INTO ?`, backup); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, from := range []string{path, backup} {
		s, err := Open(from, clock)
		if err != nil {
			t.Fatal(err)
		}
		var v int
		if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
			t.Fatalf("user_version = %d, %v", v, err)
		}
		p, err := s.Project(ctx, "app")
		if err != nil || p.Repo != appRepo || p.Settings != "" {
			t.Fatalf("migrated project = %+v, %v", p, err)
		}
		if got, err := s.AdoptProject(ctx, "app", "v1\nnow\n"); err != nil || got != "v1\nnow\n" {
			t.Fatalf("adopt = %q, %v", got, err)
		}
		if got := strings.Join(projectEvents(t, s), "|"); got != "project.adopted app" {
			t.Fatalf("events = %q", got)
		}
		dst := filepath.Join(t.TempDir(), "again.db")
		if err := s.Backup(ctx, dst); err != nil {
			t.Fatal(err)
		}
		b, err := Open(dst, clock)
		if err != nil {
			t.Fatal(err)
		}
		if p, err := b.Project(ctx, "app"); err != nil || p.Settings != "v1\nnow\n" {
			t.Fatalf("backed-up project = %+v, %v", p, err)
		}
		b.Close()
		s.Close()
	}
}
