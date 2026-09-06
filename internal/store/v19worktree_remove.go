package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrCanonicalV19WorktreeRemoveConflict marks an exact operation, binding,
// dependency, scope, or relational constraint conflict. Callers must never
// retarget or weaken cleanup safety.
var ErrCanonicalV19WorktreeRemoveConflict = errors.New("canonical v19 worktree remove write conflict")

// ErrCanonicalV19WorktreeRemoveNotCurrent marks a WorktreeRemove whose exact
// persisted WorktreeBinding ownership is no longer open/current for removal.
var ErrCanonicalV19WorktreeRemoveNotCurrent = errors.New("canonical v19 worktree remove is not current")

// ErrCanonicalV19WorktreeRemoveTransition marks an illegal or stale exact
// external-operation state transition.
var ErrCanonicalV19WorktreeRemoveTransition = errors.New("canonical v19 worktree remove transition conflict")

// CanonicalV19WorktreeRemovePrepareInput identifies one fresh logical native
// Git WorktreeRemove request.
type CanonicalV19WorktreeRemovePrepareInput struct {
	OperationID  string
	OperationKey string
	AttemptID    string
	BindingID    string
	CreatedAt    string
}

// CanonicalV19WorktreeRemoveRequest is the exact immutable remove request.
// Exact identity fields are copied from the immutable WorktreeBinding.
type CanonicalV19WorktreeRemoveRequest struct {
	OperationID                    string
	OperationKey                   string
	RequestDigest                  string
	ProjectID                      string
	TaskID                         string
	PlanID                         string
	AttemptID                      string
	WorkspaceBindingID             string
	BindingID                      string
	RepositoryLocator              string
	Path                           string
	BasisRevision                  string
	ExpectedPhysicalIdentityDigest string
	ExpectedCommonGitDir           string
	ExpectedPrivateGitDir          string
	ExpectedLockReason             string
	ExpectedHeadRevision           string
	CreatedAt                      string
}

// CanonicalV19WorktreeRemoveTransitionInput records typed evidence for a
// nonsuccess WorktreeRemove state transition.
type CanonicalV19WorktreeRemoveTransitionInput struct {
	OperationID    string
	State          string
	ObservedAt     string
	EvidenceDigest string
}

// CanonicalV19WorktreeRemovedEvidence is positive exact Git/filesystem
// postcondition evidence that the exact linked worktree registration and
// physical resource are absent.
type CanonicalV19WorktreeRemovedEvidence struct {
	OperationID    string
	RemovedAt      string
	EvidenceDigest string
}

type canonicalV19WorktreeRemoveCurrent struct {
	Request        CanonicalV19WorktreeRemoveRequest
	State          string
	StateChangedAt string
}

// PrepareCanonicalV19WorktreeRemove records a prepared native Git remove request
// plus exact worktree/workspace claims. Terminal ancestors are allowed, but the
// exact WorktreeBinding must be unreleased with no open SessionBinding.
func PrepareCanonicalV19WorktreeRemove(
	ctx context.Context,
	homeDir string,
	input CanonicalV19WorktreeRemovePrepareInput,
) (CanonicalV19WorktreeRemoveRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19WorktreeRemovePrepareInput(input); err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, canonicalV19WorktreeRemoveWriteError("prepare", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, fmt.Errorf("prepare canonical v19 WorktreeRemove: %w", err)
	}
	request, err := buildCanonicalV19WorktreeRemoveRequest(ctx, tx, input)
	if err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, fmt.Errorf("prepare canonical v19 WorktreeRemove: %w", err)
	}
	if err := requireCanonicalV19WorktreeRemoveNoOpenSession(ctx, tx, request.BindingID); err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, err
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO external_operation(
		id,kind,adapter_ref,operation_key,request_digest,project_id,task_id,plan_id,attempt_id,
		primary_scope_kind,primary_scope_key,state,created_at,state_changed_at,state_evidence_digest,
		submitted_at,finalized_at
	) VALUES(?,'worktree-remove',?,?,?,?,?,?,?,'worktree',?,'prepared',?,?,'','','')`,
		request.OperationID, canonicalV19GitWorktreeAdapterRef, request.OperationKey, request.RequestDigest,
		request.ProjectID, request.TaskID, request.PlanID, request.AttemptID, request.BindingID,
		request.CreatedAt, request.CreatedAt); err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, canonicalV19WorktreeRemoveConstraintError("prepare", "insert external operation", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO worktree_remove_operation(
		operation_id,binding_id,expected_physical_identity_digest,expected_common_git_dir,
		expected_private_git_dir,expected_lock_reason,expected_head_revision
	) VALUES(?,?,?,?,?,?,?)`, request.OperationID, request.BindingID,
		request.ExpectedPhysicalIdentityDigest, request.ExpectedCommonGitDir,
		request.ExpectedPrivateGitDir, request.ExpectedLockReason, request.ExpectedHeadRevision); err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, canonicalV19WorktreeRemoveConstraintError("prepare", "insert typed WorktreeRemove", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_scope_claim(operation_id,scope_kind,scope_key,created_at)
		VALUES(?,'worktree',?,?)`, request.OperationID, request.BindingID, request.CreatedAt); err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, canonicalV19WorktreeRemoveConstraintError("prepare", "claim exact worktree scope", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_scope_claim(operation_id,scope_kind,scope_key,created_at)
		VALUES(?,'workspace',?,?)`, request.OperationID, request.WorkspaceBindingID, request.CreatedAt); err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, canonicalV19WorktreeRemoveConstraintError("prepare", "claim exact workspace scope", request.OperationID, err)
	}

	if err := tx.Commit(); err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, canonicalV19WorktreeRemoveWriteError("prepare", "commit writer", err)
	}
	committed = true
	return request, nil
}

