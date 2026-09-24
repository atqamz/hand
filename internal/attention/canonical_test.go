package attention

import (
	"reflect"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

func TestDeriveCanonicalPartialAttentionKeepsExactUnresolvedOperationsOrdered(t *testing.T) {
	snapshot := store.CanonicalV19FleetSnapshot{
		FleetID: "fleet-1",
		UnacknowledgedInputs: []store.CanonicalV19SnapshotWorkerInput{
			{ID: "input-1", AttemptID: "attempt-1"},
		},
		UnresolvedOperations: []store.CanonicalV19SnapshotOperation{
			{ID: "wake-2", Kind: "worker-wake", State: "uncertain", ProjectID: "project-2", TaskID: "task-2", PlanID: "plan-2", AttemptID: "attempt-2", SessionBindingID: "session-2", ExecutorBindingID: "executor-2"},
			{ID: "remove-1", Kind: "worktree-remove", State: "submitted", ProjectID: "project-1", TaskID: "task-1", PlanID: "plan-1", AttemptID: "attempt-1"},
			{ID: "wake-1", Kind: "worker-wake", State: "prepared", ProjectID: "project-1", TaskID: "task-1", PlanID: "plan-1", AttemptID: "attempt-1", SessionBindingID: "session-1", ExecutorBindingID: "executor-1"},
		},
	}
	first := DeriveCanonicalPartial(snapshot)
	if first.Completeness != "partial" || len(first.Items) != 3 {
		t.Fatalf("partial operation Attention = %#v", first)
	}
	if got := []string{first.Items[0].EvidenceID, first.Items[1].EvidenceID, first.Items[2].EvidenceID}; !reflect.DeepEqual(got, []string{"remove-1", "wake-1", "wake-2"}) {
		t.Fatalf("exact Attention order = %v", got)
	}
	for _, item := range first.Items {
		if item.Priority != 10 || item.FleetID != "fleet-1" || item.EvidenceID == "input-1" || len(item.AvailableActions) != 0 {
			t.Fatalf("unsafe or misattributed Attention = %#v", item)
		}
		if item.OperationKind == "worker-wake" && (item.Code != "worker-wake-unresolved" || item.SessionBindingID == "" || item.ExecutorBindingID == "") {
			t.Fatalf("WorkerWake lost exact mechanism identity: %#v", item)
		}
	}
	snapshot.UnresolvedOperations[0], snapshot.UnresolvedOperations[2] = snapshot.UnresolvedOperations[2], snapshot.UnresolvedOperations[0]
	if again := DeriveCanonicalPartial(snapshot); !reflect.DeepEqual(again, first) {
		t.Fatalf("Attention depends on row arrival order: first=%#v again=%#v", first, again)
	}
}

func TestDeriveCanonicalPartialAttentionNeverCallsEmptyAllClear(t *testing.T) {
	result := DeriveCanonicalPartial(store.CanonicalV19FleetSnapshot{FleetID: "fleet-1"})
	if result.Completeness != "partial" || len(result.Items) != 0 {
		t.Fatalf("empty partial Attention = %#v", result)
	}
}
