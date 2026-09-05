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

const canonicalV19GitWorktreeAdapterRef = "builtin/git-worktree"

// ErrCanonicalV19WorktreeCreateConflict marks an exact operation, binding,
// scope, or relational constraint conflict. Callers must never retarget.
var ErrCanonicalV19WorktreeCreateConflict = errors.New("canonical v19 worktree create write conflict")

// ErrCanonicalV19WorktreeCreateNotCurrent marks a WorktreeCreate whose exact
// Project/Task/Plan/Attempt/WorkspaceBinding lineage is no longer current.
var ErrCanonicalV19WorktreeCreateNotCurrent = errors.New("canonical v19 worktree create is not current")

// ErrCanonicalV19WorktreeCreateTransition marks an illegal or stale exact
// external-operation state transition.
var ErrCanonicalV19WorktreeCreateTransition = errors.New("canonical v19 worktree create transition conflict")

// CanonicalV19WorktreeCreatePrepareInput identifies one fresh logical native
// Git WorktreeCreate request. Repository/basis/lock evidence is derived from
// the exact current Plan and its captured WorkspaceBinding.
type CanonicalV19WorktreeCreatePrepareInput struct {
	OperationID   string
	OperationKey  string
	AttemptID     string
	BindingID     string
	RequestedPath string
	CreatedAt     string
}

// CanonicalV19WorktreeCreateRequest is the exact immutable request persisted
// before any external Git/filesystem mutation is authorized.
type CanonicalV19WorktreeCreateRequest struct {
	OperationID         string
	OperationKey        string
	RequestDigest       string
	ProjectID           string
	TaskID              string
	PlanID              string
	AttemptID           string
	WorkspaceBindingID  string
	BindingID           string
	RepositoryLocator   string
	RequestedPath       string
	BasisRevision       string
	ExpectedCommonGitDir string
	ExpectedLockReason  string
	CreatedAt           string
}

// CanonicalV19WorktreeCreateTransitionInput records typed evidence for a
// non-success WorktreeCreate state transition.
type CanonicalV19WorktreeCreateTransitionInput struct {
	OperationID   string
	State         string
	ObservedAt    string
	EvidenceDigest string
}

// CanonicalV19WorktreeBindingEvidence is positive exact native Git/filesystem
// evidence accepted only while the same WorktreeCreate remains current.
type CanonicalV19WorktreeBindingEvidence struct {
	OperationID            string
	Path                   string
	CommonGitDir           string
	PrivateGitDir          string
	LockReason             string
	BasisRevision          string
	HeadRevision           string
	PhysicalIdentityDigest string
	EstablishedAt          string
	EvidenceDigest         string
}

type canonicalV19WorktreeCreateCurrent struct {
	Request        CanonicalV19WorktreeCreateRequest
	State          string
	StateChangedAt string
}

