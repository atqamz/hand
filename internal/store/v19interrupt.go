package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"unicode/utf8"
)

// ErrCanonicalV19InterruptConflict marks an exact executor-control, ownership,
// scope, or relational conflict. Callers must never retarget.
var ErrCanonicalV19InterruptConflict = errors.New("canonical v19 interrupt write conflict")

// ErrCanonicalV19InterruptNotCurrent marks an Interrupt whose exact active
// Attempt/open SessionBinding/ExecutorBinding ownership no longer matches.
var ErrCanonicalV19InterruptNotCurrent = errors.New("canonical v19 interrupt is not current")

// ErrCanonicalV19InterruptTransition marks an illegal or stale exact external
// operation transition for Interrupt.
var ErrCanonicalV19InterruptTransition = errors.New("canonical v19 interrupt transition conflict")

// CanonicalV19InterruptPrepareInput identifies one fresh exact executor-control
// request. ReasonCode is bounded machine-readable intent, not provider prose.
type CanonicalV19InterruptPrepareInput struct {
	OperationID       string
	OperationKey      string
	ExecutorBindingID string
	ReasonCode        string
	CreatedAt         string
}

// CanonicalV19InterruptRequest is the exact immutable request persisted before
// any provider interruption mutation is authorized.
type CanonicalV19InterruptRequest struct {
	OperationID         string
	OperationKey        string
	RequestDigest       string
	ProjectID           string
	TaskID              string
	PlanID              string
	AttemptID           string
	SessionBindingID    string
	ExecutorBindingID   string
	AdapterRef          string
	ProviderExecutorKey string
	ReasonCode          string
	CreatedAt           string
}

// CanonicalV19InterruptTransitionInput records exact nonsuccess provider
// evidence for one Interrupt operation.
type CanonicalV19InterruptTransitionInput struct {
	OperationID    string
	State          string
	ObservedAt     string
	EvidenceDigest string
}

// CanonicalV19ExecutorInterruptedEvidence is positive provider evidence that
// the exact ExecutorBinding has ceased as the requested Interrupt postcondition.
type CanonicalV19ExecutorInterruptedEvidence struct {
	OperationID    string
	ObservedAt     string
	EvidenceDigest string
}

type canonicalV19InterruptCurrent struct {
	Request        CanonicalV19InterruptRequest
	State          string
	StateChangedAt string
}

