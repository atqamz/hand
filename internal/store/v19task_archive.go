package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrCanonicalV19TaskArchiveConflict = errors.New("canonical v19 Task archive conflicts with exact history")
var ErrCanonicalV19TaskArchiveNotEligible = errors.New("canonical v19 Task archive is not eligible")

type CanonicalV19TaskArchiveInput struct {
	TaskID         string
	ActorKind      string
	ActorRef       string
	ArchivedAt     string
	Reason         string
	EvidenceDigest string
}

func ArchiveCanonicalV19Task(ctx context.Context, homeDir string, input CanonicalV19TaskArchiveInput) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19TaskArchiveInput(input); err != nil {
		return err
	}
	db, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19TaskArchiveWriteError("begin writer", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("archive canonical v19 Task: %w", err)
	}
	var existing CanonicalV19TaskArchiveInput
	err = tx.QueryRowContext(ctx, `SELECT actor_kind,actor_ref,archived_at,reason,evidence_digest
		FROM task_archive WHERE task_id=?`, input.TaskID).Scan(
		&existing.ActorKind, &existing.ActorRef, &existing.ArchivedAt, &existing.Reason, &existing.EvidenceDigest)
	if err == nil {
		if existing.ActorKind == input.ActorKind && existing.ActorRef == input.ActorRef &&
			existing.ArchivedAt == input.ArchivedAt && existing.Reason == input.Reason &&
			existing.EvidenceDigest == input.EvidenceDigest {
			return nil
		}
		return fmt.Errorf("%w: Task %q", ErrCanonicalV19TaskArchiveConflict, input.TaskID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return canonicalV19TaskArchiveWriteError("read exact archive", err)
	}
	var lifecycle string
	var operationExists, worktreeExists, sessionExists, executorExists bool
	err = tx.QueryRowContext(ctx, `SELECT t.lifecycle,
		EXISTS(SELECT 1 FROM external_operation o WHERE o.task_id=t.id),
		EXISTS(SELECT 1 FROM attempt_worktree_binding w
			JOIN attempt a ON a.id=w.attempt_id JOIN plan p ON p.id=a.plan_id WHERE p.task_id=t.id),
		EXISTS(SELECT 1 FROM session_binding s
			JOIN attempt a ON a.id=s.attempt_id JOIN plan p ON p.id=a.plan_id WHERE p.task_id=t.id),
		EXISTS(SELECT 1 FROM executor_binding e
			JOIN attempt a ON a.id=e.attempt_id JOIN plan p ON p.id=a.plan_id WHERE p.task_id=t.id)
		FROM task t WHERE t.id=?`, input.TaskID).Scan(
		&lifecycle, &operationExists, &worktreeExists, &sessionExists, &executorExists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: Task %q is missing", ErrCanonicalV19TaskArchiveNotEligible, input.TaskID)
	}
	if err != nil {
		return canonicalV19TaskArchiveWriteError("read exact Task lineage", err)
	}
	if lifecycle != "satisfied" && lifecycle != "superseded" && lifecycle != "abandoned" {
		return fmt.Errorf("%w: Task %q is not terminal", ErrCanonicalV19TaskArchiveNotEligible, input.TaskID)
	}
	if operationExists || worktreeExists || sessionExists || executorExists {
		return fmt.Errorf("%w: Task %q has resource or effect lineage requiring external observation", ErrCanonicalV19TaskArchiveNotEligible, input.TaskID)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_archive(
		task_id,actor_kind,actor_ref,archived_at,reason,evidence_digest
	) VALUES(?,?,?,?,?,?)`, input.TaskID, input.ActorKind, input.ActorRef,
		input.ArchivedAt, input.Reason, input.EvidenceDigest); err != nil {
		if isSQLiteConstraint(err) {
			return fmt.Errorf("%w: Task %q: %v", ErrCanonicalV19TaskArchiveNotEligible, input.TaskID, err)
		}
		return canonicalV19TaskArchiveWriteError("insert exact archive", err)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19TaskArchiveWriteError("commit writer", err)
	}
	return nil
}

func validateCanonicalV19TaskArchiveInput(input CanonicalV19TaskArchiveInput) error {
	if input.TaskID == "" || input.ActorRef == "" || input.Reason == "" {
		return errors.New("archive canonical v19 Task: Task ID, actor reference and reason are required")
	}
	if input.ActorKind != "operator" && input.ActorKind != "supervisor" {
		return fmt.Errorf("archive canonical v19 Task: actor kind %q is not canonical", input.ActorKind)
	}
	if len(input.ActorRef) > 256 || len(input.Reason) > 1024 {
		return errors.New("archive canonical v19 Task: actor reference or reason exceeds the canonical bound")
	}
	if len(input.ArchivedAt) > 64 {
		return errors.New("archive canonical v19 Task: archive time exceeds the canonical bound")
	}
	if _, err := time.Parse(time.RFC3339Nano, input.ArchivedAt); err != nil {
		return fmt.Errorf("archive canonical v19 Task: archive time: %w", err)
	}
	if len(input.EvidenceDigest) != 64 {
		return errors.New("archive canonical v19 Task: evidence digest must be exact lowercase SHA-256")
	}
	for _, digit := range input.EvidenceDigest {
		if digit < '0' || digit > '9' && digit < 'a' || digit > 'f' {
			return errors.New("archive canonical v19 Task: evidence digest must be exact lowercase SHA-256")
		}
	}
	return nil
}

func canonicalV19TaskArchiveWriteError(action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("archive canonical v19 Task: %s: %w", action, ErrContention)
	}
	return fmt.Errorf("archive canonical v19 Task: %s: %w", action, err)
}
