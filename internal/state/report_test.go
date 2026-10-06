package state

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func runningAttempt(t *testing.T, s *Store) Attempt {
	t.Helper()
	task := activeTask(t, s)
	ctx := context.Background()
	spec := claudeSpec
	spec.TaskID = task.ID
	a, err := s.AddAttempt(ctx, spec, "/w")
	if err != nil {
		t.Fatal(err)
	}
	if a, err = s.AttemptRunning(ctx, a.ID, Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "1"}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestReportLifecycle(t *testing.T) {
	s, _ := openTest(t)
	a := runningAttempt(t, s)
	ctx := context.Background()
	for _, bad := range [][2]string{{"finished", "x"}, {ReportDone, "  "}, {ReportDone, strings.Repeat("x", MaxReportBytes+1)}} {
		if _, err := s.AddReport(ctx, a.ID, bad[0], bad[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("AddReport(%q) err = %v, want ErrInvalid", bad[0], err)
		}
	}
	r, err := s.AddReport(ctx, a.ID, ReportDone, "\n  Fixed login  \nCommit abc123\n")
	if err != nil || r.ID != 1 || r.TaskID != a.TaskID || r.Summary() != "Fixed login" {
		t.Fatalf("report = %+v, %v", r, err)
	}
	events, _ := s.RecentEvents(ctx, 1)
	if events[0].Kind != "attempt.reported" || events[0].Detail != "a1: r1 done" {
		t.Fatalf("event = %+v", events[0])
	}
	if n, _ := s.UnackedReportCount(ctx); n != 1 {
		t.Fatalf("unacked = %d", n)
	}
	if _, err := s.AckReport(ctx, r.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ack without actor err = %v", err)
	}
	acked, err := s.AckReport(ctx, r.ID, "supervisor")
	if err != nil || acked.AckedBy != "supervisor" || acked.AckedAt == "" {
		t.Fatalf("ack = %+v, %v", acked, err)
	}
	if _, err := s.AckReport(ctx, r.ID, "supervisor"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second ack err = %v", err)
	}
	if got, _ := s.Reports(ctx, ReportFilter{Unacked: true}, 10); len(got) != 0 {
		t.Fatalf("unacked after ack = %+v", got)
	}
	latest, ok, err := s.LatestReport(ctx, ReportFilter{TaskID: a.TaskID})
	if err != nil || !ok || latest.ID != r.ID {
		t.Fatalf("latest = %+v, %v, %v", latest, ok, err)
	}
	if _, err := s.EndAttempt(ctx, a.ID, AttemptStopped, "stopped"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddReport(ctx, a.ID, ReportDone, "late"); !errors.Is(err, ErrConflict) {
		t.Fatalf("report after stop err = %v", err)
	}
	if _, err := s.Reports(ctx, ReportFilter{}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("limit 0 err = %v", err)
	}
}

func TestRecordQuietLabelsTheTurnInOneTransaction(t *testing.T) {
	s, _ := openTest(t)
	a := runningAttempt(t, s)
	ctx := context.Background()
	check := func(want string) {
		t.Helper()
		if _, got, err := s.RecordQuiet(ctx, a.ID); err != nil || got != want {
			t.Fatalf("RecordQuiet = %q, %v; want %q", got, err, want)
		}
	}
	check("turn ended without a new report")
	if _, err := s.AddReport(ctx, a.ID, ReportDone, "Fixed login"); err != nil {
		t.Fatal(err)
	}
	check("turn ended; reported r1 done")
	check("turn ended without a new report")
	events, _ := s.RecentEvents(ctx, 1)
	if events[0].Kind != "attempt.idle" || events[0].Detail != "a1: turn ended without a new report" {
		t.Fatalf("event = %+v", events[0])
	}
}

func TestTaskCannotBeDoneWithAnUnreadReport(t *testing.T) {
	s, _ := openTest(t)
	a := runningAttempt(t, s)
	ctx := context.Background()
	r, err := s.AddReport(ctx, a.ID, ReportDone, "Fixed login")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndAttempt(ctx, a.ID, AttemptStopped, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, a.TaskID, StatusDone); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "unread report r1") {
		t.Fatalf("done with an unread report err = %v", err)
	}
	if _, err := s.AckReport(ctx, r.ID, "supervisor"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, a.TaskID, StatusDone); err != nil {
		t.Fatalf("done after ack: %v", err)
	}
}

func TestAbandonIgnoresUnreadReports(t *testing.T) {
	s, _ := openTest(t)
	a := runningAttempt(t, s)
	ctx := context.Background()
	if _, err := s.AddReport(ctx, a.ID, ReportStuck, "blocked on credentials"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndAttempt(ctx, a.ID, AttemptStopped, "giving up"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, a.TaskID, StatusAbandoned); err != nil {
		t.Fatalf("abandon with an unread report: %v", err)
	}
}

func TestBoardFactsAreGroupedQueries(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	seedProject(t, s)
	if _, err := s.CreateFleet(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	var tasks []Task
	for _, title := range []string{"one", "two", "three"} {
		task, err := s.AddTask(ctx, "hand", title, "")
		if err != nil {
			t.Fatal(err)
		}
		if task, err = s.Transition(ctx, task.ID, StatusActive); err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
	}
	start := func(taskID int64) Attempt {
		spec := claudeSpec
		spec.TaskID = taskID
		a, err := s.AddAttempt(ctx, spec, "/w")
		if err != nil {
			t.Fatal(err)
		}
		if a, err = s.AttemptRunning(ctx, a.ID, Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "1"}); err != nil {
			t.Fatal(err)
		}
		return a
	}
	first := start(tasks[0].ID)
	if _, err := s.AddReport(ctx, first.ID, ReportDone, "PR: https://github.com/a/b/pull/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndAttempt(ctx, first.ID, AttemptExited, "terminal exited"); err != nil {
		t.Fatal(err)
	}
	second := start(tasks[0].ID)
	third := start(tasks[1].ID)
	for _, step := range []func() error{
		func() error { _, err := s.AddReport(ctx, third.ID, ReportProgress, "halfway"); return err },
		func() error { _, err := s.Ask(ctx, tasks[2].ID, "which?"); return err },
		func() error { _, err := s.Ask(ctx, tasks[2].ID, "and?"); return err },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := s.LatestAttempts(ctx)
	if err != nil || len(latest) != 2 || latest[tasks[0].ID].ID != second.ID || latest[tasks[1].ID].ID != third.ID {
		t.Fatalf("latest = %v, %v", latest, err)
	}
	done, err := s.DoneReportAttempts(ctx)
	if err != nil || !done[first.ID] || done[third.ID] {
		t.Fatalf("done = %v, %v", done, err)
	}
	unread, err := s.UnackedReportTasks(ctx)
	if err != nil || !unread[tasks[0].ID] || !unread[tasks[1].ID] || unread[tasks[2].ID] {
		t.Fatalf("unread = %v, %v", unread, err)
	}
	asked, err := s.OpenDecisionCounts(ctx)
	if err != nil || asked[tasks[2].ID] != 2 || asked[tasks[0].ID] != 0 {
		t.Fatalf("asked = %v, %v", asked, err)
	}
	prs, err := s.ReportsMentioning(ctx, "/pull/", []int64{tasks[0].ID, tasks[2].ID})
	if err != nil || len(prs) != 1 || prs[0].AttemptID != first.ID {
		t.Fatalf("prs = %v, %v", prs, err)
	}
	if other, err := s.ReportsMentioning(ctx, "/pull/", []int64{tasks[1].ID}); err != nil || len(other) != 0 {
		t.Fatalf("reports of tasks not asked for = %v, %v", other, err)
	}
	if none, err := s.ReportsMentioning(ctx, "/pull/", nil); err != nil || len(none) != 0 {
		t.Fatalf("no tasks = %v, %v", none, err)
	}
}

func TestReportsOldestKeepsTheOldestN(t *testing.T) {
	s, _ := openTest(t)
	a := runningAttempt(t, s)
	ctx := context.Background()
	for range 5 {
		if _, err := s.AddReport(ctx, a.ID, ReportProgress, "step"); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(f ReportFilter) []int64 {
		rs, err := s.Reports(ctx, f, 2)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return out
	}
	if got := ids(ReportFilter{Unacked: true}); len(got) != 2 || got[0] != 4 || got[1] != 5 {
		t.Fatalf("newest = %v", got)
	}
	if got := ids(ReportFilter{Unacked: true, Oldest: true}); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("oldest = %v", got)
	}
}
