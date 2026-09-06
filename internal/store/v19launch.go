package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

// ErrCanonicalV19LaunchConflict marks an exact Launch resource, dependency,
// scope, or relational conflict. Callers must never retarget.
var ErrCanonicalV19LaunchConflict = errors.New("canonical v19 launch write conflict")

// ErrCanonicalV19LaunchNotCurrent marks a Launch whose exact active
// Attempt/WorktreeBinding/SessionBinding ownership no longer matches.
var ErrCanonicalV19LaunchNotCurrent = errors.New("canonical v19 launch is not current")

// ErrCanonicalV19LaunchTransition marks an illegal or stale exact external
// operation transition for Launch.
var ErrCanonicalV19LaunchTransition = errors.New("canonical v19 launch transition conflict")

// CanonicalV19LaunchEnvironmentValue is one exact persisted process environment
// entry. Secret values use value_kind=secret-ref so plaintext need not be stored;
// ValueDigest pins the resolved value that the later provider adapter must verify.
type CanonicalV19LaunchEnvironmentValue struct {
	ValueKind     string
	ValueMaterial string
	ValueDigest   string
}

// CanonicalV19LaunchSpec is the exact structured process request persisted for
// one canonical Launch. It contains process data only, never shell source or
// provider workspace/tab/pane identity.
type CanonicalV19LaunchSpec struct {
	Executable  string
	Arguments   []string
	Environment map[string]CanonicalV19LaunchEnvironmentValue
	Cwd         string
}

// CanonicalV19LaunchPrepareInput identifies one fresh logical Launch for the
// exact active Attempt and open SessionBinding.
type CanonicalV19LaunchPrepareInput struct {
	OperationID      string
	OperationKey     string
	AttemptID        string
	SessionBindingID string
	BindingID        string
	Spec             CanonicalV19LaunchSpec
	CreatedAt        string
}

// CanonicalV19LaunchRequest is the exact immutable request persisted before any
// provider execution mutation is authorized.
type CanonicalV19LaunchRequest struct {
	OperationID       string
	OperationKey      string
	RequestDigest     string
	ProjectID         string
	TaskID            string
	PlanID            string
	AttemptID         string
	WorktreeBindingID string
	SessionBindingID  string
	BindingID         string
	AdapterRef        string
	LaunchSpecDigest  string
	Spec              CanonicalV19LaunchSpec
	CreatedAt         string
}

// CanonicalV19LaunchTransitionInput records exact nonsuccess provider evidence
// for one Launch operation.
type CanonicalV19LaunchTransitionInput struct {
	OperationID    string
	State          string
	ObservedAt     string
	EvidenceDigest string
}

// CanonicalV19ExecutorBindingEvidence is positive provider evidence that the
// exact Launch established the requested ExecutorBinding.
type CanonicalV19ExecutorBindingEvidence struct {
	OperationID         string
	ProviderExecutorKey string
	EstablishedAt       string
	EvidenceDigest      string
}

type canonicalV19LaunchCurrent struct {
	Request        CanonicalV19LaunchRequest
	State          string
	StateChangedAt string
}

