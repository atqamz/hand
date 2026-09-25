package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"
)

// ErrCanonicalV19TaskConflict marks a canonical Task insert that lost an exact
// identity or ordinal constraint. Callers must not retarget another Task.
var ErrCanonicalV19TaskConflict = errors.New("canonical v19 task write conflict")

var ErrCanonicalV19TaskNotCurrent = errors.New("canonical v19 task is not current")

// ErrCanonicalV19ProjectNotCurrent marks a missing or retired exact Project.
var ErrCanonicalV19ProjectNotCurrent = errors.New("canonical v19 project is not current")

// CanonicalV19TaskCreateInput is the immutable evidence for one fresh Task.
type CanonicalV19TaskCreateInput struct {
	ID         string
	ProjectID  string
	Goal       string
	GoalDigest string
	CreatedAt  string
}

type CanonicalV19TaskSupersedeInput struct {
	PredecessorTaskID string
	SuccessorTaskID   string
	Goal              string
	GoalDigest        string
	At                string
}

// CreateCanonicalV19Task inserts one fresh active Task into an already-canonical
// active database. It never creates, migrates, or cuts over state.
func CreateCanonicalV19Task(ctx context.Context, homeDir string, input CanonicalV19TaskCreateInput) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19TaskCreateInput(input); err != nil {
		return 0, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = sqlDB.Close() }()

	// The writer DSN pins database/sql transactions to BEGIN IMMEDIATE so
	// ordinal selection and insertion share one SQLite write serialization point.
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, canonicalV19WriteError("begin Task writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return 0, fmt.Errorf("create canonical v19 Task: %w", err)
	}

	var retiredAt string
	if err := tx.QueryRowContext(ctx, `SELECT retired_at FROM project WHERE id = ?`, input.ProjectID).Scan(&retiredAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("%w: Project %q does not exist", ErrCanonicalV19ProjectNotCurrent, input.ProjectID)
		}
		return 0, canonicalV19WriteError("read exact Project", err)
	}
	if retiredAt != "" {
		return 0, fmt.Errorf("%w: Project %q is retired", ErrCanonicalV19ProjectNotCurrent, input.ProjectID)
	}

	var ordinal int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(ordinal), 0) + 1 FROM task WHERE project_id = ?`, input.ProjectID,
	).Scan(&ordinal); err != nil {
		return 0, canonicalV19WriteError("allocate Task ordinal", err)
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO task(
		id, project_id, ordinal, goal, goal_digest, supersedes_task_id,
		lifecycle, created_at, terminal_at
	) VALUES(?,?,?,?,?,NULL,'active',?,'')`,
		input.ID, input.ProjectID, ordinal, input.Goal, input.GoalDigest, input.CreatedAt)
	if err != nil {
		if isSQLiteConstraint(err) {
			return 0, fmt.Errorf("%w: Task %q", ErrCanonicalV19TaskConflict, input.ID)
		}
		return 0, canonicalV19WriteError("insert Task", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, canonicalV19WriteError("commit Task writer", err)
	}
	committed = true
	return ordinal, nil
}

