package cli

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/orient"
	"github.com/atqamz/hand/internal/state"
)

func stoppedAttempt(t *testing.T, previous string) (string, *state.Store, state.Attempt, *watcher) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	home := t.TempDir()
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
	task, err := st.AddTask(ctx, "hand", "resume", "")
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
	seen := "done"
	switch previous {
	case "quiet":
		if _, _, err := st.RecordQuiet(ctx, a.ID, ""); err != nil {
			t.Fatal(err)
		}
	case "idle":
		for range 2 {
			if _, _, err := st.RecordQuiet(ctx, a.ID, ""); err != nil {
				t.Fatal(err)
			}
		}
	case "limited":
		if err := st.NoteAttempt(ctx, a.ID, "limited", "resets at 3pm"); err != nil {
			t.Fatal(err)
		}
	case "blocked":
		if err := st.NoteAttempt(ctx, a.ID, "blocked", "Do you want to proceed?"); err != nil {
			t.Fatal(err)
		}
		seen = "blocked"
	}
	w := &watcher{
		r:              &runner{env: Env{Stdout: io.Discard, Stderr: io.Discard, Now: func() time.Time { return now }}},
		st:             st,
		seen:           map[int64]string{a.ID: seen},
		turns:          map[int64]*turn{},
		heldAt:         map[int64]time.Time{},
		blockedPending: map[int64]struct{}{},
	}
	return home, st, a, w
}

func TestWatchRecordsTheMoveOutOfAStoppedAttemptOnce(t *testing.T) {
	for _, previous := range []string{"quiet", "idle", "limited"} {
		t.Run(previous, func(t *testing.T) {
			ctx := context.Background()
			_, st, a, w := stoppedAttempt(t, previous)
			for turn := range 2 {
				if err := w.observe(ctx, luvus.Client{}, a, "working"); err != nil {
					t.Fatal(err)
				}
				if n := len(eventKinds(t, st, "attempt.working")); n != 1 {
					t.Fatalf("after turn %d the attempt.working events = %d, want 1", turn+1, n)
				}
			}
		})
	}
}

func TestWatchStillRecordsTheMoveOutOfBlockedOnce(t *testing.T) {
	ctx := context.Background()
	_, st, a, w := stoppedAttempt(t, "blocked")
	for turn := range 2 {
		if err := w.observe(ctx, luvus.Client{}, a, "working"); err != nil {
			t.Fatal(err)
		}
		if n := len(eventKinds(t, st, "attempt.working")); n != 1 {
			t.Fatalf("after turn %d the attempt.working events = %d, want 1", turn+1, n)
		}
	}
}

func TestOrientShowsRunningAfterTheMoveOutOfQuiet(t *testing.T) {
	ctx := context.Background()
	home, st, a, w := stoppedAttempt(t, "quiet")
	cell := func() string {
		doc, err := orient.Build(ctx, st, home, "", true, true, orient.DefaultBudget)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(doc.String(), "\n") {
			if strings.HasPrefix(line, "  t1,hand,resume,") {
				return line
			}
		}
		t.Fatalf("orient has no row for t1:\n%s", doc)
		return ""
	}
	if got := cell(); got != "  t1,hand,resume,none,a1 running quiet,none,0" {
		t.Fatalf("orient before the move = %q", got)
	}
	if err := w.observe(ctx, luvus.Client{}, a, "working"); err != nil {
		t.Fatal(err)
	}
	if got := cell(); got != "  t1,hand,resume,none,a1 running,none,0" {
		t.Fatalf("orient after the move = %q", got)
	}
}
