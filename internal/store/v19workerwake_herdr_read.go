package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func readCanonicalV19HerdrWorkerWakeCurrent(
	ctx context.Context,
	homeDir string,
	operationID string,
) (canonicalV19HerdrWorkerWakeCurrent, error) {
	db, err := openReadOnly(homeDir)
	if err != nil {
		return canonicalV19HerdrWorkerWakeCurrent{}, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19HerdrWorkerWakeCurrent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := loadCanonicalV19WorkerWakeCurrent(ctx, tx, operationID)
	if err != nil {
		return canonicalV19HerdrWorkerWakeCurrent{}, err
	}
	result := canonicalV19HerdrWorkerWakeCurrent{Current: current}
	if err := tx.QueryRowContext(ctx, `SELECT s.provider_session_key,e.launch_operation_id
		FROM session_binding s
		JOIN executor_binding e ON e.id=? AND e.session_binding_id=s.id
		WHERE s.id=? AND s.attempt_id=? AND s.adapter_ref=?`, current.Request.ExecutorBindingID,
		current.Request.SessionBindingID, current.Request.AttemptID, current.Request.AdapterRef).Scan(
		&result.ProviderSessionKey, &result.LaunchOperationID,
	); err != nil {
		return canonicalV19HerdrWorkerWakeCurrent{}, fmt.Errorf("read canonical v19 Herdr WorkerWake provider context: %w", err)
	}
	if result.ProviderSessionKey == "" || result.LaunchOperationID == "" {
		return canonicalV19HerdrWorkerWakeCurrent{}, fmt.Errorf("read canonical v19 Herdr WorkerWake provider context: provider Session key or Launch operation ID is empty")
	}
	return result, nil
}

func readCanonicalV19HerdrWorkerWakeTerminal(ctx context.Context, homeDir, operationID string) (string, bool, error) {
	db, err := openReadOnly(homeDir)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = db.Close() }()
	var state string
	err = db.sql.QueryRowContext(ctx, `SELECT state FROM external_operation
		WHERE id=? AND kind='worker-wake' AND state IN ('succeeded','rejected','no-effect')`, operationID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return state, true, nil
}
