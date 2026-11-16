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
	labels := map[string]bool{}
	for i, value := range want {
		scope := taskscope.Scopes[i]
		if scope.Value != value {
			t.Errorf("scope %d = %q, want %q", i, scope.Value, value)
		}
		if scope.Label == "" || labels[scope.Label] {
			t.Errorf("scope %q has the empty or repeated label %q", scope.Value, scope.Label)
		}
		labels[scope.Label] = true
		if got := taskscope.Label(scope.Value); got != scope.Label {
			t.Errorf("Label(%q) = %q, want %q", scope.Value, got, scope.Label)
		}
	}
	if got := taskscope.Label("none"); got != "none" {
		t.Errorf("Label of an unknown value = %q, want the value", got)
	}
}
