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
		CROSS JOIN task t INDEXED BY task_active_by_project
		CROSS JOIN decision d INDEXED BY decision_task_history
		LEFT JOIN decision_closure c ON c.decision_id=d.id
		WHERE t.project_id=pr.id AND t.lifecycle='active'
		  AND d.task_id=t.id AND c.decision_id IS NULL
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
