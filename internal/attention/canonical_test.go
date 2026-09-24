package attention

import (
	"fmt"
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
			{ID: "remove-1", Kind: "worktree-remove", State: "submitted", ProjectID: "project-1", TaskID: "task-1", PlanID: "plan-1", AttemptID: "attempt-1", ScopeKind: "worktree", ScopeKey: "binding-1"},
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
		if item.OperationKind == "worker-wake" &&
			((item.OperationState == "uncertain" && item.Code != "worker-wake-uncertain") ||
				(item.OperationState != "uncertain" && item.Code != "worker-wake-unresolved") ||
				item.SessionBindingID == "" || item.ExecutorBindingID == "") {
			t.Fatalf("WorkerWake lost exact mechanism identity: %#v", item)
		}
	}
	if first.Items[0].Code != "external-operation-unresolved" || first.Items[0].ScopeKind != "worktree" || first.Items[0].ScopeKey != "binding-1" {
		t.Fatalf("WorktreeRemove Attention lost exact binding scope: %#v", first.Items[0])
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

func TestDeriveCanonicalPartialAttentionShowsCurrentInputWithoutWake(t *testing.T) {
	snapshot := store.CanonicalV19FleetSnapshot{
		FleetID: "fleet-1",
		UnacknowledgedInputs: []store.CanonicalV19SnapshotWorkerInput{{
			ID: "input-1", AttemptID: "attempt-1", ExecutorBindingID: "executor-1", Ordinal: 1,
		}},
		Projects: []store.CanonicalV19SnapshotProject{{
			ID: "project-1", Tasks: []store.CanonicalV19SnapshotTask{{
				ID: "task-1", Plan: &store.CanonicalV19SnapshotPlan{
					ID: "plan-1", Attempt: &store.CanonicalV19SnapshotAttempt{ID: "attempt-1"},
				},
			}},
		}},
	}
	got := DeriveCanonicalPartial(snapshot)
	if len(got.Items) != 1 || got.Items[0].Code != "current-worker-input-unacknowledged" ||
		got.Items[0].EvidenceID != "input-1" || got.Items[0].ProjectID != "project-1" ||
		got.Items[0].TaskID != "task-1" || got.Items[0].PlanID != "plan-1" ||
		got.Items[0].AttemptID != "attempt-1" || got.Items[0].ExecutorBindingID != "executor-1" {
		t.Fatalf("current input without Wake = %#v", got)
	}
}

func TestDeriveCanonicalPartialAttentionKeepsExactLatestHandlingWorthyReport(t *testing.T) {
	snapshot := store.CanonicalV19FleetSnapshot{
		FleetID: "fleet-1",
		Projects: []store.CanonicalV19SnapshotProject{
			{ID: "project-b", Tasks: []store.CanonicalV19SnapshotTask{{ID: "task-b", Plan: &store.CanonicalV19SnapshotPlan{ID: "plan-b", Attempt: &store.CanonicalV19SnapshotAttempt{ID: "attempt-b"}}}}},
			{ID: "project-a", Tasks: []store.CanonicalV19SnapshotTask{{ID: "task-a", Plan: &store.CanonicalV19SnapshotPlan{ID: "plan-a", Attempt: &store.CanonicalV19SnapshotAttempt{ID: "attempt-a"}}}}},
		},
		LatestReportMetadata: []store.CanonicalV19SnapshotLatestWorkerReportMetadata{
			{AttemptID: "attempt-b", ReportID: "report-b", ReportState: "needs-decision"},
			{AttemptID: "attempt-old", ReportID: "report-old", ReportState: "blocked"},
			{AttemptID: "attempt-a", ReportID: "report-a", ReportState: "failed"},
		},
	}
	before := append([]store.CanonicalV19SnapshotLatestWorkerReportMetadata(nil), snapshot.LatestReportMetadata...)
	result := DeriveCanonicalPartial(snapshot)
	if result.Completeness != "partial" || len(result.Items) != 2 {
		t.Fatalf("latest report Attention = %#v", result)
	}
	if !reflect.DeepEqual(snapshot.LatestReportMetadata, before) {
		t.Fatal("Attention derivation mutated latest report metadata")
	}
	for i, want := range []struct{ project, task, plan, attempt, report, state string }{
		{"project-a", "task-a", "plan-a", "attempt-a", "report-a", "failed"},
		{"project-b", "task-b", "plan-b", "attempt-b", "report-b", "needs-decision"},
	} {
		item := result.Items[i]
		if item.Priority != 20 || item.Code != "current-latest-worker-report-unacknowledged" ||
			item.FleetID != "fleet-1" || item.ProjectID != want.project || item.TaskID != want.task ||
			item.PlanID != want.plan || item.AttemptID != want.attempt || item.EvidenceID != want.report || item.ReportState != want.state ||
			len(item.AvailableActions) != 0 {
			t.Fatalf("report Attention lost exact lineage or claimed action = %#v", item)
		}
	}
	snapshot.LatestReportMetadata[0], snapshot.LatestReportMetadata[2] = snapshot.LatestReportMetadata[2], snapshot.LatestReportMetadata[0]
	if again := DeriveCanonicalPartial(snapshot); !reflect.DeepEqual(again, result) {
		t.Fatalf("report Attention depends on arrival order: first=%#v again=%#v", result, again)
	}
}

func TestDeriveCanonicalPartialAttentionExcludesAcknowledgedAndWorkingReports(t *testing.T) {
	for _, state := range []string{"paused", "blocked", "needs-decision", "done", "failed", "working"} {
		t.Run(state, func(t *testing.T) {
			snapshot := store.CanonicalV19FleetSnapshot{
				FleetID:              "fleet-1",
				Projects:             []store.CanonicalV19SnapshotProject{{ID: "project-1", Tasks: []store.CanonicalV19SnapshotTask{{ID: "task-1", Plan: &store.CanonicalV19SnapshotPlan{ID: "plan-1", Attempt: &store.CanonicalV19SnapshotAttempt{ID: "attempt-1"}}}}}},
				LatestReportMetadata: []store.CanonicalV19SnapshotLatestWorkerReportMetadata{{AttemptID: "attempt-1", ReportID: "report-1", ReportState: state}},
			}
			result := DeriveCanonicalPartial(snapshot)
			want := 1
			if state == "working" {
				want = 0
			}
			if len(result.Items) != want {
				t.Fatalf("%s report Attention = %#v, want %d", state, result, want)
			}
			snapshot.LatestReportMetadata[0].AcknowledgementPresent = true
			if result := DeriveCanonicalPartial(snapshot); len(result.Items) != 0 {
				t.Fatalf("acknowledged %s report became Attention = %#v", state, result)
			}
		})
	}
}

func TestDeriveCanonicalPartialAttentionRequiresExactReportAndAttemptIDs(t *testing.T) {
	for _, report := range []store.CanonicalV19SnapshotLatestWorkerReportMetadata{
		{AttemptID: "", ReportID: "report-1", ReportState: "blocked"},
		{AttemptID: "attempt-1", ReportID: "", ReportState: "blocked"},
	} {
		snapshot := store.CanonicalV19FleetSnapshot{
			FleetID:              "fleet-1",
			Projects:             []store.CanonicalV19SnapshotProject{{ID: "project-1", Tasks: []store.CanonicalV19SnapshotTask{{ID: "task-1", Plan: &store.CanonicalV19SnapshotPlan{ID: "plan-1", Attempt: &store.CanonicalV19SnapshotAttempt{ID: report.AttemptID}}}}}},
			LatestReportMetadata: []store.CanonicalV19SnapshotLatestWorkerReportMetadata{report},
		}
		if result := DeriveCanonicalPartial(snapshot); len(result.Items) != 0 {
			t.Fatalf("incomplete report identity became Attention: %#v", result)
		}
	}
}

func TestDeriveCanonicalPartialAttentionSeparatesUncertainWakeFromUnsubmittedWake(t *testing.T) {
	snapshot := store.CanonicalV19FleetSnapshot{
		FleetID: "fleet-1",
		UnresolvedOperations: []store.CanonicalV19SnapshotOperation{
			{ID: "wake-prepared", Kind: "worker-wake", State: "prepared", ProjectID: "project-1", TaskID: "task-1", PlanID: "plan-1", AttemptID: "attempt-1", SessionBindingID: "session-1", ExecutorBindingID: "executor-1"},
			{ID: "wake-uncertain", Kind: "worker-wake", State: "uncertain", ProjectID: "project-2", TaskID: "task-2", PlanID: "plan-2", AttemptID: "attempt-2", SessionBindingID: "session-2", ExecutorBindingID: "executor-2"},
		},
	}
	result := DeriveCanonicalPartial(snapshot)
	if len(result.Items) != 2 || result.Items[0].Code != "worker-wake-unresolved" ||
		result.Items[1].Code != "worker-wake-uncertain" || result.Items[1].EvidenceID != "wake-uncertain" ||
		result.Items[1].AttemptID != "attempt-2" || result.Items[1].ExecutorBindingID != "executor-2" ||
		result.Items[1].Priority != 10 || len(result.Items[1].AvailableActions) != 0 {
		t.Fatalf("uncertain WorkerWake collapsed into unsubmitted mechanism state: %#v", result.Items)
	}
}

func TestDeriveCanonicalPartialAttentionSeparatesCurrentInputFromExactUnresolvedWake(t *testing.T) {
	snapshot := store.CanonicalV19FleetSnapshot{
		FleetID: "fleet-1",
		Projects: []store.CanonicalV19SnapshotProject{{ID: "project-1", Tasks: []store.CanonicalV19SnapshotTask{{
			ID: "task-1", Plan: &store.CanonicalV19SnapshotPlan{
				ID: "plan-1", Attempt: &store.CanonicalV19SnapshotAttempt{ID: "attempt-1"},
			},
		}}}},
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
	if first.Completeness != "partial" || len(first.Items) != 5 {
		t.Fatalf("exact input/wake Attention count = %d", len(first.Items))
	}
	for i, id := range []string{"input-1", "input-2", "input-3"} {
		item := first.Items[i+2]
		if item.Priority != 20 || item.Code != "current-worker-input-unacknowledged" || item.FleetID != "fleet-1" ||
			item.ProjectID != "project-1" || item.TaskID != "task-1" || item.PlanID != "plan-1" ||
			item.AttemptID != "attempt-1" || item.ExecutorBindingID != "executor-1" || item.EvidenceID != id ||
			item.OperationKind != "" || item.OperationState != "" || len(item.AvailableActions) != 0 {
			t.Fatalf("semantic input lost exact identity or gained mechanism/action authority: %#v", item)
		}
	}
	if first.Items[0].Code != "worker-wake-unresolved" || first.Items[1].Code != "worker-wake-uncertain" {
		t.Fatalf("wake mechanism Attention collapsed into input: %#v", first.Items)
	}
	snapshot.UnacknowledgedInputs[0], snapshot.UnacknowledgedInputs[2] = snapshot.UnacknowledgedInputs[2], snapshot.UnacknowledgedInputs[0]
	snapshot.UnresolvedOperations[0], snapshot.UnresolvedOperations[1] = snapshot.UnresolvedOperations[1], snapshot.UnresolvedOperations[0]
	if again := DeriveCanonicalPartial(snapshot); !reflect.DeepEqual(again, first) {
		t.Fatalf("Attention depends on input or wake arrival order: first=%#v again=%#v", first, again)
	}
}

func TestDeriveCanonicalPartialAttentionInputIsIndependentOfWake(t *testing.T) {
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
				Projects: []store.CanonicalV19SnapshotProject{{ID: "project-1", Tasks: []store.CanonicalV19SnapshotTask{{
					ID: "task-1", Plan: &store.CanonicalV19SnapshotPlan{
						ID: "plan-1", Attempt: &store.CanonicalV19SnapshotAttempt{ID: "attempt-1"},
					},
				}}}},
				UnacknowledgedInputs: []store.CanonicalV19SnapshotWorkerInput{input},
				UnresolvedOperations: []store.CanonicalV19SnapshotOperation{other}}
			result := DeriveCanonicalPartial(snapshot)
			if result.Completeness != "partial" || len(result.Items) != 2 || result.Items[0].EvidenceID != wake.ID ||
				result.Items[1].EvidenceID != input.ID || result.Items[1].ProjectID != "project-1" ||
				result.Items[1].TaskID != "task-1" || result.Items[1].PlanID != "plan-1" {
				t.Fatalf("Wake changed exact input Attention: %#v", result)
			}
		})
	}
}