// PrepareCanonicalV19Launch durably records one exact prepared Launch, its
// structured arguments/environment, and executor-control + Session claims. It
// performs no provider mutation.
func PrepareCanonicalV19Launch(
	ctx context.Context,
	homeDir string,
	input CanonicalV19LaunchPrepareInput,
) (CanonicalV19LaunchRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19LaunchPrepareInput(input); err != nil {
		return CanonicalV19LaunchRequest{}, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19LaunchRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19LaunchRequest{}, canonicalV19LaunchWriteError("prepare", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19LaunchRequest{}, fmt.Errorf("prepare canonical v19 Launch: %w", err)
	}
	request, err := buildCanonicalV19LaunchRequest(ctx, tx, input)
	if err != nil {
		return CanonicalV19LaunchRequest{}, fmt.Errorf("prepare canonical v19 Launch: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO external_operation(
		id,kind,adapter_ref,operation_key,request_digest,project_id,task_id,plan_id,attempt_id,
		primary_scope_kind,primary_scope_key,state,created_at,state_changed_at,state_evidence_digest,
		submitted_at,finalized_at
	) VALUES(?,'launch',?,?,?,?,?,?,?,'executor-control',?,'prepared',?,?,'','','')`,
		request.OperationID, request.AdapterRef, request.OperationKey, request.RequestDigest,
		request.ProjectID, request.TaskID, request.PlanID, request.AttemptID, request.BindingID,
		request.CreatedAt, request.CreatedAt); err != nil {
		return CanonicalV19LaunchRequest{}, canonicalV19LaunchConstraintError("prepare", "insert external operation", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO launch_operation(
		operation_id,attempt_id,session_binding_id,binding_id,executable,cwd,launch_spec_digest
	) VALUES(?,?,?,?,?,?,?)`, request.OperationID, request.AttemptID, request.SessionBindingID,
		request.BindingID, request.Spec.Executable, request.Spec.Cwd, request.LaunchSpecDigest); err != nil {
		return CanonicalV19LaunchRequest{}, canonicalV19LaunchConstraintError("prepare", "insert typed Launch", request.OperationID, err)
	}
	for ordinal, value := range request.Spec.Arguments {
		if _, err := tx.ExecContext(ctx, `INSERT INTO launch_argument(operation_id,ordinal,value) VALUES(?,?,?)`,
			request.OperationID, ordinal, value); err != nil {
			return CanonicalV19LaunchRequest{}, canonicalV19LaunchConstraintError("prepare", "insert Launch argument", request.OperationID, err)
		}
	}
	for _, name := range canonicalV19LaunchEnvironmentNames(request.Spec.Environment) {
		value := request.Spec.Environment[name]
		if _, err := tx.ExecContext(ctx, `INSERT INTO launch_environment(
			operation_id,name,value_kind,value_material,value_digest
		) VALUES(?,?,?,?,?)`, request.OperationID, name, value.ValueKind, value.ValueMaterial, value.ValueDigest); err != nil {
			return CanonicalV19LaunchRequest{}, canonicalV19LaunchConstraintError("prepare", "insert Launch environment", request.OperationID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_scope_claim(operation_id,scope_kind,scope_key,created_at)
		VALUES(?,'executor-control',?,?)`, request.OperationID, request.BindingID, request.CreatedAt); err != nil {
		return CanonicalV19LaunchRequest{}, canonicalV19LaunchConstraintError("prepare", "claim exact executor-control scope", request.OperationID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_scope_claim(operation_id,scope_kind,scope_key,created_at)
		VALUES(?,'session',?,?)`, request.OperationID, request.SessionBindingID, request.CreatedAt); err != nil {
		return CanonicalV19LaunchRequest{}, canonicalV19LaunchConstraintError("prepare", "claim dependent Session scope", request.OperationID, err)
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19LaunchRequest{}, canonicalV19LaunchWriteError("prepare", "commit writer", err)
	}
	committed = true
	return request, nil
}

// SubmitCanonicalV19Launch durably authorizes the exact prepared Launch.
// Provider execution mutation may begin only after it returns successfully.
func SubmitCanonicalV19Launch(
	ctx context.Context,
	homeDir string,
	operationID string,
	submittedAt string,
	evidenceDigest string,
) (CanonicalV19LaunchRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" || submittedAt == "" || evidenceDigest == "" {
		return CanonicalV19LaunchRequest{}, fmt.Errorf("submit canonical v19 Launch: operation ID, submitted_at, and evidence digest are required")
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19LaunchRequest{}, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19LaunchRequest{}, canonicalV19LaunchWriteError("submit", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19LaunchRequest{}, fmt.Errorf("submit canonical v19 Launch: %w", err)
	}
	current, err := loadCanonicalV19LaunchCurrent(ctx, tx, operationID)
	if err != nil {
		return CanonicalV19LaunchRequest{}, fmt.Errorf("submit canonical v19 Launch: %w", err)
	}
	if current.State != "prepared" || current.StateChangedAt == submittedAt {
		return CanonicalV19LaunchRequest{}, fmt.Errorf("submit canonical v19 Launch: %w: operation %q is %q", ErrCanonicalV19LaunchTransition, operationID, current.State)
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='submitted',state_changed_at=?,state_evidence_digest=?,submitted_at=?
		WHERE id=? AND state='prepared'`, submittedAt, evidenceDigest, submittedAt, operationID)
	if err != nil {
		return CanonicalV19LaunchRequest{}, canonicalV19LaunchConstraintError("submit", "authorize exact operation", operationID, err)
	}
	if err := requireCanonicalV19LaunchOneChanged(result, "submit", operationID); err != nil {
		return CanonicalV19LaunchRequest{}, err
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19LaunchRequest{}, canonicalV19LaunchWriteError("submit", "commit writer", err)
	}
	committed = true
	return current.Request, nil
}

// ClassifyCanonicalV19Launch records exact nonsuccess Launch evidence. Success
// uses EstablishCanonicalV19ExecutorBinding so Launch success and ExecutorBinding
// establishment remain atomic.
func ClassifyCanonicalV19Launch(
	ctx context.Context,
	homeDir string,
	input CanonicalV19LaunchTransitionInput,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19LaunchTransitionInput(input); err != nil {
		return err
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19LaunchWriteError("classify", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("classify canonical v19 Launch: %w", err)
	}
	current, err := loadCanonicalV19LaunchCurrent(ctx, tx, input.OperationID)
	if err != nil {
		return fmt.Errorf("classify canonical v19 Launch: %w", err)
	}
	if !canonicalV19LaunchNonsuccessTransitionAllowed(current.State, input.State) || current.StateChangedAt == input.ObservedAt {
		return fmt.Errorf("classify canonical v19 Launch: %w: %s -> %s", ErrCanonicalV19LaunchTransition, current.State, input.State)
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
		return canonicalV19LaunchConstraintError("classify", "transition exact operation", input.OperationID, err)
	}
	if err := requireCanonicalV19LaunchOneChanged(result, "classify", input.OperationID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19LaunchWriteError("classify", "commit writer", err)
	}
	committed = true
	return nil
}

// EstablishCanonicalV19ExecutorBinding atomically marks the exact Launch
// succeeded and inserts its immutable ExecutorBinding from positive provider
// establishment evidence.
func EstablishCanonicalV19ExecutorBinding(
	ctx context.Context,
	homeDir string,
	evidence CanonicalV19ExecutorBindingEvidence,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for name, value := range map[string]string{
		"operation ID": evidence.OperationID, "provider executor key": evidence.ProviderExecutorKey,
		"established_at": evidence.EstablishedAt, "evidence digest": evidence.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("establish canonical v19 ExecutorBinding: %s is empty", name)
		}
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19LaunchWriteError("establish", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("establish canonical v19 ExecutorBinding: %w", err)
	}
	current, err := loadCanonicalV19LaunchCurrent(ctx, tx, evidence.OperationID)
	if err != nil {
		return fmt.Errorf("establish canonical v19 ExecutorBinding: %w", err)
	}
	if !canonicalV19LaunchSuccessTransitionAllowed(current.State) || current.StateChangedAt == evidence.EstablishedAt {
		return fmt.Errorf("establish canonical v19 ExecutorBinding: %w: operation %q is %q", ErrCanonicalV19LaunchTransition, evidence.OperationID, current.State)
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='succeeded',state_changed_at=?,state_evidence_digest=?,finalized_at=?
		WHERE id=? AND state=?`, evidence.EstablishedAt, evidence.EvidenceDigest, evidence.EstablishedAt,
		evidence.OperationID, current.State)
	if err != nil {
		return canonicalV19LaunchConstraintError("establish", "mark exact Launch succeeded", evidence.OperationID, err)
	}
	if err := requireCanonicalV19LaunchOneChanged(result, "establish", evidence.OperationID); err != nil {
		return err
	}
	request := current.Request
	if _, err := tx.ExecContext(ctx, `INSERT INTO executor_binding(
		id,attempt_id,session_binding_id,launch_operation_id,adapter_ref,provider_executor_key,established_at
	) VALUES(?,?,?,?,?,?,?)`, request.BindingID, request.AttemptID, request.SessionBindingID,
		request.OperationID, request.AdapterRef, evidence.ProviderExecutorKey, evidence.EstablishedAt); err != nil {
		return canonicalV19LaunchConstraintError("establish", "insert exact ExecutorBinding", evidence.OperationID, err)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19LaunchWriteError("establish", "commit writer", err)
	}
	committed = true
	return nil
}

func validateCanonicalV19LaunchPrepareInput(input CanonicalV19LaunchPrepareInput) error {
	for name, value := range map[string]string{
		"operation ID": input.OperationID, "operation key": input.OperationKey,
		"Attempt ID": input.AttemptID, "SessionBinding ID": input.SessionBindingID,
		"binding ID": input.BindingID, "created_at": input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("prepare canonical v19 Launch: %s is empty", name)
		}
	}
	if err := validateCanonicalV19LaunchSpec(input.Spec); err != nil {
		return fmt.Errorf("prepare canonical v19 Launch: %w", err)
	}
	return nil
}

func validateCanonicalV19LaunchSpec(spec CanonicalV19LaunchSpec) error {
	if spec.Executable == "" {
		return errors.New("launch executable is empty")
	}
	if spec.Cwd == "" {
		return errors.New("launch cwd is empty")
	}
	for name, value := range spec.Environment {
		if name == "" {
			return errors.New("launch environment name is empty")
		}
		if value.ValueDigest == "" {
			return fmt.Errorf("Launch environment %q value digest is empty", name)
		}
		switch value.ValueKind {
		case "literal":
		case "secret-ref":
			if value.ValueMaterial == "" {
				return fmt.Errorf("Launch environment %q secret reference is empty", name)
			}
		default:
			return fmt.Errorf("Launch environment %q value kind %q is invalid", name, value.ValueKind)
		}
	}
	return nil
}

func validateCanonicalV19LaunchTransitionInput(input CanonicalV19LaunchTransitionInput) error {
	for name, value := range map[string]string{
		"operation ID": input.OperationID, "observed_at": input.ObservedAt, "evidence digest": input.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("classify canonical v19 Launch: %s is empty", name)
		}
	}
	switch input.State {
	case "uncertain", "rejected", "no-effect":
		return nil
	default:
		return fmt.Errorf("classify canonical v19 Launch: state %q requires a different writer", input.State)
	}
}

func buildCanonicalV19LaunchRequest(
	ctx context.Context,
	tx *sql.Tx,
	input CanonicalV19LaunchPrepareInput,
) (CanonicalV19LaunchRequest, error) {
	request := CanonicalV19LaunchRequest{
		OperationID: input.OperationID, OperationKey: input.OperationKey, AttemptID: input.AttemptID,
		SessionBindingID: input.SessionBindingID, BindingID: input.BindingID,
		Spec: cloneCanonicalV19LaunchSpec(input.Spec), CreatedAt: input.CreatedAt,
	}
	var worktreePath string
	err := tx.QueryRowContext(ctx, `SELECT project_current.id,t.id,p.id,s.worktree_binding_id,s.adapter_ref,b.path
		FROM session_binding s
		JOIN attempt a ON a.id=s.attempt_id AND a.lifecycle='active' AND a.terminal_at=''
		JOIN plan p ON p.id=a.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.retired_at=''
		JOIN attempt_worktree_binding b ON b.id=s.worktree_binding_id AND b.attempt_id=a.id
		WHERE s.id=? AND s.attempt_id=?
		  AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)
		  AND NOT EXISTS (SELECT 1 FROM worktree_binding_release r WHERE r.binding_id=b.id)
		  AND NOT EXISTS (SELECT 1 FROM executor_binding e WHERE e.attempt_id=a.id)`,
		input.SessionBindingID, input.AttemptID).Scan(
		&request.ProjectID, &request.TaskID, &request.PlanID, &request.WorktreeBindingID,
		&request.AdapterRef, &worktreePath)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19LaunchRequest{}, fmt.Errorf("%w: Attempt %q lacks exact active/open WorktreeBinding+SessionBinding or already has an ExecutorBinding", ErrCanonicalV19LaunchNotCurrent, input.AttemptID)
	}
	if err != nil {
		return CanonicalV19LaunchRequest{}, canonicalV19LaunchWriteError("prepare", "read exact current Session/Worktree ownership", err)
	}
	if request.Spec.Cwd != worktreePath {
		return CanonicalV19LaunchRequest{}, fmt.Errorf("%w: Launch cwd %q does not equal exact WorktreeBinding path %q", ErrCanonicalV19LaunchNotCurrent, request.Spec.Cwd, worktreePath)
	}
	request.LaunchSpecDigest = canonicalV19LaunchSpecDigest(request.Spec)
	request.RequestDigest = canonicalV19LaunchRequestDigest(request)
	return request, nil
}

