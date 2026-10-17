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
		u, err := s.Unwatched(ctx)
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
