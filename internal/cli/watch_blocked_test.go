package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

func TestConfirmBlockedDropsPendingAfterTheAttemptEnds(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	st, err := state.Open(filepath.Join(home, "hand.db"), func() time.Time { return now })
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
	if _, err := st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	w := &watcher{
		r:         &runner{env: Env{Now: func() time.Time { return now }}},
		st:        st,
		seen:      map[int64]string{a.ID: "working"},
		turns:     map[int64]*turn{},
		heldAt:    map[int64]time.Time{},
		blockedAt: map[int64]time.Time{},
	}
	if err := w.observe(ctx, luvus.Client{}, a, "blocked"); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.blockedAt[a.ID]; !ok || w.seen[a.ID] != "working" {
		t.Fatalf("first sample = seen %q, held %v", w.seen[a.ID], ok)
	}
	if _, err := st.EndAttempt(ctx, a.ID, state.AttemptStopped, "done"); err != nil {
		t.Fatal(err)
	}
	if err := w.confirmBlocked(ctx, luvus.Client{}, luvus.Capabilities{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.blockedAt[a.ID]; ok {
		t.Fatal("a pending block survived its attempt")
	}
	events, err := st.RecentEventsOf(ctx, []string{"attempt.blocked"}, 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("events = %+v, %v", events, err)
	}
}
