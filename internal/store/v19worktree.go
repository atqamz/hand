package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	handgit "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/gitworktree"
)

// ErrCanonicalV19WorktreeConflict marks an exact WorktreeCreate identity,
// scope, or binding constraint conflict. Callers must not retarget.
var ErrCanonicalV19WorktreeConflict = errors.New("canonical v19 WorktreeCreate conflict")

// ErrCanonicalV19WorktreeNotCurrent marks stale Attempt/Plan/Task/Project or
// WorkspaceBinding evidence. Callers must never retarget a successor.
var ErrCanonicalV19WorktreeNotCurrent = errors.New("canonical v19 WorktreeCreate is not current")

// ErrCanonicalV19WorktreeGitBasis marks a Project repository/basis that cannot
// be positively verified before native Git mutation.
var ErrCanonicalV19WorktreeGitBasis = errors.New("canonical v19 WorktreeCreate Git basis is not positively verified")

// ErrCanonicalV19WorktreeUnresolved marks a durable prepared/submitted/uncertain
// operation whose exact external postcondition is not yet classifiable.
var ErrCanonicalV19WorktreeUnresolved = errors.New("canonical v19 WorktreeCreate remains unresolved")

// ErrCanonicalV19WorktreeNotEstablished marks a terminal no-effect/rejected
// WorktreeCreate. The exact operation history remains durable.
var ErrCanonicalV19WorktreeNotEstablished = errors.New("canonical v19 WorktreeCreate did not establish a binding")

// CanonicalV19WorktreeCreateInput identifies one fresh logical native Git
// WorktreeCreate. RequestedPath is normalized to an absolute locator before it
// becomes durable; it is never ownership authority.
type CanonicalV19WorktreeCreateInput struct {
	OperationID  string
	OperationKey string
	AttemptID    string
	BindingID    string
	RequestedPath string
	CreatedAt    string
}

// CanonicalV19WorktreeCreateResult is the durable operation disposition after
// create/reconciliation. BindingID is populated only when State is succeeded.
type CanonicalV19WorktreeCreateResult struct {
	OperationID string
	State       string
	BindingID   string
	Path        string
}

type canonicalV19WorktreeLineage struct {
	AttemptID          string
	PlanID             string
	TaskID             string
	ProjectID          string
	WorkspaceBindingID string
	RepositoryLocator  string
	CommonGitDir       string
	BasisRevision      string
}

type canonicalV19WorktreeRequest struct {
	OperationID          string
	OperationKey         string
	RequestDigest        string
	AttemptID            string
	BindingID            string
	RequestedPath        string
	BasisRevision        string
	ExpectedCommonGitDir string
	ExpectedLockReason   string
	CreatedAt            string
	Lineage              canonicalV19WorktreeLineage
}

