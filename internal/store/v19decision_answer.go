package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CanonicalV19DecisionAnswerCreateInput records explicit operator authority, not delivery.
// The caller must establish that authority; actor provenance is not same-user authentication.
type CanonicalV19DecisionAnswerCreateInput struct {
	ID           string
	DecisionID   string
	Answer       string
	AnswerDigest string
	ActorKind    string
	ActorRef     string
	AnsweredAt   string
}

// CreateCanonicalV19DecisionAnswer answers only the exact current question.
// It creates no WorkerInput, WorkerWake, acknowledgement, Hold resolution or lifecycle transition.
func CreateCanonicalV19DecisionAnswer(ctx context.Context, homeDir string, input CanonicalV19DecisionAnswerCreateInput) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if input.ID == "" || input.DecisionID == "" || input.ActorRef == "" || input.AnsweredAt == "" {
		return fmt.Errorf("%w: Answer ID, Decision ID, operator reference and answered_at are required", ErrCanonicalV19DecisionConflict)
	}
	// Machine authority has no implemented scoped policy. A schema enum is not permission.
	if input.ActorKind != "operator" {
		return fmt.Errorf("%w: explicit operator authority is required", ErrCanonicalV19DecisionConflict)
	}
	if err := validateCanonicalV19DecisionText("answer", input.Answer); err != nil {
		return err
	}
	if canonicalV19SHA256([]byte(input.Answer)) != input.AnswerDigest {
		return fmt.Errorf("%w: Answer digest does not match exact bytes", ErrCanonicalV19DecisionConflict)
	}
	db, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19DecisionWriteError("begin Answer writer", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return canonicalV19DecisionWriteError("validate Answer writer", err)
	}
	existing, found, err := loadCanonicalV19DecisionAnswer(ctx, tx, input.DecisionID)
	if err != nil {
		return err
	}
	if found {
		if existing == input {
			return nil
		}
		return fmt.Errorf("%w: Decision %q already has a different Answer", ErrCanonicalV19DecisionConflict, input.DecisionID)
	}
	decision, found, err := loadCanonicalV19Decision(ctx, tx, input.DecisionID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: Decision %q does not exist", ErrCanonicalV19DecisionNotCurrent, input.DecisionID)
	}
	if _, closed, err := loadCanonicalV19DecisionClosure(ctx, tx, input.DecisionID); err != nil {
		return err
	} else if closed {
		return fmt.Errorf("%w: Decision %q is closed", ErrCanonicalV19DecisionNotCurrent, input.DecisionID)
	}
	if err := requireCanonicalV19DecisionCurrent(ctx, tx, decision); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_answer(id,decision_id,answer,answer_digest,actor_kind,actor_ref,answered_at)
		VALUES(?,?,?,?,?,?,?)`, input.ID, input.DecisionID, input.Answer, input.AnswerDigest,
		input.ActorKind, input.ActorRef, input.AnsweredAt); err != nil {
		return canonicalV19DecisionWriteError("insert Answer", err)
	}
	return canonicalV19DecisionWriteError("commit Answer", tx.Commit())
}

func loadCanonicalV19DecisionAnswer(ctx context.Context, tx *sql.Tx, decisionID string) (CanonicalV19DecisionAnswerCreateInput, bool, error) {
	var result CanonicalV19DecisionAnswerCreateInput
	err := tx.QueryRowContext(ctx, `SELECT id,decision_id,answer,answer_digest,actor_kind,actor_ref,answered_at
		FROM decision_answer WHERE decision_id=?`, decisionID).Scan(&result.ID, &result.DecisionID,
		&result.Answer, &result.AnswerDigest, &result.ActorKind, &result.ActorRef, &result.AnsweredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19DecisionAnswerCreateInput{}, false, nil
	}
	if err != nil {
		return CanonicalV19DecisionAnswerCreateInput{}, false, canonicalV19DecisionWriteError("read exact Answer", err)
	}
	return result, true, nil
}