// PrepareCanonicalV19WorktreeCreate durably records one prepared native Git
// request plus its exact worktree and workspace scope claims. It performs no
// external mutation.
func PrepareCanonicalV19WorktreeCreate(
	ctx context.Context,
	homeDir string,
	input CanonicalV19WorktreeCreatePrepareInput,
) (CanonicalV19WorktreeCreateRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19WorktreeCreatePrepareInput(input); err != nil {
		return CanonicalV19WorktreeCreateRequest{}, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19WorktreeCreateRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19WorktreeCreateRequest{}, canonicalV19WorktreeCreateWriteError("prepare", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19WorktreeCreateRequest{}, fmt.Errorf("prepare canonical v19 WorktreeCreate: %w", err)
	}
	request, err := buildCanonicalV19WorktreeCreateRequest(ctx, tx, input)
	if err != nil {
		return CanonicalV19WorktreeCreateRequest{}, fmt.Errorf("prepare canonical v19 WorktreeCreate: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO external_operation(
		id,kind,adapter_ref,operation_key,request_digest,project_id,task_id,plan_id,attempt_id,
		primary_scope_kind,primary_scope_key,state,created_at,state_changed_at,state_evidence_digest,
		submitted_at,finalized_at
	) VALUES(?,'worktree-create',?,?,?,?,?,?,?,'worktree',?,'prepared',? ,? ,'','','')`,
		request.OperationID, canonicalV19GitWorktreeAdapterRef, request.OperationKey, request.RequestDigest,
		request.ProjectID, request.TaskID, request.PlanID, request.AttemptID, request.BindingID,
		request.CreatedAt, request.CreatedAt); err != nil {
		return CanonicalV19WorktreeCreateRequest{}, canonicalV19WorktreeCreateConstraintError("prepare", "insert external operation", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO worktree_create_operation(
		operation_id,attempt_id,binding_id,requested_path,basis_revision,expected_common_git_dir,expected_lock_reason
	) VALUES(?,?,?,?,?,?,?)`, request.OperationID, request.AttemptID, request.BindingID,
		request.RequestedPath, request.BasisRevision, request.ExpectedCommonGitDir, request.ExpectedLockReason); err != nil {
		return CanonicalV19WorktreeCreateRequest{}, canonicalV19WorktreeCreateConstraintError("prepare", "insert typed WorktreeCreate", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_scope_claim(operation_id,scope_kind,scope_key,created_at)
		VALUES(?,'worktree',?,?)`, request.OperationID, request.BindingID, request.CreatedAt); err != nil {
		return CanonicalV19WorktreeCreateRequest{}, canonicalV19WorktreeCreateConstraintError("prepare", "claim exact worktree scope", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_scope_claim(operation_id,scope_kind,scope_key,created_at)
		VALUES(?,'workspace',?,?)`, request.OperationID, request.WorkspaceBindingID, request.CreatedAt); err != nil {
		return CanonicalV19WorktreeCreateRequest{}, canonicalV19WorktreeCreateConstraintError("prepare", "claim exact workspace scope", request.OperationID, err)
	}

	if err := tx.Commit(); err != nil {
		return CanonicalV19WorktreeCreateRequest{}, canonicalV19WorktreeCreateWriteError("prepare", "commit writer", err)
	}
	committed = true
	return request, nil
}

// SubmitCanonicalV19WorktreeCreate commits durable mutation authorization for
// the exact prepared request. External Git/filesystem mutation may start only
// after this function returns successfully.
func SubmitCanonicalV19WorktreeCreate(
	ctx context.Context,
	homeDir string,
	operationID string,
	submittedAt string,
	evidenceDigest string,
) (CanonicalV19WorktreeCreateRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" || submittedAt == "" || evidenceDigest == "" {
		return CanonicalV19WorktreeCreateRequest{}, fmt.Errorf("submit canonical v19 WorktreeCreate: operation ID, submitted_at, and evidence digest are required")
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19WorktreeCreateRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19WorktreeCreateRequest{}, canonicalV19WorktreeCreateWriteError("submit", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19WorktreeCreateRequest{}, fmt.Errorf("submit canonical v19 WorktreeCreate: %w", err)
	}
	current, err := loadCanonicalV19WorktreeCreateCurrent(ctx, tx, operationID)
	if err != nil {
		return CanonicalV19WorktreeCreateRequest{}, fmt.Errorf("submit canonical v19 WorktreeCreate: %w", err)
	}
	if current.State != "prepared" || current.StateChangedAt == submittedAt {
		return CanonicalV19WorktreeCreateRequest{}, fmt.Errorf("submit canonical v19 WorktreeCreate: %w: operation %q is %q", ErrCanonicalV19WorktreeCreateTransition, operationID, current.State)
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='submitted',state_changed_at=?,state_evidence_digest=?,submitted_at=?
		WHERE id=? AND state='prepared'`, submittedAt, evidenceDigest, submittedAt, operationID)
	if err != nil {
		return CanonicalV19WorktreeCreateRequest{}, canonicalV19WorktreeCreateConstraintError("submit", "authorize exact operation", operationID, err)
	}
	if err := requireCanonicalV19OneChanged(result, "submit", operationID); err != nil {
		return CanonicalV19WorktreeCreateRequest{}, err
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19WorktreeCreateRequest{}, canonicalV19WorktreeCreateWriteError("submit", "commit writer", err)
	}
	committed = true
	return current.Request, nil
}

// ClassifyCanonicalV19WorktreeCreate records uncertain or terminal nonsuccess
// evidence for the exact current WorktreeCreate. Success requires the separate
// binding-establishment writer so operation success and binding insertion are
// atomic.
func ClassifyCanonicalV19WorktreeCreate(
	ctx context.Context,
	homeDir string,
	input CanonicalV19WorktreeCreateTransitionInput,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19WorktreeCreateTransitionInput(input); err != nil {
		return err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19WorktreeCreateWriteError("classify", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("classify canonical v19 WorktreeCreate: %w", err)
	}
	current, err := loadCanonicalV19WorktreeCreateCurrent(ctx, tx, input.OperationID)
	if err != nil {
		return fmt.Errorf("classify canonical v19 WorktreeCreate: %w", err)
	}
	if !canonicalV19WorktreeCreateTransitionAllowed(current.State, input.State) || current.StateChangedAt == input.ObservedAt {
		return fmt.Errorf("classify canonical v19 WorktreeCreate: %w: %s -> %s", ErrCanonicalV19WorktreeCreateTransition, current.State, input.State)
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
		return canonicalV19WorktreeCreateConstraintError("classify", "transition exact operation", input.OperationID, err)
	}
	if err := requireCanonicalV19OneChanged(result, "classify", input.OperationID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19WorktreeCreateWriteError("classify", "commit writer", err)
	}
	committed = true
	return nil
}

// EstablishCanonicalV19WorktreeBinding atomically marks the exact current
// WorktreeCreate succeeded and inserts its immutable WorktreeBinding from
// positive exact native Git/filesystem observation evidence.
func EstablishCanonicalV19WorktreeBinding(
	ctx context.Context,
	homeDir string,
	evidence CanonicalV19WorktreeBindingEvidence,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19WorktreeBindingEvidence(evidence); err != nil {
		return err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19WorktreeCreateWriteError("establish", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("establish canonical v19 WorktreeBinding: %w", err)
	}
	current, err := loadCanonicalV19WorktreeCreateCurrent(ctx, tx, evidence.OperationID)
	if err != nil {
		return fmt.Errorf("establish canonical v19 WorktreeBinding: %w", err)
	}
	if current.State != "prepared" && current.State != "submitted" && current.State != "uncertain" {
		return fmt.Errorf("establish canonical v19 WorktreeBinding: %w: operation %q is %q", ErrCanonicalV19WorktreeCreateTransition, evidence.OperationID, current.State)
	}
	if current.StateChangedAt == evidence.EstablishedAt {
		return fmt.Errorf("establish canonical v19 WorktreeBinding: %w: state timestamp did not advance", ErrCanonicalV19WorktreeCreateTransition)
	}
	request := current.Request
	if evidence.Path != request.RequestedPath ||
		evidence.CommonGitDir != request.ExpectedCommonGitDir ||
		evidence.LockReason != request.ExpectedLockReason ||
		evidence.BasisRevision != request.BasisRevision ||
		evidence.HeadRevision != request.BasisRevision {
		return fmt.Errorf("establish canonical v19 WorktreeBinding: %w: positive observation does not match exact request", ErrCanonicalV19WorktreeCreateNotCurrent)
	}

	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='succeeded',state_changed_at=?,state_evidence_digest=?,finalized_at=?
		WHERE id=? AND state=?`, evidence.EstablishedAt, evidence.EvidenceDigest,
		evidence.EstablishedAt, evidence.OperationID, current.State)
	if err != nil {
		return canonicalV19WorktreeCreateConstraintError("establish", "mark exact create succeeded", evidence.OperationID, err)
	}
	if err := requireCanonicalV19OneChanged(result, "establish", evidence.OperationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO attempt_worktree_binding(
		id,attempt_id,create_operation_id,path,common_git_dir,private_git_dir,lock_reason,
		basis_revision,head_revision,physical_identity_digest,established_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, request.BindingID, request.AttemptID, request.OperationID,
		evidence.Path, evidence.CommonGitDir, evidence.PrivateGitDir, evidence.LockReason,
		evidence.BasisRevision, evidence.HeadRevision, evidence.PhysicalIdentityDigest, evidence.EstablishedAt); err != nil {
		return canonicalV19WorktreeCreateConstraintError("establish", "insert exact WorktreeBinding", evidence.OperationID, err)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19WorktreeCreateWriteError("establish", "commit writer", err)
	}
	committed = true
	return nil
}

func validateCanonicalV19WorktreeCreatePrepareInput(input CanonicalV19WorktreeCreatePrepareInput) error {
	for name, value := range map[string]string{
		"operation ID":   input.OperationID,
		"operation key":  input.OperationKey,
		"Attempt ID":     input.AttemptID,
		"binding ID":     input.BindingID,
		"requested path": input.RequestedPath,
		"created_at":     input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("prepare canonical v19 WorktreeCreate: %s is empty", name)
		}
	}
	return nil
}

func validateCanonicalV19WorktreeCreateTransitionInput(input CanonicalV19WorktreeCreateTransitionInput) error {
	for name, value := range map[string]string{
		"operation ID":    input.OperationID,
		"observed_at":     input.ObservedAt,
		"evidence digest": input.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("classify canonical v19 WorktreeCreate: %s is empty", name)
		}
	}
	switch input.State {
	case "uncertain", "rejected", "no-effect":
		return nil
	default:
		return fmt.Errorf("classify canonical v19 WorktreeCreate: state %q requires a different writer", input.State)
	}
}

func validateCanonicalV19WorktreeBindingEvidence(evidence CanonicalV19WorktreeBindingEvidence) error {
	for name, value := range map[string]string{
		"operation ID":             evidence.OperationID,
		"path":                     evidence.Path,
		"common Git directory":     evidence.CommonGitDir,
		"private Git directory":    evidence.PrivateGitDir,
		"lock reason":              evidence.LockReason,
		"basis revision":           evidence.BasisRevision,
		"HEAD revision":            evidence.HeadRevision,
		"physical identity digest": evidence.PhysicalIdentityDigest,
		"established_at":           evidence.EstablishedAt,
		"evidence digest":          evidence.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("establish canonical v19 WorktreeBinding: %s is empty", name)
		}
	}
	return nil
}