// CreateCanonicalV19Worktree runs one fresh crash-safe native Git
// WorktreeCreate lifecycle. The Git mutation is never retried by this function:
// once submission is durable, recovery must call ReconcileCanonicalV19WorktreeCreate.
func CreateCanonicalV19Worktree(
	ctx context.Context,
	homeDir string,
	input CanonicalV19WorktreeCreateInput,
) (CanonicalV19WorktreeCreateResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19WorktreeCreateInput(input); err != nil {
		return CanonicalV19WorktreeCreateResult{}, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19WorktreeCreateResult{}, err
	}
	defer func() { _ = sqlDB.Close() }()

	lineage, err := loadCanonicalV19WorktreeLineage(ctx, sqlDB, input.AttemptID)
	if err != nil {
		return CanonicalV19WorktreeCreateResult{}, fmt.Errorf("create canonical v19 Worktree: %w", err)
	}
	if err := verifyCanonicalV19WorktreeGitBasis(ctx, homeDir, lineage); err != nil {
		return CanonicalV19WorktreeCreateResult{}, fmt.Errorf("create canonical v19 Worktree: %w", err)
	}
	requestedPath, err := canonicalV19WorktreeAbsolutePath(input.RequestedPath)
	if err != nil {
		return CanonicalV19WorktreeCreateResult{}, fmt.Errorf("create canonical v19 Worktree: %w", err)
	}
	if err := requireCanonicalV19WorktreeParent(requestedPath); err != nil {
		return CanonicalV19WorktreeCreateResult{}, fmt.Errorf("create canonical v19 Worktree: %w", err)
	}
	repositoryPath := canonicalV19ObservedPath(homeDir, lineage.RepositoryLocator)
	initial := gitworktree.Observe(repositoryPath, requestedPath)
	if initial.State != gitworktree.ObservationAbsent {
		return CanonicalV19WorktreeCreateResult{}, fmt.Errorf("create canonical v19 Worktree: %w: requested path is not positively absent: %s",
			ErrCanonicalV19WorktreeConflict, canonicalV19WorktreeObservationSummary(initial))
	}

	request := canonicalV19WorktreeRequest{
		OperationID:          input.OperationID,
		OperationKey:         input.OperationKey,
		AttemptID:            input.AttemptID,
		BindingID:            input.BindingID,
		RequestedPath:        requestedPath,
		BasisRevision:        lineage.BasisRevision,
		ExpectedCommonGitDir: canonicalV19ObservedPath(homeDir, lineage.CommonGitDir),
		ExpectedLockReason:   "hand:v1:" + input.BindingID,
		CreatedAt:            input.CreatedAt,
		Lineage:              lineage,
	}
	request.RequestDigest = canonicalV19WorktreeRequestDigest(request)
	if err := prepareCanonicalV19WorktreeCreate(ctx, sqlDB, request); err != nil {
		return CanonicalV19WorktreeCreateResult{}, err
	}

	// Fresh read-only observation immediately precedes durable submission. If
	// the target changed after Tx A, do not cross the mutation boundary.
	beforeSubmit := gitworktree.Observe(repositoryPath, requestedPath)
	if beforeSubmit.State != gitworktree.ObservationAbsent {
		result, classifyErr := classifyCanonicalV19WorktreeCreate(ctx, sqlDB, request, "prepared", beforeSubmit)
		if classifyErr != nil {
			return result, classifyErr
		}
		return result, canonicalV19WorktreeDispositionError(result)
	}
	if err := verifyCanonicalV19WorktreeGitBasis(ctx, homeDir, lineage); err != nil {
		return CanonicalV19WorktreeCreateResult{OperationID: input.OperationID, State: "prepared"},
			fmt.Errorf("create canonical v19 Worktree: %w: %v", ErrCanonicalV19WorktreeUnresolved, err)
	}
	if err := submitCanonicalV19WorktreeCreate(ctx, sqlDB, request); err != nil {
		return CanonicalV19WorktreeCreateResult{}, err
	}

	// Intentionally ignore the command disposition as authority. Whether Git
	// returned nil or an error, exact observation below decides the state.
	performErr := gitworktree.Create(repositoryPath, requestedPath, request.BasisRevision, request.ExpectedLockReason)
	observed := gitworktree.Observe(repositoryPath, requestedPath)
	if performErr != nil && observed.Detail == "" {
		observed.Detail = performErr.Error()
	}
	result, err := classifyCanonicalV19WorktreeCreate(ctx, sqlDB, request, "submitted", observed)
	if err != nil {
		return result, err
	}
	return result, canonicalV19WorktreeDispositionError(result)
}

