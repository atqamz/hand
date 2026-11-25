package state

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"
)

func report(t *testing.T, s *Store, task Task, status string, ack, exit bool) {
	t.Helper()
	ctx := context.Background()
	spec := claudeSpec
	spec.TaskID = task.ID
	a, err := s.AddAttempt(ctx, spec, "/w")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttemptRunning(ctx, a.ID, Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "1"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.AddReport(ctx, a.ID, status, "text")
	if err != nil {
		t.Fatal(err)
	}
	if ack {
		if _, err := s.AckReport(ctx, r.ID, "supervisor"); err != nil {
			t.Fatal(err)
		}
	}
	if exit {
		if _, err := s.EndAttempt(ctx, a.ID, AttemptExited, "done"); err != nil {
			t.Fatal(err)
		}
	}
}

func finished(t *testing.T, s *Store, task Task) {
	t.Helper()
	report(t, s, task, ReportDone, true, true)
}

func TestClosableTasksFollowTheRule(t *testing.T) {
	ctx := context.Background()
	timer := func(waits bool) func(*testing.T, *Store, Task) {
		return func(t *testing.T, s *Store, task Task) {
			finished(t, s, task)
			if _, err := s.AddTimer(ctx, TimerSpec{After: time.Hour, TaskID: task.ID, Waits: waits}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *Store, Task)
		want  bool
	}{
		{"all clear", finished, true},
		{"older report stuck, newest done", func(t *testing.T, s *Store, task Task) {
			report(t, s, task, ReportStuck, true, true)
			finished(t, s, task)
		}, true},
		{"live running attempt", func(t *testing.T, s *Store, task Task) {
			report(t, s, task, ReportDone, true, false)
		}, false},
		{"live launching attempt", func(t *testing.T, s *Store, task Task) {
			finished(t, s, task)
			spec := claudeSpec
			spec.TaskID = task.ID
			if _, err := s.AddAttempt(ctx, spec, "/w"); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"no attempt", func(*testing.T, *Store, Task) {}, false},
		{"attempt exited without a report", func(t *testing.T, s *Store, task Task) {
			spec := claudeSpec
			spec.TaskID = task.ID
			a, err := s.AddAttempt(ctx, spec, "/w")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.EndAttempt(ctx, a.ID, AttemptFailed, "gone"); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"newest report stuck", func(t *testing.T, s *Store, task Task) {
			finished(t, s, task)
			report(t, s, task, ReportStuck, true, true)
		}, false},
		{"newest report progress", func(t *testing.T, s *Store, task Task) {
			finished(t, s, task)
			report(t, s, task, ReportProgress, true, true)
		}, false},
		{"unacked report", func(t *testing.T, s *Store, task Task) { report(t, s, task, ReportDone, false, true) }, false},
		{"older report unacked", func(t *testing.T, s *Store, task Task) {
			report(t, s, task, ReportProgress, false, true)
			finished(t, s, task)
		}, false},
		{"open decision", func(t *testing.T, s *Store, task Task) {
			finished(t, s, task)
			if _, err := s.Ask(ctx, task.ID, "ship it?"); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"answered decision", func(t *testing.T, s *Store, task Task) {
			finished(t, s, task)
			d, err := s.Ask(ctx, task.ID, "ship it?")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Answer(ctx, d.ID, "yes", "operator"); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"armed plain timer", timer(false), false},
		{"armed waits timer", timer(true), false},
		{"cancelled timer", func(t *testing.T, s *Store, task Task) {
			finished(t, s, task)
			tm, err := s.AddTimer(ctx, TimerSpec{After: time.Hour, TaskID: task.ID})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CancelTimer(ctx, tm.ID); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"open sub-task", func(t *testing.T, s *Store, task Task) {
			finished(t, s, task)
			if _, err := s.AddTaskUnder(ctx, task.ID, "hand", "child", ""); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"done sub-task", func(t *testing.T, s *Store, task Task) {
			finished(t, s, task)
			c, err := s.AddTaskUnder(ctx, task.ID, "hand", "child", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Transition(ctx, c.ID, StatusAbandoned); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"parked task", func(t *testing.T, s *Store, task Task) {
			finished(t, s, task)
			park(t, s, task.ID, "later")
		}, false},
		{"done task", func(t *testing.T, s *Store, task Task) {
			finished(t, s, task)
			if _, err := s.Transition(ctx, task.ID, StatusDone); err != nil {
				t.Fatal(err)
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openTest(t)
			task := activeTask(t, s)
			tc.setup(t, s, task)
			got, total, err := s.ClosableTasks(ctx, 10)
			if err != nil {
				t.Fatal(err)
			}
			if listed := len(got) == 1 && got[0].ID == task.ID && got[0].Title == task.Title; listed != tc.want || total != len(got) || (!tc.want && len(got) != 0) {
				t.Fatalf("closable = %+v of %d, want listed %v", got, total, tc.want)
			}
		})
	}
}

func TestClosableTasksCapRowsAndCountAll(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	first := activeTask(t, s)
	finished(t, s, first)
	var want []int64
	want = append(want, first.ID)
	for i := range 3 {
		task, err := s.AddTask(ctx, "hand", "Task "+strconv.Itoa(i), "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Transition(ctx, task.ID, StatusActive); err != nil {
			t.Fatal(err)
		}
		finished(t, s, task)
		want = append(want, task.ID)
	}
	got, total, err := s.ClosableTasks(ctx, 2)
	if err != nil || total != 4 || len(got) != 2 || got[0].ID != want[0] || got[1].ID != want[1] {
		t.Fatalf("closable = %+v of %d, %v; want the first two of %v", got, total, err, want)
	}
	if _, _, err := s.ClosableTasks(ctx, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("limit 0 err = %v, want ErrInvalid", err)
	}
	if ids := []int64{got[0].ID, got[1].ID}; !slices.IsSorted(ids) {
		t.Fatalf("rows are not in id order: %v", ids)
	}
}
