package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrCanonicalV19SessionConflict marks an exact Session resource, dependency,
// scope, or relational conflict. Callers must never retarget.
var ErrCanonicalV19SessionConflict = errors.New("canonical v19 session write conflict")

// ErrCanonicalV19SessionNotCurrent marks a Session operation whose exact
// Attempt/resource ownership no longer satisfies its persisted request.
var ErrCanonicalV19SessionNotCurrent = errors.New("canonical v19 session is not current")

// ErrCanonicalV19SessionTransition marks an illegal or stale exact external
// operation transition for Session acquisition or release.
var ErrCanonicalV19SessionTransition = errors.New("canonical v19 session transition conflict")

// CanonicalV19SessionAcquirePrepareInput identifies one fresh logical provider
// Session acquisition request for an exact active Attempt.
type CanonicalV19SessionAcquirePrepareInput struct {
	OperationID                 string
	OperationKey                string
	AttemptID                   string
	BindingID                   string
	RequestedProviderSessionKey string
	CreatedAt                   string
}

// CanonicalV19SessionAcquireRequest is the exact immutable provider Session
// request persisted before any external mutation is authorized.
type CanonicalV19SessionAcquireRequest struct {
	OperationID                 string
	OperationKey                string
	RequestDigest               string
	ProjectID                   string
	TaskID                      string
	PlanID                      string
	AttemptID                   string
	WorktreeBindingID           string
	BindingID                   string
	AdapterRef                  string
	RequestedProviderSessionKey string
	CreatedAt                   string
}

// CanonicalV19SessionAcquireTransitionInput records exact nonsuccess provider
// evidence for one Session acquisition operation.
type CanonicalV19SessionAcquireTransitionInput struct {
	OperationID    string
	State          string
	ObservedAt     string
	EvidenceDigest string
}

// CanonicalV19SessionBindingEvidence is positive provider evidence establishing
// the exact SessionBinding requested by one acquisition operation.
type CanonicalV19SessionBindingEvidence struct {
	OperationID        string
	ProviderSessionKey string
	EstablishedAt      string
	EvidenceDigest     string
}

// CanonicalV19SessionReleasePrepareInput identifies one fresh logical release
// request for one exact open SessionBinding.
type CanonicalV19SessionReleasePrepareInput struct {
	OperationID      string
	OperationKey     string
	SessionBindingID string
	CreatedAt        string
}

// CanonicalV19SessionReleaseRequest is the exact immutable provider Session
// release request derived from one open SessionBinding.
type CanonicalV19SessionReleaseRequest struct {
	OperationID                string
	OperationKey               string
	RequestDigest              string
	ProjectID                  string
	TaskID                     string
	PlanID                     string
	AttemptID                  string
	WorktreeBindingID          string
	SessionBindingID           string
	AdapterRef                 string
	ExpectedProviderSessionKey string
	CreatedAt                  string
}

// CanonicalV19SessionReleaseTransitionInput records exact nonsuccess provider
// evidence for one Session release operation.
type CanonicalV19SessionReleaseTransitionInput struct {
	OperationID    string
	State          string
	ObservedAt     string
	EvidenceDigest string
}

// CanonicalV19SessionReleasedEvidence is positive evidence that the exact
// provider Session resource is released/absent under the adapter contract.
type CanonicalV19SessionReleasedEvidence struct {
	OperationID    string
	ReleasedAt     string
	EvidenceDigest string
}

type canonicalV19SessionAcquireCurrent struct {
	Request        CanonicalV19SessionAcquireRequest
	State          string
	StateChangedAt string
}

type canonicalV19SessionReleaseCurrent struct {
	Request        CanonicalV19SessionReleaseRequest
	State          string
	StateChangedAt string
}