// ReconcileCanonicalV19WorktreeCreate observes one existing exact operation
// and classifies it without repeating any Git mutation.
func ReconcileCanonicalV19WorktreeCreate(
	ctx context.Context,
	homeDir string,
	operationID string,
) (CanonicalV19WorktreeCreateResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" {
		return CanonicalV19WorktreeCreateResult{}, fmt.Errorf("reconcile canonical v19 WorktreeCreate: operation ID is empty")
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19WorktreeCreateResult{}, err
	}
	defer func() { _ = sqlDB.Close() }()

	request, state, err := loadCanonicalV19WorktreeRequest(ctx, sqlDB, homeDir, operationID)
	if err != nil {
		return CanonicalV19WorktreeCreateResult{}, fmt.Errorf("reconcile canonical v19 WorktreeCreate: %w", err)
	}
	if state == "succeeded" || state == "rejected" || state == "no-effect" {
		result, err := canonicalV19WorktreeTerminalResult(ctx, sqlDB, request, state)
		if err != nil {
			return CanonicalV19WorktreeCreateResult{}, err
		}
		return result, canonicalV19WorktreeDispositionError(result)
	}

	repositoryPath := canonicalV19ObservedPath(homeDir, request.Lineage.RepositoryLocator)
	if err := verifyCanonicalV19WorktreeGitBasis(ctx, homeDir, request.Lineage); err != nil {
		if state == "submitted" {
			unknown := gitworktree.Observation{State: gitworktree.ObservationUnknown, Detail: err.Error()}
			result, classifyErr := classifyCanonicalV19WorktreeCreate(ctx, sqlDB, request, state, unknown)
			if classifyErr != nil {
				return result, classifyErr
			}
			return result, canonicalV19WorktreeDispositionError(result)
		}
		return CanonicalV19WorktreeCreateResult{OperationID: operationID, State: state},
			fmt.Errorf("reconcile canonical v19 WorktreeCreate: %w: %v", ErrCanonicalV19WorktreeUnresolved, err)
	}
	observed := gitworktree.Observe(repositoryPath, request.RequestedPath)
	result, err := classifyCanonicalV19WorktreeCreate(ctx, sqlDB, request, state, observed)
	if err != nil {
		return result, err
	}
	return result, canonicalV19WorktreeDispositionError(result)
}

func validateCanonicalV19WorktreeCreateInput(input CanonicalV19WorktreeCreateInput) error {
	for name, value := range map[string]string{
		"operation ID":   input.OperationID,
		"operation key":  input.OperationKey,
		"Attempt ID":     input.AttemptID,
		"binding ID":     input.BindingID,
		"requested path": input.RequestedPath,
		"created_at":     input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("create canonical v19 Worktree: %s is empty", name)
		}
	}
	return nil
}

func canonicalV19WorktreeAbsolutePath(value string) (string, error) {
	path, err := filepath.Abs(filepath.FromSlash(value))
	if err != nil {
		return "", fmt.Errorf("normalize requested path: %w", err)
	}
	return filepath.Clean(path), nil
}

func requireCanonicalV19WorktreeParent(path string) error {
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect requested path parent: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("requested path parent %q is not a direct directory", parent)
	}
	return nil
}

func loadCanonicalV19WorktreeLineage(
	ctx context.Context,
	q canonicalV19PlanQueryer,
	attemptID string,
) (canonicalV19WorktreeLineage, error) {
	var lineage canonicalV19WorktreeLineage
	err := q.QueryRowContext(ctx, `SELECT a.id,p.id,t.id,t.project_id,p.workspace_binding_id,
		w.repository_locator,w.common_git_dir,w.revision
		FROM attempt a
		JOIN plan p ON p.id=a.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.retired_at=''
		JOIN workspace_binding w ON w.id=p.workspace_binding_id
			AND w.project_id=t.project_id AND w.superseded_at=''
		WHERE a.id=? AND a.lifecycle='active' AND a.terminal_at=''`, attemptID).Scan(
		&lineage.AttemptID, &lineage.PlanID, &lineage.TaskID, &lineage.ProjectID,
		&lineage.WorkspaceBindingID, &lineage.RepositoryLocator, &lineage.CommonGitDir,
		&lineage.BasisRevision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return canonicalV19WorktreeLineage{}, fmt.Errorf("%w: Attempt %q lacks exact active Project/Task/Plan/Attempt/WorkspaceBinding lineage",
			ErrCanonicalV19WorktreeNotCurrent, attemptID)
	}
	if err != nil {
		return canonicalV19WorktreeLineage{}, canonicalV19WorktreeWriteError("read exact current lineage", err)
	}
	return lineage, nil
}

