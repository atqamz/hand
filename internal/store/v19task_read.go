package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrCanonicalV19TaskNotFound = errors.New("canonical v19 Task not found")

type CanonicalV19TaskView struct {
	ID               string
	ProjectID        string
	Ordinal          int64
	Goal             string
	GoalDigest       string
	SupersedesTaskID string
	Lifecycle        string
	CreatedAt        string
	TerminalAt       string
	Archive          *CanonicalV19TaskArchiveInput
}

func ReadCanonicalV19Task(ctx context.Context, homeDir, id string) (CanonicalV19TaskView, error) {
	var view CanonicalV19TaskView
	if ctx == nil {
		ctx = context.Background()
	}
	if id == "" {
		return view, errors.New("read canonical v19 Task: exact ID is required")
	}
	db, err := openReadOnly(homeDir)
	if err != nil {
		return view, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return view, fmt.Errorf("read canonical v19 Task: begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return view, fmt.Errorf("read canonical v19 Task: %w", err)
	}
	var supersedes sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id,project_id,ordinal,goal,goal_digest,supersedes_task_id,
		lifecycle,created_at,terminal_at FROM task WHERE id=?`, id).Scan(
		&view.ID, &view.ProjectID, &view.Ordinal, &view.Goal, &view.GoalDigest, &supersedes,
		&view.Lifecycle, &view.CreatedAt, &view.TerminalAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19TaskView{}, fmt.Errorf("%w: %q", ErrCanonicalV19TaskNotFound, id)
	}
	if err != nil {
		return CanonicalV19TaskView{}, fmt.Errorf("read canonical v19 Task: exact Task: %w", err)
	}
	if supersedes.Valid {
		view.SupersedesTaskID = supersedes.String
	}
	var archive CanonicalV19TaskArchiveInput
	err = tx.QueryRowContext(ctx, `SELECT actor_kind,actor_ref,archived_at,reason,evidence_digest
		FROM task_archive WHERE task_id=?`, id).Scan(
		&archive.ActorKind, &archive.ActorRef, &archive.ArchivedAt, &archive.Reason, &archive.EvidenceDigest)
	if err == nil {
		archive.TaskID = id
		view.Archive = &archive
	} else if !errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19TaskView{}, fmt.Errorf("read canonical v19 Task: exact archive: %w", err)
	}
	return view, nil
}