// PrepareCanonicalV19SessionAcquire durably records one exact prepared Session
// request plus its Session and Worktree scope claims. It performs no mutation.
func PrepareCanonicalV19SessionAcquire(
	ctx context.Context,
	homeDir string,
	input CanonicalV19SessionAcquirePrepareInput,
) (CanonicalV19SessionAcquireRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19SessionAcquirePrepareInput(input); err != nil {
		return CanonicalV19SessionAcquireRequest{}, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19SessionAcquireRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19SessionAcquireRequest{}, canonicalV19SessionWriteError("prepare acquire", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19SessionAcquireRequest{}, fmt.Errorf("prepare canonical v19 SessionAcquire: %w", err)
	}
	request, err := buildCanonicalV19SessionAcquireRequest(ctx, tx, input)
	if err != nil {
		return CanonicalV19SessionAcquireRequest{}, fmt.Errorf("prepare canonical v19 SessionAcquire: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO external_operation(
		id,kind,adapter_ref,operation_key,request_digest,project_id,task_id,plan_id,attempt_id,
		primary_scope_kind,primary_scope_key,state,created_at,state_changed_at,state_evidence_digest,
		submitted_at,finalized_at
	) VALUES(?,'session-acquire',?,?,?,?,?,?,?,'session',?,'prepared',?,?,'','','')`,
		request.OperationID, request.AdapterRef, request.OperationKey, request.RequestDigest,
		request.ProjectID, request.TaskID, request.PlanID, request.AttemptID, request.BindingID,
		request.CreatedAt, request.CreatedAt); err != nil {
		return CanonicalV19SessionAcquireRequest{}, canonicalV19SessionConstraintError("prepare acquire", "insert external operation", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO session_acquire_operation(
		operation_id,attempt_id,worktree_binding_id,binding_id,requested_provider_session_key
	) VALUES(?,?,?,?,?)`, request.OperationID, request.AttemptID, request.WorktreeBindingID,
		request.BindingID, request.RequestedProviderSessionKey); err != nil {
		return CanonicalV19SessionAcquireRequest{}, canonicalV19SessionConstraintError("prepare acquire", "insert typed SessionAcquire", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_scope_claim(operation_id,scope_kind,scope_key,created_at)
		VALUES(?,'session',?,?)`, request.OperationID, request.BindingID, request.CreatedAt); err != nil {
		return CanonicalV19SessionAcquireRequest{}, canonicalV19SessionConstraintError("prepare acquire", "claim exact Session scope", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_scope_claim(operation_id,scope_kind,scope_key,created_at)
		VALUES(?,'worktree',?,?)`, request.OperationID, request.WorktreeBindingID, request.CreatedAt); err != nil {
		return CanonicalV19SessionAcquireRequest{}, canonicalV19SessionConstraintError("prepare acquire", "claim dependent Worktree scope", request.OperationID, err)
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19SessionAcquireRequest{}, canonicalV19SessionWriteError("prepare acquire", "commit writer", err)
	}
	committed = true
	return request, nil
}

// SubmitCanonicalV19SessionAcquire durably authorizes the exact prepared
// acquisition. Provider mutation may begin only after it returns successfully.
func SubmitCanonicalV19SessionAcquire(
	ctx context.Context,
	homeDir string,
	operationID string,
	submittedAt string,
	evidenceDigest string,
) (CanonicalV19SessionAcquireRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" || submittedAt == "" || evidenceDigest == "" {
		return CanonicalV19SessionAcquireRequest{}, fmt.Errorf("submit canonical v19 SessionAcquire: operation ID, submitted_at, and evidence digest are required")
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19SessionAcquireRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19SessionAcquireRequest{}, canonicalV19SessionWriteError("submit acquire", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19SessionAcquireRequest{}, fmt.Errorf("submit canonical v19 SessionAcquire: %w", err)
	}
	current, err := loadCanonicalV19SessionAcquireCurrent(ctx, tx, operationID)
	if err != nil {
		return CanonicalV19SessionAcquireRequest{}, fmt.Errorf("submit canonical v19 SessionAcquire: %w", err)
	}
	if current.State != "prepared" || current.StateChangedAt == submittedAt {
		return CanonicalV19SessionAcquireRequest{}, fmt.Errorf("submit canonical v19 SessionAcquire: %w: operation %q is %q", ErrCanonicalV19SessionTransition, operationID, current.State)
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='submitted',state_changed_at=?,state_evidence_digest=?,submitted_at=?
		WHERE id=? AND state='prepared'`, submittedAt, evidenceDigest, submittedAt, operationID)
	if err != nil {
		return CanonicalV19SessionAcquireRequest{}, canonicalV19SessionConstraintError("submit acquire", "authorize exact operation", operationID, err)
	}
	if err := requireCanonicalV19SessionOneChanged(result, "submit acquire", operationID); err != nil {
		return CanonicalV19SessionAcquireRequest{}, err
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19SessionAcquireRequest{}, canonicalV19SessionWriteError("submit acquire", "commit writer", err)
	}
	committed = true
	return current.Request, nil
}

// ClassifyCanonicalV19SessionAcquire records exact nonsuccess acquisition
// evidence. Success uses EstablishCanonicalV19SessionBinding.
func ClassifyCanonicalV19SessionAcquire(
	ctx context.Context,
	homeDir string,
	input CanonicalV19SessionAcquireTransitionInput,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19SessionTransition("SessionAcquire", input.OperationID, input.State, input.ObservedAt, input.EvidenceDigest); err != nil {
		return err
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19SessionWriteError("classify acquire", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("classify canonical v19 SessionAcquire: %w", err)
	}
	current, err := loadCanonicalV19SessionAcquireCurrent(ctx, tx, input.OperationID)
	if err != nil {
		return fmt.Errorf("classify canonical v19 SessionAcquire: %w", err)
	}
	if !canonicalV19SessionNonsuccessTransitionAllowed(current.State, input.State) || current.StateChangedAt == input.ObservedAt {
		return fmt.Errorf("classify canonical v19 SessionAcquire: %w: %s -> %s", ErrCanonicalV19SessionTransition, current.State, input.State)
	}
	if err := transitionCanonicalV19SessionOperation(ctx, tx, "classify acquire", input.OperationID,
		current.State, input.State, input.ObservedAt, input.EvidenceDigest); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19SessionWriteError("classify acquire", "commit writer", err)
	}
	committed = true
	return nil
}

// EstablishCanonicalV19SessionBinding atomically marks the exact acquisition
// succeeded and inserts its immutable SessionBinding from positive evidence.
func EstablishCanonicalV19SessionBinding(
	ctx context.Context,
	homeDir string,
	evidence CanonicalV19SessionBindingEvidence,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for name, value := range map[string]string{
		"operation ID": evidence.OperationID, "provider Session key": evidence.ProviderSessionKey,
		"established_at": evidence.EstablishedAt, "evidence digest": evidence.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("establish canonical v19 SessionBinding: %s is empty", name)
		}
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19SessionWriteError("establish", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("establish canonical v19 SessionBinding: %w", err)
	}
	current, err := loadCanonicalV19SessionAcquireCurrent(ctx, tx, evidence.OperationID)
	if err != nil {
		return fmt.Errorf("establish canonical v19 SessionBinding: %w", err)
	}
	if !canonicalV19SessionSuccessTransitionAllowed(current.State) || current.StateChangedAt == evidence.EstablishedAt {
		return fmt.Errorf("establish canonical v19 SessionBinding: %w: operation %q is %q", ErrCanonicalV19SessionTransition, evidence.OperationID, current.State)
	}
	request := current.Request
	if request.RequestedProviderSessionKey != "" && request.RequestedProviderSessionKey != evidence.ProviderSessionKey {
		return fmt.Errorf("establish canonical v19 SessionBinding: %w: provider Session key differs from exact request", ErrCanonicalV19SessionNotCurrent)
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='succeeded',state_changed_at=?,state_evidence_digest=?,finalized_at=?
		WHERE id=? AND state=?`, evidence.EstablishedAt, evidence.EvidenceDigest, evidence.EstablishedAt,
		evidence.OperationID, current.State)
	if err != nil {
		return canonicalV19SessionConstraintError("establish", "mark exact acquisition succeeded", evidence.OperationID, err)
	}
	if err := requireCanonicalV19SessionOneChanged(result, "establish", evidence.OperationID); err != nil {
		return err
	}
	var ordinal int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ordinal),0)+1 FROM session_binding WHERE attempt_id=?`, request.AttemptID).Scan(&ordinal); err != nil {
		return canonicalV19SessionWriteError("establish", "allocate SessionBinding ordinal", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO session_binding(
		id,attempt_id,ordinal,worktree_binding_id,acquire_operation_id,adapter_ref,provider_session_key,established_at
	) VALUES(?,?,?,?,?,?,?,?)`, request.BindingID, request.AttemptID, ordinal, request.WorktreeBindingID,
		request.OperationID, request.AdapterRef, evidence.ProviderSessionKey, evidence.EstablishedAt); err != nil {
		return canonicalV19SessionConstraintError("establish", "insert exact SessionBinding", evidence.OperationID, err)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19SessionWriteError("establish", "commit writer", err)
	}
	committed = true
	return nil
}

