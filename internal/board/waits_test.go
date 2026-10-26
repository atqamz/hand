package board_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

const threeDays = 3 * 24 * time.Hour

func clocked(t *testing.T) (*state.Store, func(time.Duration)) {
	t.Helper()
	var mu sync.Mutex
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateFleet(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	return st, func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}
}

func wait(t *testing.T, st *state.Store, taskID int64, note string) state.Timer {
	t.Helper()
	w, err := st.AddTimer(context.Background(), state.TimerSpec{After: threeDays, TaskID: taskID, Waits: true, Note: note})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestAWaitingTaskLeavesTheFailedItemsAndComesBackWhenTheWaitFires(t *testing.T) {
	st, advance := clocked(t)
	ctx := context.Background()
	waiting := active(t, st, "Review outside PR")
	attempt(t, st, waiting.ID, state.AttemptInterrupted, "pane gone")
	flaky := active(t, st, "Flaky test")
	attempt(t, st, flaky.ID, state.AttemptFailed, "boom")
	h := board.New(st, token, board.Options{})
	failures := func() []string {
		t.Helper()
		var refs []string
		kinds, items, _ := waits(get(t, h, "/"))
		for i, k := range kinds {
			if k == "failure" {
				refs = append(refs, items[i])
			}
		}
		return refs
	}
	if got := failures(); !slices.Equal(got, []string{"a1", "a2"}) {
		t.Fatalf("failures before the wait = %q", got)
	}
	wait(t, st, waiting.ID, "outside PR")
	if got := failures(); !slices.Equal(got, []string{"a2"}) {
		t.Fatalf("failures while t1 waits = %q, want a task that does not wait to keep its red item", got)
	}
	tasks := region(get(t, h, "/"), "tasks")
	if strings.Count(tasks, `data-state="failing"`) != 1 {
		t.Fatalf("tasks while t1 waits:\n%s", tasks)
	}
	advance(threeDays)
	if _, err := st.FireTimers(ctx); err != nil {
		t.Fatal(err)
	}
	if got := failures(); !slices.Equal(got, []string{"a1", "a2"}) {
		t.Fatalf("failures after the wait fired = %q", got)
	}
	if tasks := region(get(t, h, "/"), "tasks"); strings.Count(tasks, `data-state="failing"`) != 2 || strings.Contains(tasks, "Waits until") {
		t.Fatalf("tasks after the wait fired:\n%s", tasks)
	}
}

func TestAWaitingTaskLeavesTheStalledItemAndComesBackWhenTheWaitFires(t *testing.T) {
	st, advance := clocked(t)
	ctx := context.Background()
	fx := newFixture(t)
	fx.st, fx.status, fx.screen = st, "idle", "> "
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	one := active(t, st, "Review outside PR")
	h := fx.handler()
	stalled := func() string {
		t.Helper()
		q := region(get(t, h, "/"), "queue")
		_, after, found := strings.Cut(q, `data-kind="stalled"`)
		if !found {
			return ""
		}
		_, title, _ := strings.Cut(after, `<span class="wait-title" title="`)
		title, _, _ = strings.Cut(title, `"`)
		return title
	}
	stall := func() {
		t.Helper()
		if err := st.NoteSupervisor(ctx, 1, "stalled", "idle"); err != nil {
			t.Fatal(err)
		}
	}
	wait(t, st, one.ID, "outside PR")
	advance(20 * time.Minute)
	stall()
	if got := stalled(); got != "" {
		t.Fatalf("the only active task waits, yet the Board says %q", got)
	}
	two := active(t, st, "Write docs")
	advance(20 * time.Minute)
	stall()
	if got := stalled(); got != "Supervisor idle for 20m with 1 active task and no running attempt" {
		t.Fatalf("a neighbour that does not wait: stalled = %q", got)
	}
	if _, err := st.Transition(ctx, two.ID, state.StatusAbandoned); err != nil {
		t.Fatal(err)
	}
	advance(threeDays)
	if _, err := st.FireTimers(ctx); err != nil {
		t.Fatal(err)
	}
	advance(20 * time.Minute)
	stall()
	if got := stalled(); got != "Supervisor idle for 20m with 1 active task and no running attempt" {
		t.Fatalf("after the wait fired: stalled = %q", got)
	}
}

func TestTheTaskCardAndTheTasksRowSayWhenTheTaskWaits(t *testing.T) {
	st, _ := clocked(t)
	one := active(t, st, "Review outside PR")
	two := active(t, st, "Write docs")
	w := wait(t, st, one.ID, "outside PR")
	h := board.New(st, token, board.Options{})
	const line = `Waits until Sep 29: outside PR`
	tasks := region(get(t, h, "/"), "tasks")
	contains(t, "tasks row", tasks, line)
	if strings.Count(tasks, "Waits until") != 1 {
		t.Fatalf("only t1 waits:\n%s", tasks)
	}
	contains(t, "task card", get(t, h, "/task/t1"), line)
	lacks(t, "other task card", get(t, h, "/task/"+state.TaskRef(two.ID)), "Waits until")
	contains(t, "tasks page", get(t, h, "/tasks?status=active"), line)
	if _, err := st.CancelTimer(context.Background(), w.ID); err != nil {
		t.Fatal(err)
	}
	lacks(t, "task card after cancel", get(t, h, "/task/t1"), "Waits until")
	lacks(t, "tasks after cancel", region(get(t, h, "/"), "tasks"), "Waits until")
}

func TestAWaitWithNoNoteSaysOnlyWhen(t *testing.T) {
	st, _ := clocked(t)
	one := active(t, st, "Review outside PR")
	wait(t, st, one.ID, "")
	contains(t, "task card", get(t, board.New(st, token, board.Options{}), "/task/t1"), "Waits until Sep 29</span>")
}
