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

func TestDeriveCanonicalPartialAttentionSeparatesCurrentInputFromExactUnresolvedWake(t *testing.T) {
	snapshot := store.CanonicalV19FleetSnapshot{
		FleetID: "fleet-1",
		UnacknowledgedInputs: []store.CanonicalV19SnapshotWorkerInput{
			{ID: "input-2", AttemptID: "attempt-1", ExecutorBindingID: "executor-1", Ordinal: 2},
			{ID: "input-3", AttemptID: "attempt-1", ExecutorBindingID: "executor-1", Ordinal: 3},
			{ID: "input-1", AttemptID: "attempt-1", ExecutorBindingID: "executor-1", Ordinal: 1},
		},
		UnresolvedOperations: []store.CanonicalV19SnapshotOperation{
			{ID: "wake-2", Kind: "worker-wake", State: "submitted", ProjectID: "project-1", TaskID: "task-1",
				PlanID: "plan-1", AttemptID: "attempt-1", SessionBindingID: "session-1",
				ExecutorBindingID: "executor-1", PendingThroughOrdinal: 2},
			{ID: "wake-1", Kind: "worker-wake", State: "uncertain", ProjectID: "project-2", TaskID: "task-2",
				PlanID: "plan-2", AttemptID: "attempt-2", SessionBindingID: "session-2",
				ExecutorBindingID: "executor-2", PendingThroughOrdinal: 2},
		},
	}
	inputsBefore := append([]store.CanonicalV19SnapshotWorkerInput(nil), snapshot.UnacknowledgedInputs...)
	operationsBefore := append([]store.CanonicalV19SnapshotOperation(nil), snapshot.UnresolvedOperations...)
	first := DeriveCanonicalPartial(snapshot)
	if !reflect.DeepEqual(snapshot.UnacknowledgedInputs, inputsBefore) ||
		!reflect.DeepEqual(snapshot.UnresolvedOperations, operationsBefore) {
		t.Fatal("Attention derivation mutated snapshot source")
	}
	if first.Completeness != "partial" || len(first.Items) != 4 {
		t.Fatalf("exact input/wake Attention = %#v", first)
	}
	for i, id := range []string{"input-1", "input-2"} {
		item := first.Items[i+2]
		if item.Priority != 20 || item.Code != "current-worker-input-unacknowledged" || item.FleetID != "fleet-1" ||
			item.ProjectID != "project-1" || item.TaskID != "task-1" || item.PlanID != "plan-1" ||
			item.AttemptID != "attempt-1" || item.ExecutorBindingID != "executor-1" || item.EvidenceID != id ||
			item.OperationKind != "" || item.OperationState != "" || len(item.AvailableActions) != 0 {
			t.Fatalf("semantic input lost exact identity or gained mechanism/action authority: %#v", item)
		}
	}
	if first.Items[0].Code != "worker-wake-unresolved" || first.Items[1].Code != "worker-wake-unresolved" {
		t.Fatalf("wake mechanism Attention collapsed into input: %#v", first.Items)
	}
	snapshot.UnacknowledgedInputs[0], snapshot.UnacknowledgedInputs[2] = snapshot.UnacknowledgedInputs[2], snapshot.UnacknowledgedInputs[0]
	snapshot.UnresolvedOperations[0], snapshot.UnresolvedOperations[1] = snapshot.UnresolvedOperations[1], snapshot.UnresolvedOperations[0]
	if again := DeriveCanonicalPartial(snapshot); !reflect.DeepEqual(again, first) {
		t.Fatalf("Attention depends on input or wake arrival order: first=%#v again=%#v", first, again)
	}
}

func TestDeriveCanonicalPartialAttentionRequiresExactSubmittedOrUncertainWake(t *testing.T) {
	input := store.CanonicalV19SnapshotWorkerInput{ID: "input-1", AttemptID: "attempt-1", ExecutorBindingID: "executor-1", Ordinal: 2}
	wake := store.CanonicalV19SnapshotOperation{
		ID: "wake-1", Kind: "worker-wake", State: "submitted", ProjectID: "project-1", TaskID: "task-1",
		PlanID: "plan-1", AttemptID: "attempt-1", SessionBindingID: "session-1",
		ExecutorBindingID: "executor-1", PendingThroughOrdinal: 2,
	}
	for name, change := range map[string]func(*store.CanonicalV19SnapshotOperation){
		"prepared":       func(w *store.CanonicalV19SnapshotOperation) { w.State = "prepared" },
		"other kind":     func(w *store.CanonicalV19SnapshotOperation) { w.Kind = "interrupt" },
		"other attempt":  func(w *store.CanonicalV19SnapshotOperation) { w.AttemptID = "attempt-2" },
		"other executor": func(w *store.CanonicalV19SnapshotOperation) { w.ExecutorBindingID = "executor-2" },
		"older boundary": func(w *store.CanonicalV19SnapshotOperation) { w.PendingThroughOrdinal = 1 },
		"missing plan":   func(w *store.CanonicalV19SnapshotOperation) { w.PlanID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			other := wake
			change(&other)
			snapshot := store.CanonicalV19FleetSnapshot{FleetID: "fleet-1",
				UnacknowledgedInputs: []store.CanonicalV19SnapshotWorkerInput{input},
				UnresolvedOperations: []store.CanonicalV19SnapshotOperation{other}}
			result := DeriveCanonicalPartial(snapshot)
			if result.Completeness != "partial" || len(result.Items) != 1 || result.Items[0].EvidenceID != wake.ID {
				t.Fatalf("unqualified input became Attention: %#v", result)
			}
		})
	}
}