// PrepareCanonicalV19Interrupt durably records one exact prepared Interrupt and
// its executor-control claim. It performs no provider mutation.
func PrepareCanonicalV19Interrupt(
	ctx context.Context,
	homeDir string,
	input CanonicalV19InterruptPrepareInput,
) (CanonicalV19InterruptRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19InterruptPrepareInput(input); err != nil {
		return CanonicalV19InterruptRequest{}, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19InterruptRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19InterruptRequest{}, canonicalV19InterruptWriteError("prepare", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19InterruptRequest{}, fmt.Errorf("prepare canonical v19 Interrupt: %w", err)
	}
	request, err := buildCanonicalV19InterruptRequest(ctx, tx, input)
	if err != nil {
		return CanonicalV19InterruptRequest{}, fmt.Errorf("prepare canonical v19 Interrupt: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO external_operation(
		id,kind,adapter_ref,operation_key,request_digest,project_id,task_id,plan_id,attempt_id,
		primary_scope_kind,primary_scope_key,state,created_at,state_changed_at,state_evidence_digest,
		submitted_at,finalized_at
	) VALUES(?,'interrupt',?,?,?,?,?,?,?,'executor-control',?,'prepared',?,?,'','','')`,
		request.OperationID, request.AdapterRef, request.OperationKey, request.RequestDigest,
		request.ProjectID, request.TaskID, request.PlanID, request.AttemptID, request.ExecutorBindingID,
		request.CreatedAt, request.CreatedAt); err != nil {
		return CanonicalV19InterruptRequest{}, canonicalV19InterruptConstraintError("prepare", "insert external operation", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO interrupt_operation(
		operation_id,attempt_id,executor_binding_id,reason_code
	) VALUES(?,?,?,?)`, request.OperationID, request.AttemptID, request.ExecutorBindingID, request.ReasonCode); err != nil {
		return CanonicalV19InterruptRequest{}, canonicalV19InterruptConstraintError("prepare", "insert typed Interrupt", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_scope_claim(operation_id,scope_kind,scope_key,created_at)
		VALUES(?,'executor-control',?,?)`, request.OperationID, request.ExecutorBindingID, request.CreatedAt); err != nil {
		return CanonicalV19InterruptRequest{}, canonicalV19InterruptConstraintError("prepare", "claim exact executor-control scope", request.OperationID, err)
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19InterruptRequest{}, canonicalV19InterruptWriteError("prepare", "commit writer", err)
	}
	committed = true
	return request, nil
}

// SubmitCanonicalV19Interrupt durably authorizes the exact prepared Interrupt.
// Provider mutation may begin only after it returns successfully.
func SubmitCanonicalV19Interrupt(
	ctx context.Context,
	homeDir string,
	operationID string,
	submittedAt string,
	evidenceDigest string,
) (CanonicalV19InterruptRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" || submittedAt == "" || evidenceDigest == "" {
		return CanonicalV19InterruptRequest{}, fmt.Errorf("submit canonical v19 Interrupt: operation ID, submitted_at, and evidence digest are required")
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19InterruptRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19InterruptRequest{}, canonicalV19InterruptWriteError("submit", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19InterruptRequest{}, fmt.Errorf("submit canonical v19 Interrupt: %w", err)
	}
	current, err := loadCanonicalV19InterruptCurrent(ctx, tx, operationID)
	if err != nil {
		return CanonicalV19InterruptRequest{}, fmt.Errorf("submit canonical v19 Interrupt: %w", err)
	}
	if current.State != "prepared" || current.StateChangedAt == submittedAt {
		return CanonicalV19InterruptRequest{}, fmt.Errorf("submit canonical v19 Interrupt: %w: operation %q is %q", ErrCanonicalV19InterruptTransition, operationID, current.State)
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='submitted',state_changed_at=?,state_evidence_digest=?,submitted_at=?
		WHERE id=? AND state='prepared'`, submittedAt, evidenceDigest, submittedAt, operationID)
	if err != nil {
		return CanonicalV19InterruptRequest{}, canonicalV19InterruptConstraintError("submit", "authorize exact operation", operationID, err)
	}
	if err := requireCanonicalV19InterruptOneChanged(result, "submit", operationID); err != nil {
		return CanonicalV19InterruptRequest{}, err
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19InterruptRequest{}, canonicalV19InterruptWriteError("submit", "commit writer", err)
	}
	committed = true
	return current.Request, nil
}

// ClassifyCanonicalV19Interrupt records exact nonsuccess Interrupt evidence.
// Success uses CompleteCanonicalV19Interrupt so cessation evidence is atomic.
func ClassifyCanonicalV19Interrupt(
	ctx context.Context,
	homeDir string,
	input CanonicalV19InterruptTransitionInput,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19InterruptTransitionInput(input); err != nil {
		return err
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19InterruptWriteError("classify", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("classify canonical v19 Interrupt: %w", err)
	}
	current, err := loadCanonicalV19InterruptCurrent(ctx, tx, input.OperationID)
	if err != nil {
		return fmt.Errorf("classify canonical v19 Interrupt: %w", err)
	}
	if !canonicalV19InterruptNonsuccessTransitionAllowed(current.State, input.State) || current.StateChangedAt == input.ObservedAt {
		return fmt.Errorf("classify canonical v19 Interrupt: %w: %s -> %s", ErrCanonicalV19InterruptTransition, current.State, input.State)
	}
	if err := transitionCanonicalV19InterruptOperation(ctx, tx, "classify", input.OperationID,
		current.State, input.State, input.ObservedAt, input.EvidenceDigest); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19InterruptWriteError("classify", "commit writer", err)
	}
	committed = true
	return nil
}

// CompleteCanonicalV19Interrupt atomically marks the exact Interrupt succeeded
// and closes its ExecutorBinding only from positive exact cessation evidence.
func CompleteCanonicalV19Interrupt(
	ctx context.Context,
	homeDir string,
	evidence CanonicalV19ExecutorInterruptedEvidence,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for name, value := range map[string]string{
		"operation ID": evidence.OperationID, "observed_at": evidence.ObservedAt, "evidence digest": evidence.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("complete canonical v19 Interrupt: %s is empty", name)
		}
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19InterruptWriteError("complete", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("complete canonical v19 Interrupt: %w", err)
	}
	current, err := loadCanonicalV19InterruptCurrent(ctx, tx, evidence.OperationID)
	if err != nil {
		return fmt.Errorf("complete canonical v19 Interrupt: %w", err)
	}
	if !canonicalV19InterruptSuccessTransitionAllowed(current.State) || current.StateChangedAt == evidence.ObservedAt {
		return fmt.Errorf("complete canonical v19 Interrupt: %w: operation %q is %q", ErrCanonicalV19InterruptTransition, evidence.OperationID, current.State)
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='succeeded',state_changed_at=?,state_evidence_digest=?,finalized_at=?
		WHERE id=? AND state=?`, evidence.ObservedAt, evidence.EvidenceDigest, evidence.ObservedAt,
		evidence.OperationID, current.State)
	if err != nil {
		return canonicalV19InterruptConstraintError("complete", "mark exact Interrupt succeeded", evidence.OperationID, err)
	}
	if err := requireCanonicalV19InterruptOneChanged(result, "complete", evidence.OperationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO executor_binding_termination(
		executor_binding_id,terminal_kind,interrupt_operation_id,observed_at,evidence_digest
	) VALUES(?,'interrupted',?,?,?)`, current.Request.ExecutorBindingID, evidence.OperationID,
		evidence.ObservedAt, evidence.EvidenceDigest); err != nil {
		return canonicalV19InterruptConstraintError("complete", "insert exact ExecutorBinding termination", evidence.OperationID, err)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19InterruptWriteError("complete", "commit writer", err)
	}
	committed = true
	return nil
}

func validateCanonicalV19InterruptPrepareInput(input CanonicalV19InterruptPrepareInput) error {
	for name, value := range map[string]string{
		"operation ID": input.OperationID, "operation key": input.OperationKey,
		"ExecutorBinding ID": input.ExecutorBindingID, "reason code": input.ReasonCode,
		"created_at": input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("prepare canonical v19 Interrupt: %s is empty", name)
		}
	}
	if utf8.RuneCountInString(input.ReasonCode) > 128 {
		return fmt.Errorf("prepare canonical v19 Interrupt: reason code exceeds 128 characters")
	}
	return nil
}

func validateCanonicalV19InterruptTransitionInput(input CanonicalV19InterruptTransitionInput) error {
	for name, value := range map[string]string{
		"operation ID": input.OperationID, "observed_at": input.ObservedAt, "evidence digest": input.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("classify canonical v19 Interrupt: %s is empty", name)
		}
	}
	switch input.State {
	case "uncertain", "rejected", "no-effect":
		return nil
	default:
		return fmt.Errorf("classify canonical v19 Interrupt: state %q requires a different writer", input.State)
	}
}

