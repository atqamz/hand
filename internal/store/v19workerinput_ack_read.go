package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ReadCanonicalV19WorkerInputAcknowledgement returns immutable acknowledgement evidence for one exact WorkerInput.
func ReadCanonicalV19WorkerInputAcknowledgement(
	ctx context.Context,
	homeDir string,
	workerInputID string,
) (CanonicalV19WorkerInputAcknowledgement, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if workerInputID == "" {
		return CanonicalV19WorkerInputAcknowledgement{}, false,
			fmt.Errorf("read canonical v19 WorkerInputAcknowledgement: WorkerInput ID is empty")
	}

	db, err := openReadOnly(homeDir)
	if err != nil {
		return CanonicalV19WorkerInputAcknowledgement{}, false, err
	}
	defer func() { _ = db.Close() }()

	var acknowledgement CanonicalV19WorkerInputAcknowledgement
	err = db.sql.QueryRowContext(ctx, `SELECT worker_input_id,executor_binding_id,actor_kind,observed_at,evidence_digest
		FROM worker_input_acknowledgement WHERE worker_input_id=?`, workerInputID).Scan(
		&acknowledgement.WorkerInputID, &acknowledgement.ExecutorBindingID, &acknowledgement.ActorKind,
		&acknowledgement.ObservedAt, &acknowledgement.EvidenceDigest,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19WorkerInputAcknowledgement{}, false, nil
	}
	if err != nil {
		if isSQLiteBusy(err) {
			return CanonicalV19WorkerInputAcknowledgement{}, false,
				fmt.Errorf("read canonical v19 WorkerInputAcknowledgement: %w", ErrContention)
		}
		return CanonicalV19WorkerInputAcknowledgement{}, false,
			fmt.Errorf("read canonical v19 WorkerInputAcknowledgement: %w", err)
	}
	return acknowledgement, true, nil
}
