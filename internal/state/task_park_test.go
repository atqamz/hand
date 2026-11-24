package state

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func eventLines(t *testing.T, s *Store) []string {
	t.Helper()
	events, err := s.EventsAfter(context.Background(), 0, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		out = append(out, e.Kind+" "+e.Detail+" "+TaskRef(e.TaskID))
	}
	return out
}

func park(t *testing.T, s *Store, id int64, note string) {
	t.Helper()
	if _, err := s.Transition(context.Background(), id, StatusInbox, TransitionOpts{Note: note}); err != nil {
		t.Fatal(err)
	}
}

func TestParkMovesAnActiveTaskToInboxAndRecordsTheNote(t *testing.T) {
	for note, want := range map[string]string{"": "active->inbox", "  paused by operator ": "active->inbox: paused by operator"} {
		s, _ := openTest(t)
		task := activeTask(t, s)
		ctx := context.Background()
		got, err := s.Transition(ctx, task.ID, StatusInbox, TransitionOpts{Note: note})
		if err != nil || got.Status != StatusInbox {
			t.Fatalf("park = %+v, %v", got, err)
		}
		lines := eventLines(t, s)
		if last := lines[len(lines)-1]; last != "task.inbox "+want+" t1" {
			t.Fatalf("last event = %q, want detail %q", last, want)
		}
		if _, err := s.Transition(ctx, task.ID, StatusInbox); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "task t1 is inbox and cannot become inbox") {
			t.Fatalf("park twice err = %v", err)
		}
	}
}

func TestParkRefusesNonActiveTasks(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	inbox, _ := s.AddTask(ctx, "hand", "Inbox", "")
	done, _ := s.AddTask(ctx, "hand", "Done", "")
	gone, _ := s.AddTask(ctx, "hand", "Gone", "")
	_, _ = s.Transition(ctx, done.ID, StatusActive)
	_, _ = s.Transition(ctx, done.ID, StatusDone)
	_, _ = s.Transition(ctx, gone.ID, StatusAbandoned)
	for _, id := range []int64{inbox.ID, done.ID, gone.ID} {
		if _, err := s.Transition(ctx, id, StatusInbox); !errors.Is(err, ErrConflict) {
			t.Fatalf("park %s err = %v, want ErrConflict", TaskRef(id), err)
		}
	}
	if _, err := s.Transition(ctx, 99, StatusInbox); !errors.Is(err, ErrNotFound) {
		t.Fatalf("park unknown err = %v", err)
	}
	if got, _ := s.ParkedTasks(ctx); len(got) != 0 {
		t.Fatalf("parked = %v, want none", got)
	}
}

