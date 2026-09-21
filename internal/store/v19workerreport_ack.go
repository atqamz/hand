package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrCanonicalV19WorkerReportAcknowledgementConflict = errors.New("canonical v19 WorkerReportAcknowledgement write conflict")

type CanonicalV19WorkerReportAcknowledgementCreateInput struct {
	WorkerReportID string
	ActorKind      string
	AcknowledgedAt string
	EvidenceDigest string
}

// CanonicalV19WorkerReportAcknowledgement is exact immutable handling evidence
// for one WorkerReport. It never acknowledges WorkerInput or proves outcome.
type CanonicalV19WorkerReportAcknowledgement struct {
	WorkerReportID string
	ActorKind      string
	AcknowledgedAt string
	EvidenceDigest string
}

func CreateCanonicalV19WorkerReportAcknowledgement(
	ctx context.Context,
	homeDir string,
	input CanonicalV19WorkerReportAcknowledgementCreateInput,
) (CanonicalV19WorkerReportAcknowledgement, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19WorkerReportAcknowledgementCreateInput(input); err != nil {
		return CanonicalV19WorkerReportAcknowledgement{}, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19WorkerReportAcknowledgement{}, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19WorkerReportAcknowledgement{}, canonicalV19WorkerReportAcknowledgementWriteError("begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19WorkerReportAcknowledgement{}, fmt.Errorf("create canonical v19 WorkerReportAcknowledgement: %w", err)
	}

	existing, found, err := loadCanonicalV19WorkerReportAcknowledgement(ctx, tx, input.WorkerReportID)
	if err != nil {
		return CanonicalV19WorkerReportAcknowledgement{}, err
	}
	if found {
		if canonicalV19WorkerReportAcknowledgementMatchesCreate(existing, input) {
			return existing, nil
		}
		return CanonicalV19WorkerReportAcknowledgement{}, fmt.Errorf(
			"%w: WorkerReport %q already has different acknowledgement evidence",
			ErrCanonicalV19WorkerReportAcknowledgementConflict, input.WorkerReportID,
		)
	}
	var workerReportID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM worker_report WHERE id=?`, input.WorkerReportID).Scan(&workerReportID); errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19WorkerReportAcknowledgement{}, fmt.Errorf("%w: WorkerReport %q does not exist",
			ErrCanonicalV19WorkerReportAcknowledgementConflict, input.WorkerReportID)
	} else if err != nil {
		return CanonicalV19WorkerReportAcknowledgement{}, canonicalV19WorkerReportAcknowledgementWriteError("read exact WorkerReport target", err)
	}

	result := CanonicalV19WorkerReportAcknowledgement(input)
	if _, err := tx.ExecContext(ctx, `INSERT INTO worker_report_acknowledgement(
		worker_report_id,actor_kind,acknowledged_at,evidence_digest
	) VALUES(?,?,?,?)`, result.WorkerReportID, result.ActorKind, result.AcknowledgedAt, result.EvidenceDigest); err != nil {
		if isSQLiteConstraint(err) {
			return CanonicalV19WorkerReportAcknowledgement{}, fmt.Errorf(
				"%w: WorkerReport %q: %v", ErrCanonicalV19WorkerReportAcknowledgementConflict, input.WorkerReportID, err,
			)
		}
		return CanonicalV19WorkerReportAcknowledgement{}, canonicalV19WorkerReportAcknowledgementWriteError("insert acknowledgement", err)
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19WorkerReportAcknowledgement{}, canonicalV19WorkerReportAcknowledgementWriteError("commit writer", err)
	}
	committed = true
	return result, nil
}

func validateCanonicalV19WorkerReportAcknowledgementCreateInput(
	input CanonicalV19WorkerReportAcknowledgementCreateInput,
) error {
	for name, value := range map[string]string{
		"WorkerReport ID": input.WorkerReportID, "actor kind": input.ActorKind,
		"acknowledged_at": input.AcknowledgedAt, "evidence digest": input.EvidenceDigest,
	} {
		if value == "" {
			return fmt.Errorf("%w: create canonical v19 WorkerReportAcknowledgement: %s is empty",
				ErrCanonicalV19WorkerReportAcknowledgementConflict, name)
		}
	}
	if input.ActorKind != "operator" && input.ActorKind != "supervisor" {
		return fmt.Errorf("%w: unsupported actor kind %q",
			ErrCanonicalV19WorkerReportAcknowledgementConflict, input.ActorKind)
	}
	return nil
}

func loadCanonicalV19WorkerReportAcknowledgement(
	ctx context.Context,
	tx *sql.Tx,
	workerReportID string,
) (CanonicalV19WorkerReportAcknowledgement, bool, error) {
	var acknowledgement CanonicalV19WorkerReportAcknowledgement
	err := tx.QueryRowContext(ctx, `SELECT worker_report_id,actor_kind,acknowledged_at,evidence_digest
		FROM worker_report_acknowledgement WHERE worker_report_id=?`, workerReportID).Scan(
		&acknowledgement.WorkerReportID, &acknowledgement.ActorKind,
		&acknowledgement.AcknowledgedAt, &acknowledgement.EvidenceDigest,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19WorkerReportAcknowledgement{}, false, nil
	}
	if err != nil {
		return CanonicalV19WorkerReportAcknowledgement{}, false,
			canonicalV19WorkerReportAcknowledgementWriteError("read acknowledgement identity", err)
	}
	return acknowledgement, true, nil
}

func canonicalV19WorkerReportAcknowledgementMatchesCreate(
	existing CanonicalV19WorkerReportAcknowledgement,
	input CanonicalV19WorkerReportAcknowledgementCreateInput,
) bool {
	return existing.WorkerReportID == input.WorkerReportID && existing.ActorKind == input.ActorKind &&
		existing.AcknowledgedAt == input.AcknowledgedAt && existing.EvidenceDigest == input.EvidenceDigest
}

func canonicalV19WorkerReportAcknowledgementWriteError(action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("create canonical v19 WorkerReportAcknowledgement: %s: %w", action, ErrContention)
	}
	return fmt.Errorf("create canonical v19 WorkerReportAcknowledgement: %s: %w", action, err)
}
