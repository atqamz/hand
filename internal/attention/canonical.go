package attention

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/atqamz/hand/internal/store"
)

type CanonicalPartialAttention struct {
	Completeness string
	Items        []CanonicalItem
}

type CanonicalItem struct {
	Priority          int
	Code              string
	FleetID           string
	ProjectID         string
	TaskID            string
	PlanID            string
	AttemptID         string
	EvidenceID        string
	OperationKind     string
	OperationState    string
	ScopeKind         string
	ScopeKey          string
	ReportState       string
	SessionBindingID  string
	ExecutorBindingID string
	Reason            string
	AvailableActions  []string
}

func DeriveCanonicalPartial(snapshot store.CanonicalV19FleetSnapshot) CanonicalPartialAttention {
	type activeAttemptOwner struct {
		projectID string
		taskID    string
		planID    string
	}
	result := CanonicalPartialAttention{Completeness: "partial", Items: make([]CanonicalItem, 0, len(snapshot.UnresolvedOperations)+len(snapshot.UnacknowledgedInputs)+len(snapshot.LatestReportMetadata))}
	activeAttemptOwners := make(map[string]activeAttemptOwner)
	for _, project := range snapshot.Projects {
		for _, task := range project.Tasks {
			if task.Plan != nil && task.Plan.Attempt != nil && task.Plan.Attempt.ID != "" {
				activeAttemptOwners[task.Plan.Attempt.ID] = activeAttemptOwner{project.ID, task.ID, task.Plan.ID}
			}
		}
	}
	for _, report := range snapshot.LatestReportMetadata {
		if report.ReportID == "" || report.AttemptID == "" || report.AcknowledgementPresent {
			continue
		}
		switch report.ReportState {
		case "paused", "blocked", "needs-decision", "done", "failed":
		default:
			continue
		}
		owner, ok := activeAttemptOwners[report.AttemptID]
		if !ok || owner.projectID == "" || owner.taskID == "" || owner.planID == "" {
			continue
		}
		result.Items = append(result.Items, CanonicalItem{
			Priority: 20, Code: "current-latest-worker-report-unacknowledged",
			FleetID: snapshot.FleetID, ProjectID: owner.projectID, TaskID: owner.taskID,
			PlanID: owner.planID, AttemptID: report.AttemptID, EvidenceID: report.ReportID,
			ReportState: report.ReportState,
			Reason:      fmt.Sprintf("latest WorkerReport claims %s and remains unacknowledged", report.ReportState),
		})
	}
	for i := range snapshot.UnresolvedOperations {
		operation := &snapshot.UnresolvedOperations[i]
		code := "external-operation-unresolved"
		if operation.Kind == "worker-wake" {
			code = "worker-wake-unresolved"
			if operation.State == "uncertain" {
				code = "worker-wake-uncertain"
			}
		}
		result.Items = append(result.Items, CanonicalItem{
			Priority: 10, Code: code,
			FleetID: snapshot.FleetID, ProjectID: operation.ProjectID, TaskID: operation.TaskID,
			PlanID: operation.PlanID, AttemptID: operation.AttemptID, EvidenceID: operation.ID,
			OperationKind: operation.Kind, OperationState: operation.State,
			ScopeKind: operation.ScopeKind, ScopeKey: operation.ScopeKey,
			SessionBindingID: operation.SessionBindingID, ExecutorBindingID: operation.ExecutorBindingID,
			Reason: fmt.Sprintf("external operation remains %s", operation.State),
		})
	}
	for _, input := range snapshot.UnacknowledgedInputs {
		if input.ID == "" || input.AttemptID == "" || input.ExecutorBindingID == "" || input.Ordinal <= 0 {
			continue
		}
		owner, ok := activeAttemptOwners[input.AttemptID]
		if !ok || owner.projectID == "" || owner.taskID == "" || owner.planID == "" {
			continue
		}
		result.Items = append(result.Items, CanonicalItem{
			Priority: 20, Code: "current-worker-input-unacknowledged",
			FleetID: snapshot.FleetID, ProjectID: owner.projectID, TaskID: owner.taskID,
			PlanID: owner.planID, AttemptID: input.AttemptID, EvidenceID: input.ID,
			ExecutorBindingID: input.ExecutorBindingID,
			Reason:            "exact current WorkerInput remains unacknowledged",
		})
	}
	slices.SortFunc(result.Items, func(a, b CanonicalItem) int {
		return cmp.Or(cmp.Compare(a.Priority, b.Priority), cmp.Compare(a.ProjectID, b.ProjectID),
			cmp.Compare(a.TaskID, b.TaskID), cmp.Compare(a.PlanID, b.PlanID), cmp.Compare(a.AttemptID, b.AttemptID),
			cmp.Compare(a.EvidenceID, b.EvidenceID), cmp.Compare(a.Code, b.Code),
			cmp.Compare(a.SessionBindingID, b.SessionBindingID), cmp.Compare(a.ExecutorBindingID, b.ExecutorBindingID),
			cmp.Compare(a.OperationKind, b.OperationKind), cmp.Compare(a.OperationState, b.OperationState),
			cmp.Compare(a.ScopeKind, b.ScopeKind), cmp.Compare(a.ScopeKey, b.ScopeKey),
			cmp.Compare(a.ReportState, b.ReportState))
	})
	return result
}
