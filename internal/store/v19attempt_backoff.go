package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrCanonicalV19AttemptBackoffConflict marks an exact Backoff identity,
// ordinal, or unresolved-slot conflict.
var ErrCanonicalV19AttemptBackoffConflict = errors.New("canonical v19 attempt backoff write conflict")

// ErrCanonicalV19AttemptBackoffNotCurrent marks a Backoff or owning Attempt
// that is no longer exact and current. Callers must never retarget it.
var ErrCanonicalV19AttemptBackoffNotCurrent = errors.New("canonical v19 attempt backoff is not current")

// CanonicalV19AttemptBackoffCreateInput is the immutable evidence for one
// exact active Attempt delay/re-observation episode.
type CanonicalV19AttemptBackoffCreateInput struct {
	ID             string
	AttemptID      string
	Reason         string
	NotBefore      string
	EvidenceDigest string
	CreatedAt      string
}

// CanonicalV19AttemptBackoffResolveInput closes one exact unresolved Backoff
// with immutable resolution evidence.
type CanonicalV19AttemptBackoffResolveInput struct {
	BackoffID      string
	Resolution     string
	ResolvedAt     string
	EvidenceDigest string
}

// CreateCanonicalV19AttemptBackoff creates one immutable Backoff for the exact
// current Attempt. It never retargets a successor Attempt or resolves an old
// episode implicitly.
func CreateCanonicalV19AttemptBackoff(
	ctx context.Context,
	homeDir string,
	input CanonicalV19AttemptBackoffCreateInput,
) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19AttemptBackoffCreateInput(input); err != nil {
		return 0, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, canonicalV19AttemptBackoffWriteError("create", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return 0, fmt.Errorf("create canonical v19 AttemptBackoff: %w", err)
	}
	if err := requireCanonicalV19AttemptBackoffOwnerCurrent(ctx, tx, input.AttemptID); err != nil {
		return 0, fmt.Errorf("create canonical v19 AttemptBackoff: %w", err)
	}
	if err := requireCanonicalV19AttemptBackoffSlotOpen(ctx, tx, input.AttemptID); err != nil {
		return 0, fmt.Errorf("create canonical v19 AttemptBackoff: %w", err)
	}

	ordinal, err := nextCanonicalV19AttemptBackoffOrdinal(ctx, tx, input.AttemptID)
	if err != nil {
		return 0, canonicalV19AttemptBackoffWriteError("create", "allocate ordinal", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO attempt_backoff(
		id,attempt_id,ordinal,reason,not_before,evidence_digest,created_at
	) VALUES(?,?,?,?,?,?,?)`,
		input.ID, input.AttemptID, ordinal, input.Reason, input.NotBefore,
		input.EvidenceDigest, input.CreatedAt)
	if err != nil {
		if isSQLiteConstraint(err) {
			return 0, fmt.Errorf("create canonical v19 AttemptBackoff: %w: Backoff %q",
				ErrCanonicalV19AttemptBackoffConflict, input.ID)
		}
		return 0, canonicalV19AttemptBackoffWriteError("create", "insert Backoff", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, canonicalV19AttemptBackoffWriteError("create", "commit writer", err)
	}
	committed = true
	return ordinal, nil
}

// ResolveCanonicalV19AttemptBackoff resolves only the named exact unresolved
// Backoff while its owning Project/Task/Plan/Attempt lineage remains current.
func ResolveCanonicalV19AttemptBackoff(
	ctx context.Context,
	homeDir string,
	input CanonicalV19AttemptBackoffResolveInput,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19AttemptBackoffResolveInput(input); err != nil {
		return err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19AttemptBackoffWriteError("resolve", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("resolve canonical v19 AttemptBackoff: %w", err)
	}
	if _, err := requireCanonicalV19AttemptBackoffCurrent(ctx, tx, "resolve", input.BackoffID); err != nil {
		return fmt.Errorf("resolve canonical v19 AttemptBackoff: %w", err)
	}
	if err := insertCanonicalV19AttemptBackoffResolution(ctx, tx, "resolve", input); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19AttemptBackoffWriteError("resolve", "commit writer", err)
	}
	committed = true
	return nil
}

func validateCanonicalV19AttemptBackoffCreateInput(input CanonicalV19AttemptBackoffCreateInput) error {
	for name, value := range map[string]string{
		"Backoff ID":      input.ID,
		"Attempt ID":      input.AttemptID,
		"not_before":      input.NotBefore,
		"evidence digest": input.EvidenceDigest,
		"created_at":      input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("create canonical v19 AttemptBackoff: %s is empty", name)
		}
	}
	switch input.Reason {
	case "usage-limit", "rate-limit", "provider-transient":
		return nil
	default:
		return fmt.Errorf("create canonical v19 AttemptBackoff: reason %q is not canonical", input.Reason)
	}
}

func validateCanonicalV19AttemptBackoffResolveInput(input CanonicalV19AttemptBackoffResolveInput) error {
	for name, value := range map[string]string{
		"Backoff ID":      input.BackoffID,
		"resolved_at":     input.ResolvedAt,
		"evidence digest": input.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("resolve canonical v19 AttemptBackoff: %s is empty", name)
		}
	}
	switch input.Resolution {
	case "resumed", "cancelled", "superseded":
		return nil
	default:
		return fmt.Errorf("resolve canonical v19 AttemptBackoff: resolution %q is not canonical", input.Resolution)
	}
}

func requireCanonicalV19AttemptBackoffOwnerCurrent(ctx context.Context, tx *sql.Tx, attemptID string) error {
	var exactAttemptID string
	err := tx.QueryRowContext(ctx, `SELECT a.id
		FROM attempt a
		JOIN plan p ON p.id=a.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.retired_at=''
		WHERE a.id=? AND a.lifecycle='active' AND a.terminal_at=''`, attemptID).Scan(&exactAttemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: Attempt %q does not have exact active Project/Task/Plan/Attempt lineage",
			ErrCanonicalV19AttemptBackoffNotCurrent, attemptID)
	}
	if err != nil {
		return canonicalV19AttemptBackoffWriteError("create", "read exact current Attempt lineage", err)
	}
	if exactAttemptID != attemptID {
		return fmt.Errorf("%w: Attempt identity changed", ErrCanonicalV19AttemptBackoffNotCurrent)
	}
	return nil
}

func requireCanonicalV19AttemptBackoffSlotOpen(ctx context.Context, tx *sql.Tx, attemptID string) error {
	var openBackoffID string
	err := tx.QueryRowContext(ctx, `SELECT b.id
		FROM attempt_backoff b
		LEFT JOIN attempt_backoff_resolution r ON r.backoff_id=b.id
		WHERE b.attempt_id=? AND r.backoff_id IS NULL`, attemptID).Scan(&openBackoffID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return canonicalV19AttemptBackoffWriteError("create", "read unresolved Backoff", err)
	}
	return fmt.Errorf("%w: Attempt %q already has unresolved Backoff %q",
		ErrCanonicalV19AttemptBackoffConflict, attemptID, openBackoffID)
}

func nextCanonicalV19AttemptBackoffOrdinal(ctx context.Context, tx *sql.Tx, attemptID string) (int64, error) {
	var ordinal int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(ordinal),0)+1 FROM attempt_backoff WHERE attempt_id=?`, attemptID,
	).Scan(&ordinal); err != nil {
		return 0, err
	}
	return ordinal, nil
}

func requireCanonicalV19AttemptBackoffCurrent(
	ctx context.Context,
	tx *sql.Tx,
	operation string,
	backoffID string,
) (string, error) {
	var exactBackoffID, attemptID string
	err := tx.QueryRowContext(ctx, `SELECT b.id,b.attempt_id
		FROM attempt_backoff b
		JOIN attempt a ON a.id=b.attempt_id AND a.lifecycle='active' AND a.terminal_at=''
		JOIN plan p ON p.id=a.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.retired_at=''
		LEFT JOIN attempt_backoff_resolution r ON r.backoff_id=b.id
		WHERE b.id=? AND r.backoff_id IS NULL`, backoffID).Scan(&exactBackoffID, &attemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: Backoff %q is not unresolved under exact active lineage",
			ErrCanonicalV19AttemptBackoffNotCurrent, backoffID)
	}
	if err != nil {
		return "", canonicalV19AttemptBackoffWriteError(operation, "read exact unresolved Backoff", err)
	}
	if exactBackoffID != backoffID {
		return "", fmt.Errorf("%w: Backoff identity changed", ErrCanonicalV19AttemptBackoffNotCurrent)
	}
	return attemptID, nil
}

