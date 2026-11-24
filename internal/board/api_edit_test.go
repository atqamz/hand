package board_test

import (
	"context"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
)

func TestAnEditedTaskShowsItsNewTitleAndTheEvent(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	_, before := apiInto[board.APITaskDetail](t, h, "/api/v1/task/t2")
	title, goal := "Renamed by edit", "a new goal"
	if _, _, err := fx.st.EditTask(context.Background(), 2, &title, &goal); err != nil {
		t.Fatal(err)
	}
	_, tasks := apiInto[board.APITasks](t, h, "/api/v1/tasks")
	var row *board.APITaskRow
	for _, g := range tasks.Groups {
		for i, r := range g.Tasks {
			if r.Ref == "t2" {
				row = &g.Tasks[i]
			}
		}
	}
	if row == nil || row.Title != title || row.Status != before.Task.Status {
		t.Fatalf("t2 row = %+v, before %+v", row, before.Task)
	}
	_, detail := apiInto[board.APITaskDetail](t, h, "/api/v1/task/t2")
	if detail.Task.Title != title || detail.Task.Goal != goal {
		t.Fatalf("task = %+v", detail.Task)
	}
	if len(detail.Events) != len(before.Events)+2 || detail.Events[0].Kind != "task.edited" || detail.Events[1].Kind != "task.edited" {
		t.Fatalf("events = %+v", detail.Events)
	}
	if detail.Events[0].Detail != "goal: "+before.Task.Goal+" -> "+goal || detail.Events[1].Detail != "title: "+before.Task.Title+" -> "+title {
		t.Fatalf("details = %q, %q", detail.Events[0].Detail, detail.Events[1].Detail)
	}
}