func SupersedeCanonicalV19Task(ctx context.Context, homeDir string, input CanonicalV19TaskSupersedeInput) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if input.PredecessorTaskID == "" || input.SuccessorTaskID == "" || input.Goal == "" ||
		input.GoalDigest == "" || input.At == "" {
		return 0, errors.New("supersede canonical v19 Task: predecessor ID, successor ID, goal, digest and timestamp are required")
	}
	if input.PredecessorTaskID == input.SuccessorTaskID {
		return 0, errors.New("supersede canonical v19 Task: successor must differ from predecessor")
	}
	if _, err := time.Parse(time.RFC3339Nano, input.At); err != nil {
		return 0, fmt.Errorf("supersede canonical v19 Task: terminal timestamp: %w", err)
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, canonicalV19WriteError("begin Task supersession", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return 0, fmt.Errorf("supersede canonical v19 Task: %w", err)
	}
	var projectID, retiredAt string
	var planExists, operationExists, holdExists, inboundHoldExists, decisionExists, repairExists bool
	err = tx.QueryRowContext(ctx, `SELECT t.project_id,p.retired_at,
		EXISTS(SELECT 1 FROM plan WHERE task_id=t.id),
		EXISTS(SELECT 1 FROM external_operation WHERE task_id=t.id),
		EXISTS(SELECT 1 FROM task_hold WHERE task_id=t.id),
		EXISTS(SELECT 1 FROM task_hold_blocked_on_task b
			WHERE b.blocked_on_task_id=t.id AND NOT EXISTS(
				SELECT 1 FROM task_hold_resolution r WHERE r.hold_id=b.hold_id)),
		EXISTS(SELECT 1 FROM decision WHERE task_id=t.id),
		EXISTS(SELECT 1 FROM repair_target WHERE task_id=t.id)
		FROM task t JOIN project p ON p.id=t.project_id
		WHERE t.id=? AND t.lifecycle='active' AND t.terminal_at=''`, input.PredecessorTaskID).Scan(
		&projectID, &retiredAt, &planExists, &operationExists, &holdExists, &inboundHoldExists, &decisionExists, &repairExists)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("supersede canonical v19 Task: %w: predecessor %q", ErrCanonicalV19TaskNotCurrent, input.PredecessorTaskID)
	}
	if err != nil {
		return 0, canonicalV19WriteError("read exact Task predecessor", err)
	}
	if retiredAt != "" {
		return 0, fmt.Errorf("supersede canonical v19 Task: %w: Project %q is retired", ErrCanonicalV19ProjectNotCurrent, projectID)
	}
	if planExists || operationExists || holdExists || inboundHoldExists || decisionExists || repairExists {
		return 0, fmt.Errorf("supersede canonical v19 Task: %w: predecessor %q has Plan or obligation history", ErrCanonicalV19TaskNotCurrent, input.PredecessorTaskID)
	}
	var ordinal int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ordinal),0)+1 FROM task WHERE project_id=?`, projectID).Scan(&ordinal); err != nil {
		return 0, canonicalV19WriteError("allocate successor Task ordinal", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE task SET lifecycle='superseded',terminal_at=?
		WHERE id=? AND project_id=? AND lifecycle='active' AND terminal_at=''`, input.At, input.PredecessorTaskID, projectID)
	if err != nil {
		return 0, canonicalV19WriteError("terminalize exact predecessor Task", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, canonicalV19WriteError("count terminalized predecessor Task", err)
	}
	if changed != 1 {
		return 0, fmt.Errorf("supersede canonical v19 Task: %w: predecessor changed %d rows", ErrCanonicalV19TaskNotCurrent, changed)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO task(
		id,project_id,ordinal,goal,goal_digest,supersedes_task_id,lifecycle,created_at,terminal_at
	) VALUES(?,?,?,?,?,?,'active',?,'')`, input.SuccessorTaskID, projectID, ordinal, input.Goal,
		input.GoalDigest, input.PredecessorTaskID, input.At)
	if err != nil {
		if isSQLiteConstraint(err) {
			return 0, fmt.Errorf("supersede canonical v19 Task: %w: successor %q", ErrCanonicalV19TaskConflict, input.SuccessorTaskID)
		}
		return 0, canonicalV19WriteError("insert successor Task", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, canonicalV19WriteError("commit Task supersession", err)
	}
	committed = true
	return ordinal, nil
}

func AbandonCanonicalV19Task(ctx context.Context, homeDir, taskID, abandonedAt string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if taskID == "" {
		return errors.New("abandon canonical v19 Task: Task ID is required")
	}
	if _, err := time.Parse(time.RFC3339Nano, abandonedAt); err != nil {
		return fmt.Errorf("abandon canonical v19 Task: terminal timestamp: %w", err)
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19WriteError("begin Task abandonment", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("abandon canonical v19 Task: %w", err)
	}
	var projectID, retiredAt, obligation string
	err = tx.QueryRowContext(ctx, `SELECT t.project_id,p.retired_at,CASE
		WHEN EXISTS(SELECT 1 FROM plan WHERE task_id=t.id AND lifecycle='active') THEN 'active Plan'
		WHEN EXISTS(SELECT 1 FROM external_operation WHERE task_id=t.id AND state IN ('prepared','submitted','uncertain'))
			THEN 'unresolved external operation'
		WHEN EXISTS(SELECT 1 FROM executor_binding e JOIN attempt a ON a.id=e.attempt_id JOIN plan pl ON pl.id=a.plan_id
			WHERE pl.task_id=t.id AND NOT EXISTS(
				SELECT 1 FROM executor_binding_termination x WHERE x.executor_binding_id=e.id)) THEN 'open ExecutorBinding'
		WHEN EXISTS(SELECT 1 FROM task_hold h WHERE h.task_id=t.id AND NOT EXISTS(
			SELECT 1 FROM task_hold_resolution r WHERE r.hold_id=h.id)) THEN 'open TaskHold'
		WHEN EXISTS(SELECT 1 FROM task_hold_blocked_on_task b WHERE b.blocked_on_task_id=t.id AND NOT EXISTS(
			SELECT 1 FROM task_hold_resolution r WHERE r.hold_id=b.hold_id)) THEN 'open inbound TaskHold'
		WHEN EXISTS(SELECT 1 FROM repair_target rt WHERE rt.task_id=t.id AND NOT EXISTS(
			SELECT 1 FROM repair_resolution r WHERE r.repair_id=rt.repair_id)) THEN 'open Repair'
		ELSE '' END
		FROM task t JOIN project p ON p.id=t.project_id
		WHERE t.id=? AND t.lifecycle='active' AND t.terminal_at=''`, taskID).Scan(&projectID, &retiredAt, &obligation)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("abandon canonical v19 Task: %w: Task %q", ErrCanonicalV19TaskNotCurrent, taskID)
	}
	if err != nil {
		return canonicalV19WriteError("read exact Task", err)
	}
	if retiredAt != "" {
		return fmt.Errorf("abandon canonical v19 Task: %w: Project %q is retired", ErrCanonicalV19ProjectNotCurrent, projectID)
	}
	if obligation != "" {
		return fmt.Errorf("abandon canonical v19 Task: %w: Task %q has %s", ErrCanonicalV19TaskNotCurrent, taskID, obligation)
	}
	result, err := tx.ExecContext(ctx, `UPDATE task SET lifecycle='abandoned',terminal_at=?
		WHERE id=? AND lifecycle='active' AND terminal_at=''`, abandonedAt, taskID)
	if err != nil {
		return canonicalV19WriteError("abandon exact Task", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return canonicalV19WriteError("count abandoned Task", err)
	}
	if changed != 1 {
		return fmt.Errorf("abandon canonical v19 Task: %w: Task changed %d rows", ErrCanonicalV19TaskNotCurrent, changed)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19WriteError("commit Task abandonment", err)
	}
	return nil
}

func validateCanonicalV19TaskCreateInput(input CanonicalV19TaskCreateInput) error {
	for name, value := range map[string]string{
		"Task ID":     input.ID,
		"Project ID":  input.ProjectID,
		"goal":        input.Goal,
		"goal digest": input.GoalDigest,
		"created_at":  input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("create canonical v19 Task: %s is empty", name)
		}
	}
	return nil
}

func openCanonicalV19Writer(homeDir string) (*sql.DB, error) {
	path := Path(homeDir)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("open canonical v19 writer: inspect active database: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("open canonical v19 writer: active database %s is not a direct regular file", path)
	}

	uri := "file:" + (&url.URL{Path: path}).EscapedPath() +
		"?mode=rw&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate" + sqliteTestPragmas
	sqlDB, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("open canonical v19 writer: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := validateCanonicalV19Schema(sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open canonical v19 writer: %w", err)
	}
	return sqlDB, nil
}

func validateCanonicalV19WriterTransaction(ctx context.Context, tx *sql.Tx) error {
	var foreignKeys int
	if err := tx.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return fmt.Errorf("validate canonical v19 writer transaction: foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return canonicalV19Mismatch("writer transaction PRAGMA foreign_keys = %d, want 1", foreignKeys)
	}
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("validate canonical v19 writer transaction: user_version: %w", err)
	}
	if version != canonicalV19SchemaVersion {
		return canonicalV19Mismatch("writer transaction PRAGMA user_version = %d, want %d", version, canonicalV19SchemaVersion)
	}
	identity, err := inspectCanonicalV19Identity(tx)
	if err != nil {
		return err
	}
	if identity.Fingerprint != canonicalV19SchemaFingerprint ||
		identity.Tables != canonicalV19TableCount ||
		identity.Indexes != canonicalV19IndexCount ||
		identity.Triggers != canonicalV19TriggerCount {
		return canonicalV19Mismatch("writer transaction schema identity = %s / %d / %d / %d, want %s / %d / %d / %d",
			identity.Fingerprint, identity.Tables, identity.Indexes, identity.Triggers,
			canonicalV19SchemaFingerprint, canonicalV19TableCount, canonicalV19IndexCount, canonicalV19TriggerCount)
	}
	return nil
}

func canonicalV19WriteError(action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("create canonical v19 Task: %s: %w", action, ErrContention)
	}
	return fmt.Errorf("create canonical v19 Task: %s: %w", action, err)
}
