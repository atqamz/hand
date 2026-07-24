package watcher

import (
	"fmt"
	"time"

	"github.com/atqamz/secondhand/internal/herdr"
)

// Kind values classify an Event for dashboard/log routing.
const (
	KindDone     = "done"
	KindBlocked  = "blocked"
	KindFailed   = "failed"
	KindStale    = "stale"
	KindPRMerged = "pr-merged"
)

// blockedReason is the only detail available: herdr reports agent_status without a
// free-text cause, so this mirrors herdr's own "blocked" state description.
const blockedReason = "agent needs help"

type Event struct {
	TaskID string
	Kind   string
	Text   string
	Reason string
}

// TaskState tracks what's already been observed and announced for one task, so the
// classifier only fires once per transition instead of once per poll tick.
type TaskState struct {
	Status    herdr.Status
	Probed    bool
	ChangedAt time.Time
	Blocked   bool
	Stale     bool
	PRMerged  bool
}

// NewTaskState seeds tracking for a task first observed at now, without emitting an
// event for whatever state it happens to already be in.
func NewTaskState(status herdr.Status, now time.Time) *TaskState {
	return &TaskState{Status: status, Probed: true, ChangedAt: now}
}

// ClassifyStatus compares a freshly probed status against ts and returns an
// actionable event for the transitions SPECS.md calls out (done, blocked, failed).
// Benign transitions (into working, repeated done/idle/blocked) update ts in place
// and return nil.
func ClassifyStatus(ts *TaskState, id string, status herdr.Status, probeErr error, now time.Time) *Event {
	if probeErr != nil {
		wasProbed := ts.Probed
		ts.Probed = false
		if wasProbed {
			return &Event{TaskID: id, Kind: KindFailed, Text: fmt.Sprintf("failed %s", id)}
		}
		return nil
	}
	ts.Probed = true

	if status == ts.Status {
		return nil
	}
	prevStatus := ts.Status
	ts.Status = status
	ts.ChangedAt = now
	ts.Stale = false

	switch status {
	case herdr.StatusDone, herdr.StatusIdle:
		if prevStatus == herdr.StatusWorking || prevStatus == herdr.StatusBlocked {
			ts.Blocked = false
			return &Event{TaskID: id, Kind: KindDone, Text: fmt.Sprintf("done %s", id)}
		}
	case herdr.StatusBlocked:
		if !ts.Blocked {
			ts.Blocked = true
			return &Event{TaskID: id, Kind: KindBlocked, Text: fmt.Sprintf("blocked %s: %s", id, blockedReason), Reason: blockedReason}
		}
	default:
		ts.Blocked = false
	}
	return nil
}

// ClassifyStale fires once per stale window: when a task's status hasn't changed
// for at least threshold, and it hasn't already been flagged since the last change.
func ClassifyStale(ts *TaskState, id string, now time.Time, threshold time.Duration) *Event {
	if !ts.Probed || ts.Stale {
		return nil
	}
	if now.Sub(ts.ChangedAt) < threshold {
		return nil
	}
	ts.Stale = true
	return &Event{TaskID: id, Kind: KindStale, Text: fmt.Sprintf("stale %s", id)}
}

// ClassifyPRMerged fires once, the first time merged is observed true for a task.
func ClassifyPRMerged(ts *TaskState, id string, merged bool) *Event {
	if !merged || ts.PRMerged {
		return nil
	}
	ts.PRMerged = true
	return &Event{TaskID: id, Kind: KindPRMerged, Text: fmt.Sprintf("pr-merged %s", id)}
}