func buildCanonicalV19InterruptRequest(
	ctx context.Context,
	tx *sql.Tx,
	input CanonicalV19InterruptPrepareInput,
) (CanonicalV19InterruptRequest, error) {
	request := CanonicalV19InterruptRequest{
		OperationID: input.OperationID, OperationKey: input.OperationKey,
		ExecutorBindingID: input.ExecutorBindingID, ReasonCode: input.ReasonCode, CreatedAt: input.CreatedAt,
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
		return CanonicalV19InterruptRequest{}, fmt.Errorf("%w: ExecutorBinding %q lacks exact active/open ownership", ErrCanonicalV19InterruptNotCurrent, input.ExecutorBindingID)
	}
	if err != nil {
		return CanonicalV19InterruptRequest{}, canonicalV19InterruptWriteError("prepare", "read exact current ExecutorBinding", err)
	}
	request.RequestDigest = canonicalV19InterruptRequestDigest(request)
	return request, nil
}

func loadCanonicalV19InterruptCurrent(
	ctx context.Context,
	tx *sql.Tx,
	operationID string,
) (canonicalV19InterruptCurrent, error) {
	var current canonicalV19InterruptCurrent
	request := &current.Request
	err := tx.QueryRowContext(ctx, `SELECT o.state,o.state_changed_at,o.operation_key,o.request_digest,
		o.project_id,o.task_id,o.plan_id,o.attempt_id,e.session_binding_id,i.executor_binding_id,
		o.adapter_ref,e.provider_executor_key,i.reason_code,o.created_at
		FROM external_operation o
		JOIN interrupt_operation i ON i.operation_id=o.id AND i.attempt_id=o.attempt_id
		JOIN executor_binding e ON e.id=i.executor_binding_id AND e.attempt_id=o.attempt_id
		JOIN session_binding s ON s.id=e.session_binding_id AND s.attempt_id=e.attempt_id AND s.adapter_ref=e.adapter_ref
		JOIN attempt a ON a.id=e.attempt_id AND a.lifecycle='active' AND a.terminal_at=''
		JOIN plan p ON p.id=a.plan_id AND p.id=o.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.id=o.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.id=o.project_id AND project_current.retired_at=''
		JOIN operation_scope_claim executor_claim ON executor_claim.operation_id=o.id
		  AND executor_claim.scope_kind='executor-control' AND executor_claim.scope_key=e.id
		WHERE o.id=? AND o.kind='interrupt' AND o.adapter_ref=e.adapter_ref
		  AND o.primary_scope_kind='executor-control' AND o.primary_scope_key=e.id
		  AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)
		  AND NOT EXISTS (SELECT 1 FROM executor_binding_termination x WHERE x.executor_binding_id=e.id)`, operationID).Scan(
		&current.State, &current.StateChangedAt, &request.OperationKey, &request.RequestDigest,
		&request.ProjectID, &request.TaskID, &request.PlanID, &request.AttemptID,
		&request.SessionBindingID, &request.ExecutorBindingID, &request.AdapterRef,
		&request.ProviderExecutorKey, &request.ReasonCode, &request.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return canonicalV19InterruptCurrent{}, fmt.Errorf("%w: operation %q lacks exact current Interrupt request/ownership/claim", ErrCanonicalV19InterruptNotCurrent, operationID)
	}
	if err != nil {
		return canonicalV19InterruptCurrent{}, canonicalV19InterruptWriteError("current", "read exact Interrupt", err)
	}
	request.OperationID = operationID
	if canonicalV19InterruptRequestDigest(*request) != request.RequestDigest {
		return canonicalV19InterruptCurrent{}, fmt.Errorf("%w: operation %q request digest does not match persisted Interrupt", ErrCanonicalV19InterruptNotCurrent, operationID)
	}
	return current, nil
}

