package state

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func seedProject(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.AddProject(context.Background(), "hand", "/home/me/hand"); err != nil {
		t.Fatal(err)
	}
}

func TestAddProjectValidatesNameRepoAndDuplicates(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for _, tc := range []struct{ name, repo string }{{"Hand", "/r"}, {"-x", "/r"}, {"ok", "relative/path"}} {
		if _, err := s.AddProject(ctx, tc.name, tc.repo); !errors.Is(err, ErrInvalid) {
			t.Fatalf("AddProject(%q, %q) err = %v, want ErrInvalid", tc.name, tc.repo, err)
		}
	}
	seedProject(t, s)
	if _, err := s.AddProject(ctx, "hand", "/other"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate err = %v, want ErrConflict", err)
	}
}

func TestAddTaskStartsInInboxAndNeedsProject(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.AddTask(ctx, "missing", "Fix login", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project err = %v", err)
	}
	seedProject(t, s)
	for _, title := range []string{"", "  ", "two\nlines"} {
		if _, err := s.AddTask(ctx, "hand", title, ""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("title %q err = %v, want ErrInvalid", title, err)
		}
	}
	task, err := s.AddTask(ctx, "hand", "Fix login", "users can log in again")
	if err != nil || task.ID != 1 || task.Status != StatusInbox {
		t.Fatalf("task = %+v, %v", task, err)
	}
}

func TestTransitionTable(t *testing.T) {
	allowed := map[[2]string]bool{
		{StatusInbox, StatusActive}:     true,
		{StatusInbox, StatusAbandoned}:  true,
		{StatusActive, StatusDone}:      true,
		{StatusActive, StatusAbandoned}: true,
	}
	path := [][]string{
		{StatusInbox},
		{StatusInbox, StatusActive},
		{StatusInbox, StatusActive, StatusDone},
		{StatusInbox, StatusAbandoned},
	}
	for _, from := range path {
		for _, to := range []string{StatusInbox, StatusActive, StatusDone, StatusAbandoned} {
			s, _ := openTest(t)
			seedProject(t, s)
			ctx := context.Background()
			task, err := s.AddTask(ctx, "hand", "Fix login", "")
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range from[1:] {
				if _, err := s.Transition(ctx, task.ID, step); err != nil {
					t.Fatal(err)
				}
			}
			cur := from[len(from)-1]
			_, err = s.Transition(ctx, task.ID, to)
			if allowed[[2]string{cur, to}] != (err == nil) {
				t.Fatalf("%s -> %s err = %v", cur, to, err)
			}
			if err != nil && !errors.Is(err, ErrConflict) {
				t.Fatalf("%s -> %s err = %v, want ErrConflict", cur, to, err)
			}
			got, _ := s.Task(ctx, task.ID)
			if err != nil && got.Status != cur {
				t.Fatalf("refused transition changed status to %s", got.Status)
			}
		}
	}
}

func TestConcurrentStartHasOneWinner(t *testing.T) {
	s, path := openTest(t)
	seedProject(t, s)
	task, err := s.AddTask(context.Background(), "hand", "Fix login", "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := Open(path, clock)
			if err != nil {
				errs <- err
				return
			}
			defer other.Close()
			<-start
			_, err = other.Transition(context.Background(), task.ID, StatusActive)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	var ok, conflict int
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrConflict):
			conflict++
		default:
			t.Fatalf("unexpected err: %v", err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("ok=%d conflict=%d, want 1 and 1", ok, conflict)
	}
}

func TestTransitionRecordsEvent(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	task, _ := s.AddTask(ctx, "hand", "Fix login", "")
	if _, err := s.Transition(ctx, task.ID, StatusActive); err != nil {
		t.Fatal(err)
	}
	events, _ := s.RecentEvents(ctx, 10)
	last := events[len(events)-1]
	if last.Kind != "task.active" || last.TaskID != task.ID || last.Detail != "inbox->active" {
		t.Fatalf("last event = %+v", last)
	}
}
