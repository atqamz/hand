package state

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

const threeDays = 3 * 24 * time.Hour

func twoActiveTasks(t *testing.T, s *Store) (Task, Task) {
	t.Helper()
	one := activeTask(t, s)
	two, err := s.AddTask(context.Background(), "hand", "Write docs", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(context.Background(), two.ID, StatusActive); err != nil {
		t.Fatal(err)
	}
	return one, two
}

func unwatchedIDs(t *testing.T, s *Store, managed bool) []int64 {
	t.Helper()
	u, err := s.Unwatched(context.Background(), managed)
	if err != nil {
		t.Fatal(err)
	}
	return u.Tasks
}

func TestAWaitingTaskLeavesUnwatchedButItsNeighbourStaysFlagged(t *testing.T) {
	s, _ := movingStore(t)
	ctx := context.Background()
	one, two := twoActiveTasks(t, s)
	w, err := s.AddTimer(ctx, TimerSpec{After: threeDays, TaskID: one.ID, Waits: true, Note: "outside PR"})
	if err != nil || !w.Waits || w.TaskID != one.ID {
		t.Fatalf("waits timer = %+v, %v", w, err)
	}
	if got := unwatchedIDs(t, s, true); !slices.Equal(got, []int64{two.ID}) {
		t.Fatalf("one task waits: unwatched = %v, want only %d", got, two.ID)
	}
	if u, err := s.Unwatched(ctx, true); err != nil || u.Far.ID != 0 {
		t.Fatalf("a waits timer is no far timer for the stall text: %+v, %v", u, err)
	}
	if _, err := s.AddTimer(ctx, TimerSpec{After: threeDays, TaskID: two.ID, Waits: true}); err != nil {
		t.Fatal(err)
	}
	if got := unwatchedIDs(t, s, true); len(got) != 0 {
		t.Fatalf("both tasks wait: unwatched = %v", got)
	}
}

func TestAWaitsTimerDueSoonDoesNotHideAnotherIdleTask(t *testing.T) {
	s, _ := movingStore(t)
	one, two := twoActiveTasks(t, s)
	if _, err := s.AddTimer(context.Background(), TimerSpec{After: time.Hour, TaskID: one.ID, Waits: true}); err != nil {
		t.Fatal(err)
	}
	if got := unwatchedIDs(t, s, true); !slices.Equal(got, []int64{two.ID}) {
		t.Fatalf("unwatched = %v, want only %d", got, two.ID)
	}
}

func TestAnOrdinaryTimerOnATaskStillHidesTheFleet(t *testing.T) {
	s, _ := movingStore(t)
	one, _ := twoActiveTasks(t, s)
	if _, err := s.AddTimer(context.Background(), TimerSpec{After: time.Hour, TaskID: one.ID}); err != nil {
		t.Fatal(err)
	}
	if got := unwatchedIDs(t, s, true); len(got) != 0 {
		t.Fatalf("unwatched = %v", got)
	}
}

func TestAWaitDoesNotChangeWhatAnOrdinaryTimerWatches(t *testing.T) {
	s, _ := movingStore(t)
	ctx := context.Background()
	one, two := twoActiveTasks(t, s)
	if _, err := s.AddTimer(ctx, TimerSpec{After: threeDays, TaskID: one.ID, Waits: true}); err != nil {
		t.Fatal(err)
	}
	poll, err := s.AddTimer(ctx, TimerSpec{After: time.Hour, TaskID: two.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := unwatchedIDs(t, s, true); len(got) != 0 {
		t.Fatalf("an ordinary timer due within 6h beside a wait: unwatched = %v", got)
	}
	if _, err := s.CancelTimer(ctx, poll.ID); err != nil {
		t.Fatal(err)
	}
	if got := unwatchedIDs(t, s, true); !slices.Equal(got, []int64{two.ID}) {
		t.Fatalf("after the ordinary timer ended: unwatched = %v", got)
	}
}

func TestAnOverdueWaitKeepsItsTaskOutUntilItFires(t *testing.T) {
	s, advance := movingStore(t)
	one, two := twoActiveTasks(t, s)
	if _, err := s.AddTimer(context.Background(), TimerSpec{After: time.Hour, TaskID: one.ID, Waits: true}); err != nil {
		t.Fatal(err)
	}
	advance(2 * time.Hour)
	if got := unwatchedIDs(t, s, true); !slices.Equal(got, []int64{two.ID}) {
		t.Fatalf("a wait past its due time, not yet fired: unwatched = %v", got)
	}
}

func TestAWaitsTimerCountsOnlyWhileAManagedSupervisorRuns(t *testing.T) {
	s, _ := movingStore(t)
	one, two := twoActiveTasks(t, s)
	if _, err := s.AddTimer(context.Background(), TimerSpec{After: threeDays, TaskID: one.ID, Waits: true}); err != nil {
		t.Fatal(err)
	}
	if got := unwatchedIDs(t, s, false); !slices.Equal(got, []int64{one.ID, two.ID}) {
		t.Fatalf("without a managed supervisor: unwatched = %v", got)
	}
}

func TestAWaitsTimerFiresOnceAndTheTaskIsUnwatchedAgain(t *testing.T) {
	s, advance := movingStore(t)
	ctx := context.Background()
	task := activeTask(t, s)
	if _, err := s.AddTimer(ctx, TimerSpec{After: threeDays, TaskID: task.ID, Waits: true, Note: "outside PR"}); err != nil {
		t.Fatal(err)
	}
	if got := unwatchedIDs(t, s, true); len(got) != 0 {
		t.Fatalf("armed 3d out: unwatched = %v", got)
	}
	advance(threeDays - time.Minute)
	if fired, err := s.FireTimers(ctx); err != nil || len(fired) != 0 || len(unwatchedIDs(t, s, true)) != 0 {
		t.Fatalf("before it is due: fired %q, %v, unwatched %v", fired, err, unwatchedIDs(t, s, true))
	}
	advance(time.Minute)
	if fired, err := s.FireTimers(ctx); err != nil || !slices.Equal(fired, []string{"w1: outside PR"}) {
		t.Fatalf("fired = %q, %v", fired, err)
	}
	u, err := s.Unwatched(ctx, true)
	if err != nil || !slices.Equal(u.Tasks, []int64{task.ID}) || u.Idle != 0 {
		t.Fatalf("after the fire: %+v, %v", u, err)
	}
	if fired, err := s.FireTimers(ctx); err != nil || len(fired) != 0 {
		t.Fatalf("fired twice: %q, %v", fired, err)
	}
	if waits, err := s.Waits(ctx); err != nil || len(waits) != 0 {
		t.Fatalf("waits after the fire = %v, %v", waits, err)
	}
	want := []string{
		"timer.armed w1, due 2026-09-29T00:00:00Z: outside PR (t1)",
		"timer.fired w1: outside PR (t1)",
	}
	if got := timerEvents(t, s); !slices.Equal(got, want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestALateWaitsTimerFiresOnceMarkedLate(t *testing.T) {
	s, advance := movingStore(t)
	ctx := context.Background()
	task := activeTask(t, s)
	if _, err := s.AddTimer(ctx, TimerSpec{After: threeDays, TaskID: task.ID, Waits: true, Note: "outside PR"}); err != nil {
		t.Fatal(err)
	}
	advance(threeDays + 2*time.Hour)
	if fired, err := s.FireTimers(ctx); err != nil || !slices.Equal(fired, []string{"w1, 2h late: outside PR"}) {
		t.Fatalf("fired = %q, %v", fired, err)
	}
}

func TestAWaitsTimerNeedsATaskOneShotAndAtMostThirtyDays(t *testing.T) {
	s, _ := movingStore(t)
	ctx := context.Background()
	task := activeTask(t, s)
	for name, c := range map[string]struct {
		spec TimerSpec
		err  error
		text string
	}{
		"no task":      {TimerSpec{After: threeDays, Waits: true}, ErrInvalid, "a waits timer needs a task"},
		"repeating":    {TimerSpec{Every: threeDays, TaskID: task.ID, Waits: true}, ErrInvalid, "a waits timer fires once"},
		"past the cap": {TimerSpec{After: MaxWait + time.Minute, TaskID: task.ID, Waits: true}, ErrInvalid, "a waits timer is due within 30d"},
		"at past cap":  {TimerSpec{At: fixed.Add(MaxWait + time.Hour), TaskID: task.ID, Waits: true}, ErrInvalid, "a waits timer is due within 30d"},
		"missing task": {TimerSpec{After: threeDays, TaskID: 99, Waits: true}, ErrNotFound, "task t99"},
	} {
		if _, err := s.AddTimer(ctx, c.spec); !errors.Is(err, c.err) || !strings.Contains(err.Error(), c.text) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if w, err := s.AddTimer(ctx, TimerSpec{After: MaxWait, TaskID: task.ID, Waits: true}); err != nil || !w.Due.Equal(fixed.Add(MaxWait)) {
		t.Fatalf("exactly at the cap = %+v, %v", w, err)
	}
	if _, err := s.AddTimer(ctx, TimerSpec{After: threeDays, TaskID: task.ID, Waits: true}); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "task t1 already waits (w1)") {
		t.Fatalf("a second wait on one task = %v", err)
	}
	if _, err := s.AddTimer(ctx, TimerSpec{After: 31 * 24 * time.Hour, TaskID: task.ID}); err != nil {
		t.Fatalf("an ordinary timer keeps its own limits: %v", err)
	}
	if _, err := s.Transition(ctx, task.ID, StatusDone); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTimer(ctx, TimerSpec{After: threeDays, TaskID: task.ID, Waits: true}); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "already done") {
		t.Fatalf("a finished task = %v", err)
	}
}

func TestAWaitEndsWithItsTask(t *testing.T) {
	for _, to := range []string{StatusDone, StatusAbandoned} {
		s, _ := movingStore(t)
		ctx := context.Background()
		one, two := twoActiveTasks(t, s)
		for _, id := range []int64{one.ID, two.ID} {
			if _, err := s.AddTimer(ctx, TimerSpec{After: threeDays, TaskID: id, Waits: true}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Transition(ctx, one.ID, to); err != nil {
			t.Fatal(err)
		}
		waits, err := s.Waits(ctx)
		if err != nil || len(waits) != 1 || waits[two.ID].ID != 2 {
			t.Fatalf("%s: waits = %+v, %v", to, waits, err)
		}
		if got := timerEvents(t, s); got[len(got)-1] != "timer.cancelled w1 (t1)" {
			t.Fatalf("%s: events = %q", to, got)
		}
	}
}

func TestStartingAnAttemptEndsTheTaskWaitOnly(t *testing.T) {
	s, _ := movingStore(t)
	ctx := context.Background()
	one, two := twoActiveTasks(t, s)
	for _, spec := range []TimerSpec{
		{After: threeDays, TaskID: one.ID, Waits: true},
		{After: threeDays, TaskID: two.ID, Waits: true},
		{Every: time.Hour, TaskID: one.ID},
	} {
		if _, err := s.AddTimer(ctx, spec); err != nil {
			t.Fatal(err)
		}
	}
	spec := claudeSpec
	spec.TaskID = one.ID
	if _, err := s.AddAttempt(ctx, spec, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	waits, err := s.Waits(ctx)
	if err != nil || len(waits) != 1 || waits[two.ID].ID != 2 {
		t.Fatalf("waits = %+v, %v", waits, err)
	}
	if got := armedIDs(t, s); len(got) != 2 {
		t.Fatalf("armed = %q, want the other task's wait and the poll timer", got)
	}
	if got := timerEvents(t, s); got[len(got)-1] != "timer.cancelled w1 (t1)" {
		t.Fatalf("events = %q", got)
	}
}

func TestWaitsListsArmedWaitsByTask(t *testing.T) {
	s, _ := movingStore(t)
	ctx := context.Background()
	one, two := twoActiveTasks(t, s)
	if waits, err := s.Waits(ctx); err != nil || len(waits) != 0 {
		t.Fatalf("no waits yet = %v, %v", waits, err)
	}
	if _, err := s.AddTimer(ctx, TimerSpec{After: time.Hour, TaskID: two.ID}); err != nil {
		t.Fatal(err)
	}
	w, err := s.AddTimer(ctx, TimerSpec{After: threeDays, TaskID: one.ID, Waits: true, Note: "outside PR"})
	if err != nil {
		t.Fatal(err)
	}
	waits, err := s.Waits(ctx)
	if err != nil || len(waits) != 1 || waits[one.ID].ID != w.ID || waits[one.ID].Note != "outside PR" || !waits[one.ID].Due.Equal(w.Due) {
		t.Fatalf("waits = %+v, %v", waits, err)
	}
	if _, err := s.CancelTimer(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if waits, err := s.Waits(ctx); err != nil || len(waits) != 0 {
		t.Fatalf("after cancel = %v, %v", waits, err)
	}
}
