package store

import (
	"context"
	"database/sql"
	"fmt"
)

type CanonicalV19SnapshotLatestWorkerReportMetadata struct {
	AttemptID              string
	ReportID               string
	SourcePrefixDigest     string
	SourceEndOffset        int64
	ReportState            string
	AcknowledgementPresent bool
}

const canonicalV19SnapshotLatestWorkerReportMetadataQuery = `SELECT a.id,r.id,r.source_prefix_digest,
		r.source_end_offset,r.report_state,ack.worker_report_id IS NOT NULL
		FROM task t INDEXED BY task_active_by_project
		CROSS JOIN project pr
		CROSS JOIN plan p INDEXED BY plan_active_by_task
		CROSS JOIN attempt a INDEXED BY attempt_active_by_plan
		CROSS JOIN worker_report r INDEXED BY worker_report_attempt_source_order
		LEFT JOIN worker_report_acknowledgement ack ON ack.worker_report_id=r.id
		WHERE t.lifecycle='active'
		  AND pr.id=t.project_id
		  AND p.task_id=t.id AND p.lifecycle='active'
		  AND a.plan_id=p.id AND a.lifecycle='active'
		  AND r.attempt_id=a.id
		  AND r.source_end_offset=(
			SELECT tail.source_end_offset
			FROM worker_report tail INDEXED BY worker_report_attempt_source_order
			WHERE tail.attempt_id=a.id
			ORDER BY tail.source_end_offset DESC LIMIT 1
		  )
		ORDER BY pr.ordinal,pr.id,t.ordinal,t.id,p.ordinal,p.id,a.ordinal,a.id`

func readCanonicalV19SnapshotLatestWorkerReportMetadata(ctx context.Context, tx *sql.Tx) ([]CanonicalV19SnapshotLatestWorkerReportMetadata, error) {
	rows, err := tx.QueryContext(ctx, canonicalV19SnapshotLatestWorkerReportMetadataQuery)
	if err != nil {
		return nil, fmt.Errorf("read canonical FleetSnapshot latest WorkerReports: %w", err)
	}
	defer func() { _ = rows.Close() }()
	reports := make([]CanonicalV19SnapshotLatestWorkerReportMetadata, 0)
	for rows.Next() {
		var view CanonicalV19SnapshotLatestWorkerReportMetadata
		if err := rows.Scan(&view.AttemptID, &view.ReportID, &view.SourcePrefixDigest,
			&view.SourceEndOffset, &view.ReportState, &view.AcknowledgementPresent); err != nil {
			return nil, fmt.Errorf("scan canonical FleetSnapshot latest WorkerReport: %w", err)
		}
		reports = append(reports, view)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canonical FleetSnapshot latest WorkerReports: %w", err)
	}
	return reports, nil
}