func buildCanonicalV19WorktreeCreateRequest(
	ctx context.Context,
	tx *sql.Tx,
	input CanonicalV19WorktreeCreatePrepareInput,
) (CanonicalV19WorktreeCreateRequest, error) {
	request := CanonicalV19WorktreeCreateRequest{
		OperationID:   input.OperationID,
		OperationKey:  input.OperationKey,
		AttemptID:     input.AttemptID,
		BindingID:     input.BindingID,
		RequestedPath: input.RequestedPath,
		CreatedAt:     input.CreatedAt,
	}
	err := tx.QueryRowContext(ctx, `SELECT project_current.id,t.id,p.id,w.id,w.repository_locator,w.common_git_dir,w.revision
		FROM attempt a
		JOIN plan p ON p.id=a.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.retired_at=''
		JOIN workspace_binding w ON w.id=p.workspace_binding_id AND w.project_id=t.project_id AND w.superseded_at=''
		WHERE a.id=? AND a.lifecycle='active' AND a.terminal_at=''`, input.AttemptID).Scan(
		&request.ProjectID, &request.TaskID, &request.PlanID, &request.WorkspaceBindingID,
		&request.RepositoryLocator, &request.ExpectedCommonGitDir, &request.BasisRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19WorktreeCreateRequest{}, fmt.Errorf("%w: Attempt %q lacks exact active lineage/current WorkspaceBinding", ErrCanonicalV19WorktreeCreateNotCurrent, input.AttemptID)
	}
	if err != nil {
		return CanonicalV19WorktreeCreateRequest{}, canonicalV19WorktreeCreateWriteError("prepare", "read exact current lineage", err)
	}
	request.ExpectedLockReason = "hand:v1:" + request.BindingID
	request.RequestDigest = canonicalV19WorktreeCreateDigest(request)
	return request, nil
}

