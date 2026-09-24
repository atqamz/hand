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
	SessionBindingID  string
	ExecutorBindingID string
	Reason            string
	AvailableActions  []string
}

func DeriveCanonicalPartial(snapshot store.CanonicalV19FleetSnapshot) CanonicalPartialAttention {
	result := CanonicalPartialAttention{Completeness: "partial", Items: make([]CanonicalItem, 0, len(snapshot.UnresolvedOperations))}
	for _, operation := range snapshot.UnresolvedOperations {
		code := "external-operation-unresolved"
		if operation.Kind == "worker-wake" {
			code = "worker-wake-unresolved"
		}
		result.Items = append(result.Items, CanonicalItem{
			Priority: 10, Code: code,
			FleetID: snapshot.FleetID, ProjectID: operation.ProjectID, TaskID: operation.TaskID,
			PlanID: operation.PlanID, AttemptID: operation.AttemptID, EvidenceID: operation.ID,
			OperationKind: operation.Kind, OperationState: operation.State,
			SessionBindingID: operation.SessionBindingID, ExecutorBindingID: operation.ExecutorBindingID,
			Reason: fmt.Sprintf("external operation remains %s", operation.State),
		})
	}
	slices.SortFunc(result.Items, func(a, b CanonicalItem) int {
		return cmp.Or(cmp.Compare(a.Priority, b.Priority), cmp.Compare(a.ProjectID, b.ProjectID),
			cmp.Compare(a.TaskID, b.TaskID), cmp.Compare(a.PlanID, b.PlanID), cmp.Compare(a.AttemptID, b.AttemptID),
			cmp.Compare(a.EvidenceID, b.EvidenceID))
	})
	return result
}
