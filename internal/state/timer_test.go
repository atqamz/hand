package state

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func movingStore(t *testing.T) (*Store, func(time.Duration)) {
	t.Helper()
	var mu sync.Mutex
	now := fixed
	s, err := Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}
}

func timerEvents(t *testing.T, s *Store) []string {
	t.Helper()
	events, err := s.EventsAfter(context.Background(), 0, []string{"timer.armed", "timer.fired", "timer.cancelled"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Kind + " " + e.Detail
		if e.TaskID != 0 {
			out[i] += " (" + TaskRef(e.TaskID) + ")"
		}
	}
	return out
}

func armedIDs(t *testing.T, s *Store) []string {
	t.Helper()
	timers, err := s.Timers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, w := range timers {
		out = append(out, TimerRef(w.ID)+" "+w.Due.Format(time.RFC3339))
	}
	return out
}

func TestTimersFireOnceRepeatAndCancel(t *testing.T) {
	s, advance := movingStore(t)
	ctx := context.Background()
	task := activeTask(t, s)
	one, err := s.AddTimer(ctx, TimerSpec{After: 30 * time.Minute, Note: "  check PR 909  "})
	if err != nil || one.ID != 1 || one.Note != "check PR 909" || !one.Due.Equal(fixed.Add(30*time.Minute)) || one.Status != TimerArmed {
		t.Fatalf("one = %+v, %v", one, err)
	}
	two, err := s.AddTimer(ctx, TimerSpec{After: 30 * time.Minute, Every: 30 * time.Minute, TaskID: task.ID})
	if err != nil || two.Every != 30*time.Minute || two.TaskID != task.ID {
		t.Fatalf("two = %+v, %v", two, err)
	}
	if _, err := s.AddTimer(ctx, TimerSpec{At: time.Date(2026, 9, 26, 9, 0, 0, 0, time.FixedZone("WIB", 7*3600))}); err != nil {
		t.Fatal(err)
	}
	if got := armedIDs(t, s); !slices.Equal(got, []string{"w1 2026-09-26T00:30:00Z", "w2 2026-09-26T00:30:00Z", "w3 2026-09-26T02:00:00Z"}) {
		t.Fatalf("armed = %q", got)
	}
	advance(29 * time.Minute)
	if fired, err := s.FireTimers(ctx); err != nil || len(fired) != 0 {
		t.Fatalf("fired early = %q, %v", fired, err)
	}
	advance(time.Minute)
	if fired, err := s.FireTimers(ctx); err != nil || !slices.Equal(fired, []string{"w1: check PR 909", "w2"}) {
		t.Fatalf("fired = %q, %v", fired, err)
	}
	if got := armedIDs(t, s); !slices.Equal(got, []string{"w2 2026-09-26T01:00:00Z", "w3 2026-09-26T02:00:00Z"}) {
		t.Fatalf("armed = %q", got)
	}
	advance(2*time.Hour + 40*time.Minute + 30*time.Second)
	if fired, err := s.FireTimers(ctx); err != nil || !slices.Equal(fired, []string{"w2, 2h10m late", "w3, 1h10m late"}) {
		t.Fatalf("late = %q, %v", fired, err)
	}
	if got := armedIDs(t, s); !slices.Equal(got, []string{"w2 2026-09-26T03:40:30Z"}) {
		t.Fatalf("armed = %q", got)
	}
	if _, err := s.CancelTimer(ctx, 3); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "timer w3 is already fired") {
		t.Fatalf("cancel fired = %v", err)
	}
	if w, err := s.CancelTimer(ctx, 2); err != nil || w.Status != TimerCancelled {
		t.Fatalf("cancel = %+v, %v", w, err)
	}
	if _, err := s.CancelTimer(ctx, 2); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "already cancelled") {
		t.Fatalf("cancel twice = %v", err)
	}
	if _, err := s.CancelTimer(ctx, 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel missing = %v", err)
	}
	want := []string{
		"timer.armed w1, due 2026-09-26T00:30:00Z: check PR 909",
		"timer.armed w2, due 2026-09-26T00:30:00Z, every 30m (t1)",
		"timer.armed w3, due 2026-09-26T02:00:00Z",
		"timer.fired w1: check PR 909",
		"timer.fired w2 (t1)",
		"timer.fired w2, 2h10m late (t1)",
		"timer.fired w3, 1h10m late",
		"timer.cancelled w2 (t1)",
	}
	if got := timerEvents(t, s); !slices.Equal(got, want) {
		t.Fatalf("events =\n%q\nwant\n%q", got, want)
	}
}