func loadCanonicalV19WorktreeCreateCurrent(
	ctx context.Context,
	tx *sql.Tx,
	operationID string,
) (canonicalV19WorktreeCreateCurrent, error) {
	var current canonicalV19WorktreeCreateCurrent
	request := &current.Request
	err := tx.QueryRowContext(ctx, `SELECT o.state,o.state_changed_at,o.operation_key,o.request_digest,
		o.project_id,o.task_id,o.plan_id,o.attempt_id,w.id,w.repository_locator,
		wc.binding_id,wc.requested_path,wc.basis_revision,wc.expected_common_git_dir,wc.expected_lock_reason,o.created_at
		FROM external_operation o
		JOIN worktree_create_operation wc ON wc.operation_id=o.id AND wc.attempt_id=o.attempt_id
		JOIN attempt a ON a.id=o.attempt_id AND a.lifecycle='active' AND a.terminal_at=''
		JOIN plan p ON p.id=a.plan_id AND p.id=o.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.id=o.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.id=o.project_id AND project_current.retired_at=''
		JOIN workspace_binding w ON w.id=p.workspace_binding_id AND w.project_id=t.project_id AND w.superseded_at=''
		JOIN operation_scope_claim worktree_claim ON worktree_claim.operation_id=o.id
		  AND worktree_claim.scope_kind='worktree' AND worktree_claim.scope_key=wc.binding_id
		JOIN operation_scope_claim workspace_claim ON workspace_claim.operation_id=o.id
		  AND workspace_claim.scope_kind='workspace' AND workspace_claim.scope_key=w.id
		WHERE o.id=? AND o.kind='worktree-create' AND o.adapter_ref=?
		  AND o.primary_scope_kind='worktree' AND o.primary_scope_key=wc.binding_id
		  AND wc.basis_revision=w.revision
		  AND wc.expected_common_git_dir=w.common_git_dir
		  AND wc.expected_lock_reason=('hand:v1:' || wc.binding_id)`, operationID, canonicalV19GitWorktreeAdapterRef).Scan(
		&current.State, &current.StateChangedAt, &request.OperationKey, &request.RequestDigest,
		&request.ProjectID, &request.TaskID, &request.PlanID, &request.AttemptID,
		&request.WorkspaceBindingID, &request.RepositoryLocator, &request.BindingID,
		&request.RequestedPath, &request.BasisRevision, &request.ExpectedCommonGitDir,
		&request.ExpectedLockReason, &request.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return canonicalV19WorktreeCreateCurrent{}, fmt.Errorf("%w: operation %q lacks exact current request/lineage/claims", ErrCanonicalV19WorktreeCreateNotCurrent, operationID)
	}
	if err != nil {
		return canonicalV19WorktreeCreateCurrent{}, canonicalV19WorktreeCreateWriteError("currentness", "read exact WorktreeCreate", err)
	}
	request.OperationID = operationID
	if canonicalV19WorktreeCreateDigest(*request) != request.RequestDigest {
		return canonicalV19WorktreeCreateCurrent{}, fmt.Errorf("%w: operation %q request digest does not match exact persisted request", ErrCanonicalV19WorktreeCreateNotCurrent, operationID)
	}
	return current, nil
}