func loadCanonicalV19LaunchCurrent(
	ctx context.Context,
	tx *sql.Tx,
	operationID string,
) (canonicalV19LaunchCurrent, error) {
	var current canonicalV19LaunchCurrent
	request := &current.Request
	var worktreePath string
	err := tx.QueryRowContext(ctx, `SELECT o.state,o.state_changed_at,o.operation_key,o.request_digest,
		o.project_id,o.task_id,o.plan_id,o.attempt_id,s.worktree_binding_id,l.session_binding_id,
		l.binding_id,o.adapter_ref,l.executable,l.cwd,l.launch_spec_digest,o.created_at,b.path
		FROM external_operation o
		JOIN launch_operation l ON l.operation_id=o.id AND l.attempt_id=o.attempt_id
		JOIN attempt a ON a.id=o.attempt_id AND a.lifecycle='active' AND a.terminal_at=''
		JOIN plan p ON p.id=a.plan_id AND p.id=o.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.id=o.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.id=o.project_id AND project_current.retired_at=''
		JOIN session_binding s ON s.id=l.session_binding_id AND s.attempt_id=a.id AND s.adapter_ref=o.adapter_ref
		JOIN attempt_worktree_binding b ON b.id=s.worktree_binding_id AND b.attempt_id=a.id
		JOIN operation_scope_claim executor_claim ON executor_claim.operation_id=o.id
		  AND executor_claim.scope_kind='executor-control' AND executor_claim.scope_key=l.binding_id
		JOIN operation_scope_claim session_claim ON session_claim.operation_id=o.id
		  AND session_claim.scope_kind='session' AND session_claim.scope_key=s.id
		WHERE o.id=? AND o.kind='launch'
		  AND o.primary_scope_kind='executor-control' AND o.primary_scope_key=l.binding_id
		  AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)
		  AND NOT EXISTS (SELECT 1 FROM worktree_binding_release r WHERE r.binding_id=b.id)
		  AND NOT EXISTS (
			SELECT 1 FROM executor_binding e WHERE e.attempt_id=a.id AND e.launch_operation_id<>o.id
		  )`, operationID).Scan(
		&current.State, &current.StateChangedAt, &request.OperationKey, &request.RequestDigest,
		&request.ProjectID, &request.TaskID, &request.PlanID, &request.AttemptID,
		&request.WorktreeBindingID, &request.SessionBindingID, &request.BindingID,
		&request.AdapterRef, &request.Spec.Executable, &request.Spec.Cwd,
		&request.LaunchSpecDigest, &request.CreatedAt, &worktreePath)
	if errors.Is(err, sql.ErrNoRows) {
		return canonicalV19LaunchCurrent{}, fmt.Errorf("%w: operation %q lacks exact current Launch request/ownership/claims", ErrCanonicalV19LaunchNotCurrent, operationID)
	}
	if err != nil {
		return canonicalV19LaunchCurrent{}, canonicalV19LaunchWriteError("current", "read exact Launch", err)
	}
	request.OperationID = operationID
	if request.Spec.Cwd != worktreePath {
		return canonicalV19LaunchCurrent{}, fmt.Errorf("%w: operation %q Launch cwd no longer matches exact WorktreeBinding", ErrCanonicalV19LaunchNotCurrent, operationID)
	}

	args, err := loadCanonicalV19LaunchArguments(ctx, tx, operationID)
	if err != nil {
		return canonicalV19LaunchCurrent{}, err
	}
	environment, err := loadCanonicalV19LaunchEnvironment(ctx, tx, operationID)
	if err != nil {
		return canonicalV19LaunchCurrent{}, err
	}
	request.Spec.Arguments = args
	request.Spec.Environment = environment
	if err := validateCanonicalV19LaunchSpec(request.Spec); err != nil {
		return canonicalV19LaunchCurrent{}, fmt.Errorf("%w: operation %q persisted LaunchSpec invalid: %v", ErrCanonicalV19LaunchNotCurrent, operationID, err)
	}
	if canonicalV19LaunchSpecDigest(request.Spec) != request.LaunchSpecDigest {
		return canonicalV19LaunchCurrent{}, fmt.Errorf("%w: operation %q LaunchSpec digest does not match persisted request", ErrCanonicalV19LaunchNotCurrent, operationID)
	}
	if canonicalV19LaunchRequestDigest(*request) != request.RequestDigest {
		return canonicalV19LaunchCurrent{}, fmt.Errorf("%w: operation %q request digest does not match persisted Launch", ErrCanonicalV19LaunchNotCurrent, operationID)
	}
	return current, nil
}