func verifyCanonicalV19WorktreeGitBasis(ctx context.Context, homeDir string, lineage canonicalV19WorktreeLineage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := verifyCanonicalV19PlanGitBasis(homeDir, canonicalV19PlanBasisObservation{
		ProjectID:         lineage.ProjectID,
		RepositoryLocator: lineage.RepositoryLocator,
		CommonGitDir:      lineage.CommonGitDir,
		Revision:          lineage.BasisRevision,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCanonicalV19WorktreeGitBasis, err)
	}
	return nil
}

func prepareCanonicalV19WorktreeCreate(ctx context.Context, sqlDB *sql.DB, request canonicalV19WorktreeRequest) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19WorktreeWriteError("begin prepare transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("prepare canonical v19 WorktreeCreate: %w", err)
	}
	current, err := loadCanonicalV19WorktreeLineage(ctx, tx, request.AttemptID)
	if err != nil {
		return fmt.Errorf("prepare canonical v19 WorktreeCreate: %w", err)
	}
	if current != request.Lineage {
		return fmt.Errorf("prepare canonical v19 WorktreeCreate: %w: exact lineage changed after Git observation", ErrCanonicalV19WorktreeNotCurrent)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO external_operation(
		id,kind,adapter_ref,operation_key,request_digest,project_id,task_id,plan_id,attempt_id,
		primary_scope_kind,primary_scope_key,state,created_at,state_changed_at,
		state_evidence_digest,submitted_at,finalized_at
	) VALUES(?,'worktree-create','builtin/git-worktree',?,?,?,?,?,?,?,'worktree',?,'prepared',?,?,'','','')`,
		request.OperationID, request.OperationKey, request.RequestDigest, request.Lineage.ProjectID,
		request.Lineage.TaskID, request.Lineage.PlanID, request.AttemptID, request.BindingID,
		request.CreatedAt, request.CreatedAt); err != nil {
		return canonicalV19WorktreeConstraintError("prepare", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO worktree_create_operation(
		operation_id,attempt_id,binding_id,requested_path,basis_revision,
		expected_common_git_dir,expected_lock_reason
	) VALUES(?,?,?,?,?,?,?)`, request.OperationID, request.AttemptID, request.BindingID,
		request.RequestedPath, request.BasisRevision, request.ExpectedCommonGitDir,
		request.ExpectedLockReason); err != nil {
		return canonicalV19WorktreeConstraintError("prepare typed child", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_scope_claim(operation_id,scope_kind,scope_key,created_at)
		VALUES(?,'worktree',?,?)`, request.OperationID, request.BindingID, request.CreatedAt); err != nil {
		return canonicalV19WorktreeConstraintError("prepare primary scope", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_scope_claim(operation_id,scope_kind,scope_key,created_at)
		VALUES(?,'workspace',?,?)`, request.OperationID, request.Lineage.WorkspaceBindingID, request.CreatedAt); err != nil {
		return canonicalV19WorktreeConstraintError("prepare workspace scope", request.OperationID, err)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19WorktreeWriteError("commit prepare transaction", err)
	}
	committed = true
	return nil
}

