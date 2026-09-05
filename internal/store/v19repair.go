package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrCanonicalV19RepairConflict marks an exact Repair identity, target, or
// resolution constraint conflict. Callers must never retarget another repair.
var ErrCanonicalV19RepairConflict = errors.New("canonical v19 repair write conflict")

// ErrCanonicalV19RepairNotCurrent marks a Repair or exact typed target that is
// no longer current. Historical evidence is never applied to a successor.
var ErrCanonicalV19RepairNotCurrent = errors.New("canonical v19 repair is not current")

// ErrCanonicalV19RepairTargetUnsupported marks a locked #344 target family
// whose canonical lifecycle writer/currentness predicate has not landed yet.
var ErrCanonicalV19RepairTargetUnsupported = errors.New("canonical v19 repair target family is not implemented")

// CanonicalV19RepairTarget is the exact #344 FK-backed typed target sum. Exactly
// one field must be non-empty. Later resource families remain fail-closed until
// their canonical lifecycle/currentness writers land.
type CanonicalV19RepairTarget struct {
	ProjectID           string
	WorkspaceBindingID  string
	TaskID              string
	PlanID              string
	AttemptID           string
	ExternalOperationID string
	WorktreeBindingID   string
	SessionBindingID    string
	ExecutorBindingID   string
}

// CanonicalV19RepairCreateInput is immutable diagnosis/reconciliation evidence.
type CanonicalV19RepairCreateInput struct {
	ID             string
	RepairCode     string
	Reason         string
	EvidenceDigest string
	CreatedAt      string
	Target         CanonicalV19RepairTarget
}

// CanonicalV19RepairResolveInput closes one exact unresolved Repair with
// immutable bounded evidence. ActorRef is required for operator attestation.
type CanonicalV19RepairResolveInput struct {
	RepairID       string
	Resolution     string
	ResolvedAt     string
	EvidenceDigest string
	ActorRef       string
}

