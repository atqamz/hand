package store

import (
	"context"
	"database/sql"
	"fmt"
)

type CanonicalV19SnapshotTaskHold struct {
	ID               string
	TaskID           string
	Ordinal          int64
	Kind             string
	EvidenceDigest   string
	CreatedAt        string
	BlockedOnTaskID  string
	DecisionID       string
	RecheckNotBefore string
}

const canonicalV19SnapshotCurrentTaskHoldsQuery = `SELECT h.id,h.task_id,h.ordinal,h.kind,h.evidence_digest,h.created_at,
		COALESCE(b.blocked_on_task_id,''),COALESCE(d.decision_id,''),COALESCE(c.not_before,'')
		FROM project pr
		CROSS JOIN task t INDEXED BY task_active_by_project
		CROSS JOIN task_hold h INDEXED BY task_hold_task_history
		LEFT JOIN task_hold_blocked_on_task b ON b.hold_id=h.id
		LEFT JOIN task_hold_decision d ON d.hold_id=h.id
		LEFT JOIN task_hold_recheck c ON c.hold_id=h.id
		LEFT JOIN task_hold_resolution r ON r.hold_id=h.id
		WHERE t.project_id=pr.id AND t.lifecycle='active'
		  AND h.task_id=t.id AND r.hold_id IS NULL
		ORDER BY pr.ordinal,pr.id,t.ordinal,t.id,h.ordinal,h.id`

func readCanonicalV19SnapshotCurrentTaskHolds(ctx context.Context, tx *sql.Tx) ([]CanonicalV19SnapshotTaskHold, error) {
	rows, err := tx.QueryContext(ctx, canonicalV19SnapshotCurrentTaskHoldsQuery)
	if err != nil {
		return nil, fmt.Errorf("read canonical FleetSnapshot current TaskHolds: %w", err)
	}
	defer func() { _ = rows.Close() }()
	holds := make([]CanonicalV19SnapshotTaskHold, 0)
	for rows.Next() {
		var hold CanonicalV19SnapshotTaskHold
		if err := rows.Scan(&hold.ID, &hold.TaskID, &hold.Ordinal, &hold.Kind,
			&hold.EvidenceDigest, &hold.CreatedAt, &hold.BlockedOnTaskID, &hold.DecisionID,
			&hold.RecheckNotBefore); err != nil {
			return nil, fmt.Errorf("scan canonical FleetSnapshot current TaskHold: %w", err)
		}
		holds = append(holds, hold)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canonical FleetSnapshot current TaskHolds: %w", err)
	}
	return holds, nil
}
