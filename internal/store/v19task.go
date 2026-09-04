package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
)

// ErrCanonicalV19TaskConflict marks a canonical Task insert that lost an exact
// identity or ordinal constraint. Callers must not retarget another Task.
var ErrCanonicalV19TaskConflict = errors.New("canonical v19 task write conflict")

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
