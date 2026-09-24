package store

import (
	"context"
	"database/sql"
	"fmt"
)

type CanonicalV19SnapshotWorkerInput struct {
	ID                string
	AttemptID         string
	ExecutorBindingID string
	Ordinal           int64
	OriginKind        string
	PayloadDigest     string
	CreatedAt         string
}

func readCanonicalV19SnapshotInputs(ctx context.Context, tx *sql.Tx) ([]CanonicalV19SnapshotWorkerInput, error) {
	rows, err := tx.QueryContext(ctx, `SELECT wi.id,wi.attempt_id,wi.executor_binding_id,wi.ordinal,
		wi.origin_kind,wi.payload_digest,wi.created_at
		FROM project pr
		CROSS JOIN task t INDEXED BY task_active_by_project
		CROSS JOIN plan p INDEXED BY plan_active_by_task
		CROSS JOIN attempt a INDEXED BY attempt_active_by_plan
		CROSS JOIN executor_binding e INDEXED BY executor_binding_attempt
		CROSS JOIN session_binding s
		CROSS JOIN attempt_worktree_binding b
		CROSS JOIN worker_input wi INDEXED BY worker_input_current_order
		WHERE pr.retired_at=''
		  AND t.project_id=pr.id AND t.lifecycle='active'
		  AND p.task_id=t.id AND p.lifecycle='active'
		  AND a.plan_id=p.id AND a.lifecycle='active'
		  AND e.attempt_id=a.id
		  AND s.id=e.session_binding_id AND s.attempt_id=a.id AND s.adapter_ref=e.adapter_ref
		  AND b.id=s.worktree_binding_id AND b.attempt_id=a.id
		  AND wi.executor_binding_id=e.id AND wi.attempt_id=a.id
		  AND NOT EXISTS (SELECT 1 FROM executor_binding_termination x WHERE x.executor_binding_id=e.id)
		  AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)
		  AND NOT EXISTS (SELECT 1 FROM worktree_binding_release r WHERE r.binding_id=b.id)
		  AND NOT EXISTS (SELECT 1 FROM worker_input_acknowledgement ack WHERE ack.worker_input_id=wi.id)
		ORDER BY wi.executor_binding_id,wi.ordinal,wi.id`)
	if err != nil {
		return nil, fmt.Errorf("read canonical FleetSnapshot unacknowledged inputs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	inputs := make([]CanonicalV19SnapshotWorkerInput, 0)
	for rows.Next() {
		var input CanonicalV19SnapshotWorkerInput
		if err := rows.Scan(&input.ID, &input.AttemptID, &input.ExecutorBindingID, &input.Ordinal,
			&input.OriginKind, &input.PayloadDigest, &input.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan canonical FleetSnapshot unacknowledged input: %w", err)
		}
		inputs = append(inputs, input)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canonical FleetSnapshot unacknowledged inputs: %w", err)
	}
	return inputs, nil
}
