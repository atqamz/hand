package store

import (
	"context"
	"database/sql"
	"fmt"
)

type CanonicalV19SnapshotAttemptBackoff struct {
	ProjectID      string
	TaskID         string
	PlanID         string
	AttemptID      string
	ID             string
	Reason         string
	NotBefore      string
	EvidenceDigest string
	CreatedAt      string
}

const canonicalV19SnapshotCurrentAttemptBackoffsQuery = `SELECT pr.id,t.id,p.id,a.id,
		b.id,b.reason,b.not_before,b.evidence_digest,b.created_at
		FROM task t INDEXED BY task_active_by_project
		CROSS JOIN project pr
		CROSS JOIN plan p INDEXED BY plan_active_by_task
		CROSS JOIN attempt a INDEXED BY attempt_active_by_plan
		CROSS JOIN attempt_backoff b INDEXED BY attempt_backoff_attempt_history
		LEFT JOIN attempt_backoff_resolution r ON r.backoff_id=b.id
		WHERE t.lifecycle='active'
		  AND pr.id=t.project_id
		  AND p.task_id=t.id AND p.lifecycle='active'
		  AND a.plan_id=p.id AND a.lifecycle='active'
		  AND b.attempt_id=a.id
		  AND r.backoff_id IS NULL
		ORDER BY pr.ordinal,pr.id,t.ordinal,t.id,p.ordinal,p.id,a.ordinal,a.id,b.ordinal,b.id`

func readCanonicalV19SnapshotCurrentAttemptBackoffs(ctx context.Context, tx *sql.Tx) ([]CanonicalV19SnapshotAttemptBackoff, error) {
	rows, err := tx.QueryContext(ctx, canonicalV19SnapshotCurrentAttemptBackoffsQuery)
	if err != nil {
		return nil, fmt.Errorf("read canonical FleetSnapshot current AttemptBackoffs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	backoffs := make([]CanonicalV19SnapshotAttemptBackoff, 0)
	for rows.Next() {
		var backoff CanonicalV19SnapshotAttemptBackoff
		if err := rows.Scan(&backoff.ProjectID, &backoff.TaskID, &backoff.PlanID, &backoff.AttemptID,
			&backoff.ID, &backoff.Reason, &backoff.NotBefore, &backoff.EvidenceDigest,
			&backoff.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan canonical FleetSnapshot current AttemptBackoff: %w", err)
		}
		backoffs = append(backoffs, backoff)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canonical FleetSnapshot current AttemptBackoffs: %w", err)
	}
	return backoffs, nil
}
