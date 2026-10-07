package state

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var handRepo, _ = filepath.Abs("/home/me/hand")

func seedProject(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.AddProject(context.Background(), "hand", handRepo); err != nil {
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
	other, _ := filepath.Abs("/other")
	if _, err := s.AddProject(ctx, "hand", other); !errors.Is(err, ErrConflict) {
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

func TestSearchTasks(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	seedProject(t, s)
	other, _ := filepath.Abs("/other")
	if _, err := s.AddProject(ctx, "docs", other); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	finish := func(project, title, goal, to string) Task {
		task, err := s.AddTask(ctx, project, title, goal)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Transition(ctx, task.ID, StatusActive); err != nil {
			t.Fatal(err)
		}
		s.now = func() time.Time { return day }
		if _, err := s.Transition(ctx, task.ID, to); err != nil {
			t.Fatal(err)
		}
		day = day.Add(24 * time.Hour)
		return task
	}
	finish("hand", "Fix Login cookie", "", StatusDone)
	finish("hand", "Write docs", "mention the login page", StatusDone)
	finish("docs", "Drop chore", "", StatusAbandoned)
	finish("hand", "Fix search", "", StatusDone)
	open, err := s.AddTask(ctx, "hand", "Fix inbox", "")
	if err != nil {
		t.Fatal(err)
	}

	refs := func(q TaskQuery) (string, int) {
		t.Helper()
		if q.Limit == 0 {
			q.Limit = 50
		}
		tasks, total, err := s.SearchTasks(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, task := range tasks {
			out = append(out, TaskRef(task.ID))
		}
		return strings.Join(out, " "), total
	}
	done := []string{StatusDone, StatusAbandoned}
	for _, tc := range []struct {
		name  string
		q     TaskQuery
		want  string
		total int
	}{
		{"newest first", TaskQuery{Statuses: done}, "t4 t3 t2 t1", 4},
		{"every word, any case", TaskQuery{Q: "LOGIN fix", Statuses: done}, "t1", 1},
		{"goal", TaskQuery{Q: "login page", Statuses: done}, "t2", 1},
		{"ref", TaskQuery{Q: "t3", Statuses: done}, "t3", 1},
		{"a bare number is not a ref", TaskQuery{Q: "3", Statuses: done}, "", 0},
		{"status", TaskQuery{Statuses: []string{StatusAbandoned}}, "t3", 1},
		{"project", TaskQuery{Project: "hand", Statuses: done}, "t4 t2 t1", 3},
		{"since", TaskQuery{Since: "2026-10-02", Statuses: done}, "t4 t3 t2", 3},
		{"until", TaskQuery{Until: "2026-10-02", Statuses: done}, "t2 t1", 2},
		{"both dates", TaskQuery{Since: "2026-10-02", Until: "2026-10-03", Statuses: done}, "t3 t2", 2},
		{"empty", TaskQuery{Q: "nothing here", Statuses: done}, "", 0},
		{"unfinished fall back to the update", TaskQuery{Statuses: []string{StatusInbox}}, TaskRef(open.ID), 1},
		{"page", TaskQuery{Statuses: done, Limit: 2, Offset: 2}, "t2 t1", 4},
		{"past the end", TaskQuery{Statuses: done, Limit: 2, Offset: 8}, "", 4},
	} {
		if got, total := refs(tc.q); got != tc.want || total != tc.total {
			t.Errorf("%s: got %q of %d, want %q of %d", tc.name, got, total, tc.want, tc.total)
		}
	}
	tasks, _, _ := s.SearchTasks(ctx, TaskQuery{Statuses: done, Limit: 1})
	if tasks[0].FinishedAt != "2026-10-04T12:00:00Z" {
		t.Errorf("finished at = %q", tasks[0].FinishedAt)
	}
	for _, q := range []TaskQuery{{Limit: 0}, {Limit: 1, Statuses: []string{"later"}}, {Limit: 1, Since: "yesterday"}, {Limit: 1, Until: "2026-13-01"}} {
		if _, _, err := s.SearchTasks(ctx, q); !errors.Is(err, ErrInvalid) {
			t.Errorf("SearchTasks(%+v) err = %v, want ErrInvalid", q, err)
		}
	}
}