// PrepareCanonicalV19SessionRelease records one prepared exact Session release
// request. Terminal semantic ancestors are allowed for resource cleanup.
func PrepareCanonicalV19SessionRelease(
	ctx context.Context,
	homeDir string,
	input CanonicalV19SessionReleasePrepareInput,
) (CanonicalV19SessionReleaseRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19SessionReleasePrepareInput(input); err != nil {
		return CanonicalV19SessionReleaseRequest{}, err
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19SessionReleaseRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19SessionReleaseRequest{}, canonicalV19SessionWriteError("prepare release", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19SessionReleaseRequest{}, fmt.Errorf("prepare canonical v19 SessionRelease: %w", err)
	}
	request, err := buildCanonicalV19SessionReleaseRequest(ctx, tx, input)
	if err != nil {
		return CanonicalV19SessionReleaseRequest{}, fmt.Errorf("prepare canonical v19 SessionRelease: %w", err)
	}
	if err := requireCanonicalV19SessionNoOpenExecutor(ctx, tx, request.SessionBindingID); err != nil {
		return CanonicalV19SessionReleaseRequest{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO external_operation(
		id,kind,adapter_ref,operation_key,request_digest,project_id,task_id,plan_id,attempt_id,
		primary_scope_kind,primary_scope_key,state,created_at,state_changed_at,state_evidence_digest,
		submitted_at,finalized_at
	) VALUES(?,'session-release',?,?,?,?,?,?,?,'session',?,'prepared',?,?,'','','')`,
		request.OperationID, request.AdapterRef, request.OperationKey, request.RequestDigest,
		request.ProjectID, request.TaskID, request.PlanID, request.AttemptID, request.SessionBindingID,
		request.CreatedAt, request.CreatedAt); err != nil {
		return CanonicalV19SessionReleaseRequest{}, canonicalV19SessionConstraintError("prepare release", "insert external operation", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO session_release_operation(
		operation_id,session_binding_id,expected_provider_session_key
	) VALUES(?,?,?)`, request.OperationID, request.SessionBindingID, request.ExpectedProviderSessionKey); err != nil {
		return CanonicalV19SessionReleaseRequest{}, canonicalV19SessionConstraintError("prepare release", "insert typed SessionRelease", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_scope_claim(operation_id,scope_kind,scope_key,created_at)
		VALUES(?,'session',?,?)`, request.OperationID, request.SessionBindingID, request.CreatedAt); err != nil {
		return CanonicalV19SessionReleaseRequest{}, canonicalV19SessionConstraintError("prepare release", "claim exact Session scope", request.OperationID, err)
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19SessionReleaseRequest{}, canonicalV19SessionWriteError("prepare release", "commit writer", err)
	}
	committed = true
	return request, nil
}

// SubmitCanonicalV19SessionRelease durably authorizes the exact prepared
// release. Provider mutation may begin only after it returns successfully.
func SubmitCanonicalV19SessionRelease(
	ctx context.Context,
	homeDir string,
	operationID string,
	submittedAt string,
	evidenceDigest string,
) (CanonicalV19SessionReleaseRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" || submittedAt == "" || evidenceDigest == "" {
		return CanonicalV19SessionReleaseRequest{}, fmt.Errorf("submit canonical v19 SessionRelease: operation ID, submitted_at, and evidence digest are required")
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19SessionReleaseRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19SessionReleaseRequest{}, canonicalV19SessionWriteError("submit release", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19SessionReleaseRequest{}, fmt.Errorf("submit canonical v19 SessionRelease: %w", err)
	}
	current, err := loadCanonicalV19SessionReleaseCurrent(ctx, tx, operationID)
	if err != nil {
		return CanonicalV19SessionReleaseRequest{}, fmt.Errorf("submit canonical v19 SessionRelease: %w", err)
	}
	if err := requireCanonicalV19SessionNoOpenExecutor(ctx, tx, current.Request.SessionBindingID); err != nil {
		return CanonicalV19SessionReleaseRequest{}, err
	}
	if current.State != "prepared" || current.StateChangedAt == submittedAt {
		return CanonicalV19SessionReleaseRequest{}, fmt.Errorf("submit canonical v19 SessionRelease: %w: operation %q is %q", ErrCanonicalV19SessionTransition, operationID, current.State)
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='submitted',state_changed_at=?,state_evidence_digest=?,submitted_at=?
		WHERE id=? AND state='prepared'`, submittedAt, evidenceDigest, submittedAt, operationID)
	if err != nil {
		return CanonicalV19SessionReleaseRequest{}, canonicalV19SessionConstraintError("submit release", "authorize exact operation", operationID, err)
	}
	if err := requireCanonicalV19SessionOneChanged(result, "submit release", operationID); err != nil {
		return CanonicalV19SessionReleaseRequest{}, err
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19SessionReleaseRequest{}, canonicalV19SessionWriteError("submit release", "commit writer", err)
	}
	committed = true
	return current.Request, nil
}

// ClassifyCanonicalV19SessionRelease records exact nonsuccess release evidence.
// Success uses CompleteCanonicalV19SessionRelease.
func ClassifyCanonicalV19SessionRelease(
	ctx context.Context,
	homeDir string,
	input CanonicalV19SessionReleaseTransitionInput,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19SessionTransition("SessionRelease", input.OperationID, input.State, input.ObservedAt, input.EvidenceDigest); err != nil {
		return err
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19SessionWriteError("classify release", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("classify canonical v19 SessionRelease: %w", err)
	}
	current, err := loadCanonicalV19SessionReleaseCurrent(ctx, tx, input.OperationID)
	if err != nil {
		return fmt.Errorf("classify canonical v19 SessionRelease: %w", err)
	}
	if !canonicalV19SessionNonsuccessTransitionAllowed(current.State, input.State) || current.StateChangedAt == input.ObservedAt {
		return fmt.Errorf("classify canonical v19 SessionRelease: %w: %s -> %s", ErrCanonicalV19SessionTransition, current.State, input.State)
	}
	if err := transitionCanonicalV19SessionOperation(ctx, tx, "classify release", input.OperationID,
		current.State, input.State, input.ObservedAt, input.EvidenceDigest); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19SessionWriteError("classify release", "commit writer", err)
	}
	committed = true
	return nil
}

// CompleteCanonicalV19SessionRelease atomically marks the exact release
// succeeded and inserts its immutable SessionBinding release evidence.
func CompleteCanonicalV19SessionRelease(
	ctx context.Context,
	homeDir string,
	evidence CanonicalV19SessionReleasedEvidence,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for name, value := range map[string]string{
		"operation ID": evidence.OperationID, "released_at": evidence.ReleasedAt, "evidence digest": evidence.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("complete canonical v19 SessionRelease: %s is empty", name)
		}
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19SessionWriteError("complete release", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("complete canonical v19 SessionRelease: %w", err)
	}
	current, err := loadCanonicalV19SessionReleaseCurrent(ctx, tx, evidence.OperationID)
	if err != nil {
		return fmt.Errorf("complete canonical v19 SessionRelease: %w", err)
	}
	if err := requireCanonicalV19SessionNoOpenExecutor(ctx, tx, current.Request.SessionBindingID); err != nil {
		return err
	}
	if !canonicalV19SessionSuccessTransitionAllowed(current.State) || current.StateChangedAt == evidence.ReleasedAt {
		return fmt.Errorf("complete canonical v19 SessionRelease: %w: operation %q is %q", ErrCanonicalV19SessionTransition, evidence.OperationID, current.State)
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='succeeded',state_changed_at=?,state_evidence_digest=?,finalized_at=?
		WHERE id=? AND state=?`, evidence.ReleasedAt, evidence.EvidenceDigest, evidence.ReleasedAt,
		evidence.OperationID, current.State)
	if err != nil {
		return canonicalV19SessionConstraintError("complete release", "mark exact release succeeded", evidence.OperationID, err)
	}
	if err := requireCanonicalV19SessionOneChanged(result, "complete release", evidence.OperationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO session_binding_release(
		session_binding_id,release_operation_id,released_at,evidence_digest
	) VALUES(?,?,?,?)`, current.Request.SessionBindingID, evidence.OperationID,
		evidence.ReleasedAt, evidence.EvidenceDigest); err != nil {
		return canonicalV19SessionConstraintError("complete release", "insert exact SessionBinding release", evidence.OperationID, err)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19SessionWriteError("complete release", "commit writer", err)
	}
	committed = true
	return nil
}

func validateCanonicalV19SessionAcquirePrepareInput(input CanonicalV19SessionAcquirePrepareInput) error {
	for name, value := range map[string]string{
		"operation ID": input.OperationID, "operation key": input.OperationKey,
		"Attempt ID": input.AttemptID, "binding ID": input.BindingID, "created_at": input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("prepare canonical v19 SessionAcquire: %s is empty", name)
		}
	}
	return nil
}

func validateCanonicalV19SessionReleasePrepareInput(input CanonicalV19SessionReleasePrepareInput) error {
	for name, value := range map[string]string{
		"operation ID": input.OperationID, "operation key": input.OperationKey,
		"SessionBinding ID": input.SessionBindingID, "created_at": input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("prepare canonical v19 SessionRelease: %s is empty", name)
		}
	}
	return nil
}

func validateCanonicalV19SessionTransition(kind, operationID, state, observedAt, evidenceDigest string) error {
	for name, value := range map[string]string{
		"operation ID": operationID, "observed_at": observedAt, "evidence digest": evidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("classify canonical v19 %s: %s is empty", kind, name)
		}
	}
	switch state {
	case "uncertain", "rejected", "no-effect":
		return nil
	default:
		return fmt.Errorf("classify canonical v19 %s: state %q requires a different writer", kind, state)
	}
}