func submitCanonicalV19WorktreeCreate(ctx context.Context, sqlDB *sql.DB, request canonicalV19WorktreeRequest) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19WorktreeWriteError("begin submit transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("submit canonical v19 WorktreeCreate: %w", err)
	}
	if err := requireCanonicalV19WorktreeRequestCurrent(ctx, tx, request, "prepared"); err != nil {
		return fmt.Errorf("submit canonical v19 WorktreeCreate: %w", err)
	}
	changedAt := canonicalV19WorktreeTransitionTimestamp(request.CreatedAt)
	evidence := canonicalV19WorktreeSubmissionDigest(request)
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='submitted',state_changed_at=?,state_evidence_digest=?,submitted_at=?
		WHERE id=? AND state='prepared'`, changedAt, evidence, changedAt, request.OperationID)
	if err != nil {
		return canonicalV19WorktreeWriteError("authorize exact submission", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return canonicalV19WorktreeWriteError("count exact submission", err)
	}
	if changed != 1 {
		return fmt.Errorf("submit canonical v19 WorktreeCreate: %w: operation changed %d rows, want 1", ErrCanonicalV19WorktreeNotCurrent, changed)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19WorktreeWriteError("commit submit transaction", err)
	}
	committed = true
	return nil
}

func requireCanonicalV19WorktreeRequestCurrent(
	ctx context.Context,
	tx *sql.Tx,
	request canonicalV19WorktreeRequest,
	wantState string,
) error {
	current, err := loadCanonicalV19WorktreeLineage(ctx, tx, request.AttemptID)
	if err != nil {
		return err
	}
	if current != request.Lineage {
		return fmt.Errorf("%w: exact lineage changed", ErrCanonicalV19WorktreeNotCurrent)
	}
	var state string
	err = tx.QueryRowContext(ctx, `SELECT o.state
		FROM external_operation o
		JOIN worktree_create_operation wc ON wc.operation_id=o.id
		WHERE o.id=? AND o.kind='worktree-create' AND o.adapter_ref='builtin/git-worktree'
		  AND o.operation_key=? AND o.request_digest=?
		  AND o.project_id=? AND o.task_id=? AND o.plan_id=? AND o.attempt_id=?
		  AND o.primary_scope_kind='worktree' AND o.primary_scope_key=?
		  AND wc.attempt_id=? AND wc.binding_id=? AND wc.requested_path=?
		  AND wc.basis_revision=? AND wc.expected_common_git_dir=? AND wc.expected_lock_reason=?
		  AND EXISTS (SELECT 1 FROM operation_scope_claim c
			WHERE c.operation_id=o.id AND c.scope_kind='worktree' AND c.scope_key=?)
		  AND EXISTS (SELECT 1 FROM operation_scope_claim c
			WHERE c.operation_id=o.id AND c.scope_kind='workspace' AND c.scope_key=?)`,
		request.OperationID, request.OperationKey, request.RequestDigest,
		request.Lineage.ProjectID, request.Lineage.TaskID, request.Lineage.PlanID, request.AttemptID,
		request.BindingID, request.AttemptID, request.BindingID, request.RequestedPath,
		request.BasisRevision, request.ExpectedCommonGitDir, request.ExpectedLockReason,
		request.BindingID, request.Lineage.WorkspaceBindingID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: exact operation/request/scope witness is absent", ErrCanonicalV19WorktreeNotCurrent)
	}
	if err != nil {
		return canonicalV19WorktreeWriteError("read exact operation witness", err)
	}
	if state != wantState {
		return fmt.Errorf("%w: operation state=%q, want %q", ErrCanonicalV19WorktreeNotCurrent, state, wantState)
	}
	return nil
}

func loadCanonicalV19WorktreeRequest(
	ctx context.Context,
	sqlDB *sql.DB,
	homeDir string,
	operationID string,
) (canonicalV19WorktreeRequest, string, error) {
	var request canonicalV19WorktreeRequest
	var state string
	var projectID, taskID, planID string
	err := sqlDB.QueryRowContext(ctx, `SELECT o.id,o.operation_key,o.request_digest,o.attempt_id,o.created_at,o.state,
		o.project_id,o.task_id,o.plan_id,wc.binding_id,wc.requested_path,wc.basis_revision,
		wc.expected_common_git_dir,wc.expected_lock_reason
		FROM external_operation o
		JOIN worktree_create_operation wc ON wc.operation_id=o.id
		WHERE o.id=? AND o.kind='worktree-create' AND o.adapter_ref='builtin/git-worktree'
		  AND o.primary_scope_kind='worktree' AND o.primary_scope_key=wc.binding_id`, operationID).Scan(
		&request.OperationID, &request.OperationKey, &request.RequestDigest, &request.AttemptID,
		&request.CreatedAt, &state, &projectID, &taskID, &planID, &request.BindingID,
		&request.RequestedPath, &request.BasisRevision, &request.ExpectedCommonGitDir,
		&request.ExpectedLockReason)
	if errors.Is(err, sql.ErrNoRows) {
		return canonicalV19WorktreeRequest{}, "", fmt.Errorf("%w: WorktreeCreate %q does not exist", ErrCanonicalV19WorktreeNotCurrent, operationID)
	}
	if err != nil {
		return canonicalV19WorktreeRequest{}, "", canonicalV19WorktreeWriteError("read exact WorktreeCreate", err)
	}
	lineage, err := loadCanonicalV19WorktreeLineage(ctx, sqlDB, request.AttemptID)
	if err != nil {
		return canonicalV19WorktreeRequest{}, "", err
	}
	if lineage.ProjectID != projectID || lineage.TaskID != taskID || lineage.PlanID != planID ||
		lineage.BasisRevision != request.BasisRevision ||
		!handgit.SamePath(canonicalV19ObservedPath(homeDir, lineage.CommonGitDir), request.ExpectedCommonGitDir) {
		return canonicalV19WorktreeRequest{}, "", fmt.Errorf("%w: persisted WorktreeCreate no longer matches exact current lineage", ErrCanonicalV19WorktreeNotCurrent)
	}
	request.Lineage = lineage
	if canonicalV19WorktreeRequestDigest(request) != request.RequestDigest {
		return canonicalV19WorktreeRequest{}, "", fmt.Errorf("%w: persisted WorktreeCreate request digest mismatch", ErrCanonicalV19WorktreeNotCurrent)
	}
	return request, state, nil
}

func classifyCanonicalV19WorktreeCreate(
	ctx context.Context,
	sqlDB *sql.DB,
	request canonicalV19WorktreeRequest,
	fromState string,
	observation gitworktree.Observation,
) (CanonicalV19WorktreeCreateResult, error) {
	exact := canonicalV19WorktreeObservationExact(request, observation)
	toState := ""
	switch {
	case exact:
		toState = "succeeded"
	case observation.State == gitworktree.ObservationAbsent:
		toState = "no-effect"
	case fromState == "prepared" && observation.State == gitworktree.ObservationPresent:
		// No mutation was authorized, so a foreign/mismatched presence cannot
		// be this operation's effect. Preserve it externally and close only this
		// exact operation as no-effect.
		toState = "no-effect"
	case fromState == "submitted":
		toState = "uncertain"
	case fromState == "uncertain" || fromState == "prepared":
		return CanonicalV19WorktreeCreateResult{OperationID: request.OperationID, State: fromState},
			fmt.Errorf("reconcile canonical v19 WorktreeCreate: %w: %s", ErrCanonicalV19WorktreeUnresolved, canonicalV19WorktreeObservationSummary(observation))
	default:
		return CanonicalV19WorktreeCreateResult{}, fmt.Errorf("reconcile canonical v19 WorktreeCreate: unsupported state %q", fromState)
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19WorktreeCreateResult{}, canonicalV19WorktreeWriteError("begin classify transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19WorktreeCreateResult{}, fmt.Errorf("classify canonical v19 WorktreeCreate: %w", err)
	}
	if err := requireCanonicalV19WorktreeRequestCurrent(ctx, tx, request, fromState); err != nil {
		return CanonicalV19WorktreeCreateResult{}, fmt.Errorf("classify canonical v19 WorktreeCreate: %w", err)
	}
	changedAt := canonicalV19WorktreeTransitionTimestamp(request.CreatedAt)
	var priorChangedAt string
	if err := tx.QueryRowContext(ctx, `SELECT state_changed_at FROM external_operation WHERE id=?`, request.OperationID).Scan(&priorChangedAt); err != nil {
		return CanonicalV19WorktreeCreateResult{}, canonicalV19WorktreeWriteError("read prior state timestamp", err)
	}
	changedAt = canonicalV19WorktreeTransitionTimestamp(priorChangedAt)
	evidence := canonicalV19WorktreeObservationDigest(request, observation)
	finalizedAt := ""
	if toState == "succeeded" || toState == "no-effect" || toState == "rejected" {
		finalizedAt = changedAt
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state=?,state_changed_at=?,state_evidence_digest=?,finalized_at=?
		WHERE id=? AND state=?`, toState, changedAt, evidence, finalizedAt, request.OperationID, fromState)
	if err != nil {
		return CanonicalV19WorktreeCreateResult{}, canonicalV19WorktreeWriteError("classify exact operation", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return CanonicalV19WorktreeCreateResult{}, canonicalV19WorktreeWriteError("count exact classification", err)
	}
	if changed != 1 {
		return CanonicalV19WorktreeCreateResult{}, fmt.Errorf("classify canonical v19 WorktreeCreate: %w: operation changed %d rows, want 1", ErrCanonicalV19WorktreeNotCurrent, changed)
	}

	out := CanonicalV19WorktreeCreateResult{OperationID: request.OperationID, State: toState}
	if toState == "succeeded" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO attempt_worktree_binding(
			id,attempt_id,create_operation_id,path,common_git_dir,private_git_dir,lock_reason,
			basis_revision,head_revision,physical_identity_digest,established_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, request.BindingID, request.AttemptID, request.OperationID,
			observation.Path, observation.CommonGitDir, observation.PrivateGitDir,
			observation.LockReason, request.BasisRevision, observation.HeadRevision,
			observation.PhysicalIdentityDigest, changedAt); err != nil {
			return CanonicalV19WorktreeCreateResult{}, canonicalV19WorktreeConstraintError("establish binding", request.OperationID, err)
		}
		out.BindingID = request.BindingID
		out.Path = observation.Path
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19WorktreeCreateResult{}, canonicalV19WorktreeWriteError("commit classify transaction", err)
	}
	committed = true
	return out, nil
}

