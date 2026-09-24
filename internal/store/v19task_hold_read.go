package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CanonicalV19TaskHoldView separates immutable deferral/resolution history from
// whether the owning Task is still current. Linked evidence is never resolved by reading.
type CanonicalV19TaskHoldView struct {
	Hold         CanonicalV19TaskHoldCreateInput
	Ordinal      int64
	Resolution   *CanonicalV19TaskHoldResolveInput
	OwnerCurrent bool
}

const canonicalV19TaskHoldListQuery = `SELECT h.id,h.ordinal,h.kind,COALESCE(r.resolution,'')
	FROM task_hold h INDEXED BY task_hold_task_history
	LEFT JOIN task_hold_resolution r ON r.hold_id=h.id
	WHERE h.task_id=? AND h.ordinal>?
	ORDER BY h.ordinal LIMIT ?`

type CanonicalV19TaskHoldSummary struct {
	ID         string
	Ordinal    int64
	Kind       string
	Resolution string
}

type CanonicalV19TaskHoldPage struct {
	Items            []CanonicalV19TaskHoldSummary
	NextAfterOrdinal int64
}

func ListCanonicalV19TaskHolds(ctx context.Context, homeDir, taskID string, afterOrdinal int64, limit int) (CanonicalV19TaskHoldPage, error) {
	if taskID == "" {
		return CanonicalV19TaskHoldPage{}, fmt.Errorf("list canonical v19 TaskHolds: exact Task ID is required")
	}
	if afterOrdinal < 0 {
		return CanonicalV19TaskHoldPage{}, fmt.Errorf("TaskHold history cursor must not be negative")
	}
	if limit < 1 || limit > 1000 {
		return CanonicalV19TaskHoldPage{}, fmt.Errorf("TaskHold history limit must be between 1 and 1000")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := openReadOnly(homeDir)
	if err != nil {
		return CanonicalV19TaskHoldPage{}, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CanonicalV19TaskHoldPage{}, fmt.Errorf("list canonical v19 TaskHolds: begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19TaskHoldPage{}, fmt.Errorf("list canonical v19 TaskHolds: %w", err)
	}
	var exactID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM task WHERE id=?`, taskID).Scan(&exactID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CanonicalV19TaskHoldPage{}, fmt.Errorf("%w: %q", ErrCanonicalV19TaskNotFound, taskID)
		}
		return CanonicalV19TaskHoldPage{}, fmt.Errorf("list canonical v19 TaskHolds: read exact Task: %w", err)
	}
	rows, err := tx.QueryContext(ctx, canonicalV19TaskHoldListQuery, taskID, afterOrdinal, limit+1)
	if err != nil {
		return CanonicalV19TaskHoldPage{}, fmt.Errorf("list canonical v19 TaskHolds: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	page := CanonicalV19TaskHoldPage{Items: make([]CanonicalV19TaskHoldSummary, 0, limit)}
	for rows.Next() {
		var item CanonicalV19TaskHoldSummary
		if err := rows.Scan(&item.ID, &item.Ordinal, &item.Kind, &item.Resolution); err != nil {
			return CanonicalV19TaskHoldPage{}, fmt.Errorf("list canonical v19 TaskHolds: scan: %w", err)
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return CanonicalV19TaskHoldPage{}, fmt.Errorf("list canonical v19 TaskHolds: iterate: %w", err)
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextAfterOrdinal = page.Items[limit-1].Ordinal
	}
	return page, nil
}

func ReadCanonicalV19TaskHold(ctx context.Context, homeDir, id string) (CanonicalV19TaskHoldView, error) {
	var view CanonicalV19TaskHoldView
	if ctx == nil {
		ctx = context.Background()
	}
	if id == "" {
		return view, fmt.Errorf("read canonical TaskHold: exact ID is required")
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
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return view, err
	}
	// Each optional child has hold_id as its primary key, so this remains one row
	// regardless of Fleet history size. All facts belong to the same read snapshot.
	var resolution CanonicalV19TaskHoldResolveInput
	err = tx.QueryRowContext(ctx, `SELECT h.id,h.task_id,h.ordinal,h.kind,h.reason,h.evidence_digest,h.created_at,
		COALESCE(b.blocked_on_task_id,''),COALESCE(d.decision_id,''),COALESCE(c.not_before,''),
		COALESCE(r.hold_id,''),COALESCE(r.resolution,''),COALESCE(r.resolved_at,''),COALESCE(r.evidence_digest,'')
		FROM task_hold h
		LEFT JOIN task_hold_blocked_on_task b ON b.hold_id=h.id
		LEFT JOIN task_hold_decision d ON d.hold_id=h.id
		LEFT JOIN task_hold_recheck c ON c.hold_id=h.id
		LEFT JOIN task_hold_resolution r ON r.hold_id=h.id
		WHERE h.id=?`, id).Scan(
		&view.Hold.ID, &view.Hold.TaskID, &view.Ordinal, &view.Hold.Kind, &view.Hold.Reason,
		&view.Hold.EvidenceDigest, &view.Hold.CreatedAt, &view.Hold.BlockedOnTaskID,
		&view.Hold.DecisionID, &view.Hold.RecheckNotBefore, &resolution.HoldID,
		&resolution.Resolution, &resolution.ResolvedAt, &resolution.EvidenceDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return view, fmt.Errorf("%w: TaskHold %q does not exist", ErrCanonicalV19TaskHoldNotCurrent, id)
	}
	if err != nil {
		return view, err
	}
	if resolution.HoldID != "" {
		view.Resolution = &resolution
	}
	err = requireCanonicalV19TaskHoldOwnerCurrent(ctx, tx, view.Hold.TaskID)
	if err != nil && !errors.Is(err, ErrCanonicalV19TaskHoldNotCurrent) {
		return view, err
	}
	view.OwnerCurrent = err == nil
	return view, nil
}
