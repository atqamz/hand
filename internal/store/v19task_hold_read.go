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
