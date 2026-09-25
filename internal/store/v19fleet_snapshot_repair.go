package store

import (
	"context"
	"database/sql"
	"fmt"
)

type CanonicalV19SnapshotRepair struct {
	ID             string
	ProjectID      string
	TaskID         string
	TargetKind     string
	TargetID       string
	RepairCode     string
	EvidenceDigest string
	CreatedAt      string
}

const canonicalV19SnapshotOpenRepairsQuery = `
SELECT pr.ordinal,pr.id,t.ordinal,t.id,0,0,'task',rt.task_id,r.id,r.repair_code,r.evidence_digest,r.created_at
FROM project pr
CROSS JOIN task t INDEXED BY task_active_by_project
CROSS JOIN repair_target rt INDEXED BY repair_target_task
JOIN repair r ON r.id=rt.repair_id
LEFT JOIN repair_resolution rr ON rr.repair_id=r.id
WHERE t.project_id=pr.id AND t.lifecycle='active' AND rt.task_id=t.id AND rr.repair_id IS NULL
UNION ALL
SELECT pr.ordinal,pr.id,t.ordinal,t.id,p.ordinal,0,'plan',rt.plan_id,r.id,r.repair_code,r.evidence_digest,r.created_at
FROM project pr
CROSS JOIN task t INDEXED BY task_active_by_project
CROSS JOIN plan p INDEXED BY plan_active_by_task
CROSS JOIN repair_target rt INDEXED BY repair_target_plan
JOIN repair r ON r.id=rt.repair_id
LEFT JOIN repair_resolution rr ON rr.repair_id=r.id
WHERE t.project_id=pr.id AND t.lifecycle='active' AND p.task_id=t.id AND p.lifecycle='active'
  AND rt.plan_id=p.id AND rr.repair_id IS NULL
UNION ALL
SELECT pr.ordinal,pr.id,t.ordinal,t.id,p.ordinal,a.ordinal,'attempt',rt.attempt_id,r.id,r.repair_code,r.evidence_digest,r.created_at
FROM project pr
CROSS JOIN task t INDEXED BY task_active_by_project
CROSS JOIN plan p INDEXED BY plan_active_by_task
CROSS JOIN attempt a INDEXED BY attempt_active_by_plan
CROSS JOIN repair_target rt INDEXED BY repair_target_attempt
JOIN repair r ON r.id=rt.repair_id
LEFT JOIN repair_resolution rr ON rr.repair_id=r.id
WHERE t.project_id=pr.id AND t.lifecycle='active' AND p.task_id=t.id AND p.lifecycle='active'
  AND a.plan_id=p.id AND a.lifecycle='active'
  AND rt.attempt_id=a.id AND rr.repair_id IS NULL
UNION ALL
SELECT pr.ordinal,pr.id,0,'',0,0,'project',rt.project_id,r.id,r.repair_code,r.evidence_digest,r.created_at
FROM project pr
CROSS JOIN repair_target rt INDEXED BY repair_target_project
JOIN repair r ON r.id=rt.repair_id
LEFT JOIN repair_resolution rr ON rr.repair_id=r.id
WHERE rt.project_id=pr.id AND rr.repair_id IS NULL
UNION ALL
SELECT pr.ordinal,pr.id,0,'',0,0,'workspace',rt.workspace_binding_id,r.id,r.repair_code,r.evidence_digest,r.created_at
FROM project pr
CROSS JOIN workspace_binding wb INDEXED BY workspace_binding_project_history
CROSS JOIN repair_target rt INDEXED BY repair_target_workspace
JOIN repair r ON r.id=rt.repair_id
LEFT JOIN repair_resolution rr ON rr.repair_id=r.id
WHERE wb.project_id=pr.id AND rt.workspace_binding_id=wb.id AND rr.repair_id IS NULL
ORDER BY 1,2,3,4,5,6,9`

func readCanonicalV19SnapshotOpenRepairs(ctx context.Context, tx *sql.Tx) ([]CanonicalV19SnapshotRepair, error) {
	rows, err := tx.QueryContext(ctx, canonicalV19SnapshotOpenRepairsQuery)
	if err != nil {
		return nil, fmt.Errorf("read canonical FleetSnapshot open Repairs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	repairs := make([]CanonicalV19SnapshotRepair, 0)
	for rows.Next() {
		var projectOrdinal, taskOrdinal, planOrdinal, attemptOrdinal int64
		var repair CanonicalV19SnapshotRepair
		if err := rows.Scan(&projectOrdinal, &repair.ProjectID, &taskOrdinal, &repair.TaskID, &planOrdinal, &attemptOrdinal,
			&repair.TargetKind, &repair.TargetID, &repair.ID, &repair.RepairCode, &repair.EvidenceDigest,
			&repair.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan canonical FleetSnapshot open Repair: %w", err)
		}
		repairs = append(repairs, repair)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canonical FleetSnapshot open Repairs: %w", err)
	}
	return repairs, nil
}