func TestDeriveCanonicalPartialAttentionKeepsLargeExactWakeSetOrdered(t *testing.T) {
	const count = 4096
	snapshot := store.CanonicalV19FleetSnapshot{FleetID: "fleet-1"}
	for i := count - 1; i >= 0; i-- {
		id := fmt.Sprintf("%04d", i)
		snapshot.Projects = append(snapshot.Projects, store.CanonicalV19SnapshotProject{
			ID: "project-" + id, Tasks: []store.CanonicalV19SnapshotTask{{
				ID: "task-" + id, Plan: &store.CanonicalV19SnapshotPlan{
					ID: "plan-" + id, Attempt: &store.CanonicalV19SnapshotAttempt{ID: "attempt-" + id},
				},
			}},
		})
		snapshot.UnacknowledgedInputs = append(snapshot.UnacknowledgedInputs, store.CanonicalV19SnapshotWorkerInput{
			ID: "input-" + id, AttemptID: "attempt-" + id, ExecutorBindingID: "executor-" + id, Ordinal: 1,
		})
		snapshot.UnresolvedOperations = append(snapshot.UnresolvedOperations, store.CanonicalV19SnapshotOperation{
			ID: "wake-" + id, Kind: "worker-wake", State: "submitted", ProjectID: "project-" + id,
			TaskID: "task-" + id, PlanID: "plan-" + id, AttemptID: "attempt-" + id,
			SessionBindingID: "session-" + id, ExecutorBindingID: "executor-" + id, PendingThroughOrdinal: 1,
		})
	}
	first := DeriveCanonicalPartial(snapshot)
	if first.Completeness != "partial" || len(first.Items) != count*2 {
		t.Fatalf("large exact wake set has %d items, want %d", len(first.Items), count*2)
	}
	for i := range count {
		id := fmt.Sprintf("%04d", i)
		if first.Items[i].EvidenceID != "wake-"+id || first.Items[count+i].EvidenceID != "input-"+id ||
			first.Items[count+i].ProjectID != "project-"+id || first.Items[count+i].ExecutorBindingID != "executor-"+id {
			t.Fatalf("large exact wake order at %d: wake=%#v input=%#v", i, first.Items[i], first.Items[count+i])
		}
	}
	if again := DeriveCanonicalPartial(snapshot); !reflect.DeepEqual(again, first) {
		t.Fatal("large exact wake set changed between identical reads")
	}
}