// SubmitCanonicalV19WorktreeRemove durably authorizes the exact prepared request.
// The caller must freshly prove native identity, dependency, preservation, and
// registration preconditions; external mutation may start only after success.
func SubmitCanonicalV19WorktreeRemove(
	ctx context.Context,
	homeDir string,
	operationID string,
	submittedAt string,
	evidenceDigest string,
) (CanonicalV19WorktreeRemoveRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" || submittedAt == "" || evidenceDigest == "" {
		return CanonicalV19WorktreeRemoveRequest{}, fmt.Errorf("submit canonical v19 WorktreeRemove: operation ID, submitted_at, and evidence digest are required")
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, canonicalV19WorktreeRemoveWriteError("submit", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, fmt.Errorf("submit canonical v19 WorktreeRemove: %w", err)
	}
	current, err := loadCanonicalV19WorktreeRemoveCurrent(ctx, tx, operationID)
	if err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, fmt.Errorf("submit canonical v19 WorktreeRemove: %w", err)
	}
	if err := requireCanonicalV19WorktreeRemoveNoOpenSession(ctx, tx, current.Request.BindingID); err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, err
	}
	if current.State != "prepared" || current.StateChangedAt == submittedAt {
		return CanonicalV19WorktreeRemoveRequest{}, fmt.Errorf("submit canonical v19 WorktreeRemove: %w: operation %q is %q", ErrCanonicalV19WorktreeRemoveTransition, operationID, current.State)
	}

	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='submitted',state_changed_at=?,state_evidence_digest=?,submitted_at=?
		WHERE id=? AND state='prepared'`, submittedAt, evidenceDigest, submittedAt, operationID)
	if err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, canonicalV19WorktreeRemoveConstraintError("submit", "authorize exact operation", operationID, err)
	}
	if err := requireCanonicalV19WorktreeRemoveOneChanged(result, "submit", operationID); err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, err
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, canonicalV19WorktreeRemoveWriteError("submit", "commit writer", err)
	}
	committed = true
	return current.Request, nil
}

// ClassifyCanonicalV19WorktreeRemove records exact nonsuccess evidence.
// Success uses CompleteCanonicalV19WorktreeRemove so operation success and the
// immutable WorktreeBinding release relation are committed atomically.
func ClassifyCanonicalV19WorktreeRemove(
	ctx context.Context,
	homeDir string,
	input CanonicalV19WorktreeRemoveTransitionInput,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19WorktreeRemoveTransitionInput(input); err != nil {
		return err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19WorktreeRemoveWriteError("classify", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("classify canonical v19 WorktreeRemove: %w", err)
	}
	current, err := loadCanonicalV19WorktreeRemoveCurrent(ctx, tx, input.OperationID)
	if err != nil {
		return fmt.Errorf("classify canonical v19 WorktreeRemove: %w", err)
	}
	if !canonicalV19WorktreeRemoveTransitionAllowed(current.State, input.State) || current.StateChangedAt == input.ObservedAt {
		return fmt.Errorf("classify canonical v19 WorktreeRemove: %w: %s -> %s", ErrCanonicalV19WorktreeRemoveTransition, current.State, input.State)
	}

	query := `UPDATE external_operation SET state=?,state_changed_at=?,state_evidence_digest=?`
	args := []any{input.State, input.ObservedAt, input.EvidenceDigest}
	if input.State == "rejected" || input.State == "no-effect" {
		query += `,finalized_at=?`
		args = append(args, input.ObservedAt)
	}
	query += ` WHERE id=? AND state=?`
	args = append(args, input.OperationID, current.State)
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return canonicalV19WorktreeRemoveConstraintError("classify", "transition exact operation", input.OperationID, err)
	}
	if err := requireCanonicalV19WorktreeRemoveOneChanged(result, "classify", input.OperationID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19WorktreeRemoveWriteError("classify", "commit writer", err)
	}
	committed = true
	return nil
}

// CompleteCanonicalV19WorktreeRemove atomically marks the exact remove
// succeeded and inserts the immutable WorktreeBinding release relation from
// positive exact Git/filesystem absence evidence.
func CompleteCanonicalV19WorktreeRemove(
	ctx context.Context,
	homeDir string,
	evidence CanonicalV19WorktreeRemovedEvidence,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19WorktreeRemovedEvidence(evidence); err != nil {
		return err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19WorktreeRemoveWriteError("complete", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("complete canonical v19 WorktreeRemove: %w", err)
	}
	current, err := loadCanonicalV19WorktreeRemoveCurrent(ctx, tx, evidence.OperationID)
	if err != nil {
		return fmt.Errorf("complete canonical v19 WorktreeRemove: %w", err)
	}
	if err := requireCanonicalV19WorktreeRemoveNoOpenSession(ctx, tx, current.Request.BindingID); err != nil {
		return err
	}
	if current.State != "prepared" && current.State != "submitted" && current.State != "uncertain" {
		return fmt.Errorf("complete canonical v19 WorktreeRemove: %w: operation %q is %q", ErrCanonicalV19WorktreeRemoveTransition, evidence.OperationID, current.State)
	}
	if current.StateChangedAt == evidence.RemovedAt {
		return fmt.Errorf("complete canonical v19 WorktreeRemove: %w: state timestamp did not advance", ErrCanonicalV19WorktreeRemoveTransition)
	}

	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='succeeded',state_changed_at=?,state_evidence_digest=?,finalized_at=?
		WHERE id=? AND state=?`, evidence.RemovedAt, evidence.EvidenceDigest,
		evidence.RemovedAt, evidence.OperationID, current.State)
	if err != nil {
		return canonicalV19WorktreeRemoveConstraintError("complete", "mark exact remove succeeded", evidence.OperationID, err)
	}
	if err := requireCanonicalV19WorktreeRemoveOneChanged(result, "complete", evidence.OperationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO worktree_binding_release(
		binding_id,remove_operation_id,released_at,evidence_digest
	) VALUES(?,?,?,?)`, current.Request.BindingID, evidence.OperationID,
		evidence.RemovedAt, evidence.EvidenceDigest); err != nil {
		return canonicalV19WorktreeRemoveConstraintError("complete", "insert exact WorktreeBinding release", evidence.OperationID, err)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19WorktreeRemoveWriteError("complete", "commit writer", err)
	}
	committed = true
	return nil
}

func validateCanonicalV19WorktreeRemovePrepareInput(input CanonicalV19WorktreeRemovePrepareInput) error {
	for name, value := range map[string]string{
		"operation ID":  input.OperationID,
		"operation key": input.OperationKey,
		"Attempt ID":    input.AttemptID,
		"binding ID":    input.BindingID,
		"created_at":    input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("prepare canonical v19 WorktreeRemove: %s is empty", name)
		}
	}
	return nil
}

func validateCanonicalV19WorktreeRemoveTransitionInput(input CanonicalV19WorktreeRemoveTransitionInput) error {
	for name, value := range map[string]string{
		"operation ID":    input.OperationID,
		"observed_at":     input.ObservedAt,
		"evidence digest": input.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("classify canonical v19 WorktreeRemove: %s is empty", name)
		}
	}
	switch input.State {
	case "uncertain", "rejected", "no-effect":
		return nil
	default:
		return fmt.Errorf("classify canonical v19 WorktreeRemove: state %q requires a different writer", input.State)
	}
}

func validateCanonicalV19WorktreeRemovedEvidence(evidence CanonicalV19WorktreeRemovedEvidence) error {
	for name, value := range map[string]string{
		"operation ID":    evidence.OperationID,
		"removed_at":      evidence.RemovedAt,
		"evidence digest": evidence.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("complete canonical v19 WorktreeRemove: %s is empty", name)
		}
	}
	return nil
}

func buildCanonicalV19WorktreeRemoveRequest(
	ctx context.Context,
	tx *sql.Tx,
	input CanonicalV19WorktreeRemovePrepareInput,
) (CanonicalV19WorktreeRemoveRequest, error) {
	request := CanonicalV19WorktreeRemoveRequest{
		OperationID:  input.OperationID,
		OperationKey: input.OperationKey,
		AttemptID:    input.AttemptID,
		BindingID:    input.BindingID,
		CreatedAt:    input.CreatedAt,
	}
	err := tx.QueryRowContext(ctx, `SELECT project_owner.id,t.id,p.id,w.id,w.repository_locator,
		b.path,b.basis_revision,b.physical_identity_digest,b.common_git_dir,b.private_git_dir,
		b.lock_reason,b.head_revision
		FROM attempt_worktree_binding b
		JOIN attempt a ON a.id=b.attempt_id
		JOIN plan p ON p.id=a.plan_id
		JOIN task t ON t.id=p.task_id
		JOIN project project_owner ON project_owner.id=t.project_id
		JOIN workspace_binding w ON w.id=p.workspace_binding_id AND w.project_id=t.project_id
		WHERE b.id=? AND b.attempt_id=?
		  AND NOT EXISTS (SELECT 1 FROM worktree_binding_release r WHERE r.binding_id=b.id)`,
		input.BindingID, input.AttemptID).Scan(
		&request.ProjectID, &request.TaskID, &request.PlanID, &request.WorkspaceBindingID,
		&request.RepositoryLocator, &request.Path, &request.BasisRevision,
		&request.ExpectedPhysicalIdentityDigest, &request.ExpectedCommonGitDir,
		&request.ExpectedPrivateGitDir, &request.ExpectedLockReason, &request.ExpectedHeadRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19WorktreeRemoveRequest{}, fmt.Errorf("%w: binding %q is not an unreleased exact WorktreeBinding of Attempt %q", ErrCanonicalV19WorktreeRemoveNotCurrent, input.BindingID, input.AttemptID)
	}
	if err != nil {
		return CanonicalV19WorktreeRemoveRequest{}, canonicalV19WorktreeRemoveWriteError("prepare", "read exact WorktreeBinding ownership", err)
	}
	request.RequestDigest = canonicalV19WorktreeRemoveDigest(request)
	return request, nil
}

func loadCanonicalV19WorktreeRemoveCurrent(
	ctx context.Context,
	tx *sql.Tx,
	operationID string,
) (canonicalV19WorktreeRemoveCurrent, error) {
	var current canonicalV19WorktreeRemoveCurrent
	request := &current.Request
	err := tx.QueryRowContext(ctx, `SELECT o.state,o.state_changed_at,o.operation_key,o.request_digest,
		o.project_id,o.task_id,o.plan_id,o.attempt_id,w.id,w.repository_locator,
		b.id,b.path,b.basis_revision,wr.expected_physical_identity_digest,
		wr.expected_common_git_dir,wr.expected_private_git_dir,wr.expected_lock_reason,
		wr.expected_head_revision,o.created_at
		FROM external_operation o
		JOIN worktree_remove_operation wr ON wr.operation_id=o.id
		JOIN attempt_worktree_binding b ON b.id=wr.binding_id AND b.attempt_id=o.attempt_id
		JOIN attempt a ON a.id=b.attempt_id
		JOIN plan p ON p.id=a.plan_id AND p.id=o.plan_id
		JOIN task t ON t.id=p.task_id AND t.id=o.task_id
		JOIN project project_owner ON project_owner.id=t.project_id AND project_owner.id=o.project_id
		JOIN workspace_binding w ON w.id=p.workspace_binding_id AND w.project_id=t.project_id
		JOIN operation_scope_claim worktree_claim ON worktree_claim.operation_id=o.id
		  AND worktree_claim.scope_kind='worktree' AND worktree_claim.scope_key=b.id
		JOIN operation_scope_claim workspace_claim ON workspace_claim.operation_id=o.id
		  AND workspace_claim.scope_kind='workspace' AND workspace_claim.scope_key=w.id
		WHERE o.id=? AND o.kind='worktree-remove' AND o.adapter_ref=?
		  AND o.primary_scope_kind='worktree' AND o.primary_scope_key=b.id
		  AND wr.expected_physical_identity_digest=b.physical_identity_digest
		  AND wr.expected_common_git_dir=b.common_git_dir
		  AND wr.expected_private_git_dir=b.private_git_dir
		  AND wr.expected_lock_reason=b.lock_reason
		  AND wr.expected_head_revision=b.head_revision
		  AND NOT EXISTS (SELECT 1 FROM worktree_binding_release r WHERE r.binding_id=b.id)`,
		operationID, canonicalV19GitWorktreeAdapterRef).Scan(
		&current.State, &current.StateChangedAt, &request.OperationKey, &request.RequestDigest,
		&request.ProjectID, &request.TaskID, &request.PlanID, &request.AttemptID,
		&request.WorkspaceBindingID, &request.RepositoryLocator, &request.BindingID,
		&request.Path, &request.BasisRevision, &request.ExpectedPhysicalIdentityDigest,
		&request.ExpectedCommonGitDir, &request.ExpectedPrivateGitDir,
		&request.ExpectedLockReason, &request.ExpectedHeadRevision, &request.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return canonicalV19WorktreeRemoveCurrent{}, fmt.Errorf("%w: operation %q lacks exact unreleased WorktreeRemove request/ownership/claims", ErrCanonicalV19WorktreeRemoveNotCurrent, operationID)
	}
	if err != nil {
		return canonicalV19WorktreeRemoveCurrent{}, canonicalV19WorktreeRemoveWriteError("currentness", "read exact WorktreeRemove", err)
	}
	request.OperationID = operationID
	if canonicalV19WorktreeRemoveDigest(*request) != request.RequestDigest {
		return canonicalV19WorktreeRemoveCurrent{}, fmt.Errorf("%w: operation %q request digest does not match exact persisted request", ErrCanonicalV19WorktreeRemoveNotCurrent, operationID)
	}
	return current, nil
}

func requireCanonicalV19WorktreeRemoveNoOpenSession(ctx context.Context, tx *sql.Tx, bindingID string) error {
	var openCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*)
		FROM session_binding s
		WHERE s.worktree_binding_id=?
		  AND NOT EXISTS (
			SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id
		  )`, bindingID).Scan(&openCount); err != nil {
		return canonicalV19WorktreeRemoveWriteError("currentness", "read dependent SessionBinding", err)
	}
	if openCount != 0 {
		return fmt.Errorf("canonical v19 WorktreeRemove: %w: binding %q has %d open dependent SessionBinding(s)", ErrCanonicalV19WorktreeRemoveConflict, bindingID, openCount)
	}
	return nil
}

