package board_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

func TestAParkedTaskIsListedInInboxWithoutItsFailure(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	_, before := apiInto[board.APIStatus](t, h, "/api/v1/status")
	if !slices.ContainsFunc(before.Needs.Items, func(n board.APINeed) bool { return n.Ref == "a2" }) {
		t.Fatalf("fixture has no failure need on a2: %+v", before.Needs.Items)
	}
	if _, err := fx.st.Transition(context.Background(), 2, state.StatusInbox, state.TransitionOpts{Note: "paused by operator"}); err != nil {
		t.Fatal(err)
	}
	_, after := apiInto[board.APIStatus](t, h, "/api/v1/status")
	if slices.ContainsFunc(after.Needs.Items, func(n board.APINeed) bool { return n.Ref == "a2" }) {
		t.Fatalf("a parked task keeps its failure need: %+v", after.Needs.Items)
	}
	if after.Tasks.Active != before.Tasks.Active-1 || after.Tasks.Inbox != before.Tasks.Inbox+1 {
		t.Fatalf("counts %+v -> %+v", before.Tasks, after.Tasks)
	}
	_, tasks := apiInto[board.APITasks](t, h, "/api/v1/tasks")
	var parked *board.APITaskRow
	for _, g := range tasks.Groups {
		for i, row := range g.Tasks {
			if row.Ref == "t2" {
				parked = &g.Tasks[i]
				if g.Name != "Inbox" {
					t.Fatalf("t2 is in group %q", g.Name)
				}
			}
		}
	}
	if parked == nil || parked.Status != state.StatusInbox {
		t.Fatalf("t2 row = %+v", parked)
	}
	_, detail := apiInto[board.APITaskDetail](t, h, "/api/v1/task/t2")
	if detail.Task.Status != state.StatusInbox {
		t.Fatalf("task = %+v", detail.Task)
	}
	last := detail.Events[0]
	if last.Kind != "task.inbox" || last.Detail != "active->inbox: paused by operator" {
		t.Fatalf("events = %+v", detail.Events)
	}
}
