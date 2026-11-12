package desktop

import (
	"reflect"
	"testing"
)

func TestATimelineThatShrankDropsTheHeldPage(t *testing.T) {
	have := Timeline{
		Older:   40,
		Entries: []Entry{{No: 41, Text: "held"}, {No: 42, Text: "newest"}},
	}
	next := Timeline{
		Older:   20,
		Entries: []Entry{{No: 42, Text: "newest"}, {No: 43, Text: "added"}},
	}
	got := mergeTimeline(have, next)
	want := Timeline{Older: 20, Entries: []Entry{{No: 42, Text: "newest"}, {No: 43, Text: "added"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("a shrunken timeline kept the held page: %+v", got)
	}
}

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
	a.polled(0, 1, "f0", BoardScreen, "", pollScreenID(BoardScreen, ""),
		Poll{StatusFresh: true, Status: Status{Pill: "changed"}}, nil)
	if a.Status.Pill != "keep" {
		t.Fatalf("a poll older than the last read applied its status: %+v", a.Status)
	}
}

func TestAQueuedMessageDeliveredLaterIsKeptOnce(t *testing.T) {
	have := Timeline{Older: 10, Entries: []Entry{
		{Role: "operator", Input: 7, Text: "hi", At: "2026-11-01T04:40:00Z", Queued: true},
		{Role: "supervisor", Text: "mid", At: "2026-11-01T04:43:00Z"},
		{Role: "supervisor", Text: "old", At: "2026-11-01T04:39:00Z"},
	}}
	next := Timeline{Older: 20, Entries: []Entry{
		{Role: "operator", Input: 7, No: 9, Text: "hi", At: "2026-11-01T04:46:00Z", Delivery: "delivered"},
		{Role: "supervisor", Text: "mid", At: "2026-11-01T04:43:00Z"},
	}}
	got := mergeTimeline(have, next)
	var texts []string
	for _, e := range got.Entries {
		texts = append(texts, e.Text)
	}
	if !reflect.DeepEqual(texts, []string{"hi", "mid", "old"}) || got.Older != 10 || got.Entries[0].Delivery != "delivered" {
		t.Fatalf("merged %q at %d, want hi once, delivered, with the old page kept", texts, got.Older)
	}
}