func loadCanonicalV19LaunchArguments(ctx context.Context, tx *sql.Tx, operationID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,value FROM launch_argument WHERE operation_id=? ORDER BY ordinal`, operationID)
	if err != nil {
		return nil, canonicalV19LaunchWriteError("current", "read Launch arguments", err)
	}
	defer func() { _ = rows.Close() }()
	arguments := make([]string, 0)
	for rows.Next() {
		var ordinal int
		var value string
		if err := rows.Scan(&ordinal, &value); err != nil {
			return nil, canonicalV19LaunchWriteError("current", "scan Launch argument", err)
		}
		if ordinal != len(arguments) {
			return nil, fmt.Errorf("%w: operation %q Launch argument ordinals are not contiguous", ErrCanonicalV19LaunchNotCurrent, operationID)
		}
		arguments = append(arguments, value)
	}
	if err := rows.Err(); err != nil {
		return nil, canonicalV19LaunchWriteError("current", "iterate Launch arguments", err)
	}
	return arguments, nil
}

func loadCanonicalV19LaunchEnvironment(
	ctx context.Context,
	tx *sql.Tx,
	operationID string,
) (map[string]CanonicalV19LaunchEnvironmentValue, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name,value_kind,value_material,value_digest
		FROM launch_environment WHERE operation_id=? ORDER BY name`, operationID)
	if err != nil {
		return nil, canonicalV19LaunchWriteError("current", "read Launch environment", err)
	}
	defer func() { _ = rows.Close() }()
	environment := make(map[string]CanonicalV19LaunchEnvironmentValue)
	for rows.Next() {
		var name string
		var value CanonicalV19LaunchEnvironmentValue
		if err := rows.Scan(&name, &value.ValueKind, &value.ValueMaterial, &value.ValueDigest); err != nil {
			return nil, canonicalV19LaunchWriteError("current", "scan Launch environment", err)
		}
		environment[name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, canonicalV19LaunchWriteError("current", "iterate Launch environment", err)
	}
	return environment, nil
}

