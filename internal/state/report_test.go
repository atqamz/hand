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

func TestReportedSinceQuietFollowsTheEventLog(t *testing.T) {
	s, _ := openTest(t)
	a := runningAttempt(t, s)
	ctx := context.Background()
	check := func(want bool) {
		t.Helper()
		if got, err := s.ReportedSinceQuiet(ctx, a.ID); err != nil || got != want {
			t.Fatalf("ReportedSinceQuiet = %v, %v; want %v", got, err, want)
		}
	}
	check(false)
	if _, err := s.AddReport(ctx, a.ID, ReportProgress, "halfway"); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := s.NoteAttempt(ctx, a.ID, "quiet", "turn ended"); err != nil {
		t.Fatal(err)
	}
	check(false)
	if _, err := s.AddReport(ctx, a.ID, ReportDone, "done"); err != nil {
		t.Fatal(err)
	}
	check(true)
}