func canonicalV19WorktreeRemoveTransitionAllowed(from, to string) bool {
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

func canonicalV19WorktreeRemoveDigest(request CanonicalV19WorktreeRemoveRequest) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:worktree-remove-request:v1")
	writeCanonicalV19DigestField(hash, "project_id", request.ProjectID)
	writeCanonicalV19DigestField(hash, "task_id", request.TaskID)
	writeCanonicalV19DigestField(hash, "plan_id", request.PlanID)
	writeCanonicalV19DigestField(hash, "attempt_id", request.AttemptID)
	writeCanonicalV19DigestField(hash, "workspace_binding_id", request.WorkspaceBindingID)
	writeCanonicalV19DigestField(hash, "binding_id", request.BindingID)
	writeCanonicalV19DigestField(hash, "repository_locator", request.RepositoryLocator)
	writeCanonicalV19DigestField(hash, "path", request.Path)
	writeCanonicalV19DigestField(hash, "basis_revision", request.BasisRevision)
	writeCanonicalV19DigestField(hash, "expected_physical_identity_digest", request.ExpectedPhysicalIdentityDigest)
	writeCanonicalV19DigestField(hash, "expected_common_git_dir", request.ExpectedCommonGitDir)
	writeCanonicalV19DigestField(hash, "expected_private_git_dir", request.ExpectedPrivateGitDir)
	writeCanonicalV19DigestField(hash, "expected_lock_reason", request.ExpectedLockReason)
	writeCanonicalV19DigestField(hash, "expected_head_revision", request.ExpectedHeadRevision)
	return hex.EncodeToString(hash.Sum(nil))
}

func requireCanonicalV19WorktreeRemoveOneChanged(result sql.Result, operation, operationID string) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return canonicalV19WorktreeRemoveWriteError(operation, "count exact state transition", err)
	}
	if changed != 1 {
		return fmt.Errorf("%s canonical v19 WorktreeRemove: %w: operation %q changed %d rows, want 1", operation, ErrCanonicalV19WorktreeRemoveTransition, operationID, changed)
	}
	return nil
}

func canonicalV19WorktreeRemoveConstraintError(operation, action, operationID string, err error) error {
	if isSQLiteConstraint(err) {
		return fmt.Errorf("%s canonical v19 WorktreeRemove: %w: operation %q", operation, ErrCanonicalV19WorktreeRemoveConflict, operationID)
	}
	return canonicalV19WorktreeRemoveWriteError(operation, action, err)
}

func canonicalV19WorktreeRemoveWriteError(operation, action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("%s canonical v19 WorktreeRemove: %s: %w", operation, action, ErrContention)
	}
	return fmt.Errorf("%s canonical v19 WorktreeRemove: %s: %w", operation, action, err)
}
