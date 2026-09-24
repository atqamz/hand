package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type CanonicalV19CurrentPlanRouting struct {
	ID               string
	Intent           string
	Judgment         string
	PolicyRevisionID string
}

const canonicalV19CurrentPlanRoutingQuery = `SELECT p.id,p.intent,p.judgment,p.policy_revision_id
	FROM plan p
	JOIN task t ON t.id=p.task_id AND t.lifecycle='active' AND t.terminal_at=''
	JOIN project pr ON pr.id=t.project_id AND pr.retired_at=''
	WHERE p.id=? AND p.lifecycle='active' AND p.terminal_at=''`

func ReadCanonicalV19CurrentPlanRouting(ctx context.Context, homeDir, id string) (CanonicalV19CurrentPlanRouting, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if id == "" {
		return CanonicalV19CurrentPlanRouting{}, fmt.Errorf("%w: exact Plan ID is required", ErrCanonicalV19PlanNotCurrent)
	}
	db, err := openReadOnly(homeDir)
	if err != nil {
		return CanonicalV19CurrentPlanRouting{}, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CanonicalV19CurrentPlanRouting{}, fmt.Errorf("read canonical Plan route: begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19CurrentPlanRouting{}, fmt.Errorf("read canonical Plan route: %w", err)
	}
	var view CanonicalV19CurrentPlanRouting
	err = tx.QueryRowContext(ctx, canonicalV19CurrentPlanRoutingQuery, id).Scan(&view.ID, &view.Intent, &view.Judgment, &view.PolicyRevisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19CurrentPlanRouting{}, fmt.Errorf("%w: Plan %q has no exact active Project/Task/Plan lineage", ErrCanonicalV19PlanNotCurrent, id)
	}
	if err != nil {
		return CanonicalV19CurrentPlanRouting{}, fmt.Errorf("read canonical Plan route: %w", err)
	}
	return view, nil
}
