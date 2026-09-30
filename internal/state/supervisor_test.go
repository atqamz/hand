package state

import (
	"context"
	"errors"
	"slices"
	"strings"
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
	if err := s.DeliverSupervisorInput(ctx, a.ID, false); err != nil {
		t.Fatal(err)
	}
	if pending, _ := s.PendingSupervisorInputs(ctx); len(pending) != 1 || pending[0].ID != b.ID {
		t.Fatalf("pending after one delivery = %+v", pending)
	}
	if err := s.DeliverSupervisorInput(ctx, a.ID, false); !errors.Is(err, ErrConflict) {
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
	_ = s.DeliverSupervisorInput(ctx, in.ID, false)
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

func TestAddSupervisorInputRefusesWhatSendRefuses(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	long := make([]byte, MaxMessageBytes+1)
	for i := range long {
		long[i] = 'a'
	}
	for _, bad := range []string{"[hand v1 wake]\nx", "\n [hand v1 wake]", "a\x00b", "a\x1b[31mb", "a\rb", "   \n", "\xff", string(long)} {
		if _, err := s.AddSupervisorInput(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("AddSupervisorInput(%q) err = %v", bad, err)
		}
	}
	if pending, err := s.PendingSupervisorInputs(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("pending = %v, %v", pending, err)
	}
	if _, err := s.AddSupervisorInput(ctx, "tabs\tand\nlines"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(SupervisorKeys, "esc") || len(SupervisorKeys) != 7 {
		t.Fatalf("keys = %q", SupervisorKeys)
	}
}

func runningSupervisor(t *testing.T, s *Store) Supervisor {
	t.Helper()
	ctx := context.Background()
	sup, err := s.AddSupervisor(ctx, supervisorSpec)
	if err != nil {
		t.Fatal(err)
	}
	if sup, err = s.SupervisorRunning(ctx, sup.ID, supervisorTerm); err != nil {
		t.Fatal(err)
	}
	return sup
}

func TestASupervisorSwitchIsOneAtATime(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	sup := runningSupervisor(t, s)
	got, err := s.SetSupervisorSwitch(ctx, sup.ID, "opus", "high")
	if err != nil || !got.Switching() || got.SwitchModel != "opus" || got.SwitchEffort != "high" {
		t.Fatalf("set = %+v, %v", got, err)
	}
	if latest, _, _ := s.LatestSupervisor(ctx); latest.SwitchModel != "opus" || latest.SwitchEffort != "high" {
		t.Fatalf("stored = %+v", latest)
	}
	if _, err := s.SetSupervisorSwitch(ctx, sup.ID, "haiku", "low"); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "--cancel") {
		t.Fatalf("second set = %v", err)
	}
	if got, err = s.CancelSupervisorSwitch(ctx, sup.ID); err != nil || got.Switching() || got.SwitchEffort != "" {
		t.Fatalf("cancel = %+v, %v", got, err)
	}
	if _, err := s.CancelSupervisorSwitch(ctx, sup.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("second cancel = %v", err)
	}
	events, err := s.EventsAfter(ctx, 0, []string{"supervisor.switch"}, 10)
	if err != nil || len(events) != 2 || events[0].Detail != "s1: to opus high" || events[1].Detail != "s1: canceled" {
		t.Fatalf("events = %+v, %v", events, err)
	}
}

func TestASwitchNeedsARunningSupervisor(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	sup := runningSupervisor(t, s)
	if _, err := s.SetSupervisorSwitch(ctx, sup.ID, "opus", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty effort = %v", err)
	}
	if _, err := s.EndSupervisor(ctx, sup.ID, AttemptStopped, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSupervisorSwitch(ctx, sup.ID, "opus", "high"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stopped = %v", err)
	}
}

func TestASupervisorStartsWithoutASwitch(t *testing.T) {
	s, _ := openTest(t)
	if sup := runningSupervisor(t, s); sup.Switching() {
		t.Fatalf("new row = %+v", sup)
	}
}

func TestSessionSupervisorsListsOneSessionInOrder(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	first := runningSupervisor(t, s)
	if _, err := s.EndSupervisor(ctx, first.ID, AttemptStopped, "switch"); err != nil {
		t.Fatal(err)
	}
	second := runningSupervisor(t, s)
	if _, err := s.EndSupervisor(ctx, second.ID, AttemptStopped, "done"); err != nil {
		t.Fatal(err)
	}
	other := supervisorSpec
	other.Session = "1b4e28ba-2fa1-11d2-883f-0016d3cca427"
	if _, err := s.AddSupervisor(ctx, other); err != nil {
		t.Fatal(err)
	}
	got, err := s.SessionSupervisors(ctx, supervisorSpec.Session)
	if err != nil || len(got) != 2 || got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("session supervisors = %+v, %v", got, err)
	}
}

func TestSupervisorsRunOnlyOnSupervisorHarnesses(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.AddSupervisor(ctx, SupervisorSpec{Harness: "agy", Model: "gemini-3.8-flash-low", Argv: []string{"/bin/agy"}}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), `harness "agy" runs workers only; the supervisor runs on claude, codex or opencode (atqamz/hand#736)`) {
		t.Fatalf("an agy supervisor = %v", err)
	}
	task := activeTask(t, s)
	if _, err := s.AddAttempt(ctx, AttemptSpec{TaskID: task.ID, Harness: "agy", Model: "gemini-3.8-flash-low", Argv: []string{"/bin/agy", "-i", "x"}}, t.TempDir()); err != nil {
		t.Fatalf("an agy worker = %v", err)
	}
}
