package state

import (
	"context"
	"errors"
	"slices"
	"testing"
)

var claudeSpec = AttemptSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "--model", "sonnet", "fix it"}}

func activeTask(t *testing.T, s *Store) Task {
	t.Helper()
	seedProject(t, s)
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	task, err := s.AddTask(ctx, "hand", "Fix login", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, task.ID, StatusActive); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestAttemptNeedsTheHomesFleet(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	task, err := s.AddTask(ctx, "hand", "Fix login", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, task.ID, StatusActive); err != nil {
		t.Fatal(err)
	}
	spec := claudeSpec
	spec.TaskID = task.ID
	if _, err := s.AddAttempt(ctx, spec, t.TempDir()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("add without a fleet = %v, want ErrNotFound", err)
	}
}

func TestAttemptLifecycle(t *testing.T) {
	s, _ := openTest(t)
	task := activeTask(t, s)
	ctx := context.Background()
	f, err := s.Fleet(ctx)
	if err != nil {
		t.Fatal(err)
	}
	spec := claudeSpec
	spec.TaskID = task.ID
	a, err := s.AddAttempt(ctx, spec, "/home/me/.hand/worktrees")
	if err != nil || a.ID != 1 || a.Status != AttemptLaunching {
		t.Fatalf("add = %+v, %v", a, err)
	}
	if a.Worktree != "/home/me/.hand/worktrees/t1-a1" || a.Branch != "hand/"+f.ID+"/t1-a1" {
		t.Fatalf("layout = %s %s", a.Worktree, a.Branch)
	}
	term := Terminal{ServerGeneration: "g1", TerminalID: "tid", PaneID: "2", PID: 42, StartMarker: "900"}
	if _, err := s.AttemptRunning(ctx, a.ID, term); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttemptRunning(ctx, a.ID, term); !errors.Is(err, ErrConflict) {
		t.Fatalf("second running err = %v", err)
	}
	if _, err := s.CleanAttempt(ctx, a.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("clean live err = %v", err)
	}
	ended, err := s.EndAttempt(ctx, a.ID, AttemptStopped, "stopped by operator")
	if err != nil || ended.Status != AttemptStopped || ended.EndedAt == "" {
		t.Fatalf("end = %+v, %v", ended, err)
	}
	if _, err := s.EndAttempt(ctx, a.ID, AttemptExited, "late"); !errors.Is(err, ErrConflict) {
		t.Fatalf("end twice err = %v", err)
	}
	got, err := s.Attempt(ctx, a.ID)
	if err != nil || got.Terminal != term || !slices.Equal(got.Argv, spec.Argv) || got.Reason != "stopped by operator" {
		t.Fatalf("read back = %+v, %v", got, err)
	}
	if _, err := s.CleanAttempt(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanAttempt(ctx, a.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("clean twice err = %v", err)
	}
	if err := s.NoteAttempt(ctx, a.ID, "sent", "12 bytes"); err != nil {
		t.Fatal(err)
	}
	events, _ := s.RecentEvents(ctx, 1)
	if events[0].Kind != "attempt.sent" || events[0].Detail != "a1: 12 bytes" {
		t.Fatalf("note event = %+v", events[0])
	}
}

func TestAddAttemptNeedsAnActiveTaskWithoutALiveAttempt(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	task, _ := s.AddTask(ctx, "hand", "Fix login", "")
	spec := claudeSpec
	spec.TaskID = task.ID
	if _, err := s.AddAttempt(ctx, spec, "/w"); !errors.Is(err, ErrConflict) {
		t.Fatalf("inbox task err = %v", err)
	}
	missing := spec
	missing.TaskID = 99
	if _, err := s.AddAttempt(ctx, missing, "/w"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task err = %v", err)
	}
	if _, err := s.Transition(ctx, task.ID, StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddAttempt(ctx, spec, "/w"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddAttempt(ctx, spec, "/w"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second live attempt err = %v", err)
	}
	bad := spec
	bad.Harness = "gemini"
	if _, err := s.AddAttempt(ctx, bad, "/w"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad harness err = %v", err)
	}
}

func TestTaskCannotCloseWithALiveAttempt(t *testing.T) {
	s, _ := openTest(t)
	task := activeTask(t, s)
	ctx := context.Background()
	spec := claudeSpec
	spec.TaskID = task.ID
	a, err := s.AddAttempt(ctx, spec, "/w")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, task.ID, StatusDone); !errors.Is(err, ErrConflict) {
		t.Fatalf("done with live attempt err = %v", err)
	}
	if _, err := s.EndAttempt(ctx, a.ID, AttemptFailed, "launch failed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, task.ID, StatusDone); err != nil {
		t.Fatal(err)
	}
}

func TestAttemptListsAndLatest(t *testing.T) {
	s, _ := openTest(t)
	task := activeTask(t, s)
	ctx := context.Background()
	spec := claudeSpec
	spec.TaskID = task.ID
	for range 3 {
		a, err := s.AddAttempt(ctx, spec, "/w")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.EndAttempt(ctx, a.ID, AttemptFailed, "x"); err != nil {
			t.Fatal(err)
		}
	}
	last, err := s.AddAttempt(ctx, spec, "/w")
	if err != nil {
		t.Fatal(err)
	}
	live, err := s.LiveAttempts(ctx)
	if err != nil || len(live) != 1 || live[0].ID != last.ID {
		t.Fatalf("live = %+v, %v", live, err)
	}
	recent, err := s.Attempts(ctx, task.ID, 2)
	if err != nil || len(recent) != 2 || recent[0].ID != 3 || recent[1].ID != 4 {
		t.Fatalf("recent = %+v, %v", recent, err)
	}
	latest, ok, err := s.LatestAttempt(ctx, task.ID)
	if err != nil || !ok || latest.ID != 4 {
		t.Fatalf("latest = %+v, %v, %v", latest, ok, err)
	}
	if _, err := s.Attempts(ctx, 0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("limit 0 err = %v", err)
	}
	if _, err := s.Project(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project err = %v", err)
	}
	if p, err := s.Project(ctx, "hand"); err != nil || p.Repo != "/home/me/hand" {
		t.Fatalf("project = %+v, %v", p, err)
	}
}
