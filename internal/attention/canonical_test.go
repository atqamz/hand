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

func TestDeriveCanonicalPartialAttentionWaitsForBoundedInputCondition(t *testing.T) {
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
	for _, operations := range [][]store.CanonicalV19SnapshotOperation{nil, {{
		ID: "wake-1", Kind: "worker-wake", State: "submitted", ProjectID: "project-1", TaskID: "task-1",
		PlanID: "plan-1", AttemptID: "attempt-1", SessionBindingID: "session-1", ExecutorBindingID: "executor-1",
		PendingThroughOrdinal: 1,
	}}} {
		snapshot.UnresolvedOperations = operations
		got := DeriveCanonicalPartial(snapshot)
		if got.Completeness != "partial" || len(got.Items) != len(operations) {
			t.Fatalf("unbounded input became Attention: %#v", got)
		}
		if len(operations) == 1 && (got.Items[0].Code != "worker-wake-unresolved" || got.Items[0].EvidenceID != "wake-1") {
			t.Fatalf("Wake mechanism lost exact Attention: %#v", got)
		}
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