func buildCanonicalV19SessionAcquireRequest(
	ctx context.Context,
	tx *sql.Tx,
	input CanonicalV19SessionAcquirePrepareInput,
) (CanonicalV19SessionAcquireRequest, error) {
	request := CanonicalV19SessionAcquireRequest{
		OperationID: input.OperationID, OperationKey: input.OperationKey, AttemptID: input.AttemptID,
		BindingID: input.BindingID, RequestedProviderSessionKey: input.RequestedProviderSessionKey, CreatedAt: input.CreatedAt,
	}
	err := tx.QueryRowContext(ctx, `SELECT project_current.id,t.id,p.id,a.session_adapter_ref,b.id
		FROM attempt a
		JOIN plan p ON p.id=a.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.retired_at=''
		JOIN attempt_worktree_binding b ON b.attempt_id=a.id
		WHERE a.id=? AND a.lifecycle='active' AND a.terminal_at=''
		  AND NOT EXISTS (SELECT 1 FROM worktree_binding_release r WHERE r.binding_id=b.id)
		  AND NOT EXISTS (SELECT 1 FROM session_binding s WHERE s.attempt_id=a.id)`, input.AttemptID).Scan(
		&request.ProjectID, &request.TaskID, &request.PlanID, &request.AdapterRef, &request.WorktreeBindingID)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19SessionAcquireRequest{}, fmt.Errorf("%w: Attempt %q lacks exact active lineage/open WorktreeBinding or already has a SessionBinding", ErrCanonicalV19SessionNotCurrent, input.AttemptID)
	}
	if err != nil {
		return CanonicalV19SessionAcquireRequest{}, canonicalV19SessionWriteError("prepare acquire", "read exact current Attempt/WorktreeBinding", err)
	}
	request.RequestDigest = canonicalV19SessionAcquireDigest(request)
	return request, nil
}

