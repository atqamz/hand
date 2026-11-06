package cli

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

func pendingBlockWatcher(t *testing.T) (*state.Store, state.Attempt, *watcher) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateFleet(ctx, "f"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProject(ctx, "hand", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.AddTask(ctx, "hand", "blocked", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Transition(ctx, task.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"claude"}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err = st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"})
	if err != nil {
		t.Fatal(err)
	}
	w := &watcher{
		r:              &runner{env: Env{Stdout: io.Discard, Stderr: io.Discard, Now: func() time.Time { return now }}},
		st:             st,
		seen:           map[int64]string{a.ID: "working"},
		turns:          map[int64]*turn{},
		heldAt:         map[int64]time.Time{},
		blockedPending: map[int64]struct{}{},
	}
	return st, a, w
}

func eventKinds(t *testing.T, st *state.Store, kinds ...string) []string {
	t.Helper()
	events, err := st.RecentEventsOf(context.Background(), kinds, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

func TestConfirmBlockedDropsPendingAfterTheAttemptEnds(t *testing.T) {
	ctx := context.Background()
	st, a, w := pendingBlockWatcher(t)
	if err := w.observe(ctx, luvus.Client{}, a, "blocked"); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.blockedPending[a.ID]; !ok || w.seen[a.ID] != "working" {
		t.Fatalf("first sample = seen %q, pending %v", w.seen[a.ID], ok)
	}
	if _, err := st.EndAttempt(ctx, a.ID, state.AttemptStopped, "done"); err != nil {
		t.Fatal(err)
	}
	if err := w.confirmBlocked(ctx, luvus.Client{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.blockedPending[a.ID]; ok {
		t.Fatal("a pending block survived its attempt")
	}
	if events := eventKinds(t, st, "attempt.blocked"); len(events) != 0 {
		t.Fatalf("events = %v", events)
	}
}

func TestAPendingBlockDoesNotSurviveAnIdleWhileHeld(t *testing.T) {
	ctx := context.Background()
	st, a, w := pendingBlockWatcher(t)
	w.seen[a.ID] = "held"
	if err := w.observe(ctx, luvus.Client{}, a, "blocked"); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.blockedPending[a.ID]; !ok {
		t.Fatal("the first blocked sample did not record a pending block")
	}
	if err := w.observe(ctx, luvus.Client{}, a, "idle"); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.blockedPending[a.ID]; ok {
		t.Fatal("a pending block survived the idle that cleared it while held")
	}
	if err := w.observe(ctx, luvus.Client{}, a, "blocked"); err != nil {
		t.Fatal(err)
	}
	if events := eventKinds(t, st, "attempt.blocked"); len(events) != 0 {
		t.Fatalf("a block after an idle while held recorded %v", events)
	}
}

func TestAFlickerDoesNotSeedTheNextBlock(t *testing.T) {
	ctx := context.Background()
	st, a, w := pendingBlockWatcher(t)
	for range 3 {
		if err := w.observe(ctx, luvus.Client{}, a, "blocked"); err != nil {
			t.Fatal(err)
		}
		if err := w.observe(ctx, luvus.Client{}, a, "working"); err != nil {
			t.Fatal(err)
		}
	}
	if events := eventKinds(t, st, "attempt.blocked", "attempt.working"); len(events) != 0 {
		t.Fatalf("flickers recorded %v", events)
	}
	if err := w.observe(ctx, luvus.Client{}, a, "blocked"); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.blockedPending[a.ID]; !ok {
		t.Fatal("the first sample of the real block recorded no pending entry")
	}
	if events := eventKinds(t, st, "attempt.blocked", "attempt.working"); len(events) != 0 {
		t.Fatalf("the first sample of the real block recorded %v", events)
	}
	if err := w.observe(ctx, luvus.Client{}, a, "blocked"); err != nil {
		t.Fatal(err)
	}
	if events := eventKinds(t, st, "attempt.blocked", "attempt.working"); len(events) != 1 || events[0] != "attempt.blocked" {
		t.Fatalf("the second sample recorded %v", events)
	}
}
