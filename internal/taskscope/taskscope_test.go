package taskscope_test

import (
	"testing"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/taskscope"
)

func TestTheTaskStatusValuesAreTheStatesTheStoreUses(t *testing.T) {
	want := []string{"", "finished", "all", state.StatusDone, state.StatusAbandoned, state.StatusActive, state.StatusInbox}
	if len(taskscope.Scopes) != len(want) {
		t.Fatalf("scopes = %d, want %d", len(taskscope.Scopes), len(want))
	}
	for i, value := range want {
		if got := taskscope.Scopes[i].Value; got != value {
			t.Errorf("scope %d = %q, want %q", i, got, value)
		}
	}
}