func canonicalV19WorktreeObservationExact(request canonicalV19WorktreeRequest, observation gitworktree.Observation) bool {
	return observation.State == gitworktree.ObservationPresent && observation.Detail == "" &&
		!observation.Prunable && observation.Detached &&
		handgit.SamePath(observation.Path, request.RequestedPath) &&
		handgit.SamePath(observation.CommonGitDir, request.ExpectedCommonGitDir) &&
		observation.PrivateGitDir != "" && observation.LockReason == request.ExpectedLockReason &&
		observation.HeadRevision == request.BasisRevision && observation.PhysicalIdentityDigest != ""
}

func canonicalV19WorktreeTerminalResult(
	ctx context.Context,
	sqlDB *sql.DB,
	request canonicalV19WorktreeRequest,
	state string,
) (CanonicalV19WorktreeCreateResult, error) {
	result := CanonicalV19WorktreeCreateResult{OperationID: request.OperationID, State: state}
	if state != "succeeded" {
		return result, nil
	}
	if err := sqlDB.QueryRowContext(ctx, `SELECT id,path FROM attempt_worktree_binding
		WHERE create_operation_id=? AND attempt_id=?`, request.OperationID, request.AttemptID).Scan(&result.BindingID, &result.Path); err != nil {
		return CanonicalV19WorktreeCreateResult{}, canonicalV19WorktreeWriteError("read successful WorktreeBinding", err)
	}
	return result, nil
}

