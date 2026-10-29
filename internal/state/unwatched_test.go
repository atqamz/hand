package state

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestUnwatchedNamesActiveTasksThatNothingWatches(t *testing.T) {
	var mu sync.Mutex
	now := fixed
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}
	s, err := Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	want := func(tasks []int64, idle time.Duration, stalls int) Unwatched {
		t.Helper()
		u, err := s.Unwatched(ctx, true)
		if err != nil || !slices.Equal(u.Tasks, tasks) || u.Idle != idle || u.Stalls != stalls {
			t.Fatalf("unwatched = %+v, %v; want tasks %v, idle %v, stalls %d", u, err, tasks, idle, stalls)
		}
		return u
	}
	task := activeTask(t, s)
	two, err := s.AddTask(ctx, "hand", "Write docs", "")
	if err != nil {
		t.Fatal(err)
	}
	want([]int64{task.ID}, 0, 0)
	if _, err := s.Transition(ctx, two.ID, StatusActive); err != nil {
		t.Fatal(err)
	}
	advance(23 * time.Minute)
	u := want([]int64{task.ID, two.ID}, 23*time.Minute, 0)
	if !u.Since.Equal(fixed) || !u.Last.IsZero() {
		t.Fatalf("since = %v, last = %v", u.Since, u.Last)
	}
	sup, err := s.AddSupervisor(ctx, SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude"}})
	if err != nil {
		t.Fatal(err)
	}
	advance(time.Minute)
	want([]int64{task.ID, two.ID}, time.Minute, 0)
	advance(20 * time.Minute)
	if err := s.NoteSupervisor(ctx, sup.ID, "stalled", "idle 20m"); err != nil {
		t.Fatal(err)
	}
	seq, err := s.LastEventSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	advance(time.Minute)
	if err := s.AdvanceWakeCursor(ctx, sup.ID, seq); err != nil {
		t.Fatal(err)
	}
	advance(39 * time.Minute)
	if err := s.NoteSupervisor(ctx, sup.ID, "stalled", "idle 1h"); err != nil {
		t.Fatal(err)
	}
	u = want([]int64{task.ID, two.ID}, time.Hour+time.Minute, 2)
	if !u.Last.Equal(now) {
		t.Fatalf("last stall = %v, want %v", u.Last, now)
	}
	if got := u.IdleFor(); got != "1h1m" {
		t.Fatalf("idle = %q", got)
	}
	d, err := s.Ask(ctx, task.ID, "Keep the cookie?")
	if err != nil {
		t.Fatal(err)
	}
	want(nil, 0, 0)
	advance(5 * time.Minute)
	if _, err := s.Withdraw(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	want([]int64{task.ID, two.ID}, 0, 0)
	spec := claudeSpec
	spec.TaskID = two.ID
	if _, err := s.AddAttempt(ctx, spec, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	want(nil, 0, 0)
}

func TestIdleForReadsInHoursAndMinutes(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                               "<1m",
		59 * time.Second:                "<1m",
		23 * time.Minute:                "23m",
		time.Hour:                       "1h",
		72*time.Minute + 30*time.Second: "1h12m",
		26 * time.Hour:                  "26h",
	} {
		if got := (Unwatched{Idle: d}).IdleFor(); got != want {
			t.Fatalf("IdleFor(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestAnArmedTimerWatchesTheFleet(t *testing.T) {
	s, advance := movingStore(t)
	ctx := context.Background()
	task := activeTask(t, s)
	want := func(tasks []int64, idle time.Duration) {
		t.Helper()
		u, err := s.Unwatched(ctx, true)
		if err != nil || !slices.Equal(u.Tasks, tasks) || u.Idle != idle {
			t.Fatalf("unwatched = %+v, %v; want tasks %v, idle %v", u, err, tasks, idle)
		}
	}
	advance(10 * time.Minute)
	want([]int64{task.ID}, 10*time.Minute)
	if _, err := s.AddTimer(ctx, TimerSpec{After: 30 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	want(nil, 0)
	advance(30 * time.Minute)
	if _, err := s.FireTimers(ctx); err != nil {
		t.Fatal(err)
	}
	advance(5 * time.Minute)
	want([]int64{task.ID}, 5*time.Minute)
	w, err := s.AddTimer(ctx, TimerSpec{Every: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	want(nil, 0)
	if _, err := s.CancelTimer(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	want([]int64{task.ID}, 0)
}

func TestATimerWatchesOnlyWhileDueWithinSixHours(t *testing.T) {
	s, advance := movingStore(t)
	ctx := context.Background()
	task := activeTask(t, s)
	watched := func() bool {
		t.Helper()
		u, err := s.Unwatched(ctx, true)
		if err != nil {
			t.Fatal(err)
		}
		return len(u.Tasks) == 0
	}
	near, err := s.AddTimer(ctx, TimerSpec{After: 5*time.Hour + 59*time.Minute})
	if err != nil || !watched() {
		t.Fatalf("a timer due in 5h59m: %v, watched %v", err, watched())
	}
	if _, err := s.CancelTimer(ctx, near.ID); err != nil {
		t.Fatal(err)
	}
	far, err := s.AddTimer(ctx, TimerSpec{After: 6*time.Hour + time.Minute})
	if err != nil || watched() {
		t.Fatalf("a timer due in 6h01m: %v, watched %v", err, watched())
	}
	u, err := s.Unwatched(ctx, true)
	if err != nil || !slices.Equal(u.Tasks, []int64{task.ID}) || u.Far.ID != far.ID || u.FarIn != 6*time.Hour+time.Minute {
		t.Fatalf("unwatched = %+v, %v", u, err)
	}
	advance(2 * time.Minute)
	if !watched() {
		t.Fatal("a timer that came within 6h did not start watching")
	}
}

func TestARepeatingTimerWatchesWhileItsNextDueIsWithinSixHours(t *testing.T) {
	s, advance := movingStore(t)
	ctx := context.Background()
	activeTask(t, s)
	if _, err := s.AddTimer(ctx, TimerSpec{Every: 7 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	if u, err := s.Unwatched(ctx, true); err != nil || len(u.Tasks) != 1 {
		t.Fatalf("a timer repeating every 7h: %+v, %v", u, err)
	}
	advance(7 * time.Hour)
	if _, err := s.FireTimers(ctx); err != nil {
		t.Fatal(err)
	}
	if u, err := s.Unwatched(ctx, true); err != nil || len(u.Tasks) != 1 {
		t.Fatalf("after its fire: %+v, %v", u, err)
	}
	if _, err := s.AddTimer(ctx, TimerSpec{Every: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if u, err := s.Unwatched(ctx, true); err != nil || len(u.Tasks) != 0 {
		t.Fatalf("a timer repeating every hour: %+v, %v", u, err)
	}
}

func TestAheadReadsInDaysAndHours(t *testing.T) {
	for d, want := range map[time.Duration]string{
		7 * time.Hour:                 "7h",
		7*time.Hour + 30*time.Minute:  "7h30m",
		24 * time.Hour:                "1d",
		31*24*time.Hour + 5*time.Hour: "31d",
	} {
		if got := Ahead(d); got != want {
			t.Fatalf("Ahead(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestATimerWatchesOnlyWhenAManagedSupervisorRuns(t *testing.T) {
	s, _ := movingStore(t)
	ctx := context.Background()
	task := activeTask(t, s)
	if _, err := s.AddTimer(ctx, TimerSpec{After: 30 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	if u, err := s.Unwatched(ctx, true); err != nil || len(u.Tasks) != 0 {
		t.Fatalf("with a managed supervisor: %+v, %v", u, err)
	}
	u, err := s.Unwatched(ctx, false)
	if err != nil || !slices.Equal(u.Tasks, []int64{task.ID}) || u.Far.ID != 0 {
		t.Fatalf("without a managed supervisor: %+v, %v", u, err)
	}
}

func TestOnlyARunningSupervisorOnTheCurrentServerIsManaged(t *testing.T) {
	for _, c := range []struct {
		status, generation string
		want               bool
	}{
		{AttemptRunning, "gen-1", true},
		{AttemptRunning, "gen-2", false},
		{AttemptLaunching, "gen-1", false},
		{AttemptInterrupted, "gen-1", false},
		{AttemptExited, "gen-1", false},
	} {
		sup := Supervisor{Status: c.status, Terminal: Terminal{ServerGeneration: "gen-1"}}
		if got := sup.Managed(c.generation); got != c.want {
			t.Fatalf("%s supervisor on gen-1, server %s: managed = %v, want %v", c.status, c.generation, got, c.want)
		}
	}
}

func TestUnwatchedReadsTheClockOnce(t *testing.T) {
	var mu sync.Mutex
	now, tick := fixed, time.Duration(0)
	s, err := Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		at := now
		now = now.Add(tick)
		return at
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	task := activeTask(t, s)
	far, err := s.AddTimer(ctx, TimerSpec{After: 7 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	now, tick = now.Add(20*time.Minute), 2*time.Minute
	mu.Unlock()
	u, err := s.Unwatched(ctx, true)
	if err != nil || !slices.Equal(u.Tasks, []int64{task.ID}) || u.Far.ID != far.ID || !u.At.Equal(fixed.Add(20*time.Minute)) || u.Idle != 20*time.Minute || u.FarIn != 6*time.Hour+40*time.Minute {
		t.Fatalf("unwatched = %+v, %v; want t%d idle 20m, w%d due in 6h40m", u, err, task.ID, far.ID)
	}
}

func TestAModelChangeDoesNotRestartTheIdleClock(t *testing.T) {
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
	ctx := context.Background()
	task := activeTask(t, s)
	sup, err := s.AddSupervisor(ctx, SupervisorSpec{Harness: "opencode", Argv: []string{"/bin/opencode"}})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	now = now.Add(30 * time.Minute)
	mu.Unlock()
	if err := s.SetSupervisorModel(ctx, sup.ID, "anthropic/claude-sonnet-5-5", ""); err != nil {
		t.Fatal(err)
	}
	u, err := s.Unwatched(ctx, true)
	if err != nil || !slices.Equal(u.Tasks, []int64{task.ID}) || u.Idle != 30*time.Minute {
		t.Fatalf("unwatched = %+v, %v; want t%d idle 30m", u, err, task.ID)
	}
}
