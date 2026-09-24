package store

import (
	"context"
	"database/sql"
	"fmt"
)

type CanonicalV19SnapshotCurrentExecutorBinding struct {
	ProjectID                      string
	TaskID                         string
	PlanID                         string
	AttemptID                      string
	WorktreeBindingID              string
	WorktreePath                   string
	WorktreePhysicalIdentityDigest string
	SessionBindingID               string
	AdapterRef                     string
	ProviderSessionKey             string
	ExecutorBindingID              string
	ProviderExecutorKey            string
}

const canonicalV19SnapshotCurrentExecutorBindingsQuery = `SELECT pr.id,t.id,p.id,a.id,
		b.id,b.path,b.physical_identity_digest,
		s.id,s.adapter_ref,s.provider_session_key,
		e.id,e.provider_executor_key
		FROM task t INDEXED BY task_active_by_project
		CROSS JOIN project pr
		CROSS JOIN plan p INDEXED BY plan_active_by_task
		CROSS JOIN attempt a INDEXED BY attempt_active_by_plan
		CROSS JOIN executor_binding e INDEXED BY executor_binding_attempt
		CROSS JOIN session_binding s
		CROSS JOIN attempt_worktree_binding b
		WHERE t.lifecycle='active'
		  AND pr.id=t.project_id AND pr.retired_at=''
		  AND p.task_id=t.id AND p.lifecycle='active'
		  AND a.plan_id=p.id AND a.lifecycle='active'
		  AND e.attempt_id=a.id
		  AND s.id=e.session_binding_id AND s.attempt_id=a.id AND s.adapter_ref=e.adapter_ref
		  AND b.id=s.worktree_binding_id AND b.attempt_id=a.id
		  AND NOT EXISTS (SELECT 1 FROM executor_binding_termination x WHERE x.executor_binding_id=e.id)
		  AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)
		  AND NOT EXISTS (SELECT 1 FROM worktree_binding_release r WHERE r.binding_id=b.id)
		ORDER BY pr.ordinal,pr.id,t.ordinal,t.id,p.ordinal,p.id,a.ordinal,a.id,s.ordinal,s.id,e.id`

func readCanonicalV19SnapshotCurrentExecutorBindings(ctx context.Context, tx *sql.Tx) ([]CanonicalV19SnapshotCurrentExecutorBinding, error) {
	rows, err := tx.QueryContext(ctx, canonicalV19SnapshotCurrentExecutorBindingsQuery)
	if err != nil {
		return nil, fmt.Errorf("read canonical FleetSnapshot current executors: %w", err)
	}
	defer func() { _ = rows.Close() }()
	executors := make([]CanonicalV19SnapshotCurrentExecutorBinding, 0)
	for rows.Next() {
		var executor CanonicalV19SnapshotCurrentExecutorBinding
		if err := rows.Scan(&executor.ProjectID, &executor.TaskID, &executor.PlanID, &executor.AttemptID,
			&executor.WorktreeBindingID, &executor.WorktreePath, &executor.WorktreePhysicalIdentityDigest,
			&executor.SessionBindingID, &executor.AdapterRef, &executor.ProviderSessionKey,
			&executor.ExecutorBindingID, &executor.ProviderExecutorKey); err != nil {
			return nil, fmt.Errorf("scan canonical FleetSnapshot current executor: %w", err)
		}
		executors = append(executors, executor)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canonical FleetSnapshot current executors: %w", err)
	}
	return executors, nil
}