func canonicalV19WorktreeCreateTransitionAllowed(from, to string) bool {
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

func canonicalV19WorktreeCreateDigest(request CanonicalV19WorktreeCreateRequest) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:worktree-create-request:v1")
	writeCanonicalV19DigestField(hash, "project_id", request.ProjectID)
	writeCanonicalV19DigestField(hash, "task_id", request.TaskID)
	writeCanonicalV19DigestField(hash, "plan_id", request.PlanID)
	writeCanonicalV19DigestField(hash, "attempt_id", request.AttemptID)
	writeCanonicalV19DigestField(hash, "workspace_binding_id", request.WorkspaceBindingID)
	writeCanonicalV19DigestField(hash, "binding_id", request.BindingID)
	writeCanonicalV19DigestField(hash, "repository_locator", request.RepositoryLocator)
	writeCanonicalV19DigestField(hash, "requested_path", request.RequestedPath)
	writeCanonicalV19DigestField(hash, "basis_revision", request.BasisRevision)
	writeCanonicalV19DigestField(hash, "expected_common_git_dir", request.ExpectedCommonGitDir)
	writeCanonicalV19DigestField(hash, "expected_lock_reason", request.ExpectedLockReason)
	return hex.EncodeToString(hash.Sum(nil))
}

type canonicalV19DigestWriter interface {
	Write([]byte) (int, error)
}

func writeCanonicalV19DigestField(writer canonicalV19DigestWriter, name, value string) {
	_, _ = writer.Write([]byte(strconv.Itoa(len(name))))
	_, _ = writer.Write([]byte(":"))
	_, _ = writer.Write([]byte(name))
	_, _ = writer.Write([]byte(strconv.Itoa(len(value))))
	_, _ = writer.Write([]byte(":"))
	_, _ = writer.Write([]byte(value))
}

func requireCanonicalV19OneChanged(result sql.Result, operation, operationID string) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return canonicalV19WorktreeCreateWriteError(operation, "count exact state transition", err)
	}
	if changed != 1 {
		return fmt.Errorf("%s canonical v19 WorktreeCreate: %w: operation %q changed %d rows, want 1", operation, ErrCanonicalV19WorktreeCreateTransition, operationID, changed)
	}
	return nil
}

func canonicalV19WorktreeCreateConstraintError(operation, action, operationID string, err error) error {
	if isSQLiteConstraint(err) {
		return fmt.Errorf("%s canonical v19 WorktreeCreate: %w: operation %q", operation, ErrCanonicalV19WorktreeCreateConflict, operationID)
	}
	return canonicalV19WorktreeCreateWriteError(operation, action, err)
}

func canonicalV19WorktreeCreateWriteError(operation, action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("%s canonical v19 WorktreeCreate: %s: %w", operation, action, ErrContention)
	}
	return fmt.Errorf("%s canonical v19 WorktreeCreate: %s: %w", operation, action, err)
}