func loadCanonicalV19SessionAcquireCurrent(
	ctx context.Context,
	tx *sql.Tx,
	operationID string,
) (canonicalV19SessionAcquireCurrent, error) {
	var current canonicalV19SessionAcquireCurrent
	request := &current.Request
	err := tx.QueryRowContext(ctx, `SELECT o.state,o.state_changed_at,o.operation_key,o.request_digest,
		o.project_id,o.task_id,o.plan_id,o.attempt_id,sa.worktree_binding_id,sa.binding_id,
		o.adapter_ref,sa.requested_provider_session_key,o.created_at
		FROM external_operation o
		JOIN session_acquire_operation sa ON sa.operation_id=o.id AND sa.attempt_id=o.attempt_id
		JOIN attempt a ON a.id=o.attempt_id AND a.lifecycle='active' AND a.terminal_at=''
		JOIN plan p ON p.id=a.plan_id AND p.id=o.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.id=o.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.id=o.project_id AND project_current.retired_at=''
		JOIN attempt_worktree_binding b ON b.id=sa.worktree_binding_id AND b.attempt_id=a.id
		JOIN operation_scope_claim session_claim ON session_claim.operation_id=o.id
		  AND session_claim.scope_kind='session' AND session_claim.scope_key=sa.binding_id
		JOIN operation_scope_claim worktree_claim ON worktree_claim.operation_id=o.id
		  AND worktree_claim.scope_kind='worktree' AND worktree_claim.scope_key=b.id
		WHERE o.id=? AND o.kind='session-acquire' AND o.adapter_ref=a.session_adapter_ref
		  AND o.primary_scope_kind='session' AND o.primary_scope_key=sa.binding_id
		  AND NOT EXISTS (SELECT 1 FROM worktree_binding_release r WHERE r.binding_id=b.id)
		  AND NOT EXISTS (
			SELECT 1 FROM session_binding s
			WHERE s.attempt_id=a.id AND s.acquire_operation_id<>o.id
		  )`, operationID).Scan(
		&current.State, &current.StateChangedAt, &request.OperationKey, &request.RequestDigest,
		&request.ProjectID, &request.TaskID, &request.PlanID, &request.AttemptID,
		&request.WorktreeBindingID, &request.BindingID, &request.AdapterRef,
		&request.RequestedProviderSessionKey, &request.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return canonicalV19SessionAcquireCurrent{}, fmt.Errorf("%w: operation %q lacks exact current SessionAcquire request/ownership/claims", ErrCanonicalV19SessionNotCurrent, operationID)
	}
	if err != nil {
		return canonicalV19SessionAcquireCurrent{}, canonicalV19SessionWriteError("current acquire", "read exact SessionAcquire", err)
	}
	request.OperationID = operationID
	if canonicalV19SessionAcquireDigest(*request) != request.RequestDigest {
		return canonicalV19SessionAcquireCurrent{}, fmt.Errorf("%w: operation %q request digest does not match persisted SessionAcquire", ErrCanonicalV19SessionNotCurrent, operationID)
	}
	return current, nil
}

