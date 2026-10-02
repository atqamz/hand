package state

import (
	"context"
	"testing"
	"time"
)

func TestTrackRecordCountsEndedAttemptsPerSpec(t *testing.T) {
	now := fixed
	path := t.TempDir() + "/hand.db"
	s, err := Open(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	seedProject(t, s)
	if _, err := s.CreateFleet(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	step := func(min int) { now = fixed.Add(time.Duration(min) * time.Minute) }
	task := func(title string) int64 {
		tk, err := s.AddTask(ctx, "hand", title, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Transition(ctx, tk.ID, StatusActive); err != nil {
			t.Fatal(err)
		}
		return tk.ID
	}
	start := func(taskID int64, model string) int64 {
		spec := claudeSpec
		spec.TaskID, spec.Model = taskID, model
		a, err := s.AddAttempt(ctx, spec, "/w")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AttemptRunning(ctx, a.ID, Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "1"}); err != nil {
			t.Fatal(err)
		}
		return a.ID
	}
	note := func(id int64, kind string) {
		if err := s.NoteAttempt(ctx, id, kind, "x"); err != nil {
			t.Fatal(err)
		}
	}
	report := func(id int64, status string) {
		r, err := s.AddReport(ctx, id, status, "body")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AckReport(ctx, r.ID, "supervisor"); err != nil {
			t.Fatal(err)
		}
	}
	end := func(id int64) {
		if _, err := s.EndAttempt(ctx, id, AttemptExited, ""); err != nil {
			t.Fatal(err)
		}
	}
	done := func(taskID int64) {
		if _, err := s.Transition(ctx, taskID, StatusDone); err != nil {
			t.Fatal(err)
		}
	}

	t1 := task("one")
	step(0)
	a1 := start(t1, "sonnet")
	note(a1, "sent")
	note(a1, "sent")
	note(a1, "quiet")
	note(a1, "blocked")
	report(a1, ReportStuck)
	step(5)
	end(a1)
	a2 := start(t1, "sonnet")
	step(15)
	report(a2, ReportDone)
	step(60)
	end(a2)
	done(t1)

	t2 := task("two")
	step(100)
	a3 := start(t2, "opus")
	note(a3, "sent")
	step(130)
	report(a3, ReportDone)
	end(a3)
	done(t2)

	t3 := task("three")
	start(t3, "opus")

	got, err := s.TrackRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Track{
		{Harness: "claude", Model: "opus", Effort: "low", N: 1, FirstTry: 1, Sent: 1, Median: 30, Timed: true},
		{Harness: "claude", Model: "sonnet", Effort: "low", N: 2, FirstTry: 1, Reattempt: 1, Stuck: 1, Sent: 2, Wakes: 2, Median: 10, Timed: true},
	}
	if len(got) != len(want) {
		t.Fatalf("track = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("track[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestTrackRecordEmpty(t *testing.T) {
	s, _ := openTest(t)
	got, err := s.TrackRecord(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("track = %+v, %v", got, err)
	}
}
