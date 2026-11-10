package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func TestRolloverIsDueOnlyOutsideTheCooldownAndAfterNoFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateFleet(ctx, "f"); err != nil {
		t.Fatal(err)
	}
	sup, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	w := &watcher{r: &runner{env: Env{Now: func() time.Time { return now }}}, st: st}
	if !w.rolloverDue(ctx, sup) {
		t.Fatal("a fleet that never rolled over is not due")
	}
	if err := st.NoteSupervisor(ctx, sup.ID, "rolled_over", "continues s0"); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		after time.Duration
		due   bool
	}{{0, false}, {59 * time.Minute, false}, {time.Minute, true}, {24 * time.Hour, true}} {
		now = now.Add(step.after)
		if got := w.rolloverDue(ctx, sup); got != step.due {
			t.Fatalf("%v after the last rollover: due = %v, want %v", step.after, got, step.due)
		}
	}
	w.rollFailed.Store(sup.ID)
	if w.rolloverDue(ctx, sup) {
		t.Fatal("a supervisor that wrote no note is due again")
	}
}