func TestDeriveCanonicalPartialAttentionNeverTakesInputOwnerFromWake(t *testing.T) {
	snapshot := store.CanonicalV19FleetSnapshot{
		FleetID: "fleet-1",
		Projects: []store.CanonicalV19SnapshotProject{{ID: "project-real", Tasks: []store.CanonicalV19SnapshotTask{{
			ID: "task-real", Plan: &store.CanonicalV19SnapshotPlan{
				ID: "plan-real", Attempt: &store.CanonicalV19SnapshotAttempt{ID: "attempt-1"},
			},
		}}}},
		UnacknowledgedInputs: []store.CanonicalV19SnapshotWorkerInput{
			{ID: "input-2", AttemptID: "attempt-1", ExecutorBindingID: "executor-1", Ordinal: 2},
			{ID: "input-1", AttemptID: "attempt-1", ExecutorBindingID: "executor-1", Ordinal: 1},
		},
		UnresolvedOperations: []store.CanonicalV19SnapshotOperation{
			{ID: "wake-c", Kind: "worker-wake", State: "submitted", ProjectID: "project-c", TaskID: "task-c", PlanID: "plan-c", AttemptID: "attempt-1", SessionBindingID: "session-c", ExecutorBindingID: "executor-1", PendingThroughOrdinal: 3},
			{ID: "wake-a", Kind: "worker-wake", State: "submitted", ProjectID: "project-a", TaskID: "task-a", PlanID: "plan-a", AttemptID: "attempt-1", SessionBindingID: "session-a", ExecutorBindingID: "executor-1", PendingThroughOrdinal: 1},
			{ID: "wake-b", Kind: "worker-wake", State: "uncertain", ProjectID: "project-b", TaskID: "task-b", PlanID: "plan-b", AttemptID: "attempt-1", SessionBindingID: "session-b", ExecutorBindingID: "executor-1", PendingThroughOrdinal: 2},
		},
	}
	result := DeriveCanonicalPartial(snapshot)
	if len(result.Items) != 5 || result.Items[3].EvidenceID != "input-1" || result.Items[4].EvidenceID != "input-2" ||
		result.Items[3].ProjectID != "project-real" || result.Items[4].ProjectID != "project-real" ||
		result.Items[3].TaskID != "task-real" || result.Items[4].TaskID != "task-real" {
		t.Fatalf("Wake misattributed current input: %#v", result)
	}
}

