package store

import (
	"context"
	"database/sql"
	"fmt"
)

type CanonicalV19TaskSummary struct {
	ID         string
	ProjectID  string
	Ordinal    int64
	GoalDigest string
	Lifecycle  string
	Archived   bool
}

func ListCanonicalV19Tasks(ctx context.Context, homeDir, scope string) ([]CanonicalV19TaskSummary, error) {
	filter := ""
	switch scope {
	case "unarchived":
		filter = "WHERE a.task_id IS NULL"
	case "archived":
		filter = "WHERE a.task_id IS NOT NULL"
	case "all":
	default:
		return nil, fmt.Errorf("unsupported canonical Task list scope %q", scope)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := openReadOnly(homeDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("list canonical v19 Tasks: begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return nil, fmt.Errorf("list canonical v19 Tasks: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT t.id,t.project_id,t.ordinal,t.goal_digest,t.lifecycle,
		a.task_id IS NOT NULL
		FROM project p JOIN task t ON t.project_id=p.id
		LEFT JOIN task_archive a ON a.task_id=t.id `+filter+`
		ORDER BY p.ordinal,p.id,t.ordinal,t.id`)
	if err != nil {
		return nil, fmt.Errorf("list canonical v19 Tasks: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]CanonicalV19TaskSummary, 0)
	for rows.Next() {
		var item CanonicalV19TaskSummary
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Ordinal,
			&item.GoalDigest, &item.Lifecycle, &item.Archived); err != nil {
			return nil, fmt.Errorf("list canonical v19 Tasks: scan: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list canonical v19 Tasks: iterate: %w", err)
	}
	return items, nil
}
