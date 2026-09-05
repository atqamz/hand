package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrCanonicalV19TaskHoldConflict marks an exact TaskHold identity, ordinal,
// typed-child, or resolution constraint conflict. Callers must not retarget.
var ErrCanonicalV19TaskHoldConflict = errors.New("canonical v19 task hold write conflict")

// ErrCanonicalV19TaskHoldNotCurrent marks a TaskHold or owning Task that is no
// longer exact and current. Callers must never resolve a successor implicitly.
var ErrCanonicalV19TaskHoldNotCurrent = errors.New("canonical v19 task hold is not current")

// CanonicalV19TaskHoldCreateInput is immutable Task-level deferral evidence.
type CanonicalV19TaskHoldCreateInput struct {
	ID               string
	TaskID           string
	Kind             string
	Reason           string
	EvidenceDigest   string
	CreatedAt        string
	BlockedOnTaskID  string
	DecisionID       string
	RecheckNotBefore string
}

// CanonicalV19TaskHoldResolveInput closes one exact unresolved TaskHold with
// immutable resolution evidence.
type CanonicalV19TaskHoldResolveInput struct {
	HoldID         string
	Resolution     string
	ResolvedAt     string
	EvidenceDigest string
}

// CreateCanonicalV19TaskHold creates one immutable TaskHold and any requested
// typed child relations in one canonical writer transaction.
func CreateCanonicalV19TaskHold(
	ctx context.Context,
	homeDir string,
	input CanonicalV19TaskHoldCreateInput,
) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19TaskHoldCreateInput(input); err != nil {
		return 0, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, canonicalV19TaskHoldWriteError("create", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return 0, fmt.Errorf("create canonical v19 TaskHold: %w", err)
	}
	if err := requireCanonicalV19TaskHoldOwnerCurrent(ctx, tx, input.TaskID); err != nil {
		return 0, fmt.Errorf("create canonical v19 TaskHold: %w", err)
	}

	ordinal, err := nextCanonicalV19TaskHoldOrdinal(ctx, tx, input.TaskID)
	if err != nil {
		return 0, canonicalV19TaskHoldWriteError("create", "allocate ordinal", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_hold(
		id,task_id,ordinal,kind,reason,evidence_digest,created_at
	) VALUES(?,?,?,?,?,?,?)`,
		input.ID, input.TaskID, ordinal, input.Kind, input.Reason,
		input.EvidenceDigest, input.CreatedAt); err != nil {
		return 0, canonicalV19TaskHoldConstraintError("create", "insert TaskHold", input.ID, err)
	}

	if input.BlockedOnTaskID != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_hold_blocked_on_task(hold_id,blocked_on_task_id)
			VALUES(?,?)`, input.ID, input.BlockedOnTaskID); err != nil {
			return 0, canonicalV19TaskHoldConstraintError("create", "insert blocked-on Task relation", input.ID, err)
		}
	}
	if input.DecisionID != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_hold_decision(hold_id,decision_id)
			VALUES(?,?)`, input.ID, input.DecisionID); err != nil {
			return 0, canonicalV19TaskHoldConstraintError("create", "insert Decision relation", input.ID, err)
		}
	}
	if input.RecheckNotBefore != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_hold_recheck(hold_id,not_before)
			VALUES(?,?)`, input.ID, input.RecheckNotBefore); err != nil {
			return 0, canonicalV19TaskHoldConstraintError("create", "insert recheck relation", input.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, canonicalV19TaskHoldWriteError("create", "commit writer", err)
	}
	committed = true
	return ordinal, nil
}

