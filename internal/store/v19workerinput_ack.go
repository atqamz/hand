package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrCanonicalV19WorkerInputAcknowledgementConflict marks immutable acknowledgement identity or evidence drift.
var ErrCanonicalV19WorkerInputAcknowledgementConflict = errors.New("canonical v19 WorkerInputAcknowledgement write conflict")

// CanonicalV19WorkerInputAcknowledgementCreateInput is exact Worker drain evidence for one WorkerInput.
type CanonicalV19WorkerInputAcknowledgementCreateInput struct {
	WorkerInputID     string
	ExecutorBindingID string
	ObservedAt        string
	EvidenceDigest    string
}

// CanonicalV19WorkerInputAcknowledgement is immutable proof that one Worker observed one exact WorkerInput.
type CanonicalV19WorkerInputAcknowledgement struct {
	WorkerInputID     string
	ExecutorBindingID string
	ActorKind         string
	ObservedAt        string
	EvidenceDigest    string
}

// CreateCanonicalV19WorkerInputAcknowledgement appends exact Worker drain evidence.
// Historical exact attachment remains valid after executor terminalization; no successor is retargeted.
func CreateCanonicalV19WorkerInputAcknowledgement(
	ctx context.Context,
	homeDir string,
	input CanonicalV19WorkerInputAcknowledgementCreateInput,
) (CanonicalV19WorkerInputAcknowledgement, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19WorkerInputAcknowledgementCreateInput(input); err != nil {
		return CanonicalV19WorkerInputAcknowledgement{}, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19WorkerInputAcknowledgement{}, err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19WorkerInputAcknowledgement{}, canonicalV19WorkerInputAcknowledgementWriteError("begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19WorkerInputAcknowledgement{}, fmt.Errorf("create canonical v19 WorkerInputAcknowledgement: %w", err)
	}

	existing, found, err := loadCanonicalV19WorkerInputAcknowledgement(ctx, tx, input.WorkerInputID)
	if err != nil {
		return CanonicalV19WorkerInputAcknowledgement{}, err
	}
	if found {
		if canonicalV19WorkerInputAcknowledgementMatchesCreate(existing, input) {
			return existing, nil
		}
		return CanonicalV19WorkerInputAcknowledgement{}, fmt.Errorf(
			"%w: WorkerInput %q already has different acknowledgement evidence",
			ErrCanonicalV19WorkerInputAcknowledgementConflict, input.WorkerInputID,
		)
	}

	if err := requireCanonicalV19WorkerInputAcknowledgementTarget(ctx, tx, input); err != nil {
		return CanonicalV19WorkerInputAcknowledgement{}, err
	}
	result := CanonicalV19WorkerInputAcknowledgement{
		WorkerInputID: input.WorkerInputID, ExecutorBindingID: input.ExecutorBindingID,
		ActorKind: "worker", ObservedAt: input.ObservedAt, EvidenceDigest: input.EvidenceDigest,
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO worker_input_acknowledgement(
		worker_input_id,executor_binding_id,actor_kind,observed_at,evidence_digest
	) VALUES(?,?,'worker',?,?)`, result.WorkerInputID, result.ExecutorBindingID,
		result.ObservedAt, result.EvidenceDigest); err != nil {
		if isSQLiteConstraint(err) {
			return CanonicalV19WorkerInputAcknowledgement{}, fmt.Errorf(
				"%w: WorkerInput %q: %v", ErrCanonicalV19WorkerInputAcknowledgementConflict, input.WorkerInputID, err,
			)
		}
		return CanonicalV19WorkerInputAcknowledgement{}, canonicalV19WorkerInputAcknowledgementWriteError("insert acknowledgement", err)
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19WorkerInputAcknowledgement{}, canonicalV19WorkerInputAcknowledgementWriteError("commit writer", err)
	}
	committed = true
	return result, nil
}

func validateCanonicalV19WorkerInputAcknowledgementCreateInput(
	input CanonicalV19WorkerInputAcknowledgementCreateInput,
) error {
	for name, value := range map[string]string{
		"WorkerInput ID": input.WorkerInputID, "ExecutorBinding ID": input.ExecutorBindingID,
		"observed_at": input.ObservedAt, "evidence digest": input.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("create canonical v19 WorkerInputAcknowledgement: %s is empty", name)
		}
	}
	return nil
}

func requireCanonicalV19WorkerInputAcknowledgementTarget(
	ctx context.Context,
	tx *sql.Tx,
	input CanonicalV19WorkerInputAcknowledgementCreateInput,
) error {
	var workerInputID, executorBindingID string
	err := tx.QueryRowContext(ctx, `SELECT wi.id,wi.executor_binding_id
		FROM worker_input wi
		JOIN executor_binding e ON e.id=wi.executor_binding_id
		WHERE wi.id=? AND wi.executor_binding_id=?`,
		input.WorkerInputID, input.ExecutorBindingID).Scan(&workerInputID, &executorBindingID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: WorkerInput %q does not belong to exact ExecutorBinding %q",
			ErrCanonicalV19WorkerInputAcknowledgementConflict, input.WorkerInputID, input.ExecutorBindingID)
	}
	if err != nil {
		return canonicalV19WorkerInputAcknowledgementWriteError("read exact WorkerInput target", err)
	}
	if workerInputID != input.WorkerInputID || executorBindingID != input.ExecutorBindingID {
		return fmt.Errorf("%w: exact WorkerInput acknowledgement target changed",
			ErrCanonicalV19WorkerInputAcknowledgementConflict)
	}
	return nil
}

func loadCanonicalV19WorkerInputAcknowledgement(
	ctx context.Context,
	tx *sql.Tx,
	workerInputID string,
) (CanonicalV19WorkerInputAcknowledgement, bool, error) {
	var acknowledgement CanonicalV19WorkerInputAcknowledgement
	err := tx.QueryRowContext(ctx, `SELECT worker_input_id,executor_binding_id,actor_kind,observed_at,evidence_digest
		FROM worker_input_acknowledgement WHERE worker_input_id=?`, workerInputID).Scan(
		&acknowledgement.WorkerInputID, &acknowledgement.ExecutorBindingID, &acknowledgement.ActorKind,
		&acknowledgement.ObservedAt, &acknowledgement.EvidenceDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19WorkerInputAcknowledgement{}, false, nil
	}
	if err != nil {
		return CanonicalV19WorkerInputAcknowledgement{}, false,
			canonicalV19WorkerInputAcknowledgementWriteError("read acknowledgement identity", err)
	}
	return acknowledgement, true, nil
}

func canonicalV19WorkerInputAcknowledgementMatchesCreate(
	existing CanonicalV19WorkerInputAcknowledgement,
	input CanonicalV19WorkerInputAcknowledgementCreateInput,
) bool {
	return existing.WorkerInputID == input.WorkerInputID &&
		existing.ExecutorBindingID == input.ExecutorBindingID && existing.ActorKind == "worker" &&
		existing.ObservedAt == input.ObservedAt && existing.EvidenceDigest == input.EvidenceDigest
}

func canonicalV19WorkerInputAcknowledgementWriteError(action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("create canonical v19 WorkerInputAcknowledgement: %s: %w", action, ErrContention)
	}
	return fmt.Errorf("create canonical v19 WorkerInputAcknowledgement: %s: %w", action, err)
}
