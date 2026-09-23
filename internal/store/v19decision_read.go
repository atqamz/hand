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