// ResolveCanonicalV19TaskHold resolves only the named exact unresolved Hold
// while its owning Project/Task lineage remains current.
func ResolveCanonicalV19TaskHold(
	ctx context.Context,
	homeDir string,
	input CanonicalV19TaskHoldResolveInput,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19TaskHoldResolveInput(input); err != nil {
		return err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19TaskHoldWriteError("resolve", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("resolve canonical v19 TaskHold: %w", err)
	}
	if err := requireCanonicalV19TaskHoldCurrent(ctx, tx, input.HoldID); err != nil {
		return fmt.Errorf("resolve canonical v19 TaskHold: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO task_hold_resolution(
		hold_id,resolution,resolved_at,evidence_digest
	) VALUES(?,?,?,?)`, input.HoldID, input.Resolution, input.ResolvedAt, input.EvidenceDigest); err != nil {
		return canonicalV19TaskHoldConstraintError("resolve", "insert TaskHold resolution", input.HoldID, err)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19TaskHoldWriteError("resolve", "commit writer", err)
	}
	committed = true
	return nil
}

func validateCanonicalV19TaskHoldCreateInput(input CanonicalV19TaskHoldCreateInput) error {
	for name, value := range map[string]string{
		"TaskHold ID":     input.ID,
		"Task ID":         input.TaskID,
		"evidence digest": input.EvidenceDigest,
		"created_at":      input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("create canonical v19 TaskHold: %s is empty", name)
		}
	}
	switch input.Kind {
	case "operator", "blocked":
	default:
		return fmt.Errorf("create canonical v19 TaskHold: kind %q is not canonical", input.Kind)
	}
	if input.BlockedOnTaskID != "" && input.Kind != "blocked" {
		return fmt.Errorf("create canonical v19 TaskHold: blocked-on Task requires blocked kind")
	}
	return nil
}

func validateCanonicalV19TaskHoldResolveInput(input CanonicalV19TaskHoldResolveInput) error {
	for name, value := range map[string]string{
		"TaskHold ID":     input.HoldID,
		"resolved_at":     input.ResolvedAt,
		"evidence digest": input.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("resolve canonical v19 TaskHold: %s is empty", name)
		}
	}
	switch input.Resolution {
	case "released", "cancelled", "superseded":
		return nil
	default:
		return fmt.Errorf("resolve canonical v19 TaskHold: resolution %q is not canonical", input.Resolution)
	}
}

func requireCanonicalV19TaskHoldOwnerCurrent(ctx context.Context, tx *sql.Tx, taskID string) error {
	var exactTaskID string
	err := tx.QueryRowContext(ctx, `SELECT t.id
		FROM task t
		JOIN project p ON p.id=t.project_id AND p.retired_at=''
		WHERE t.id=? AND t.lifecycle='active' AND t.terminal_at=''`, taskID).Scan(&exactTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: Task %q does not have exact active Project/Task lineage",
			ErrCanonicalV19TaskHoldNotCurrent, taskID)
	}
	if err != nil {
		return canonicalV19TaskHoldWriteError("create", "read exact current Task lineage", err)
	}
	if exactTaskID != taskID {
		return fmt.Errorf("%w: Task identity changed", ErrCanonicalV19TaskHoldNotCurrent)
	}
	return nil
}

func nextCanonicalV19TaskHoldOrdinal(ctx context.Context, tx *sql.Tx, taskID string) (int64, error) {
	var ordinal int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(ordinal),0)+1 FROM task_hold WHERE task_id=?`, taskID,
	).Scan(&ordinal); err != nil {
		return 0, err
	}
	return ordinal, nil
}

func requireCanonicalV19TaskHoldCurrent(ctx context.Context, tx *sql.Tx, holdID string) error {
	var exactHoldID string
	err := tx.QueryRowContext(ctx, `SELECT h.id
		FROM task_hold h
		JOIN task t ON t.id=h.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project p ON p.id=t.project_id AND p.retired_at=''
		LEFT JOIN task_hold_resolution r ON r.hold_id=h.id
		WHERE h.id=? AND r.hold_id IS NULL`, holdID).Scan(&exactHoldID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: TaskHold %q is not unresolved under exact active lineage",
			ErrCanonicalV19TaskHoldNotCurrent, holdID)
	}
	if err != nil {
		return canonicalV19TaskHoldWriteError("resolve", "read exact unresolved TaskHold", err)
	}
	if exactHoldID != holdID {
		return fmt.Errorf("%w: TaskHold identity changed", ErrCanonicalV19TaskHoldNotCurrent)
	}
	return nil
}

func canonicalV19TaskHoldConstraintError(operation, action, holdID string, err error) error {
	if isSQLiteConstraint(err) {
		return fmt.Errorf("%s canonical v19 TaskHold: %w: TaskHold %q", operation, ErrCanonicalV19TaskHoldConflict, holdID)
	}
	return canonicalV19TaskHoldWriteError(operation, action, err)
}

func canonicalV19TaskHoldWriteError(operation, action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("%s canonical v19 TaskHold: %s: %w", operation, action, ErrContention)
	}
	return fmt.Errorf("%s canonical v19 TaskHold: %s: %w", operation, action, err)
}
