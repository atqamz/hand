package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
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
	if err := tx.QueryRowContext(ctx, `SELECT p.fleet_id,b.path,s.provider_session_key
		FROM project p
		JOIN session_binding s ON s.id=? AND s.attempt_id=? AND s.adapter_ref=?
		JOIN attempt_worktree_binding b ON b.id=s.worktree_binding_id AND b.attempt_id=s.attempt_id
		WHERE p.id=?`, current.Request.SessionBindingID, current.Request.AttemptID,
		current.Request.AdapterRef, current.Request.ProjectID).Scan(
		&result.FleetID, &result.WorktreePath, &result.ProviderSessionKey,
	); err != nil {
		return canonicalV19HerdrWorkerWakeCurrent{}, fmt.Errorf("read canonical v19 Herdr WorkerWake provider context: %w", err)
	}
	if result.FleetID == "" || result.WorktreePath == "" || result.ProviderSessionKey == "" {
		return canonicalV19HerdrWorkerWakeCurrent{}, fmt.Errorf("read canonical v19 Herdr WorkerWake provider context: Fleet ID, Worktree path, or provider Session key is empty")
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM worker_input wi
		WHERE wi.executor_binding_id=? AND wi.ordinal<=?
		  AND NOT EXISTS (SELECT 1 FROM worker_input_acknowledgement a WHERE a.worker_input_id=wi.id)
	)`, current.Request.ExecutorBindingID, current.Request.PendingThroughOrdinal).Scan(&pending); err != nil {
		return canonicalV19HerdrWorkerWakeCurrent{}, fmt.Errorf("read canonical v19 Herdr WorkerWake pending boundary: %w", err)
	}
	result.PendingInput = pending == 1
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

func finalizeCanonicalV19HerdrWorkerWakeObservation(
	current canonicalV19HerdrWorkerWakeCurrent,
	observed canonicalV19HerdrWorkerWakeObservation,
) canonicalV19HerdrWorkerWakeObservation {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:herdr-worker-wake-observation:v1")
	writeCanonicalV19DigestField(hash, "operation_id", current.Current.Request.OperationID)
	writeCanonicalV19DigestField(hash, "request_digest", current.Current.Request.RequestDigest)
	writeCanonicalV19DigestField(hash, "fleet_id", current.FleetID)
	writeCanonicalV19DigestField(hash, "provider_session_key", current.ProviderSessionKey)
	writeCanonicalV19DigestField(hash, "provider_executor_key", current.Current.Request.ProviderExecutorKey)
	writeCanonicalV19DigestField(hash, "doorbell_digest", current.Current.Request.DoorbellDigest)
	writeCanonicalV19DigestField(hash, "state", string(observed.State))
	writeCanonicalV19DigestField(hash, "pending_input", strconv.FormatBool(observed.PendingInput))
	writeCanonicalV19DigestField(hash, "process_alive", strconv.FormatBool(observed.ProcessAlive))
	writeCanonicalV19DigestField(hash, "provider_accepted", strconv.FormatBool(observed.ProviderAccepted))
	writeCanonicalV19DigestField(hash, "agent", observed.Agent)
	writeCanonicalV19DigestField(hash, "agent_status", string(observed.AgentStatus))
	writeCanonicalV19DigestField(hash, "workspace_id", observed.WorkspaceID)
	writeCanonicalV19DigestField(hash, "tab_id", observed.TabID)
	writeCanonicalV19DigestField(hash, "pane_id", observed.PaneID)
	writeCanonicalV19DigestField(hash, "pane_cwd", observed.PaneCwd)
	writeCanonicalV19DigestField(hash, "shell_pid", strconv.Itoa(observed.ShellPID))
	writeCanonicalV19DigestField(hash, "process_group_id", strconv.Itoa(observed.ProcessGroupID))
	writeCanonicalV19DigestField(hash, "process_id", strconv.Itoa(observed.ProcessID))
	writeCanonicalV19DigestField(hash, "process_digest", observed.ProcessDigest)
	writeCanonicalV19DigestField(hash, "reason", observed.Reason)
	observed.EvidenceDigest = hex.EncodeToString(hash.Sum(nil))
	return observed
}