// CreateCanonicalV19Repair creates one immutable Repair and exactly one typed
// target in the same canonical writer transaction.
func CreateCanonicalV19Repair(ctx context.Context, homeDir string, input CanonicalV19RepairCreateInput) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19RepairCreateInput(input); err != nil {
		return err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19RepairWriteError("create", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("create canonical v19 Repair: %w", err)
	}
	if err := requireCanonicalV19RepairTargetCurrent(ctx, tx, input.Target); err != nil {
		return fmt.Errorf("create canonical v19 Repair: %w", err)
	}

	values := canonicalV19RepairTargetValues(input.Target)
	if _, err := tx.ExecContext(ctx, `INSERT INTO repair_target(
		repair_id,project_id,workspace_binding_id,task_id,plan_id,attempt_id,
		external_operation_id,worktree_binding_id,session_binding_id,executor_binding_id
	) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		input.ID, values[0], values[1], values[2], values[3], values[4],
		values[5], values[6], values[7], values[8]); err != nil {
		return canonicalV19RepairConstraintError("create", "insert Repair target", input.ID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO repair(
		id,repair_code,reason,evidence_digest,created_at
	) VALUES(?,?,?,?,?)`, input.ID, input.RepairCode, input.Reason, input.EvidenceDigest, input.CreatedAt); err != nil {
		return canonicalV19RepairConstraintError("create", "insert Repair", input.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19RepairWriteError("create", "commit writer", err)
	}
	committed = true
	return nil
}

// ResolveCanonicalV19Repair resolves only the named exact unresolved Repair
// after revalidating the typed target captured by that Repair itself.
func ResolveCanonicalV19Repair(ctx context.Context, homeDir string, input CanonicalV19RepairResolveInput) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19RepairResolveInput(input); err != nil {
		return err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19RepairWriteError("resolve", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("resolve canonical v19 Repair: %w", err)
	}
	target, err := loadCanonicalV19UnresolvedRepairTarget(ctx, tx, input.RepairID)
	if err != nil {
		return fmt.Errorf("resolve canonical v19 Repair: %w", err)
	}
	if err := requireCanonicalV19RepairTargetCurrent(ctx, tx, target); err != nil {
		return fmt.Errorf("resolve canonical v19 Repair: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO repair_resolution(
		repair_id,resolution,resolved_at,evidence_digest,actor_ref
	) VALUES(?,?,?,?,?)`, input.RepairID, input.Resolution, input.ResolvedAt, input.EvidenceDigest, input.ActorRef); err != nil {
		return canonicalV19RepairConstraintError("resolve", "insert Repair resolution", input.RepairID, err)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19RepairWriteError("resolve", "commit writer", err)
	}
	committed = true
	return nil
}

func validateCanonicalV19RepairCreateInput(input CanonicalV19RepairCreateInput) error {
	for name, value := range map[string]string{
		"Repair ID":       input.ID,
		"repair_code":     input.RepairCode,
		"evidence digest": input.EvidenceDigest,
		"created_at":      input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("create canonical v19 Repair: %s is empty", name)
		}
	}
	if len(input.RepairCode) > 128 {
		return fmt.Errorf("create canonical v19 Repair: repair_code exceeds 128 bytes")
	}
	if countCanonicalV19RepairTargets(input.Target) != 1 {
		return fmt.Errorf("create canonical v19 Repair: exactly one typed target is required")
	}
	return requireCanonicalV19RepairTargetFamilySupported(input.Target)
}

func validateCanonicalV19RepairResolveInput(input CanonicalV19RepairResolveInput) error {
	for name, value := range map[string]string{
		"Repair ID":       input.RepairID,
		"resolved_at":     input.ResolvedAt,
		"evidence digest": input.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("resolve canonical v19 Repair: %s is empty", name)
		}
	}
	switch input.Resolution {
	case "repaired", "no-longer-applicable", "superseded":
		return nil
	case "operator-attested":
		if input.ActorRef == "" {
			return fmt.Errorf("resolve canonical v19 Repair: operator-attested requires actor_ref")
		}
		return nil
	default:
		return fmt.Errorf("resolve canonical v19 Repair: resolution %q is not canonical", input.Resolution)
	}
}

func countCanonicalV19RepairTargets(target CanonicalV19RepairTarget) int {
	count := 0
	for _, value := range []string{
		target.ProjectID,
		target.WorkspaceBindingID,
		target.TaskID,
		target.PlanID,
		target.AttemptID,
		target.ExternalOperationID,
		target.WorktreeBindingID,
		target.SessionBindingID,
		target.ExecutorBindingID,
	} {
		if value != "" {
			count++
		}
	}
	return count
}

func requireCanonicalV19RepairTargetFamilySupported(target CanonicalV19RepairTarget) error {
	switch {
	case target.ProjectID != "", target.WorkspaceBindingID != "", target.TaskID != "", target.PlanID != "", target.AttemptID != "":
		return nil
	case target.ExternalOperationID != "":
		return fmt.Errorf("%w: external_operation", ErrCanonicalV19RepairTargetUnsupported)
	case target.WorktreeBindingID != "":
		return fmt.Errorf("%w: WorktreeBinding", ErrCanonicalV19RepairTargetUnsupported)
	case target.SessionBindingID != "":
		return fmt.Errorf("%w: SessionBinding", ErrCanonicalV19RepairTargetUnsupported)
	case target.ExecutorBindingID != "":
		return fmt.Errorf("%w: ExecutorBinding", ErrCanonicalV19RepairTargetUnsupported)
	default:
		return fmt.Errorf("create canonical v19 Repair: exactly one typed target is required")
	}
}

func canonicalV19RepairTargetValues(target CanonicalV19RepairTarget) [9]any {
	return [9]any{
		canonicalV19RepairNullable(target.ProjectID),
		canonicalV19RepairNullable(target.WorkspaceBindingID),
		canonicalV19RepairNullable(target.TaskID),
		canonicalV19RepairNullable(target.PlanID),
		canonicalV19RepairNullable(target.AttemptID),
		canonicalV19RepairNullable(target.ExternalOperationID),
		canonicalV19RepairNullable(target.WorktreeBindingID),
		canonicalV19RepairNullable(target.SessionBindingID),
		canonicalV19RepairNullable(target.ExecutorBindingID),
	}
}

func canonicalV19RepairNullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func requireCanonicalV19RepairTargetCurrent(ctx context.Context, tx *sql.Tx, target CanonicalV19RepairTarget) error {
	if countCanonicalV19RepairTargets(target) != 1 {
		return fmt.Errorf("%w: Repair target shape is not exactly one typed target", ErrCanonicalV19RepairNotCurrent)
	}
	if err := requireCanonicalV19RepairTargetFamilySupported(target); err != nil {
		return err
	}

	var exactID string
	var err error
	var wanted string
	switch {
	case target.ProjectID != "":
		wanted = target.ProjectID
		err = tx.QueryRowContext(ctx, `SELECT id FROM project WHERE id=? AND retired_at=''`, wanted).Scan(&exactID)
	case target.WorkspaceBindingID != "":
		wanted = target.WorkspaceBindingID
		err = tx.QueryRowContext(ctx, `SELECT w.id FROM workspace_binding w
			JOIN project p ON p.id=w.project_id AND p.retired_at=''
			WHERE w.id=? AND w.superseded_at=''`, wanted).Scan(&exactID)
	case target.TaskID != "":
		wanted = target.TaskID
		err = tx.QueryRowContext(ctx, `SELECT t.id FROM task t
			JOIN project p ON p.id=t.project_id AND p.retired_at=''
			WHERE t.id=? AND t.lifecycle='active' AND t.terminal_at=''`, wanted).Scan(&exactID)
	case target.PlanID != "":
		wanted = target.PlanID
		err = tx.QueryRowContext(ctx, `SELECT plan_current.id FROM plan plan_current
			JOIN task t ON t.id=plan_current.task_id AND t.lifecycle='active' AND t.terminal_at=''
			JOIN project p ON p.id=t.project_id AND p.retired_at=''
			WHERE plan_current.id=? AND plan_current.lifecycle='active' AND plan_current.terminal_at=''`, wanted).Scan(&exactID)
	case target.AttemptID != "":
		wanted = target.AttemptID
		err = tx.QueryRowContext(ctx, `SELECT a.id FROM attempt a
			JOIN plan plan_current ON plan_current.id=a.plan_id AND plan_current.lifecycle='active' AND plan_current.terminal_at=''
			JOIN task t ON t.id=plan_current.task_id AND t.lifecycle='active' AND t.terminal_at=''
			JOIN project p ON p.id=t.project_id AND p.retired_at=''
			WHERE a.id=? AND a.lifecycle='active' AND a.terminal_at=''`, wanted).Scan(&exactID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: exact typed target %q is not current", ErrCanonicalV19RepairNotCurrent, wanted)
	}
	if err != nil {
		return canonicalV19RepairWriteError("currentness", "read exact typed target", err)
	}
	if exactID != wanted {
		return fmt.Errorf("%w: typed target identity changed", ErrCanonicalV19RepairNotCurrent)
	}
	return nil
}

func loadCanonicalV19UnresolvedRepairTarget(ctx context.Context, tx *sql.Tx, repairID string) (CanonicalV19RepairTarget, error) {
	var project, workspace, task, plan, attempt sql.NullString
	var operation, worktree, session, executor sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT
		t.project_id,t.workspace_binding_id,t.task_id,t.plan_id,t.attempt_id,
		t.external_operation_id,t.worktree_binding_id,t.session_binding_id,t.executor_binding_id
		FROM repair r
		JOIN repair_target t ON t.repair_id=r.id
		LEFT JOIN repair_resolution rr ON rr.repair_id=r.id
		WHERE r.id=? AND rr.repair_id IS NULL`, repairID).Scan(
		&project, &workspace, &task, &plan, &attempt,
		&operation, &worktree, &session, &executor,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19RepairTarget{}, fmt.Errorf("%w: Repair %q is not exact and unresolved", ErrCanonicalV19RepairNotCurrent, repairID)
	}
	if err != nil {
		return CanonicalV19RepairTarget{}, canonicalV19RepairWriteError("resolve", "load exact unresolved Repair target", err)
	}
	return CanonicalV19RepairTarget{
		ProjectID:           project.String,
		WorkspaceBindingID:  workspace.String,
		TaskID:              task.String,
		PlanID:              plan.String,
		AttemptID:           attempt.String,
		ExternalOperationID: operation.String,
		WorktreeBindingID:   worktree.String,
		SessionBindingID:    session.String,
		ExecutorBindingID:   executor.String,
	}, nil
}

func canonicalV19RepairConstraintError(operation, action, repairID string, err error) error {
	if isSQLiteConstraint(err) {
		return fmt.Errorf("%s canonical v19 Repair: %w: Repair %q", operation, ErrCanonicalV19RepairConflict, repairID)
	}
	return canonicalV19RepairWriteError(operation, action, err)
}

func canonicalV19RepairWriteError(operation, action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("%s canonical v19 Repair: %s: %w", operation, action, ErrContention)
	}
	return fmt.Errorf("%s canonical v19 Repair: %s: %w", operation, action, err)
}
