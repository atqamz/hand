package store

import (
	"context"
	"database/sql"
	"fmt"
)

type CanonicalV19SnapshotOperation struct {
	ID                    string
	Kind                  string
	State                 string
	ProjectID             string
	TaskID                string
	PlanID                string
	AttemptID             string
	ScopeKind             string
	ScopeKey              string
	StateChangedAt        string
	StateEvidenceDigest   string
	SessionBindingID      string
	ExecutorBindingID     string
	PendingThroughOrdinal int64
}

func readCanonicalV19SnapshotOperations(ctx context.Context, tx *sql.Tx) ([]CanonicalV19SnapshotOperation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT o.id,o.kind,o.state,o.project_id,
		COALESCE(o.task_id,''),COALESCE(o.plan_id,''),COALESCE(o.attempt_id,''),
		o.primary_scope_kind,o.primary_scope_key,o.state_changed_at,o.state_evidence_digest,
		COALESCE(w.session_binding_id,''),COALESCE(w.executor_binding_id,''),
		COALESCE(w.pending_through_ordinal,0)
		FROM external_operation o
		LEFT JOIN worker_wake_operation w ON w.operation_id=o.id
		WHERE o.state IN ('prepared','submitted','uncertain')
		ORDER BY o.project_id,o.kind,o.created_at,o.id`)
	if err != nil {
		return nil, fmt.Errorf("read canonical FleetSnapshot unresolved operations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	operations := make([]CanonicalV19SnapshotOperation, 0)
	for rows.Next() {
		var operation CanonicalV19SnapshotOperation
		if err := rows.Scan(&operation.ID, &operation.Kind, &operation.State, &operation.ProjectID,
			&operation.TaskID, &operation.PlanID, &operation.AttemptID, &operation.ScopeKind,
			&operation.ScopeKey, &operation.StateChangedAt, &operation.StateEvidenceDigest,
			&operation.SessionBindingID, &operation.ExecutorBindingID, &operation.PendingThroughOrdinal); err != nil {
			return nil, fmt.Errorf("scan canonical FleetSnapshot unresolved operation: %w", err)
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canonical FleetSnapshot unresolved operations: %w", err)
	}
	return operations, nil
}
