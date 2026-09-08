package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrCanonicalV19WorkerInputConflict marks exact WorkerInput identity or ordering conflicts.
// Callers must never retarget the semantic input to another execution generation.
var ErrCanonicalV19WorkerInputConflict = errors.New("canonical v19 WorkerInput write conflict")

// ErrCanonicalV19WorkerInputNotCurrent marks a WorkerInput whose exact active Attempt and open
// ExecutorBinding ownership no longer matches.
var ErrCanonicalV19WorkerInputNotCurrent = errors.New("canonical v19 WorkerInput is not current")

// CanonicalV19WorkerInputCreateInput is one immutable semantic instruction for an exact ExecutorBinding.
type CanonicalV19WorkerInputCreateInput struct {
	ID                string
	AttemptID         string
	ExecutorBindingID string
	Payload           string
	PayloadDigest     string
	OriginKind        string
	CreatedAt         string
}

// CanonicalV19WorkerInput is the exact immutable semantic input persisted for one execution generation.
type CanonicalV19WorkerInput struct {
	ID                string
	AttemptID         string
	ExecutorBindingID string
	Ordinal           int64
	Payload           string
	PayloadDigest     string
	OriginKind        string
	CreatedAt         string
}

// CreateCanonicalV19WorkerInput durably appends semantic input to one exact current ExecutorBinding.
// It performs no provider mutation; WorkerWake is a separate external operation.
func CreateCanonicalV19WorkerInput(
	ctx context.Context,
	homeDir string,
	input CanonicalV19WorkerInputCreateInput,
) (CanonicalV19WorkerInput, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19WorkerInputCreateInput(input); err != nil {
		return CanonicalV19WorkerInput{}, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19WorkerInput{}, err
	}
	defer func() { _ = sqlDB.Close() }()

	// openCanonicalV19Writer pins database/sql transactions to BEGIN IMMEDIATE,
	// so currentness proof, ordinal allocation, and insertion serialize together.
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19WorkerInput{}, canonicalV19WorkerInputWriteError("begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19WorkerInput{}, fmt.Errorf("create canonical v19 WorkerInput: %w", err)
	}

	existing, found, err := loadCanonicalV19WorkerInputByID(ctx, tx, input.ID)
	if err != nil {
		return CanonicalV19WorkerInput{}, err
	}
	if found {
		if canonicalV19WorkerInputMatchesCreate(existing, input) {
			return existing, nil
		}
		return CanonicalV19WorkerInput{}, fmt.Errorf("%w: WorkerInput %q already exists with different immutable evidence",
			ErrCanonicalV19WorkerInputConflict, input.ID)
	}

	if err := requireCanonicalV19WorkerInputCurrent(ctx, tx, input); err != nil {
		return CanonicalV19WorkerInput{}, err
	}
	var ordinal int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ordinal), 0) + 1
		FROM worker_input WHERE executor_binding_id=?`, input.ExecutorBindingID).Scan(&ordinal); err != nil {
		return CanonicalV19WorkerInput{}, canonicalV19WorkerInputWriteError("allocate exact ExecutorBinding ordinal", err)
	}

	result := CanonicalV19WorkerInput{
		ID: input.ID, AttemptID: input.AttemptID, ExecutorBindingID: input.ExecutorBindingID,
		Ordinal: ordinal, Payload: input.Payload, PayloadDigest: input.PayloadDigest,
		OriginKind: input.OriginKind, CreatedAt: input.CreatedAt,
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO worker_input(
		id,attempt_id,executor_binding_id,ordinal,payload,payload_digest,origin_kind,created_at
	) VALUES(?,?,?,?,?,?,?,?)`, result.ID, result.AttemptID, result.ExecutorBindingID, result.Ordinal,
		result.Payload, result.PayloadDigest, result.OriginKind, result.CreatedAt); err != nil {
		if isSQLiteConstraint(err) {
			return CanonicalV19WorkerInput{}, fmt.Errorf("%w: WorkerInput %q", ErrCanonicalV19WorkerInputConflict, input.ID)
		}
		return CanonicalV19WorkerInput{}, canonicalV19WorkerInputWriteError("insert exact semantic input", err)
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19WorkerInput{}, canonicalV19WorkerInputWriteError("commit writer", err)
	}
	committed = true
	return result, nil
}

