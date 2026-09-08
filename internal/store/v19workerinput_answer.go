package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CanonicalV19AnswerWorkerInputCreateInput binds one semantic input to one exact answered Decision.
type CanonicalV19AnswerWorkerInputCreateInput struct {
	ID                string
	AttemptID         string
	ExecutorBindingID string
	Payload           string
	PayloadDigest     string
	AnswerOriginID    string
	DecisionID        string
	AnswerID          string
	CreatedAt         string
}

type canonicalV19AnswerWorkerInputRecord struct {
	Input          CanonicalV19WorkerInput
	AnswerOriginID string
	DecisionID     string
	AnswerID       string
}

// CreateCanonicalV19AnswerWorkerInput appends one immutable Answer-origin instruction.
// It binds existing DecisionAnswer evidence and performs no provider mutation.
func CreateCanonicalV19AnswerWorkerInput(
	ctx context.Context,
	homeDir string,
	input CanonicalV19AnswerWorkerInputCreateInput,
) (CanonicalV19WorkerInput, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19AnswerWorkerInputCreateInput(input); err != nil {
		return CanonicalV19WorkerInput{}, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19WorkerInput{}, err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19WorkerInput{}, canonicalV19WorkerInputWriteError("begin Answer-origin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19WorkerInput{}, fmt.Errorf("create canonical v19 Answer-origin WorkerInput: %w", err)
	}

	existing, found, err := loadCanonicalV19AnswerWorkerInputByID(ctx, tx, input.ID)
	if err != nil {
		return CanonicalV19WorkerInput{}, err
	}
	if found {
		if canonicalV19AnswerWorkerInputMatchesCreate(existing, input) {
			return existing.Input, nil
		}
		return CanonicalV19WorkerInput{}, fmt.Errorf("%w: WorkerInput %q already exists with different Answer-origin evidence",
			ErrCanonicalV19WorkerInputConflict, input.ID)
	}

	base := CanonicalV19WorkerInputCreateInput{
		ID: input.ID, AttemptID: input.AttemptID, ExecutorBindingID: input.ExecutorBindingID,
		Payload: input.Payload, PayloadDigest: input.PayloadDigest, OriginKind: "answer", CreatedAt: input.CreatedAt,
	}
	if err := requireCanonicalV19WorkerInputCurrent(ctx, tx, base); err != nil {
		return CanonicalV19WorkerInput{}, err
	}
	if err := requireCanonicalV19AnswerWorkerInputSource(ctx, tx, input); err != nil {
		return CanonicalV19WorkerInput{}, err
	}

	var ordinal int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ordinal),0)+1
		FROM worker_input WHERE executor_binding_id=?`, input.ExecutorBindingID).Scan(&ordinal); err != nil {
		return CanonicalV19WorkerInput{}, canonicalV19WorkerInputWriteError("allocate Answer-origin ordinal", err)
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO worker_input_answer_origin(
		id,worker_input_id,decision_id,answer_id
	) VALUES(?,?,?,?)`, input.AnswerOriginID, input.ID, input.DecisionID, input.AnswerID); err != nil {
		return CanonicalV19WorkerInput{}, canonicalV19AnswerWorkerInputConstraintError("insert Answer-origin binding", input.ID, err)
	}

	result := CanonicalV19WorkerInput{
		ID: input.ID, AttemptID: input.AttemptID, ExecutorBindingID: input.ExecutorBindingID,
		Ordinal: ordinal, Payload: input.Payload, PayloadDigest: input.PayloadDigest,
		OriginKind: "answer", CreatedAt: input.CreatedAt,
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO worker_input(
		id,attempt_id,executor_binding_id,ordinal,payload,payload_digest,origin_kind,answer_origin_id,created_at
	) VALUES(?,?,?,?,?,?, 'answer',?,?)`, result.ID, result.AttemptID, result.ExecutorBindingID, result.Ordinal,
		[]byte(result.Payload), result.PayloadDigest, input.AnswerOriginID, result.CreatedAt); err != nil {
		return CanonicalV19WorkerInput{}, canonicalV19AnswerWorkerInputConstraintError("insert Answer-origin WorkerInput", input.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19WorkerInput{}, canonicalV19AnswerWorkerInputConstraintError("commit Answer-origin writer", input.ID, err)
	}
	committed = true
	return result, nil
}

func validateCanonicalV19AnswerWorkerInputCreateInput(input CanonicalV19AnswerWorkerInputCreateInput) error {
	for name, value := range map[string]string{
		"WorkerInput ID": input.ID, "Attempt ID": input.AttemptID,
		"ExecutorBinding ID": input.ExecutorBindingID, "payload": input.Payload,
		"payload digest": input.PayloadDigest, "Answer-origin ID": input.AnswerOriginID,
		"Decision ID": input.DecisionID, "Answer ID": input.AnswerID, "created_at": input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("create canonical v19 Answer-origin WorkerInput: %s is empty", name)
		}
	}
	return nil
}

func requireCanonicalV19AnswerWorkerInputSource(
	ctx context.Context,
	tx *sql.Tx,
	input CanonicalV19AnswerWorkerInputCreateInput,
) error {
	var decisionID, answerID string
	err := tx.QueryRowContext(ctx, `SELECT d.id,da.id
		FROM decision d
		JOIN decision_answer da ON da.decision_id=d.id
		JOIN attempt a ON a.id=?
		JOIN plan p ON p.id=a.plan_id
		WHERE d.id=? AND da.id=?
		  AND ((d.scope_kind='attempt' AND d.attempt_id=a.id) OR
		       (d.scope_kind='plan' AND d.plan_id=a.plan_id) OR
		       (d.scope_kind='task' AND d.task_id=p.task_id))`,
		input.AttemptID, input.DecisionID, input.AnswerID).Scan(&decisionID, &answerID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: Answer %q / Decision %q does not exactly scope to Attempt %q",
			ErrCanonicalV19WorkerInputConflict, input.AnswerID, input.DecisionID, input.AttemptID)
	}
	if err != nil {
		return canonicalV19WorkerInputWriteError("read exact Answer-origin source", err)
	}
	if decisionID != input.DecisionID || answerID != input.AnswerID {
		return fmt.Errorf("%w: exact Answer-origin source changed", ErrCanonicalV19WorkerInputConflict)
	}
	return nil
}

func loadCanonicalV19AnswerWorkerInputByID(
	ctx context.Context,
	tx *sql.Tx,
	workerInputID string,
) (canonicalV19AnswerWorkerInputRecord, bool, error) {
	var record canonicalV19AnswerWorkerInputRecord
	var payload []byte
	var answerOriginID, decisionID, answerID sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT wi.id,wi.attempt_id,wi.executor_binding_id,wi.ordinal,
		wi.payload,wi.payload_digest,wi.origin_kind,wi.created_at,wi.answer_origin_id,ao.decision_id,ao.answer_id
		FROM worker_input wi
		LEFT JOIN worker_input_answer_origin ao ON ao.id=wi.answer_origin_id
		WHERE wi.id=?`, workerInputID).Scan(
		&record.Input.ID, &record.Input.AttemptID, &record.Input.ExecutorBindingID, &record.Input.Ordinal,
		&payload, &record.Input.PayloadDigest, &record.Input.OriginKind, &record.Input.CreatedAt,
		&answerOriginID, &decisionID, &answerID)
	if errors.Is(err, sql.ErrNoRows) {
		return canonicalV19AnswerWorkerInputRecord{}, false, nil
	}
	if err != nil {
		return canonicalV19AnswerWorkerInputRecord{}, false,
			canonicalV19WorkerInputWriteError("read exact Answer-origin WorkerInput identity", err)
	}
	record.Input.Payload = string(payload)
	record.AnswerOriginID = answerOriginID.String
	record.DecisionID = decisionID.String
	record.AnswerID = answerID.String
	return record, true, nil
}

func canonicalV19AnswerWorkerInputMatchesCreate(
	existing canonicalV19AnswerWorkerInputRecord,
	input CanonicalV19AnswerWorkerInputCreateInput,
) bool {
	return existing.Input.ID == input.ID && existing.Input.AttemptID == input.AttemptID &&
		existing.Input.ExecutorBindingID == input.ExecutorBindingID && existing.Input.Payload == input.Payload &&
		existing.Input.PayloadDigest == input.PayloadDigest && existing.Input.OriginKind == "answer" &&
		existing.Input.CreatedAt == input.CreatedAt && existing.AnswerOriginID == input.AnswerOriginID &&
		existing.DecisionID == input.DecisionID && existing.AnswerID == input.AnswerID
}

func canonicalV19AnswerWorkerInputConstraintError(action, workerInputID string, err error) error {
	if isSQLiteConstraint(err) {
		return fmt.Errorf("%w: WorkerInput %q %s: %v", ErrCanonicalV19WorkerInputConflict, workerInputID, action, err)
	}
	return canonicalV19WorkerInputWriteError(action, err)
}
