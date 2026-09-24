package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CanonicalV19DecisionView is one bounded snapshot of authority history.
// OwnerCurrent is separate from whether the Decision was answered or closed.
type CanonicalV19DecisionView struct {
	Decision     CanonicalV19DecisionCreateInput
	Answer       *CanonicalV19DecisionAnswerCreateInput
	Closure      *CanonicalV19DecisionCloseInput
	OwnerCurrent bool
}

type CanonicalV19DecisionSummary struct {
	ID        string
	CreatedAt string
	ScopeKind string
	State     string
}

type CanonicalV19DecisionPage struct {
	Items     []CanonicalV19DecisionSummary
	NextAfter string
}

const canonicalV19DecisionListBase = `SELECT d.id,d.created_at,d.scope_kind,
	CASE WHEN a.decision_id IS NOT NULL THEN 'answered'
		WHEN c.decision_id IS NOT NULL THEN 'closed' ELSE 'open' END
	FROM decision d INDEXED BY decision_task_history
	LEFT JOIN decision_answer a ON a.decision_id=d.id
	LEFT JOIN decision_closure c ON c.decision_id=d.id
	WHERE d.task_id=?`

const canonicalV19DecisionListQuery = canonicalV19DecisionListBase + `
	ORDER BY d.created_at DESC,d.id LIMIT ?`

const canonicalV19DecisionListAfterQuery = canonicalV19DecisionListBase + `
	AND (d.created_at<? OR (d.created_at=? AND d.id>?))
	ORDER BY d.created_at DESC,d.id LIMIT ?`

func ListCanonicalV19Decisions(ctx context.Context, homeDir, taskID, after string, limit int) (CanonicalV19DecisionPage, error) {
	var page CanonicalV19DecisionPage
	if taskID == "" {
		return page, fmt.Errorf("list canonical Decisions: exact Task ID is required")
	}
	if limit < 1 || limit > 1000 {
		return page, fmt.Errorf("list canonical Decisions: limit must be between 1 and 1000")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := openReadOnly(homeDir)
	if err != nil {
		return page, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, fmt.Errorf("list canonical Decisions: begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return page, fmt.Errorf("list canonical Decisions: %w", err)
	}
	var taskExists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM task WHERE id=?`, taskID).Scan(&taskExists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return page, fmt.Errorf("%w: %q", ErrCanonicalV19TaskNotFound, taskID)
		}
		return page, fmt.Errorf("list canonical Decisions: read Task: %w", err)
	}
	query := canonicalV19DecisionListQuery
	args := []any{taskID}
	if after != "" {
		var createdAt string
		if err := tx.QueryRowContext(ctx, `SELECT created_at FROM decision WHERE id=? AND task_id=?`, after, taskID).Scan(&createdAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return page, fmt.Errorf("list canonical Decisions: cursor %q does not belong to Task %q", after, taskID)
			}
			return page, fmt.Errorf("list canonical Decisions: read cursor: %w", err)
		}
		query = canonicalV19DecisionListAfterQuery
		args = append(args, createdAt, createdAt, after)
	}
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return page, fmt.Errorf("list canonical Decisions: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	page.Items = make([]CanonicalV19DecisionSummary, 0, limit)
	for rows.Next() {
		var item CanonicalV19DecisionSummary
		if err := rows.Scan(&item.ID, &item.CreatedAt, &item.ScopeKind, &item.State); err != nil {
			return CanonicalV19DecisionPage{}, fmt.Errorf("list canonical Decisions: scan: %w", err)
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return CanonicalV19DecisionPage{}, fmt.Errorf("list canonical Decisions: iterate: %w", err)
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextAfter = page.Items[limit-1].ID
	}
	return page, nil
}

// ReadCanonicalV19Decision reads one exact ID without answering, closing, or
// delivering it. Missing/incompatible stores refuse without bootstrap or migration.
func ReadCanonicalV19Decision(ctx context.Context, homeDir, id string) (CanonicalV19DecisionView, error) {
	var view CanonicalV19DecisionView
	if ctx == nil {
		ctx = context.Background()
	}
	if id == "" {
		return view, fmt.Errorf("read canonical Decision: exact ID is required")
	}
	db, err := openReadOnly(homeDir)
	if err != nil {
		return view, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return view, err
	}
	defer func() { _ = tx.Rollback() }()
	// This validator only reads schema and FK settings, including inside this snapshot.
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return view, err
	}
	decision, found, err := loadCanonicalV19Decision(ctx, tx, id)
	if err != nil {
		return view, err
	}
	if !found {
		return view, fmt.Errorf("%w: Decision %q does not exist", ErrCanonicalV19DecisionNotCurrent, id)
	}
	view.Decision = decision
	answer, answered, err := loadCanonicalV19DecisionAnswer(ctx, tx, id)
	if err != nil {
		return view, err
	}
	if answered {
		view.Answer = &answer
	}
	closure, closed, err := loadCanonicalV19DecisionClosure(ctx, tx, id)
	if err != nil {
		return view, err
	}
	if closed {
		view.Closure = &closure
	}
	err = requireCanonicalV19DecisionCurrent(ctx, tx, decision)
	if err != nil && !errors.Is(err, ErrCanonicalV19DecisionNotCurrent) {
		return view, err
	}
	view.OwnerCurrent = err == nil
	return view, nil
}