func validateCanonicalV19WorkerInputCreateInput(input CanonicalV19WorkerInputCreateInput) error {
	for name, value := range map[string]string{
		"WorkerInput ID": input.ID, "Attempt ID": input.AttemptID,
		"ExecutorBinding ID": input.ExecutorBindingID, "payload": input.Payload,
		"payload digest": input.PayloadDigest, "origin kind": input.OriginKind,
		"created_at": input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("create canonical v19 WorkerInput: %s is empty", name)
		}
	}
	return nil
}

func requireCanonicalV19WorkerInputCurrent(
	ctx context.Context,
	tx *sql.Tx,
	input CanonicalV19WorkerInputCreateInput,
) error {
	var attemptID, executorBindingID string
	err := tx.QueryRowContext(ctx, `SELECT a.id,e.id
		FROM executor_binding e
		JOIN attempt a ON a.id=e.attempt_id AND a.lifecycle='active' AND a.terminal_at=''
		JOIN plan p ON p.id=a.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.retired_at=''
		JOIN session_binding s ON s.id=e.session_binding_id AND s.attempt_id=a.id AND s.adapter_ref=e.adapter_ref
		JOIN attempt_worktree_binding b ON b.id=s.worktree_binding_id AND b.attempt_id=a.id
		WHERE e.id=? AND e.attempt_id=?
		  AND NOT EXISTS (SELECT 1 FROM executor_binding_termination x WHERE x.executor_binding_id=e.id)
		  AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)
		  AND NOT EXISTS (SELECT 1 FROM worktree_binding_release r WHERE r.binding_id=b.id)`,
		input.ExecutorBindingID, input.AttemptID).Scan(&attemptID, &executorBindingID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: Attempt %q / ExecutorBinding %q lacks exact active/open ownership",
			ErrCanonicalV19WorkerInputNotCurrent, input.AttemptID, input.ExecutorBindingID)
	}
	if err != nil {
		return canonicalV19WorkerInputWriteError("read exact current execution ownership", err)
	}
	if attemptID != input.AttemptID || executorBindingID != input.ExecutorBindingID {
		return fmt.Errorf("%w: exact execution ownership changed", ErrCanonicalV19WorkerInputNotCurrent)
	}
	return nil
}

func loadCanonicalV19WorkerInputByID(
	ctx context.Context,
	tx *sql.Tx,
	workerInputID string,
) (CanonicalV19WorkerInput, bool, error) {
	var input CanonicalV19WorkerInput
	err := tx.QueryRowContext(ctx, `SELECT id,attempt_id,executor_binding_id,ordinal,payload,payload_digest,origin_kind,created_at
		FROM worker_input WHERE id=?`, workerInputID).Scan(
		&input.ID, &input.AttemptID, &input.ExecutorBindingID, &input.Ordinal,
		&input.Payload, &input.PayloadDigest, &input.OriginKind, &input.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19WorkerInput{}, false, nil
	}
	if err != nil {
		return CanonicalV19WorkerInput{}, false, canonicalV19WorkerInputWriteError("read exact WorkerInput identity", err)
	}
	return input, true, nil
}

func canonicalV19WorkerInputMatchesCreate(existing CanonicalV19WorkerInput, input CanonicalV19WorkerInputCreateInput) bool {
	return existing.ID == input.ID && existing.AttemptID == input.AttemptID &&
		existing.ExecutorBindingID == input.ExecutorBindingID && existing.Payload == input.Payload &&
		existing.PayloadDigest == input.PayloadDigest && existing.OriginKind == input.OriginKind &&
		existing.CreatedAt == input.CreatedAt
}

func canonicalV19WorkerInputWriteError(action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("create canonical v19 WorkerInput: %s: %w", action, ErrContention)
	}
	return fmt.Errorf("create canonical v19 WorkerInput: %s: %w", action, err)
}
