package watcher

import (
	"errors"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/herdr"
)

func TestClassifyStatusWorkingToDoneFiresDone(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)

	if e := ClassifyStatus(ts, "task-1", herdr.StatusDone, nil, now.Add(time.Second)); e == nil || e.Kind != KindDone {
		t.Fatalf("got %+v, want done event", e)
	}
	if e := ClassifyStatus(ts, "task-1", herdr.StatusDone, nil, now.Add(2*time.Second)); e != nil {
		t.Fatalf("repeated done state fired again: %+v", e)
	}
}

func TestClassifyStatusWorkingToIdleFiresDone(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)

	if e := ClassifyStatus(ts, "task-1", herdr.StatusIdle, nil, now.Add(time.Second)); e == nil || e.Kind != KindDone {
		t.Fatalf("got %+v, want done event", e)
	}
}

func TestClassifyStatusIdleToWorkingIsBenign(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusIdle, now)

	if e := ClassifyStatus(ts, "task-1", herdr.StatusWorking, nil, now.Add(time.Second)); e != nil {
		t.Fatalf("got %+v, want no event for resuming work", e)
	}
}

func TestClassifyStatusBlockedFiresOnceUntilResolved(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)

	e := ClassifyStatus(ts, "task-1", herdr.StatusBlocked, nil, now.Add(time.Second))
	if e == nil || e.Kind != KindBlocked || e.Text != "blocked task-1: agent needs help" {
		t.Fatalf("got %+v, want blocked event", e)
	}

	if e := ClassifyStatus(ts, "task-1", herdr.StatusBlocked, nil, now.Add(2*time.Second)); e != nil {
		t.Fatalf("repeated blocked state fired again: %+v", e)
	}

	if e := ClassifyStatus(ts, "task-1", herdr.StatusWorking, nil, now.Add(3*time.Second)); e != nil {
		t.Fatalf("leaving blocked fired an event: %+v", e)
	}

	if e := ClassifyStatus(ts, "task-1", herdr.StatusBlocked, nil, now.Add(4*time.Second)); e == nil || e.Kind != KindBlocked {
		t.Fatalf("got %+v, want blocked event to refire after resolving and re-blocking", e)
	}
}

func TestClassifyStatusProbeFailureFiresFailedOnce(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)
	probeErr := errors.New("pane not found")

	if e := ClassifyStatus(ts, "task-1", "", probeErr, now.Add(time.Second)); e == nil || e.Kind != KindFailed {
		t.Fatalf("got %+v, want failed event", e)
	}
	if e := ClassifyStatus(ts, "task-1", "", probeErr, now.Add(2*time.Second)); e != nil {
		t.Fatalf("repeated probe failure fired again: %+v", e)
	}
}

func TestClassifyStatusRecoveryAfterFailureCanFireDone(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)
	probeErr := errors.New("pane not found")

	if e := ClassifyStatus(ts, "task-1", "", probeErr, now.Add(time.Second)); e == nil || e.Kind != KindFailed {
		t.Fatalf("got %+v, want failed event", e)
	}
	if e := ClassifyStatus(ts, "task-1", herdr.StatusDone, nil, now.Add(2*time.Second)); e == nil || e.Kind != KindDone {
		t.Fatalf("got %+v, want done event on recovery", e)
	}
}

func TestClassifyStaleFiresOncePerWindow(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)
	threshold := 5 * time.Minute

	if e := ClassifyStale(ts, "task-1", now.Add(time.Minute), threshold); e != nil {
		t.Fatalf("got %+v, want no stale event before threshold", e)
	}
	if e := ClassifyStale(ts, "task-1", now.Add(6*time.Minute), threshold); e == nil || e.Kind != KindStale {
		t.Fatalf("got %+v, want stale event", e)
	}
	if e := ClassifyStale(ts, "task-1", now.Add(10*time.Minute), threshold); e != nil {
		t.Fatalf("stale event fired again in the same window: %+v", e)
	}

	ClassifyStatus(ts, "task-1", herdr.StatusDone, nil, now.Add(11*time.Minute))
	if e := ClassifyStale(ts, "task-1", now.Add(12*time.Minute), threshold); e != nil {
		t.Fatalf("stale fired right after a status change reset the window: %+v", e)
	}
}

func TestClassifyStaleSkipsUnprobedTasks(t *testing.T) {
	now := time.Now()
	ts := NewTaskState(herdr.StatusWorking, now)
	ClassifyStatus(ts, "task-1", "", errors.New("down"), now.Add(time.Second))

	if e := ClassifyStale(ts, "task-1", now.Add(time.Hour), time.Minute); e != nil {
		t.Fatalf("got %+v, want no stale event while probe is failing", e)
	}
}

func TestClassifyPRMergedFiresOnce(t *testing.T) {
	ts := NewTaskState(herdr.StatusWorking, time.Now())

	if e := ClassifyPRMerged(ts, "task-1", false); e != nil {
		t.Fatalf("got %+v, want no event when not merged", e)
	}
	if e := ClassifyPRMerged(ts, "task-1", true); e == nil || e.Kind != KindPRMerged {
		t.Fatalf("got %+v, want pr-merged event", e)
	}
	if e := ClassifyPRMerged(ts, "task-1", true); e != nil {
		t.Fatalf("pr-merged fired again: %+v", e)
	}
}