func buildCanonicalV19SessionReleaseRequest(
	ctx context.Context,
	tx *sql.Tx,
	input CanonicalV19SessionReleasePrepareInput,
) (CanonicalV19SessionReleaseRequest, error) {
	request := CanonicalV19SessionReleaseRequest{
		OperationID: input.OperationID, OperationKey: input.OperationKey,
		SessionBindingID: input.SessionBindingID, CreatedAt: input.CreatedAt,
	}
	err := tx.QueryRowContext(ctx, `SELECT project_owner.id,t.id,p.id,a.id,s.worktree_binding_id,s.adapter_ref,s.provider_session_key
		FROM session_binding s
		JOIN attempt a ON a.id=s.attempt_id
		JOIN plan p ON p.id=a.plan_id
		JOIN task t ON t.id=p.task_id
		JOIN project project_owner ON project_owner.id=t.project_id
		WHERE s.id=?
		  AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)`,
		input.SessionBindingID).Scan(&request.ProjectID, &request.TaskID, &request.PlanID, &request.AttemptID,
		&request.WorktreeBindingID, &request.AdapterRef, &request.ExpectedProviderSessionKey)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19SessionReleaseRequest{}, fmt.Errorf("%w: SessionBinding %q is not exact and open", ErrCanonicalV19SessionNotCurrent, input.SessionBindingID)
	}
	if err != nil {
		return CanonicalV19SessionReleaseRequest{}, canonicalV19SessionWriteError("prepare release", "read exact open SessionBinding", err)
	}
	request.RequestDigest = canonicalV19SessionReleaseDigest(request)
	return request, nil
}

