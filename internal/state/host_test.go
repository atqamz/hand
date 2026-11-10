package state

import (
	"context"
	"testing"
)

func TestHostHandoverInterruptsOnlyTheNamedLiveAttempts(t *testing.T) {
	s, _ := openTest(t)
	task := activeTask(t, s)
	ctx := context.Background()
	spec := claudeSpec
	spec.TaskID = task.ID
	a, err := s.AddAttempt(ctx, spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttemptRunning(ctx, a.ID, Terminal{TerminalID: "1", PID: 77, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.LastEventSeq(ctx)
	if err := s.HostHandover(ctx, "fleet.took_over", "from laptop", "moved", []int64{a.ID, 999}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Attempt(ctx, a.ID)
	if err != nil || got.Status != AttemptInterrupted || got.Reason != "moved" || got.EndedAt == "" {
		t.Fatalf("attempt = %+v, %v", got, err)
	}
	events, err := s.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range events {
		if e.Seq > before {
			kinds = append(kinds, e.Kind+" "+e.Detail)
		}
	}
	if len(kinds) != 2 || kinds[0] != "attempt.interrupted "+AttemptRef(a.ID)+": moved" || kinds[1] != "fleet.took_over from laptop" {
		t.Fatalf("events = %q", kinds)
	}
	again := before + 2
	if err := s.HostHandover(ctx, "fleet.unlocked", "x", "r", []int64{a.ID}); err != nil {
		t.Fatal(err)
	}
	if last, _ := s.LastEventSeq(ctx); last != again+1 {
		t.Fatalf("a second handover wrote %d events, want only the unlock", last-again)
	}
}

func TestHostHandoverFailsALaunchingAttempt(t *testing.T) {
	s, _ := openTest(t)
	task := activeTask(t, s)
	ctx := context.Background()
	spec := claudeSpec
	spec.TaskID = task.ID
	a, err := s.AddAttempt(ctx, spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.HostHandover(ctx, "fleet.took_over", "from laptop", "moved", []int64{a.ID}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Attempt(ctx, a.ID)
	if err != nil || got.Status != AttemptFailed || got.Reason != "moved" || got.EndedAt == "" {
		t.Fatalf("attempt = %+v, %v", got, err)
	}
}
