package state

import (
	"context"
	"errors"
	"slices"
	"testing"
)

var supervisorSpec = SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "medium", Argv: []string{"/bin/claude", "x"}, Session: "0f8fad5b-d9cb-469f-a165-70867728950e"}

var supervisorTerm = Terminal{ServerGeneration: "g1", TerminalID: "t1", PaneID: "3", PID: 42, StartMarker: "900"}

func TestSupervisorLifecycleAllowsOneLiveRow(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	sup, err := s.AddSupervisor(ctx, supervisorSpec)
	if err != nil || sup.ID != 1 || sup.Status != AttemptLaunching || !sup.Live() {
		t.Fatalf("add = %+v, %v", sup, err)
	}
	if _, err := s.SupervisorRunning(ctx, sup.ID, supervisorTerm); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddSupervisor(ctx, supervisorSpec); !errors.Is(err, ErrConflict) {
		t.Fatalf("second live add = %v, want ErrConflict", err)
	}
	if _, err := s.EndSupervisor(ctx, sup.ID, AttemptInterrupted, "luvus server restarted"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.LiveSupervisor(ctx); ok || err != nil {
		t.Fatalf("live after end = %v, %v", ok, err)
	}
	latest, ok, err := s.LatestSupervisor(ctx)
	if err != nil || !ok || latest.ID != sup.ID || latest.Status != AttemptInterrupted || latest.PaneID != "3" {
		t.Fatalf("latest = %+v, %v, %v", latest, ok, err)
	}
	next := supervisorSpec
	next.WakeCursor = 7
	again, err := s.AddSupervisor(ctx, next)
	if err != nil || again.ID != 2 || again.Session != supervisorSpec.Session || again.WakeCursor != 7 {
		t.Fatalf("resume add = %+v, %v", again, err)
	}
	for _, bad := range []SupervisorSpec{{Harness: "gemini", Argv: []string{"x"}}, {Harness: "claude"}} {
		if _, err := s.AddSupervisor(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("add %+v = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestSupervisorEndsFollowTheStatusRules(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	sup, _ := s.AddSupervisor(ctx, supervisorSpec)
	if _, err := s.EndSupervisor(ctx, sup.ID, AttemptExited, "x"); !errors.Is(err, ErrConflict) {
		t.Fatalf("launching to exited = %v", err)
	}
	if _, err := s.SupervisorRunning(ctx, sup.ID, supervisorTerm); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndSupervisor(ctx, sup.ID, AttemptFailed, "x"); !errors.Is(err, ErrConflict) {
		t.Fatalf("running to failed = %v", err)
	}
	if _, err := s.EndSupervisor(ctx, sup.ID, AttemptStopped, "stopped by operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndSupervisor(ctx, sup.ID, AttemptStopped, "again"); !errors.Is(err, ErrConflict) {
		t.Fatalf("ending twice = %v", err)
	}
}

func TestSupervisorInputsQueueAndDeliverOnce(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	a, err := s.AddSupervisorInput(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.AddSupervisorInput(ctx, "second")
	pending, err := s.PendingSupervisorInputs(ctx)
	if err != nil || len(pending) != 2 || pending[0].ID != a.ID || pending[1].Body != "second" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if err := s.DeliverSupervisorInput(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if pending, _ := s.PendingSupervisorInputs(ctx); len(pending) != 1 || pending[0].ID != b.ID {
		t.Fatalf("pending after one delivery = %+v", pending)
	}
	if err := s.DeliverSupervisorInput(ctx, a.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("second delivery = %v", err)
	}
	if _, err := s.AddSupervisorInput(ctx, ""); err == nil {
		t.Fatal("an empty input was stored")
	}
}

func TestWakeCursorOnlyMovesForward(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	sup, _ := s.AddSupervisor(ctx, supervisorSpec)
	if err := s.AdvanceWakeCursor(ctx, sup.ID, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceWakeCursor(ctx, sup.ID, 3); !errors.Is(err, ErrConflict) {
		t.Fatalf("backwards = %v", err)
	}
	if got, _, _ := s.LiveSupervisor(ctx); got.WakeCursor != 5 {
		t.Fatalf("cursor = %d", got.WakeCursor)
	}
}

func TestSupervisorEventsAreRecorded(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	sup, _ := s.AddSupervisor(ctx, supervisorSpec)
	_, _ = s.SupervisorRunning(ctx, sup.ID, supervisorTerm)
	if err := s.SetSupervisorSession(ctx, sup.ID, "abc"); err != nil {
		t.Fatal(err)
	}
	in, _ := s.AddSupervisorInput(ctx, "hello")
	_ = s.DeliverSupervisorInput(ctx, in.ID)
	_, _ = s.EndSupervisor(ctx, sup.ID, AttemptInterrupted, "restart")
	events, err := s.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	want := []string{"supervisor.launching", "supervisor.started", "supervisor.session", "supervisor.input", "supervisor.delivered", "supervisor.interrupted"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("events = %q, want %q", kinds, want)
	}
	if got, _, _ := s.LatestSupervisor(ctx); got.Session != "abc" || SupervisorRef(got.ID) != "s1" {
		t.Fatalf("latest = %+v", got)
	}
}
