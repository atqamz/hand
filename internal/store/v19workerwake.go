package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// ErrCanonicalV19WorkerWakeConflict marks exact WorkerWake ownership, scope, or relational conflicts.
var ErrCanonicalV19WorkerWakeConflict = errors.New("canonical v19 WorkerWake write conflict")

// ErrCanonicalV19WorkerWakeNotCurrent marks a WorkerWake whose exact active/open execution no longer matches.
var ErrCanonicalV19WorkerWakeNotCurrent = errors.New("canonical v19 WorkerWake is not current")

// ErrCanonicalV19WorkerWakeTransition marks an illegal or stale WorkerWake external-operation transition.
var ErrCanonicalV19WorkerWakeTransition = errors.New("canonical v19 WorkerWake transition conflict")

// CanonicalV19WorkerWakePrepareInput identifies one fresh mechanism-only wake request.
type CanonicalV19WorkerWakePrepareInput struct {
	OperationID           string
	OperationKey          string
	ExecutorBindingID     string
	PendingThroughOrdinal int64
	WakeReason            string
	DoorbellDigest        string
	CreatedAt             string
}

// CanonicalV19WorkerWakeRequest is the exact immutable request persisted before provider mutation is authorized.
type CanonicalV19WorkerWakeRequest struct {
	OperationID           string
	OperationKey          string
	RequestDigest         string
	ProjectID             string
	TaskID                string
	PlanID                string
	AttemptID             string
	SessionBindingID      string
	ExecutorBindingID     string
	AdapterRef            string
	ProviderExecutorKey   string
	PendingThroughOrdinal int64
	WakeReason            string
	DoorbellDigest        string
	CreatedAt             string
}

// CanonicalV19WorkerWakeTransitionInput records exact nonsuccess mechanism evidence.
type CanonicalV19WorkerWakeTransitionInput struct {
	OperationID    string
	State          string
	ObservedAt     string
	EvidenceDigest string
}

// CanonicalV19WorkerWakeSucceededEvidence records positive evidence for the exact mechanism postcondition.
type CanonicalV19WorkerWakeSucceededEvidence struct {
	OperationID    string
	ObservedAt     string
	EvidenceDigest string
}

type canonicalV19WorkerWakeCurrent struct {
	Request        CanonicalV19WorkerWakeRequest
	State          string
	StateChangedAt string
}

