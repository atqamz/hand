package desktop

import "testing"

func TestAPollForAnotherRefLeavesTheOpenTaskAlone(t *testing.T) {
	a := &App{
		Fleet:  Fleet{ID: "f0"},
		Screen: TaskScreen,
		Ref:    "t2",
		Task:   TaskDetail{Task: Task{Ref: "t2"}},
	}
	a.polled(0, 0, "f0", TaskScreen, "t1", pollScreenID(TaskScreen, "t1"),
		Poll{ScreenFresh: true, Task: TaskDetail{Task: Task{Ref: "t1"}}}, nil)
	if a.Task.Task.Ref != "t2" {
		t.Fatalf("a poll for another ref wrote the open task: %+v", a.Task.Task)
	}
}

func TestAPollOlderThanTheLastReadIsDropped(t *testing.T) {
	a := &App{Fleet: Fleet{ID: "f0"}, Status: Status{Pill: "keep"}}
	a.polled(0, 1, "f0", StatusScreen, "", pollScreenID(StatusScreen, ""),
		Poll{StatusFresh: true, Status: Status{Pill: "changed"}}, nil)
	if a.Status.Pill != "keep" {
		t.Fatalf("a poll older than the last read applied its status: %+v", a.Status)
	}
}