func canonicalV19WorktreeDispositionError(result CanonicalV19WorktreeCreateResult) error {
	switch result.State {
	case "succeeded":
		return nil
	case "no-effect", "rejected":
		return fmt.Errorf("%w: operation %q state=%s", ErrCanonicalV19WorktreeNotEstablished, result.OperationID, result.State)
	case "prepared", "submitted", "uncertain":
		return fmt.Errorf("%w: operation %q state=%s", ErrCanonicalV19WorktreeUnresolved, result.OperationID, result.State)
	default:
		return fmt.Errorf("canonical v19 WorktreeCreate operation %q has unexpected state %q", result.OperationID, result.State)
	}
}

func canonicalV19WorktreeRequestDigest(request canonicalV19WorktreeRequest) string {
	payload, err := json.Marshal(struct {
		AttemptID            string `json:"attempt_id"`
		BindingID            string `json:"binding_id"`
		RequestedPath        string `json:"requested_path"`
		BasisRevision        string `json:"basis_revision"`
		ExpectedCommonGitDir string `json:"expected_common_git_dir"`
		ExpectedLockReason   string `json:"expected_lock_reason"`
	}{request.AttemptID, request.BindingID, request.RequestedPath, request.BasisRevision,
		request.ExpectedCommonGitDir, request.ExpectedLockReason})
	if err != nil {
		panic(fmt.Sprintf("encode canonical v19 WorktreeCreate request: %v", err))
	}
	return canonicalV19SHA256(append([]byte("hand:v19:worktree-create:request:v1\x00"), payload...))
}