func loadCanonicalV19SessionReleaseCurrent(
	ctx context.Context,
	tx *sql.Tx,
	operationID string,
) (canonicalV19SessionReleaseCurrent, error) {
	var current canonicalV19SessionReleaseCurrent
	request := &current.Request
	err := tx.QueryRowContext(ctx, `SELECT o.state,o.state_changed_at,o.operation_key,o.request_digest,
		o.project_id,o.task_id,o.plan_id,o.attempt_id,s.worktree_binding_id,s.id,o.adapter_ref,
		sr.expected_provider_session_key,o.created_at
		FROM external_operation o
		JOIN session_release_operation sr ON sr.operation_id=o.id
		JOIN session_binding s ON s.id=sr.session_binding_id AND s.attempt_id=o.attempt_id
		JOIN attempt a ON a.id=s.attempt_id
		JOIN plan p ON p.id=a.plan_id AND p.id=o.plan_id
		JOIN task t ON t.id=p.task_id AND t.id=o.task_id
		JOIN project project_owner ON project_owner.id=t.project_id AND project_owner.id=o.project_id
		JOIN operation_scope_claim session_claim ON session_claim.operation_id=o.id
		  AND session_claim.scope_kind='session' AND session_claim.scope_key=s.id
		WHERE o.id=? AND o.kind='session-release' AND o.adapter_ref=s.adapter_ref
		  AND o.primary_scope_kind='session' AND o.primary_scope_key=s.id
		  AND sr.expected_provider_session_key=s.provider_session_key
		  AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)`, operationID).Scan(
		&current.State, &current.StateChangedAt, &request.OperationKey, &request.RequestDigest,
		&request.ProjectID, &request.TaskID, &request.PlanID, &request.AttemptID,
		&request.WorktreeBindingID, &request.SessionBindingID, &request.AdapterRef,
		&request.ExpectedProviderSessionKey, &request.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return canonicalV19SessionReleaseCurrent{}, fmt.Errorf("%w: operation %q lacks exact open SessionRelease request/ownership/claim", ErrCanonicalV19SessionNotCurrent, operationID)
	}
	if err != nil {
		return canonicalV19SessionReleaseCurrent{}, canonicalV19SessionWriteError("current release", "read exact SessionRelease", err)
	}
	request.OperationID = operationID
	if canonicalV19SessionReleaseDigest(*request) != request.RequestDigest {
		return canonicalV19SessionReleaseCurrent{}, fmt.Errorf("%w: operation %q request digest does not match persisted SessionRelease", ErrCanonicalV19SessionNotCurrent, operationID)
	}
	return current, nil
}

