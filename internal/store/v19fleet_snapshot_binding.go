package store

import (
	"context"
	"database/sql"
	"fmt"
)

type CanonicalV19SnapshotCurrentWorktreeBinding struct {
	ProjectID              string
	TaskID                 string
	PlanID                 string
	AttemptID              string
	ID                     string
	CreateOperationID      string
	Path                   string
	CommonGitDir           string
	PrivateGitDir          string
	LockReason             string
	BasisRevision          string
	HeadRevision           string
	PhysicalIdentityDigest string
	EstablishedAt          string
}

type CanonicalV19SnapshotCurrentSessionBinding struct {
	ProjectID          string
	TaskID             string
	PlanID             string
	AttemptID          string
	WorktreeBindingID  string
	ID                 string
	Ordinal            int64
	AcquireOperationID string
	AdapterRef         string
	ProviderSessionKey string
	EstablishedAt      string
}

const canonicalV19SnapshotCurrentWorktreeBindingsQuery = `SELECT pr.id,t.id,p.id,a.id,
		b.id,b.create_operation_id,b.path,b.common_git_dir,b.private_git_dir,b.lock_reason,
		b.basis_revision,b.head_revision,b.physical_identity_digest,b.established_at
		FROM task t INDEXED BY task_active_by_project
		CROSS JOIN project pr
		CROSS JOIN plan p INDEXED BY plan_active_by_task
		CROSS JOIN attempt a INDEXED BY attempt_active_by_plan
		CROSS JOIN attempt_worktree_binding b
		WHERE t.lifecycle='active'
		  AND pr.id=t.project_id
		  AND p.task_id=t.id AND p.lifecycle='active'
		  AND a.plan_id=p.id AND a.lifecycle='active'
		  AND b.attempt_id=a.id
		  AND NOT EXISTS (SELECT 1 FROM worktree_binding_release r WHERE r.binding_id=b.id)
		ORDER BY pr.ordinal,pr.id,t.ordinal,t.id,p.ordinal,p.id,a.ordinal,a.id,b.id`

const canonicalV19SnapshotCurrentSessionBindingsQuery = `SELECT pr.id,t.id,p.id,a.id,
		b.id,s.id,s.ordinal,s.acquire_operation_id,s.adapter_ref,s.provider_session_key,s.established_at
		FROM task t INDEXED BY task_active_by_project
		CROSS JOIN project pr
		CROSS JOIN plan p INDEXED BY plan_active_by_task
		CROSS JOIN attempt a INDEXED BY attempt_active_by_plan
		CROSS JOIN attempt_worktree_binding b
		CROSS JOIN session_binding s INDEXED BY session_binding_attempt_history
		WHERE t.lifecycle='active'
		  AND pr.id=t.project_id
		  AND p.task_id=t.id AND p.lifecycle='active'
		  AND a.plan_id=p.id AND a.lifecycle='active'
		  AND b.attempt_id=a.id
		  AND s.attempt_id=a.id AND s.worktree_binding_id=b.id
		  AND NOT EXISTS (SELECT 1 FROM worktree_binding_release r WHERE r.binding_id=b.id)
		  AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)
		ORDER BY pr.ordinal,pr.id,t.ordinal,t.id,p.ordinal,p.id,a.ordinal,a.id,s.ordinal,s.id`

func readCanonicalV19SnapshotCurrentWorktreeBindings(ctx context.Context, tx *sql.Tx) ([]CanonicalV19SnapshotCurrentWorktreeBinding, error) {
	rows, err := tx.QueryContext(ctx, canonicalV19SnapshotCurrentWorktreeBindingsQuery)
	if err != nil {
		return nil, fmt.Errorf("read canonical FleetSnapshot current Worktrees: %w", err)
	}
	defer func() { _ = rows.Close() }()
	bindings := make([]CanonicalV19SnapshotCurrentWorktreeBinding, 0)
	for rows.Next() {
		var binding CanonicalV19SnapshotCurrentWorktreeBinding
		if err := rows.Scan(&binding.ProjectID, &binding.TaskID, &binding.PlanID, &binding.AttemptID,
			&binding.ID, &binding.CreateOperationID, &binding.Path, &binding.CommonGitDir, &binding.PrivateGitDir, &binding.LockReason,
			&binding.BasisRevision, &binding.HeadRevision, &binding.PhysicalIdentityDigest, &binding.EstablishedAt); err != nil {
			return nil, fmt.Errorf("scan canonical FleetSnapshot current Worktree: %w", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canonical FleetSnapshot current Worktrees: %w", err)
	}
	return bindings, nil
}

func readCanonicalV19SnapshotCurrentSessionBindings(ctx context.Context, tx *sql.Tx) ([]CanonicalV19SnapshotCurrentSessionBinding, error) {
	rows, err := tx.QueryContext(ctx, canonicalV19SnapshotCurrentSessionBindingsQuery)
	if err != nil {
		return nil, fmt.Errorf("read canonical FleetSnapshot current Sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	bindings := make([]CanonicalV19SnapshotCurrentSessionBinding, 0)
	for rows.Next() {
		var binding CanonicalV19SnapshotCurrentSessionBinding
		if err := rows.Scan(&binding.ProjectID, &binding.TaskID, &binding.PlanID, &binding.AttemptID,
			&binding.WorktreeBindingID, &binding.ID, &binding.Ordinal, &binding.AcquireOperationID, &binding.AdapterRef,
			&binding.ProviderSessionKey, &binding.EstablishedAt); err != nil {
			return nil, fmt.Errorf("scan canonical FleetSnapshot current Session: %w", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canonical FleetSnapshot current Sessions: %w", err)
	}
	return bindings, nil
}
