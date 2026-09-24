package store

import (
	"context"
	"database/sql"
	"fmt"
)

const (
	canonicalV19TaskListAllQuery = `SELECT t.id,t.project_id,t.ordinal,t.goal_digest,t.lifecycle,
		a.task_id IS NOT NULL
		FROM task t LEFT JOIN task_archive a ON a.task_id=t.id
		WHERE t.id>? ORDER BY t.id LIMIT ?`
	canonicalV19TaskListUnarchivedQuery = `SELECT t.id,t.project_id,t.ordinal,t.goal_digest,t.lifecycle,
		a.task_id IS NOT NULL
		FROM task t LEFT JOIN task_archive a ON a.task_id=t.id
		WHERE t.id>? AND a.task_id IS NULL ORDER BY t.id LIMIT ?`
	canonicalV19TaskListArchivedQuery = `SELECT t.id,t.project_id,t.ordinal,t.goal_digest,t.lifecycle,1
		FROM task_archive a CROSS JOIN task t
		WHERE a.task_id>? AND t.id=a.task_id ORDER BY a.task_id LIMIT ?`
)

type CanonicalV19TaskSummary struct {
	ID         string
	ProjectID  string
	Ordinal    int64
	GoalDigest string
	Lifecycle  string
	Archived   bool
}

type CanonicalV19TaskPage struct {
	Items     []CanonicalV19TaskSummary
	NextAfter string
}

func ListCanonicalV19Tasks(ctx context.Context, homeDir, scope, after string, limit int) (CanonicalV19TaskPage, error) {
	if limit < 1 || limit > 1000 {
		return CanonicalV19TaskPage{}, fmt.Errorf("canonical Task list limit must be between 1 and 1000")
	}
	query := canonicalV19TaskListAllQuery
	switch scope {
	case "unarchived":
		query = canonicalV19TaskListUnarchivedQuery
	case "archived":
		query = canonicalV19TaskListArchivedQuery
	case "all":
	default:
		return CanonicalV19TaskPage{}, fmt.Errorf("unsupported canonical Task list scope %q", scope)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := openReadOnly(homeDir)
	if err != nil {
		return CanonicalV19TaskPage{}, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CanonicalV19TaskPage{}, fmt.Errorf("list canonical v19 Tasks: begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19TaskPage{}, fmt.Errorf("list canonical v19 Tasks: %w", err)
	}
	rows, err := tx.QueryContext(ctx, query, after, limit+1)
	if err != nil {
		return CanonicalV19TaskPage{}, fmt.Errorf("list canonical v19 Tasks: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	page := CanonicalV19TaskPage{Items: make([]CanonicalV19TaskSummary, 0, limit)}
	for rows.Next() {
		var item CanonicalV19TaskSummary
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Ordinal,
			&item.GoalDigest, &item.Lifecycle, &item.Archived); err != nil {
			return CanonicalV19TaskPage{}, fmt.Errorf("list canonical v19 Tasks: scan: %w", err)
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return CanonicalV19TaskPage{}, fmt.Errorf("list canonical v19 Tasks: iterate: %w", err)
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextAfter = page.Items[limit-1].ID
	}
	return page, nil
}