// PrepareCanonicalV19WorkerWake durably records one exact prepared WorkerWake and executor-control claim.
func PrepareCanonicalV19WorkerWake(
	ctx context.Context,
	homeDir string,
	input CanonicalV19WorkerWakePrepareInput,
) (CanonicalV19WorkerWakeRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19WorkerWakePrepareInput(input); err != nil {
		return CanonicalV19WorkerWakeRequest{}, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19WorkerWakeRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19WorkerWakeRequest{}, canonicalV19WorkerWakeWriteError("prepare", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19WorkerWakeRequest{}, fmt.Errorf("prepare canonical v19 WorkerWake: %w", err)
	}
	request, err := buildCanonicalV19WorkerWakeRequest(ctx, tx, input)
	if err != nil {
		return CanonicalV19WorkerWakeRequest{}, fmt.Errorf("prepare canonical v19 WorkerWake: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO external_operation(
		id,kind,adapter_ref,operation_key,request_digest,project_id,task_id,plan_id,attempt_id,
		primary_scope_kind,primary_scope_key,state,created_at,state_changed_at,state_evidence_digest,
		submitted_at,finalized_at
	) VALUES(?,'worker-wake',?,?,?,?,?,?,?,'executor-control',?,'prepared',?,?,'','','')`,
		request.OperationID, request.AdapterRef, request.OperationKey, request.RequestDigest,
		request.ProjectID, request.TaskID, request.PlanID, request.AttemptID, request.ExecutorBindingID,
		request.CreatedAt, request.CreatedAt); err != nil {
		return CanonicalV19WorkerWakeRequest{}, canonicalV19WorkerWakeConstraintError("prepare", "insert external operation", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO worker_wake_operation(
		operation_id,attempt_id,session_binding_id,executor_binding_id,pending_through_ordinal,wake_reason,doorbell_digest
	) VALUES(?,?,?,?,?,?,?)`, request.OperationID, request.AttemptID, request.SessionBindingID,
		request.ExecutorBindingID, request.PendingThroughOrdinal, request.WakeReason, request.DoorbellDigest); err != nil {
		return CanonicalV19WorkerWakeRequest{}, canonicalV19WorkerWakeConstraintError("prepare", "insert typed WorkerWake", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_scope_claim(operation_id,scope_kind,scope_key,created_at)
		VALUES(?,'executor-control',?,?)`, request.OperationID, request.ExecutorBindingID, request.CreatedAt); err != nil {
		return CanonicalV19WorkerWakeRequest{}, canonicalV19WorkerWakeConstraintError("prepare", "claim exact executor-control scope", request.OperationID, err)
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19WorkerWakeRequest{}, canonicalV19WorkerWakeWriteError("prepare", "commit writer", err)
	}
	committed = true
	return request, nil
}

// SubmitCanonicalV19WorkerWake durably authorizes the exact prepared WorkerWake.
func SubmitCanonicalV19WorkerWake(
	ctx context.Context,
	homeDir string,
	operationID string,
	submittedAt string,
	evidenceDigest string,
) (CanonicalV19WorkerWakeRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" || submittedAt == "" || evidenceDigest == "" {
		return CanonicalV19WorkerWakeRequest{}, fmt.Errorf("submit canonical v19 WorkerWake: operation ID, submitted_at, and evidence digest are required")
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19WorkerWakeRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19WorkerWakeRequest{}, canonicalV19WorkerWakeWriteError("submit", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19WorkerWakeRequest{}, fmt.Errorf("submit canonical v19 WorkerWake: %w", err)
	}
	current, err := loadCanonicalV19WorkerWakeCurrent(ctx, tx, operationID)
	if err != nil {
		return CanonicalV19WorkerWakeRequest{}, fmt.Errorf("submit canonical v19 WorkerWake: %w", err)
	}
	if current.State != "prepared" || current.StateChangedAt == submittedAt {
		return CanonicalV19WorkerWakeRequest{}, fmt.Errorf("submit canonical v19 WorkerWake: %w: operation %q is %q", ErrCanonicalV19WorkerWakeTransition, operationID, current.State)
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='submitted',state_changed_at=?,state_evidence_digest=?,submitted_at=?
		WHERE id=? AND state='prepared'`, submittedAt, evidenceDigest, submittedAt, operationID)
	if err != nil {
		return CanonicalV19WorkerWakeRequest{}, canonicalV19WorkerWakeConstraintError("submit", "authorize exact operation", operationID, err)
	}
	if err := requireCanonicalV19WorkerWakeOneChanged(result, "submit", operationID); err != nil {
		return CanonicalV19WorkerWakeRequest{}, err
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19WorkerWakeRequest{}, canonicalV19WorkerWakeWriteError("submit", "commit writer", err)
	}
	committed = true
	return current.Request, nil
}

// ClassifyCanonicalV19WorkerWake records exact nonsuccess mechanism evidence.
func ClassifyCanonicalV19WorkerWake(
	ctx context.Context,
	homeDir string,
	input CanonicalV19WorkerWakeTransitionInput,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19WorkerWakeTransitionInput(input); err != nil {
		return err
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19WorkerWakeWriteError("classify", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("classify canonical v19 WorkerWake: %w", err)
	}
	current, err := loadCanonicalV19WorkerWakeCurrent(ctx, tx, input.OperationID)
	if err != nil {
		return fmt.Errorf("classify canonical v19 WorkerWake: %w", err)
	}
	if !canonicalV19WorkerWakeNonsuccessTransitionAllowed(current.State, input.State) || current.StateChangedAt == input.ObservedAt {
		return fmt.Errorf("classify canonical v19 WorkerWake: %w: %s -> %s", ErrCanonicalV19WorkerWakeTransition, current.State, input.State)
	}
	if err := transitionCanonicalV19WorkerWakeOperation(ctx, tx, "classify", input.OperationID,
		current.State, input.State, input.ObservedAt, input.EvidenceDigest); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19WorkerWakeWriteError("classify", "commit writer", err)
	}
	committed = true
	return nil
}

// CompleteCanonicalV19WorkerWake marks the exact WorkerWake succeeded from positive mechanism evidence only.
func CompleteCanonicalV19WorkerWake(
	ctx context.Context,
	homeDir string,
	evidence CanonicalV19WorkerWakeSucceededEvidence,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for name, value := range map[string]string{
		"operation ID": evidence.OperationID, "observed_at": evidence.ObservedAt, "evidence digest": evidence.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("complete canonical v19 WorkerWake: %s is empty", name)
		}
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19WorkerWakeWriteError("complete", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("complete canonical v19 WorkerWake: %w", err)
	}
	current, err := loadCanonicalV19WorkerWakeCurrent(ctx, tx, evidence.OperationID)
	if err != nil {
		return fmt.Errorf("complete canonical v19 WorkerWake: %w", err)
	}
	if !canonicalV19WorkerWakeSuccessTransitionAllowed(current.State) || current.StateChangedAt == evidence.ObservedAt {
		return fmt.Errorf("complete canonical v19 WorkerWake: %w: operation %q is %q", ErrCanonicalV19WorkerWakeTransition, evidence.OperationID, current.State)
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='succeeded',state_changed_at=?,state_evidence_digest=?,finalized_at=?
		WHERE id=? AND state=?`, evidence.ObservedAt, evidence.EvidenceDigest, evidence.ObservedAt,
		evidence.OperationID, current.State)
	if err != nil {
		return canonicalV19WorkerWakeConstraintError("complete", "mark exact WorkerWake succeeded", evidence.OperationID, err)
	}
	if err := requireCanonicalV19WorkerWakeOneChanged(result, "complete", evidence.OperationID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19WorkerWakeWriteError("complete", "commit writer", err)
	}
	committed = true
	return nil
}

func validateCanonicalV19WorkerWakePrepareInput(input CanonicalV19WorkerWakePrepareInput) error {
	for name, value := range map[string]string{
		"operation ID": input.OperationID, "operation key": input.OperationKey,
		"ExecutorBinding ID": input.ExecutorBindingID, "wake reason": input.WakeReason,
		"doorbell digest": input.DoorbellDigest, "created_at": input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("prepare canonical v19 WorkerWake: %s is empty", name)
		}
	}
	if input.PendingThroughOrdinal <= 0 {
		return fmt.Errorf("prepare canonical v19 WorkerWake: pending-through ordinal must be positive")
	}
	if utf8.RuneCountInString(input.WakeReason) > 128 {
		return fmt.Errorf("prepare canonical v19 WorkerWake: wake reason exceeds 128 characters")
	}
	return nil
}

func validateCanonicalV19WorkerWakeTransitionInput(input CanonicalV19WorkerWakeTransitionInput) error {
	for name, value := range map[string]string{
		"operation ID": input.OperationID, "observed_at": input.ObservedAt, "evidence digest": input.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("classify canonical v19 WorkerWake: %s is empty", name)
		}
	}
	switch input.State {
	case "uncertain", "rejected", "no-effect":
		return nil
	default:
		return fmt.Errorf("classify canonical v19 WorkerWake: state %q requires a different writer", input.State)
	}
}

func buildCanonicalV19WorkerWakeRequest(
	ctx context.Context,
	tx *sql.Tx,
	input CanonicalV19WorkerWakePrepareInput,
) (CanonicalV19WorkerWakeRequest, error) {
	request := CanonicalV19WorkerWakeRequest{
		OperationID: input.OperationID, OperationKey: input.OperationKey,
		ExecutorBindingID: input.ExecutorBindingID, PendingThroughOrdinal: input.PendingThroughOrdinal,
		WakeReason: input.WakeReason, DoorbellDigest: input.DoorbellDigest, CreatedAt: input.CreatedAt,
	}
	err := tx.QueryRowContext(ctx, `SELECT project_current.id,t.id,p.id,a.id,e.session_binding_id,e.adapter_ref,e.provider_executor_key
		FROM executor_binding e
		JOIN attempt a ON a.id=e.attempt_id AND a.lifecycle='active' AND a.terminal_at=''
		JOIN plan p ON p.id=a.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.retired_at=''
		JOIN session_binding s ON s.id=e.session_binding_id AND s.attempt_id=a.id AND s.adapter_ref=e.adapter_ref
		WHERE e.id=?
		  AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)
		  AND NOT EXISTS (SELECT 1 FROM executor_binding_termination x WHERE x.executor_binding_id=e.id)`,
		input.ExecutorBindingID).Scan(
		&request.ProjectID, &request.TaskID, &request.PlanID, &request.AttemptID,
		&request.SessionBindingID, &request.AdapterRef, &request.ProviderExecutorKey)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19WorkerWakeRequest{}, fmt.Errorf("%w: ExecutorBinding %q lacks exact active/open ownership", ErrCanonicalV19WorkerWakeNotCurrent, input.ExecutorBindingID)
	}
	if err != nil {
		return CanonicalV19WorkerWakeRequest{}, canonicalV19WorkerWakeWriteError("prepare", "read exact current ExecutorBinding", err)
	}

	var maxOrdinal int64
	var hasPending int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(wi.ordinal),0),
		EXISTS(SELECT 1 FROM worker_input pending
			WHERE pending.executor_binding_id=? AND pending.ordinal<=?
			  AND NOT EXISTS (SELECT 1 FROM worker_input_acknowledgement a WHERE a.worker_input_id=pending.id))
		FROM worker_input wi WHERE wi.executor_binding_id=?`, input.ExecutorBindingID,
		input.PendingThroughOrdinal, input.ExecutorBindingID).Scan(&maxOrdinal, &hasPending); err != nil {
		return CanonicalV19WorkerWakeRequest{}, canonicalV19WorkerWakeWriteError("prepare", "read pending WorkerInput boundary", err)
	}
	if input.PendingThroughOrdinal > maxOrdinal {
		return CanonicalV19WorkerWakeRequest{}, fmt.Errorf("%w: pending-through ordinal %d exceeds exact ExecutorBinding max input ordinal %d",
			ErrCanonicalV19WorkerWakeConflict, input.PendingThroughOrdinal, maxOrdinal)
	}
	if hasPending != 1 {
		return CanonicalV19WorkerWakeRequest{}, fmt.Errorf("%w: no unacknowledged WorkerInput exists through ordinal %d for ExecutorBinding %q",
			ErrCanonicalV19WorkerWakeConflict, input.PendingThroughOrdinal, input.ExecutorBindingID)
	}
	request.RequestDigest = canonicalV19WorkerWakeRequestDigest(request)
	return request, nil
}

func loadCanonicalV19WorkerWakeCurrent(
	ctx context.Context,
	tx *sql.Tx,
	operationID string,
) (canonicalV19WorkerWakeCurrent, error) {
	var current canonicalV19WorkerWakeCurrent
	request := &current.Request
	err := tx.QueryRowContext(ctx, `SELECT o.state,o.state_changed_at,o.operation_key,o.request_digest,
		o.project_id,o.task_id,o.plan_id,o.attempt_id,w.session_binding_id,w.executor_binding_id,
		o.adapter_ref,e.provider_executor_key,w.pending_through_ordinal,w.wake_reason,w.doorbell_digest,o.created_at
		FROM external_operation o
		JOIN worker_wake_operation w ON w.operation_id=o.id AND w.attempt_id=o.attempt_id
		JOIN executor_binding e ON e.id=w.executor_binding_id AND e.attempt_id=o.attempt_id
		  AND e.session_binding_id=w.session_binding_id
		JOIN session_binding s ON s.id=w.session_binding_id AND s.attempt_id=e.attempt_id AND s.adapter_ref=e.adapter_ref
		JOIN attempt a ON a.id=e.attempt_id AND a.lifecycle='active' AND a.terminal_at=''
		JOIN plan p ON p.id=a.plan_id AND p.id=o.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.id=o.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.id=o.project_id AND project_current.retired_at=''
		JOIN operation_scope_claim executor_claim ON executor_claim.operation_id=o.id
		  AND executor_claim.scope_kind='executor-control' AND executor_claim.scope_key=e.id
		WHERE o.id=? AND o.kind='worker-wake' AND o.adapter_ref=e.adapter_ref
		  AND o.primary_scope_kind='executor-control' AND o.primary_scope_key=e.id
		  AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)
		  AND NOT EXISTS (SELECT 1 FROM executor_binding_termination x WHERE x.executor_binding_id=e.id)`, operationID).Scan(
		&current.State, &current.StateChangedAt, &request.OperationKey, &request.RequestDigest,
		&request.ProjectID, &request.TaskID, &request.PlanID, &request.AttemptID,
		&request.SessionBindingID, &request.ExecutorBindingID, &request.AdapterRef,
		&request.ProviderExecutorKey, &request.PendingThroughOrdinal, &request.WakeReason,
		&request.DoorbellDigest, &request.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return canonicalV19WorkerWakeCurrent{}, fmt.Errorf("%w: operation %q lacks exact current WorkerWake request/ownership/claim", ErrCanonicalV19WorkerWakeNotCurrent, operationID)
	}
	if err != nil {
		return canonicalV19WorkerWakeCurrent{}, canonicalV19WorkerWakeWriteError("current", "read exact WorkerWake", err)
	}
	request.OperationID = operationID
	if canonicalV19WorkerWakeRequestDigest(*request) != request.RequestDigest {
		return canonicalV19WorkerWakeCurrent{}, fmt.Errorf("%w: operation %q request digest does not match persisted WorkerWake", ErrCanonicalV19WorkerWakeNotCurrent, operationID)
	}
	return current, nil
}

func canonicalV19WorkerWakeRequestDigest(request CanonicalV19WorkerWakeRequest) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:worker-wake-request:v1")
	writeCanonicalV19DigestField(hash, "project_id", request.ProjectID)
	writeCanonicalV19DigestField(hash, "task_id", request.TaskID)
	writeCanonicalV19DigestField(hash, "plan_id", request.PlanID)
	writeCanonicalV19DigestField(hash, "attempt_id", request.AttemptID)
	writeCanonicalV19DigestField(hash, "session_binding_id", request.SessionBindingID)
	writeCanonicalV19DigestField(hash, "executor_binding_id", request.ExecutorBindingID)
	writeCanonicalV19DigestField(hash, "adapter_ref", request.AdapterRef)
	writeCanonicalV19DigestField(hash, "provider_executor_key", request.ProviderExecutorKey)
	writeCanonicalV19DigestField(hash, "pending_through_ordinal", strconv.FormatInt(request.PendingThroughOrdinal, 10))
	writeCanonicalV19DigestField(hash, "wake_reason", request.WakeReason)
	writeCanonicalV19DigestField(hash, "doorbell_digest", request.DoorbellDigest)
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalV19WorkerWakeNonsuccessTransitionAllowed(from, to string) bool {
	switch from {
	case "prepared":
		return to == "no-effect"
	case "submitted":
		return to == "uncertain" || to == "rejected" || to == "no-effect"
	case "uncertain":
		return to == "rejected" || to == "no-effect"
	default:
		return false
	}
}

func canonicalV19WorkerWakeSuccessTransitionAllowed(from string) bool {
	return from == "prepared" || from == "submitted" || from == "uncertain"
}

func transitionCanonicalV19WorkerWakeOperation(
	ctx context.Context,
	tx *sql.Tx,
	operation string,
	operationID string,
	from string,
	to string,
	observedAt string,
	evidenceDigest string,
) error {
	query := `UPDATE external_operation SET state=?,state_changed_at=?,state_evidence_digest=?`
	args := []any{to, observedAt, evidenceDigest}
	if to == "rejected" || to == "no-effect" {
		query += `,finalized_at=?`
		args = append(args, observedAt)
	}
	query += ` WHERE id=? AND state=?`
	args = append(args, operationID, from)
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return canonicalV19WorkerWakeConstraintError(operation, "transition exact operation", operationID, err)
	}
	return requireCanonicalV19WorkerWakeOneChanged(result, operation, operationID)
}

func requireCanonicalV19WorkerWakeOneChanged(result sql.Result, operation, operationID string) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return canonicalV19WorkerWakeWriteError(operation, "count exact state transition", err)
	}
	if changed != 1 {
		return fmt.Errorf("%s canonical v19 WorkerWake: %w: operation %q changed %d rows, want 1", operation, ErrCanonicalV19WorkerWakeTransition, operationID, changed)
	}
	return nil
}

func canonicalV19WorkerWakeConstraintError(operation, action, operationID string, err error) error {
	if isSQLiteConstraint(err) {
		return fmt.Errorf("%s canonical v19 WorkerWake: %w: operation %q", operation, ErrCanonicalV19WorkerWakeConflict, operationID)
	}
	return canonicalV19WorkerWakeWriteError(operation, action, err)
}

func canonicalV19WorkerWakeWriteError(operation, action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("%s canonical v19 WorkerWake: %s: %w", operation, action, ErrContention)
	}
	return fmt.Errorf("%s canonical v19 WorkerWake: %s: %w", operation, action, err)
}