func canonicalV19WorktreeSubmissionDigest(request canonicalV19WorktreeRequest) string {
	return canonicalV19SHA256([]byte("hand:v19:worktree-create:submitted:v1\x00" + request.OperationID + "\x00" + request.RequestDigest))
}

func canonicalV19WorktreeObservationDigest(request canonicalV19WorktreeRequest, observation gitworktree.Observation) string {
	payload, err := json.Marshal(struct {
		OperationID            string                       `json:"operation_id"`
		State                  gitworktree.ObservationState `json:"state"`
		Path                   string                       `json:"path"`
		CommonGitDir           string                       `json:"common_git_dir"`
		PrivateGitDir          string                       `json:"private_git_dir"`
		LockReason             string                       `json:"lock_reason"`
		HeadRevision           string                       `json:"head_revision"`
		PhysicalIdentityDigest string                       `json:"physical_identity_digest"`
		Detached               bool                         `json:"detached"`
		Prunable               bool                         `json:"prunable"`
		Detail                 string                       `json:"detail"`
	}{request.OperationID, observation.State, observation.Path, observation.CommonGitDir,
		observation.PrivateGitDir, observation.LockReason, observation.HeadRevision,
		observation.PhysicalIdentityDigest, observation.Detached, observation.Prunable,
		observation.Detail})
	if err != nil {
		panic(fmt.Sprintf("encode canonical v19 WorktreeCreate observation: %v", err))
	}
	return canonicalV19SHA256(append([]byte("hand:v19:worktree-create:observation:v1\x00"), payload...))
}

func canonicalV19WorktreeObservationSummary(observation gitworktree.Observation) string {
	if observation.Detail != "" {
		return fmt.Sprintf("state=%s path=%q detail=%q", observation.State, observation.Path, observation.Detail)
	}
	return fmt.Sprintf("state=%s path=%q head=%q locked=%q", observation.State, observation.Path, observation.HeadRevision, observation.LockReason)
}

func canonicalV19WorktreeTransitionTimestamp(previous string) string {
	now := time.Now().UTC()
	if parsed, err := time.Parse(time.RFC3339Nano, previous); err == nil && !now.After(parsed) {
		now = parsed.Add(time.Nanosecond)
	}
	return now.Format(time.RFC3339Nano)
}

func canonicalV19WorktreeConstraintError(action, operationID string, err error) error {
	if isSQLiteConstraint(err) {
		return fmt.Errorf("canonical v19 WorktreeCreate: %s: %w: operation %q", action, ErrCanonicalV19WorktreeConflict, operationID)
	}
	return canonicalV19WorktreeWriteError(action, err)
}

func canonicalV19WorktreeWriteError(action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("canonical v19 WorktreeCreate: %s: %w", action, ErrContention)
	}
	return fmt.Errorf("canonical v19 WorktreeCreate: %s: %w", action, err)
}
