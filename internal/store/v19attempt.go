package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrCanonicalV19AttemptConflict marks an Attempt identity, ordinal, or active
// child write that lost an exact relational constraint.
var ErrCanonicalV19AttemptConflict = errors.New("canonical v19 attempt write conflict")

// ErrCanonicalV19AttemptNotCurrent marks an exact Project/Task/Plan lineage
// that is no longer current. Callers must never retarget another Plan.
var ErrCanonicalV19AttemptNotCurrent = errors.New("canonical v19 attempt lineage is not current")

// CanonicalV19AttemptCreateInput is the immutable resolved execution
// provenance frozen by one Attempt.
type CanonicalV19AttemptCreateInput struct {
	ID                   string
	PlanID               string
	WorkerHarnessRef     string
	WorkerHarnessVersion string
	WorkerProfileRef     string
	ModelRef             string
	EffortRef            string
	SessionAdapterRef    string
	CreatedAt            string
}

// CreateCanonicalV19Attempt creates one fresh active Attempt under the exact
// active Plan. Retry is the same operation after the previous Attempt became
// terminal; this writer never terminalizes or retargets existing Attempt rows.
func CreateCanonicalV19Attempt(ctx context.Context, homeDir string, input CanonicalV19AttemptCreateInput) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19AttemptCreateInput(input); err != nil {
		return 0, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, canonicalV19AttemptWriteError("begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return 0, fmt.Errorf("create canonical v19 Attempt: %w", err)
	}
	if err := requireCanonicalV19AttemptPlanCurrent(ctx, tx, input.PlanID); err != nil {
		return 0, fmt.Errorf("create canonical v19 Attempt: %w", err)
	}
	if err := requireCanonicalV19AttemptSlotOpen(ctx, tx, input.PlanID); err != nil {
		return 0, fmt.Errorf("create canonical v19 Attempt: %w", err)
	}

	ordinal, err := nextCanonicalV19AttemptOrdinal(ctx, tx, input.PlanID)
	if err != nil {
		return 0, canonicalV19AttemptWriteError("allocate ordinal", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO attempt(
		id,plan_id,ordinal,worker_harness_ref,worker_harness_version,worker_profile_ref,
		model_ref,effort_ref,session_adapter_ref,lifecycle,created_at,terminal_at
	) VALUES(?,?,?,?,?,?,?,?,?,'active',?,'')`,
		input.ID, input.PlanID, ordinal, input.WorkerHarnessRef, input.WorkerHarnessVersion,
		input.WorkerProfileRef, input.ModelRef, input.EffortRef, input.SessionAdapterRef, input.CreatedAt)
	if err != nil {
		if isSQLiteConstraint(err) {
			return 0, fmt.Errorf("create canonical v19 Attempt: %w: Attempt %q", ErrCanonicalV19AttemptConflict, input.ID)
		}
		return 0, canonicalV19AttemptWriteError("insert Attempt", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, canonicalV19AttemptWriteError("commit writer", err)
	}
	committed = true
	return ordinal, nil
}

func validateCanonicalV19AttemptCreateInput(input CanonicalV19AttemptCreateInput) error {
	for name, value := range map[string]string{
		"Attempt ID":          input.ID,
		"Plan ID":             input.PlanID,
		"Worker Harness ref":  input.WorkerHarnessRef,
		"Session adapter ref": input.SessionAdapterRef,
		"created_at":          input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("create canonical v19 Attempt: %s is empty", name)
		}
	}
	return nil
}

func requireCanonicalV19AttemptPlanCurrent(ctx context.Context, tx *sql.Tx, planID string) error {
	var exactPlanID string
	err := tx.QueryRowContext(ctx, `SELECT p.id
		FROM plan p
		JOIN task t ON t.id=p.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.retired_at=''
		WHERE p.id=? AND p.lifecycle='active' AND p.terminal_at=''`, planID).Scan(&exactPlanID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: Plan %q does not have exact active Project/Task/Plan lineage", ErrCanonicalV19AttemptNotCurrent, planID)
	}
	if err != nil {
		return canonicalV19AttemptWriteError("read exact current Plan lineage", err)
	}
	if exactPlanID != planID {
		return fmt.Errorf("%w: Plan identity changed", ErrCanonicalV19AttemptNotCurrent)
	}
	return nil
}

func requireCanonicalV19AttemptSlotOpen(ctx context.Context, tx *sql.Tx, planID string) error {
	var activeAttemptID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM attempt WHERE plan_id=? AND lifecycle='active'`, planID).Scan(&activeAttemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return canonicalV19AttemptWriteError("read active Attempt", err)
	}
	return fmt.Errorf("%w: Plan %q already has active Attempt %q", ErrCanonicalV19AttemptConflict, planID, activeAttemptID)
}

func nextCanonicalV19AttemptOrdinal(ctx context.Context, tx *sql.Tx, planID string) (int64, error) {
	var ordinal int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ordinal),0)+1 FROM attempt WHERE plan_id=?`, planID).Scan(&ordinal); err != nil {
		return 0, err
	}
	return ordinal, nil
}

func canonicalV19AttemptWriteError(action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("create canonical v19 Attempt: %s: %w", action, ErrContention)
	}
	return fmt.Errorf("create canonical v19 Attempt: %s: %w", action, err)
}
