package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CanonicalV19AttemptTerminalizeInput names one exact Attempt terminal
// transition. Terminal evidence is immutable once committed.
type CanonicalV19AttemptTerminalizeInput struct {
	AttemptID         string
	Lifecycle         string
	TerminalAt        string
	BackoffResolution *CanonicalV19AttemptBackoffResolveInput
}

// TerminalizeCanonicalV19Attempt transitions only the named exact active Attempt
// under current Project/Task/Plan lineage. An optional unresolved Backoff closure
// is committed in the same writer transaction before terminalization.
func TerminalizeCanonicalV19Attempt(ctx context.Context, homeDir string, input CanonicalV19AttemptTerminalizeInput) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19AttemptTerminalizeInput(input); err != nil {
		return err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19AttemptTerminalWriteError("begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("terminalize canonical v19 Attempt: %w", err)
	}
	if err := requireCanonicalV19AttemptCurrent(ctx, tx, input.AttemptID); err != nil {
		return fmt.Errorf("terminalize canonical v19 Attempt: %w", err)
	}
	if input.BackoffResolution != nil {
		if err := resolveCanonicalV19AttemptBackoffForTerminalization(
			ctx, tx, input.AttemptID, *input.BackoffResolution,
		); err != nil {
			return fmt.Errorf("terminalize canonical v19 Attempt: %w", err)
		}
	}

	result, err := tx.ExecContext(ctx, `UPDATE attempt
		SET lifecycle=?, terminal_at=?
		WHERE id=? AND lifecycle='active' AND terminal_at=''`,
		input.Lifecycle, input.TerminalAt, input.AttemptID)
	if err != nil {
		if isSQLiteConstraint(err) {
			return fmt.Errorf("terminalize canonical v19 Attempt: %w: Attempt %q", ErrCanonicalV19AttemptConflict, input.AttemptID)
		}
		return canonicalV19AttemptTerminalWriteError("update exact Attempt", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return canonicalV19AttemptTerminalWriteError("count exact Attempt transition", err)
	}
	if changed != 1 {
		return fmt.Errorf("terminalize canonical v19 Attempt: %w: Attempt %q changed %d rows, want 1", ErrCanonicalV19AttemptNotCurrent, input.AttemptID, changed)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19AttemptTerminalWriteError("commit writer", err)
	}
	committed = true
	return nil
}

func validateCanonicalV19AttemptTerminalizeInput(input CanonicalV19AttemptTerminalizeInput) error {
	if input.AttemptID == "" {
		return fmt.Errorf("terminalize canonical v19 Attempt: Attempt ID is empty")
	}
	switch input.Lifecycle {
	case "completed", "failed", "interrupted":
	default:
		return fmt.Errorf("terminalize canonical v19 Attempt: lifecycle %q is not canonical terminal state", input.Lifecycle)
	}
	if input.TerminalAt == "" {
		return fmt.Errorf("terminalize canonical v19 Attempt: terminal_at is empty")
	}
	if input.BackoffResolution != nil {
		if err := validateCanonicalV19AttemptBackoffResolveInput(*input.BackoffResolution); err != nil {
			return fmt.Errorf("terminalize canonical v19 Attempt: Backoff resolution: %w", err)
		}
	}
	return nil
}

func requireCanonicalV19AttemptCurrent(ctx context.Context, tx *sql.Tx, attemptID string) error {
	var exactAttemptID string
	err := tx.QueryRowContext(ctx, `SELECT a.id
		FROM attempt a
		JOIN plan p ON p.id=a.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.retired_at=''
		WHERE a.id=? AND a.lifecycle='active' AND a.terminal_at=''`, attemptID).Scan(&exactAttemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: Attempt %q does not have exact active Project/Task/Plan/Attempt lineage", ErrCanonicalV19AttemptNotCurrent, attemptID)
	}
	if err != nil {
		return canonicalV19AttemptTerminalWriteError("read exact current Attempt lineage", err)
	}
	if exactAttemptID != attemptID {
		return fmt.Errorf("%w: Attempt identity changed", ErrCanonicalV19AttemptNotCurrent)
	}
	return nil
}

func canonicalV19AttemptTerminalWriteError(action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("terminalize canonical v19 Attempt: %s: %w", action, ErrContention)
	}
	return fmt.Errorf("terminalize canonical v19 Attempt: %s: %w", action, err)
}