func BenchmarkDeriveCanonicalPartialLargeExactWakeSet(b *testing.B) {
	const count = 4096
	snapshot := store.CanonicalV19FleetSnapshot{FleetID: "fleet-1"}
	for i := range count {
		id := fmt.Sprintf("%04d", i)
		snapshot.Projects = append(snapshot.Projects, store.CanonicalV19SnapshotProject{
			ID: "project-" + id, Tasks: []store.CanonicalV19SnapshotTask{{
				ID: "task-" + id, Plan: &store.CanonicalV19SnapshotPlan{
					ID: "plan-" + id, Attempt: &store.CanonicalV19SnapshotAttempt{ID: "attempt-" + id},
				},
			}},
		})
		snapshot.UnacknowledgedInputs = append(snapshot.UnacknowledgedInputs, store.CanonicalV19SnapshotWorkerInput{
			ID: "input-" + id, AttemptID: "attempt-" + id, ExecutorBindingID: "executor-" + id, Ordinal: 1,
		})
		snapshot.UnresolvedOperations = append(snapshot.UnresolvedOperations, store.CanonicalV19SnapshotOperation{
			ID: "wake-" + id, Kind: "worker-wake", State: "submitted", ProjectID: "project-" + id,
			TaskID: "task-" + id, PlanID: "plan-" + id, AttemptID: "attempt-" + id,
			SessionBindingID: "session-" + id, ExecutorBindingID: "executor-" + id, PendingThroughOrdinal: 1,
		})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		DeriveCanonicalPartial(snapshot)
	}
}
