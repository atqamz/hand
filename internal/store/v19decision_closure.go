package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type CanonicalV19DecisionCloseInput struct {
	DecisionID     string
	Reason         string
	ClosedAt       string
	EvidenceDigest string
}

// CloseCanonicalV19Decision appends exact stale/cancelled closure, never an Answer.
// Stale closure requires positively stale ownership; observation/query errors are not proof.
func CloseCanonicalV19Decision(ctx context.Context, homeDir string, input CanonicalV19DecisionCloseInput) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if input.DecisionID == "" || input.ClosedAt == "" || input.EvidenceDigest == "" ||
		(input.Reason != "stale" && input.Reason != "cancelled") {
		return fmt.Errorf("%w: exact Decision, stale/cancelled reason, closed_at and evidence digest are required", ErrCanonicalV19DecisionConflict)
	}
	db, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19DecisionWriteError("begin closure writer", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return canonicalV19DecisionWriteError("validate closure writer", err)
	}
	existing, found, err := loadCanonicalV19DecisionClosure(ctx, tx, input.DecisionID)
	if err != nil {
		return err
	}
	if found {
		if existing == input {
			return nil
		}
		return fmt.Errorf("%w: Decision %q already has different closure evidence", ErrCanonicalV19DecisionConflict, input.DecisionID)
	}
	decision, found, err := loadCanonicalV19Decision(ctx, tx, input.DecisionID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: Decision %q does not exist", ErrCanonicalV19DecisionNotCurrent, input.DecisionID)
	}
	if _, answered, err := loadCanonicalV19DecisionAnswer(ctx, tx, input.DecisionID); err != nil {
		return err
	} else if answered {
		return fmt.Errorf("%w: Decision %q is already answered", ErrCanonicalV19DecisionConflict, input.DecisionID)
	}
	if input.Reason == "stale" {
		err := requireCanonicalV19DecisionCurrent(ctx, tx, decision)
		if err == nil {
			return fmt.Errorf("%w: Decision %q is still current", ErrCanonicalV19DecisionConflict, input.DecisionID)
		}
		if !errors.Is(err, ErrCanonicalV19DecisionNotCurrent) {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_closure(decision_id,reason,closed_at,evidence_digest)
		VALUES(?,?,?,?)`, input.DecisionID, input.Reason, input.ClosedAt, input.EvidenceDigest); err != nil {
		return canonicalV19DecisionWriteError("insert closure", err)
	}
	return canonicalV19DecisionWriteError("commit closure", tx.Commit())
}

func loadCanonicalV19DecisionClosure(ctx context.Context, tx *sql.Tx, decisionID string) (CanonicalV19DecisionCloseInput, bool, error) {
	var result CanonicalV19DecisionCloseInput
	err := tx.QueryRowContext(ctx, `SELECT decision_id,reason,closed_at,evidence_digest
		FROM decision_closure WHERE decision_id=?`, decisionID).Scan(
		&result.DecisionID, &result.Reason, &result.ClosedAt, &result.EvidenceDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19DecisionCloseInput{}, false, nil
	}
	if err != nil {
		return CanonicalV19DecisionCloseInput{}, false, canonicalV19DecisionWriteError("read exact closure", err)
	}
	return result, true, nil
}
