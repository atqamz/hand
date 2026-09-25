package store

import (
	"context"
	"database/sql"
	"fmt"
)

type CanonicalV19SnapshotDecision struct {
	ID                       string
	TaskID                   string
	PlanID                   string
	AttemptID                string
	ScopeKind                string
	ChoicesDigest            string
	TriggeringWorkerReportID string
	CreatedAt                string
}

const canonicalV19SnapshotCurrentDecisionsQuery = `SELECT d.id,d.task_id,COALESCE(d.plan_id,''),COALESCE(d.attempt_id,''),
		d.scope_kind,d.choices_digest,COALESCE(d.triggering_worker_report_id,''),d.created_at
		FROM project pr
		CROSS JOIN task t
		LEFT JOIN task_archive ta ON ta.task_id=t.id
		CROSS JOIN decision d INDEXED BY decision_task_history
		LEFT JOIN decision_answer da ON da.decision_id=d.id
		LEFT JOIN decision_closure dc ON dc.decision_id=d.id
		WHERE t.project_id=pr.id AND ta.task_id IS NULL
		  AND d.task_id=t.id AND da.decision_id IS NULL AND dc.decision_id IS NULL
		ORDER BY pr.ordinal,pr.id,t.ordinal,t.id,d.created_at,d.id`

func readCanonicalV19SnapshotCurrentDecisions(ctx context.Context, tx *sql.Tx) ([]CanonicalV19SnapshotDecision, error) {
	rows, err := tx.QueryContext(ctx, canonicalV19SnapshotCurrentDecisionsQuery)
	if err != nil {
		return nil, fmt.Errorf("read canonical FleetSnapshot current Decisions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	decisions := make([]CanonicalV19SnapshotDecision, 0)
	for rows.Next() {
		var decision CanonicalV19SnapshotDecision
		if err := rows.Scan(&decision.ID, &decision.TaskID, &decision.PlanID, &decision.AttemptID,
			&decision.ScopeKind, &decision.ChoicesDigest, &decision.TriggeringWorkerReportID,
			&decision.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan canonical FleetSnapshot current Decision: %w", err)
		}
		decisions = append(decisions, decision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canonical FleetSnapshot current Decisions: %w", err)
	}
	return decisions, nil
}