func resolveCanonicalV19AttemptBackoffForTerminalization(
	ctx context.Context,
	tx *sql.Tx,
	attemptID string,
	input CanonicalV19AttemptBackoffResolveInput,
) error {
	ownerAttemptID, err := requireCanonicalV19AttemptBackoffCurrent(ctx, tx, "terminalize", input.BackoffID)
	if err != nil {
		return err
	}
	if ownerAttemptID != attemptID {
		return fmt.Errorf("%w: Backoff %q belongs to Attempt %q, not %q",
			ErrCanonicalV19AttemptBackoffNotCurrent, input.BackoffID, ownerAttemptID, attemptID)
	}
	return insertCanonicalV19AttemptBackoffResolution(ctx, tx, "terminalize", input)
}

func insertCanonicalV19AttemptBackoffResolution(
	ctx context.Context,
	tx *sql.Tx,
	operation string,
	input CanonicalV19AttemptBackoffResolveInput,
) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO attempt_backoff_resolution(
		backoff_id,resolution,resolved_at,evidence_digest
	) VALUES(?,?,?,?)`, input.BackoffID, input.Resolution, input.ResolvedAt, input.EvidenceDigest)
	if err != nil {
		if isSQLiteConstraint(err) {
			return fmt.Errorf("%s canonical v19 AttemptBackoff: %w: Backoff %q",
				operation, ErrCanonicalV19AttemptBackoffConflict, input.BackoffID)
		}
		return canonicalV19AttemptBackoffWriteError(operation, "insert Backoff resolution", err)
	}
	return nil
}

func canonicalV19AttemptBackoffWriteError(operation, action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("%s canonical v19 AttemptBackoff: %s: %w", operation, action, ErrContention)
	}
	return fmt.Errorf("%s canonical v19 AttemptBackoff: %s: %w", operation, action, err)
}