func TestTimerLimits(t *testing.T) {
	s, advance := movingStore(t)
	ctx := context.Background()
	task := activeTask(t, s)
	for name, c := range map[string]struct {
		spec TimerSpec
		err  error
		text string
	}{
		"too soon":     {TimerSpec{After: 59 * time.Second}, ErrInvalid, "due at least 1m from now"},
		"past":         {TimerSpec{At: fixed.Add(-time.Hour)}, ErrInvalid, "due at least 1m from now"},
		"too often":    {TimerSpec{Every: 59 * time.Second}, ErrInvalid, "at most once a minute"},
		"long note":    {TimerSpec{After: time.Hour, Note: strings.Repeat("漢", MaxTimerNote+1)}, ErrInvalid, "at most 200 characters"},
		"bad note":     {TimerSpec{After: time.Hour, Note: "a\xffb"}, ErrInvalid, "at most 200 characters of UTF-8"},
		"two lines":    {TimerSpec{After: time.Hour, Note: "a\nb"}, ErrInvalid, "one line"},
		"missing task": {TimerSpec{After: time.Hour, TaskID: 99}, ErrNotFound, "task t99"},
	} {
		if _, err := s.AddTimer(ctx, c.spec); !errors.Is(err, c.err) || !strings.Contains(err.Error(), c.text) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if _, err := s.AddTimer(ctx, TimerSpec{After: time.Minute, Note: strings.Repeat("漢", MaxTimerNote)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTimer(ctx, TimerSpec{Every: time.Minute}); err != nil {
		t.Fatal(err)
	}
	for range MaxTimers - 2 {
		if _, err := s.AddTimer(ctx, TimerSpec{After: time.Hour}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddTimer(ctx, TimerSpec{After: time.Hour}); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "20 timers are armed") {
		t.Fatalf("21st = %v", err)
	}
	advance(time.Minute)
	if fired, err := s.FireTimers(ctx); err != nil || len(fired) != 2 {
		t.Fatalf("fired = %q, %v", fired, err)
	}
	if _, err := s.AddTimer(ctx, TimerSpec{After: time.Hour}); err != nil {
		t.Fatalf("after a one-shot fired: %v", err)
	}
	if _, err := s.Transition(ctx, task.ID, StatusAbandoned); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTimer(ctx, TimerSpec{After: time.Hour, TaskID: task.ID}); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "task t1 is already abandoned") {
		t.Fatalf("finished task = %v", err)
	}
}

func TestFinishingATaskCancelsItsTimers(t *testing.T) {
	s, _ := movingStore(t)
	ctx := context.Background()
	one := activeTask(t, s)
	two, err := s.AddTask(ctx, "hand", "Write docs", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []TimerSpec{{Every: time.Hour, TaskID: one.ID}, {After: time.Hour}, {After: time.Hour, TaskID: two.ID}, {After: 2 * time.Hour, TaskID: one.ID}} {
		if _, err := s.AddTimer(ctx, spec); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Transition(ctx, one.ID, StatusDone); err != nil {
		t.Fatal(err)
	}
	if got := armedIDs(t, s); !slices.Equal(got, []string{"w2 2026-09-26T01:00:00Z", "w3 2026-09-26T01:00:00Z"}) {
		t.Fatalf("armed after done = %q", got)
	}
	if _, err := s.Transition(ctx, two.ID, StatusAbandoned); err != nil {
		t.Fatal(err)
	}
	if got := armedIDs(t, s); !slices.Equal(got, []string{"w2 2026-09-26T01:00:00Z"}) {
		t.Fatalf("armed after abandon = %q", got)
	}
	events, err := s.EventsAfter(ctx, 0, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	var tail []string
	for _, e := range events[len(events)-5:] {
		tail = append(tail, e.Kind+" "+e.Detail+" "+TaskRef(e.TaskID))
	}
	if want := []string{"task.done active->done t1", "timer.cancelled w1 t1", "timer.cancelled w4 t1", "task.abandoned inbox->abandoned t2", "timer.cancelled w3 t2"}; !slices.Equal(tail, want) {
		t.Fatalf("events = %q", tail)
	}
}

func TestSpanReadsLikeADuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Minute:                     "1m",
		90 * time.Second:                "1m30s",
		30 * time.Minute:                "30m",
		time.Hour:                       "1h",
		2*time.Hour + 10*time.Minute:    "2h10m",
		time.Hour + 30*time.Second:      "1h0m30s",
		26 * time.Hour:                  "26h",
		72*time.Minute + 30*time.Second: "1h12m30s",
	} {
		if got := Span(d); got != want {
			t.Fatalf("Span(%v) = %q, want %q", d, got, want)
		}
	}
}
