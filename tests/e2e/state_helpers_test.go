//go:build e2e

package e2e

import (
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func writeTaskAttempt(t *testing.T, home string, task state.Task, attempt state.Attempt) {
	t.Helper()
	if err := state.CreateTask(home, task); err != nil {
		t.Fatal(err)
	}
	attempt.TaskID = task.ID
	if _, err := state.CreateAttempt(home, attempt); err != nil {
		t.Fatal(err)
	}
}

func readTaskAttempt(t *testing.T, home, id string) (state.Task, state.Attempt) {
	t.Helper()
	history, err := state.ReadHistory(home, id)
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil {
		t.Fatalf("task %q has no active attempt", id)
	}
	return history.Task, *history.ActiveAttempt
}