func canonicalV19InterruptRequestDigest(request CanonicalV19InterruptRequest) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:interrupt-request:v1")
	writeCanonicalV19DigestField(hash, "project_id", request.ProjectID)
	writeCanonicalV19DigestField(hash, "task_id", request.TaskID)
	writeCanonicalV19DigestField(hash, "plan_id", request.PlanID)
	writeCanonicalV19DigestField(hash, "attempt_id", request.AttemptID)
	writeCanonicalV19DigestField(hash, "session_binding_id", request.SessionBindingID)
	writeCanonicalV19DigestField(hash, "executor_binding_id", request.ExecutorBindingID)
	writeCanonicalV19DigestField(hash, "adapter_ref", request.AdapterRef)
	writeCanonicalV19DigestField(hash, "provider_executor_key", request.ProviderExecutorKey)
	writeCanonicalV19DigestField(hash, "reason_code", request.ReasonCode)
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalV19InterruptNonsuccessTransitionAllowed(from, to string) bool {
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

func canonicalV19InterruptSuccessTransitionAllowed(from string) bool {
	return from == "prepared" || from == "submitted" || from == "uncertain"
}

func transitionCanonicalV19InterruptOperation(
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
		return canonicalV19InterruptConstraintError(operation, "transition exact operation", operationID, err)
	}
	return requireCanonicalV19InterruptOneChanged(result, operation, operationID)
}

func requireCanonicalV19InterruptOneChanged(result sql.Result, operation, operationID string) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return canonicalV19InterruptWriteError(operation, "count exact state transition", err)
	}
	if changed != 1 {
		return fmt.Errorf("%s canonical v19 Interrupt: %w: operation %q changed %d rows, want 1", operation, ErrCanonicalV19InterruptTransition, operationID, changed)
	}
	return nil
}

func canonicalV19InterruptConstraintError(operation, action, operationID string, err error) error {
	if isSQLiteConstraint(err) {
		return fmt.Errorf("%s canonical v19 Interrupt: %w: operation %q", operation, ErrCanonicalV19InterruptConflict, operationID)
	}
	return canonicalV19InterruptWriteError(operation, action, err)
}

func canonicalV19InterruptWriteError(operation, action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("%s canonical v19 Interrupt: %s: %w", operation, action, ErrContention)
	}
	return fmt.Errorf("%s canonical v19 Interrupt: %s: %w", operation, action, err)
}