func TestParkNoteRules(t *testing.T) {
	s, _ := openTest(t)
	task := activeTask(t, s)
	ctx := context.Background()
	for name, note := range map[string]string{
		"long":    strings.Repeat("漢", MaxTaskNote+1),
		"newline": "two\nlines",
		"tab":     "a\tb",
		"escape":  "a\x1b[31mb",
		"invalid": "a\xffb",
	} {
		if _, err := s.Transition(ctx, task.ID, StatusInbox, TransitionOpts{Note: note}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s note err = %v, want ErrInvalid", name, err)
		}
	}
	if got, _ := s.Task(ctx, task.ID); got.Status != StatusActive {
		t.Fatalf("refused note changed status to %s", got.Status)
	}
	for _, to := range []string{StatusDone, StatusAbandoned} {
		if _, err := s.Transition(ctx, task.ID, to, TransitionOpts{Note: "why"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s with a note err = %v, want ErrInvalid", to, err)
		}
	}
	if _, err := s.Transition(ctx, task.ID, StatusInbox, TransitionOpts{Note: strings.Repeat("漢", MaxTaskNote)}); err != nil {
		t.Fatalf("200 characters: %v", err)
	}
}

func TestTransitionTakesOneSetOfOptions(t *testing.T) {
	s, _ := openTest(t)
	task := activeTask(t, s)
	if _, err := s.Transition(context.Background(), task.ID, StatusInbox, TransitionOpts{}, TransitionOpts{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("two option sets err = %v, want ErrInvalid", err)
	}
}

func TestParkRefusesALiveAttemptAndChangesNothing(t *testing.T) {
	for _, running := range []bool{false, true} {
		s, _ := movingStore(t)
		task := activeTask(t, s)
		ctx := context.Background()
		spec := claudeSpec
		spec.TaskID = task.ID
		a, err := s.AddAttempt(ctx, spec, "/w")
		if err != nil {
			t.Fatal(err)
		}
		if running {
			if _, err := s.AttemptRunning(ctx, a.ID, Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "1"}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.AddTimer(ctx, TimerSpec{After: time.Hour, TaskID: task.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Ask(ctx, task.ID, "Keep it?"); err != nil {
			t.Fatal(err)
		}
		before := eventLines(t, s)
		_, err = s.Transition(ctx, task.ID, StatusInbox, TransitionOpts{Note: "wait"})
		if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "task t1 has live attempt a1; stop it first") {
			t.Fatalf("running=%v park err = %v", running, err)
		}
		if got, _ := s.Task(ctx, task.ID); got.Status != StatusActive {
			t.Fatalf("status = %s, want active", got.Status)
		}
		if n, _ := s.OpenDecisionCount(ctx, 0); n != 1 {
			t.Fatalf("open decisions = %d, want 1", n)
		}
		if got := armedIDs(t, s); len(got) != 1 {
			t.Fatalf("armed = %q, want 1", got)
		}
		if after := eventLines(t, s); !slices.Equal(before, after) {
			t.Fatalf("refused park wrote events: %q", after[len(before):])
		}
	}
}

func TestParkCancelsTimersAndWithdrawsDecisionsInOneStep(t *testing.T) {
	s, _ := movingStore(t)
	one := activeTask(t, s)
	ctx := context.Background()
	two, _ := s.AddTask(ctx, "hand", "Write docs", "")
	for _, spec := range []TimerSpec{{Every: time.Hour, TaskID: one.ID}, {After: time.Hour}, {After: time.Hour, TaskID: two.ID}, {After: 2 * time.Hour, TaskID: one.ID, Waits: true}} {
		if _, err := s.AddTimer(ctx, spec); err != nil {
			t.Fatal(err)
		}
	}
	d, err := s.Ask(ctx, one.ID, "Keep the old cookie name?")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ask(ctx, two.ID, "Other task?"); err != nil {
		t.Fatal(err)
	}
	park(t, s, one.ID, "")
	if got := armedIDs(t, s); !slices.Equal(got, []string{"w2 2026-09-26T01:00:00Z", "w3 2026-09-26T01:00:00Z"}) {
		t.Fatalf("armed = %q", got)
	}
	if n, _ := s.OpenDecisionCount(ctx, one.ID); n != 0 {
		t.Fatalf("open decisions of the parked task = %d", n)
	}
	if n, _ := s.OpenDecisionCount(ctx, two.ID); n != 1 {
		t.Fatalf("open decisions of the other task = %d", n)
	}
	if _, err := s.Answer(ctx, d.ID, "yes", "operator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("answer after park err = %v", err)
	}
	lines := eventLines(t, s)
	tail := lines[len(lines)-4:]
	for _, want := range []string{"task.inbox active->inbox t1", "decision.withdrawn " + DecisionRef(d.ID) + " t1", "timer.cancelled w1 t1", "timer.cancelled w4 t1"} {
		if !slices.Contains(tail, want) {
			t.Fatalf("events = %q, missing %q", tail, want)
		}
	}
	if waits, _ := s.Waits(ctx); len(waits) != 0 {
		t.Fatalf("waits = %v, want none", waits)
	}
}

func TestParkKeepsReportsAttemptsPlanAttachmentsAndFamily(t *testing.T) {
	s, _ := openTest(t)
	a := runningAttempt(t, s)
	ctx := context.Background()
	parent, _ := s.AddTask(ctx, "hand", "Parent", "")
	child, _ := s.AddTaskUnder(ctx, a.TaskID, "hand", "Child", "")
	if _, err := s.SetParent(ctx, a.TaskID, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, child.ID, StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPlan(ctx, a.TaskID, "1. fix it"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachFiles(ctx, a.TaskID, []string{"spec.md"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.AddReport(ctx, a.ID, ReportDone, "Fixed login")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndAttempt(ctx, a.ID, AttemptStopped, "done"); err != nil {
		t.Fatal(err)
	}
	park(t, s, a.TaskID, "")
	got, _ := s.Task(ctx, a.TaskID)
	if got.ParentID != parent.ID || got.Status != StatusInbox {
		t.Fatalf("task = %+v", got)
	}
	if c, _ := s.Task(ctx, child.ID); c.Status != StatusActive || c.ParentID != a.TaskID {
		t.Fatalf("child = %+v, want active under the parked task", c)
	}
	if _, ok, _ := s.CurrentPlan(ctx, a.TaskID); !ok {
		t.Fatal("plan lost")
	}
	if files, _ := s.TaskFiles(ctx, a.TaskID); len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	if rep, _ := s.Report(ctx, r.ID); rep.AckedAt != "" {
		t.Fatal("report was acked by park")
	}
	if at, _ := s.Attempt(ctx, a.ID); at.Status != AttemptStopped || at.CleanedAt != "" {
		t.Fatalf("attempt = %+v", at)
	}
}

func TestParkedTaskTakesNoAttemptUntilStartedAndContinuesAfterClean(t *testing.T) {
	s, _ := openTest(t)
	task := activeTask(t, s)
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
	if _, err := s.EndAttempt(ctx, a.ID, AttemptStopped, "paused"); err != nil {
		t.Fatal(err)
	}
	park(t, s, task.ID, "")
	if _, err := s.AddAttempt(ctx, spec, "/w"); !errors.Is(err, ErrConflict) {
		t.Fatalf("attempt on a parked task err = %v, want ErrConflict", err)
	}
	if _, err := s.Transition(ctx, task.ID, StatusActive); err != nil {
		t.Fatal(err)
	}
	lines := eventLines(t, s)
	if last := lines[len(lines)-1]; last != "task.active inbox->active t1" {
		t.Fatalf("last event = %q", last)
	}
	cont := spec
	cont.Continues = a.ID
	if _, err := s.AddAttempt(ctx, cont, "/w"); !errors.Is(err, ErrConflict) {
		t.Fatalf("continue before clean err = %v, want ErrConflict", err)
	}
	if _, err := s.CleanAttempt(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	next, err := s.AddAttempt(ctx, cont, "/w")
	if err != nil || next.Continues != a.ID || next.Branch != a.Branch {
		t.Fatalf("continue = %+v, %v", next, err)
	}
}

func TestParkedTasksReturnsTheLatestNoteOfInboxTasksOnly(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	var ids []int64
	for _, title := range []string{"Never parked", "Parked bare", "Parked twice", "Restarted"} {
		task, _ := s.AddTask(ctx, "hand", title, "")
		_, _ = s.Transition(ctx, task.ID, StatusActive)
		ids = append(ids, task.ID)
	}
	if _, err := s.Transition(ctx, ids[0], StatusInbox, TransitionOpts{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids[1:] {
		park(t, s, id, "")
	}
	for _, step := range []struct {
		id   int64
		note string
	}{{ids[2], "first reason"}, {ids[2], "second reason"}, {ids[3], "gone"}} {
		if _, err := s.Transition(ctx, step.id, StatusActive); err != nil {
			t.Fatal(err)
		}
		park(t, s, step.id, step.note)
	}
	if _, err := s.Transition(ctx, ids[3], StatusActive); err != nil {
		t.Fatal(err)
	}
	fresh, _ := s.AddTask(ctx, "hand", "Fresh inbox", "")
	got, err := s.ParkedTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]string{ids[0]: "", ids[1]: "", ids[2]: "second reason"}
	if len(got) != len(want) {
		t.Fatalf("parked = %v, want %v", got, want)
	}
	for id, note := range want {
		if n, ok := got[id]; !ok || n != note {
			t.Fatalf("parked = %v, want %v", got, want)
		}
	}
	if _, ok := got[fresh.ID]; ok {
		t.Fatal("a never-parked inbox task is parked")
	}
}

func TestParkedTaskIsNotUnwatchedButItsActiveSiblingIs(t *testing.T) {
	s, _ := openTest(t)
	one := activeTask(t, s)
	ctx := context.Background()
	two, _ := s.AddTask(ctx, "hand", "Sibling", "")
	if _, err := s.Transition(ctx, two.ID, StatusActive); err != nil {
		t.Fatal(err)
	}
	park(t, s, one.ID, "")
	u, err := s.Unwatched(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(u.Tasks, []int64{two.ID}) {
		t.Fatalf("unwatched = %v, want only %d", u.Tasks, two.ID)
	}
}

func TestConcurrentParkHasOneWinner(t *testing.T) {
	s, path := openTest(t)
	task := activeTask(t, s)
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
			_, err = other.Transition(context.Background(), task.ID, StatusInbox, TransitionOpts{Note: "race"})
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
	n := 0
	for _, l := range eventLines(t, s) {
		if strings.HasPrefix(l, "task.inbox ") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("task.inbox events = %d, want 1", n)
	}
}