func requireCanonicalV19SessionNoOpenExecutor(ctx context.Context, tx *sql.Tx, sessionBindingID string) error {
	var openCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM executor_binding e
		WHERE e.session_binding_id=?
		  AND NOT EXISTS (SELECT 1 FROM executor_binding_termination t WHERE t.executor_binding_id=e.id)`, sessionBindingID).Scan(&openCount); err != nil {
		return canonicalV19SessionWriteError("current release", "read dependent ExecutorBinding", err)
	}
	if openCount != 0 {
		return fmt.Errorf("canonical v19 SessionRelease: %w: SessionBinding %q has %d open ExecutorBinding(s)", ErrCanonicalV19SessionConflict, sessionBindingID, openCount)
	}
	return nil
}

func canonicalV19SessionAcquireDigest(request CanonicalV19SessionAcquireRequest) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:session-acquire-request:v1")
	writeCanonicalV19DigestField(hash, "project_id", request.ProjectID)
	writeCanonicalV19DigestField(hash, "task_id", request.TaskID)
	writeCanonicalV19DigestField(hash, "plan_id", request.PlanID)
	writeCanonicalV19DigestField(hash, "attempt_id", request.AttemptID)
	writeCanonicalV19DigestField(hash, "worktree_binding_id", request.WorktreeBindingID)
	writeCanonicalV19DigestField(hash, "binding_id", request.BindingID)
	writeCanonicalV19DigestField(hash, "adapter_ref", request.AdapterRef)
	writeCanonicalV19DigestField(hash, "requested_provider_session_key", request.RequestedProviderSessionKey)
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalV19SessionReleaseDigest(request CanonicalV19SessionReleaseRequest) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:session-release-request:v1")
	writeCanonicalV19DigestField(hash, "project_id", request.ProjectID)
	writeCanonicalV19DigestField(hash, "task_id", request.TaskID)
	writeCanonicalV19DigestField(hash, "plan_id", request.PlanID)
	writeCanonicalV19DigestField(hash, "attempt_id", request.AttemptID)
	writeCanonicalV19DigestField(hash, "worktree_binding_id", request.WorktreeBindingID)
	writeCanonicalV19DigestField(hash, "session_binding_id", request.SessionBindingID)
	writeCanonicalV19DigestField(hash, "adapter_ref", request.AdapterRef)
	writeCanonicalV19DigestField(hash, "expected_provider_session_key", request.ExpectedProviderSessionKey)
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalV19SessionNonsuccessTransitionAllowed(from, to string) bool {
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

func canonicalV19SessionSuccessTransitionAllowed(from string) bool {
	return from == "prepared" || from == "submitted" || from == "uncertain"
}

func transitionCanonicalV19SessionOperation(
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
		return canonicalV19SessionConstraintError(operation, "transition exact operation", operationID, err)
	}
	return requireCanonicalV19SessionOneChanged(result, operation, operationID)
}

func requireCanonicalV19SessionOneChanged(result sql.Result, operation, operationID string) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return canonicalV19SessionWriteError(operation, "count exact state transition", err)
	}
	if changed != 1 {
		return fmt.Errorf("%s canonical v19 Session: %w: operation %q changed %d rows, want 1", operation, ErrCanonicalV19SessionTransition, operationID, changed)
	}
	return nil
}

func canonicalV19SessionConstraintError(operation, action, operationID string, err error) error {
	if isSQLiteConstraint(err) {
		return fmt.Errorf("%s canonical v19 Session: %w: operation %q", operation, ErrCanonicalV19SessionConflict, operationID)
	}
	return canonicalV19SessionWriteError(operation, action, err)
}

func canonicalV19SessionWriteError(operation, action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("%s canonical v19 Session: %s: %w", operation, action, ErrContention)
	}
	return fmt.Errorf("%s canonical v19 Session: %s: %w", operation, action, err)
}