func canonicalV19LaunchNonsuccessTransitionAllowed(from, to string) bool {
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

func canonicalV19LaunchSuccessTransitionAllowed(from string) bool {
	return from == "prepared" || from == "submitted" || from == "uncertain"
}

func canonicalV19LaunchSpecDigest(spec CanonicalV19LaunchSpec) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:launch-spec:v1")
	writeCanonicalV19DigestField(hash, "executable", spec.Executable)
	writeCanonicalV19DigestField(hash, "cwd", spec.Cwd)
	writeCanonicalV19DigestField(hash, "argument_count", fmt.Sprintf("%d", len(spec.Arguments)))
	for ordinal, value := range spec.Arguments {
		writeCanonicalV19DigestField(hash, fmt.Sprintf("argument_%d", ordinal), value)
	}
	names := canonicalV19LaunchEnvironmentNames(spec.Environment)
	writeCanonicalV19DigestField(hash, "environment_count", fmt.Sprintf("%d", len(names)))
	for _, name := range names {
		value := spec.Environment[name]
		writeCanonicalV19DigestField(hash, "environment_name", name)
		writeCanonicalV19DigestField(hash, "environment_kind", value.ValueKind)
		writeCanonicalV19DigestField(hash, "environment_material", value.ValueMaterial)
		writeCanonicalV19DigestField(hash, "environment_digest", value.ValueDigest)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalV19LaunchRequestDigest(request CanonicalV19LaunchRequest) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:launch-request:v1")
	writeCanonicalV19DigestField(hash, "project_id", request.ProjectID)
	writeCanonicalV19DigestField(hash, "task_id", request.TaskID)
	writeCanonicalV19DigestField(hash, "plan_id", request.PlanID)
	writeCanonicalV19DigestField(hash, "attempt_id", request.AttemptID)
	writeCanonicalV19DigestField(hash, "worktree_binding_id", request.WorktreeBindingID)
	writeCanonicalV19DigestField(hash, "session_binding_id", request.SessionBindingID)
	writeCanonicalV19DigestField(hash, "binding_id", request.BindingID)
	writeCanonicalV19DigestField(hash, "adapter_ref", request.AdapterRef)
	writeCanonicalV19DigestField(hash, "launch_spec_digest", request.LaunchSpecDigest)
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalV19LaunchEnvironmentNames(environment map[string]CanonicalV19LaunchEnvironmentValue) []string {
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func cloneCanonicalV19LaunchSpec(spec CanonicalV19LaunchSpec) CanonicalV19LaunchSpec {
	clone := CanonicalV19LaunchSpec{
		Executable: spec.Executable,
		Arguments:  append([]string(nil), spec.Arguments...),
		Cwd:        spec.Cwd,
	}
	if spec.Environment != nil {
		clone.Environment = make(map[string]CanonicalV19LaunchEnvironmentValue, len(spec.Environment))
		for name, value := range spec.Environment {
			clone.Environment[name] = value
		}
	}
	return clone
}

func requireCanonicalV19LaunchOneChanged(result sql.Result, operation, operationID string) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return canonicalV19LaunchWriteError(operation, "count exact state transition", err)
	}
	if changed != 1 {
		return fmt.Errorf("%s canonical v19 Launch: %w: operation %q changed %d rows, want 1", operation, ErrCanonicalV19LaunchTransition, operationID, changed)
	}
	return nil
}

func canonicalV19LaunchConstraintError(operation, action, operationID string, err error) error {
	if isSQLiteConstraint(err) {
		return fmt.Errorf("%s canonical v19 Launch: %w: operation %q", operation, ErrCanonicalV19LaunchConflict, operationID)
	}
	return canonicalV19LaunchWriteError(operation, action, err)
}

func canonicalV19LaunchWriteError(operation, action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("%s canonical v19 Launch: %s: %w", operation, action, ErrContention)
	}
	return fmt.Errorf("%s canonical v19 Launch: %s: %w", operation, action, err)
}
