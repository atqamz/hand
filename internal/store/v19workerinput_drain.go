package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrCanonicalV19WorkerInputDrainNotCurrent marks a drain whose exact active/open execution no longer matches.
var ErrCanonicalV19WorkerInputDrainNotCurrent = errors.New("canonical v19 WorkerInput drain is not current")

// CanonicalV19WorkerInputDrainInput identifies one exact Worker execution generation.
type CanonicalV19WorkerInputDrainInput struct {
	AttemptID         string
	ExecutorBindingID string
}

// DrainCanonicalV19WorkerInputs returns every pending semantic input for one exact current ExecutorBinding
// in canonical ordinal order. It performs no mutation and never treats WorkerWake state as acknowledgement.
func DrainCanonicalV19WorkerInputs(
	ctx context.Context,
	homeDir string,
	input CanonicalV19WorkerInputDrainInput,
) ([]CanonicalV19WorkerInput, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if input.AttemptID == "" || input.ExecutorBindingID == "" {
		return nil, fmt.Errorf("drain canonical v19 WorkerInput: Attempt ID and ExecutorBinding ID are required")
	}

	db, err := openReadOnly(homeDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, canonicalV19WorkerInputDrainReadError("begin snapshot", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := requireCanonicalV19WorkerInputDrainCurrent(ctx, tx, input); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT wi.id,wi.attempt_id,wi.executor_binding_id,wi.ordinal,
		wi.payload,wi.payload_digest,wi.origin_kind,wi.created_at
		FROM worker_input wi
		WHERE wi.attempt_id=? AND wi.executor_binding_id=?
		  AND NOT EXISTS (
			SELECT 1 FROM worker_input_acknowledgement a WHERE a.worker_input_id=wi.id
		  )
		ORDER BY wi.ordinal ASC`, input.AttemptID, input.ExecutorBindingID)
	if err != nil {
		return nil, canonicalV19WorkerInputDrainReadError("read pending inputs", err)
	}
	defer func() { _ = rows.Close() }()

	pending := make([]CanonicalV19WorkerInput, 0)
	for rows.Next() {
		var workerInput CanonicalV19WorkerInput
		var payload []byte
		if err := rows.Scan(
			&workerInput.ID, &workerInput.AttemptID, &workerInput.ExecutorBindingID, &workerInput.Ordinal,
			&payload, &workerInput.PayloadDigest, &workerInput.OriginKind, &workerInput.CreatedAt,
		); err != nil {
			return nil, canonicalV19WorkerInputDrainReadError("scan pending input", err)
		}
		workerInput.Payload = string(payload)
		pending = append(pending, workerInput)
	}
	if err := rows.Err(); err != nil {
		return nil, canonicalV19WorkerInputDrainReadError("iterate pending inputs", err)
	}
	return pending, nil
}

func requireCanonicalV19WorkerInputDrainCurrent(
	ctx context.Context,
	tx *sql.Tx,
	input CanonicalV19WorkerInputDrainInput,
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
			ErrCanonicalV19WorkerInputDrainNotCurrent, input.AttemptID, input.ExecutorBindingID)
	}
	if err != nil {
		return canonicalV19WorkerInputDrainReadError("read exact current execution ownership", err)
	}
	if attemptID != input.AttemptID || executorBindingID != input.ExecutorBindingID {
		return fmt.Errorf("%w: exact execution ownership changed", ErrCanonicalV19WorkerInputDrainNotCurrent)
	}
	return nil
}

func canonicalV19WorkerInputDrainReadError(action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("drain canonical v19 WorkerInput: %s: %w", action, ErrContention)
	}
	return fmt.Errorf("drain canonical v19 WorkerInput: %s: %w", action, err)
}
