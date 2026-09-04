package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	handgit "github.com/atqamz/hand/internal/git"
)

// ErrCanonicalV19PlanConflict marks a canonical Plan identity, ordinal, or
// lineage write that lost an exact relational constraint.
var ErrCanonicalV19PlanConflict = errors.New("canonical v19 plan write conflict")

// ErrCanonicalV19PlanNotCurrent marks stale exact Task/Project/binding/policy
// or predecessor evidence. Callers must not retarget current state.
var ErrCanonicalV19PlanNotCurrent = errors.New("canonical v19 plan evidence is not current")

// ErrCanonicalV19PlanGitBasis marks a captured WorkspaceBinding whose exact
// repository/revision could not be positively verified.
var ErrCanonicalV19PlanGitBasis = errors.New("canonical v19 plan Git basis is not positively verified")

// CanonicalV19PlanCreateInput is the immutable meaning captured by one Plan.
type CanonicalV19PlanCreateInput struct {
	ID                 string
	TaskID             string
	Intent             string
	Judgment           string
	Basis              string
	Brief              string
	BriefDigest        string
	WorkspaceBindingID string
	PolicyRevisionID   string
	CreatedAt          string
}

// CanonicalV19PlanReplanInput supersedes one exact active predecessor and
// creates its exact successor atomically.
type CanonicalV19PlanReplanInput struct {
	PredecessorPlanID string
	Successor         CanonicalV19PlanCreateInput
	SupersededAt      string
}

type canonicalV19PlanBasisObservation struct {
	ProjectID         string
	RepositoryLocator string
	CommonGitDir      string
	Revision          string
}

type canonicalV19PlanQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CreateCanonicalV19RootPlan creates the first Plan for one exact active Task.
// It never chooses a replacement Task, WorkspaceBinding, or PolicyRevision.
func CreateCanonicalV19RootPlan(ctx context.Context, homeDir string, input CanonicalV19PlanCreateInput) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19PlanCreateInput("create canonical v19 root Plan", input); err != nil {
		return 0, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = sqlDB.Close() }()

	observed, err := observeCanonicalV19PlanBasis(ctx, sqlDB, input)
	if err != nil {
		return 0, fmt.Errorf("create canonical v19 root Plan: %w", err)
	}
	if err := verifyCanonicalV19PlanGitBasis(homeDir, observed); err != nil {
		return 0, fmt.Errorf("create canonical v19 root Plan: %w", err)
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, canonicalV19PlanWriteError("create canonical v19 root Plan", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return 0, fmt.Errorf("create canonical v19 root Plan: %w", err)
	}
	current, err := observeCanonicalV19PlanBasis(ctx, tx, input)
	if err != nil {
		return 0, fmt.Errorf("create canonical v19 root Plan: %w", err)
	}
	if current != observed {
		return 0, fmt.Errorf("create canonical v19 root Plan: %w: captured basis changed after Git observation", ErrCanonicalV19PlanNotCurrent)
	}

	ordinal, err := nextCanonicalV19PlanOrdinal(ctx, tx, input.TaskID)
	if err != nil {
		return 0, canonicalV19PlanWriteError("create canonical v19 root Plan", "allocate ordinal", err)
	}
	if ordinal != 1 {
		return 0, fmt.Errorf("create canonical v19 root Plan: %w: Task %q already has Plan history", ErrCanonicalV19PlanConflict, input.TaskID)
	}
	if err := insertCanonicalV19Plan(ctx, tx, input, ordinal, "root", ""); err != nil {
		return 0, fmt.Errorf("create canonical v19 root Plan: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, canonicalV19PlanWriteError("create canonical v19 root Plan", "commit writer", err)
	}
	committed = true
	return ordinal, nil
}

// ReplanCanonicalV19Plan supersedes only the named active predecessor and
// creates one immutable successor. Stale predecessors are never retargeted.
func ReplanCanonicalV19Plan(ctx context.Context, homeDir string, input CanonicalV19PlanReplanInput) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if input.PredecessorPlanID == "" {
		return 0, fmt.Errorf("replan canonical v19 Plan: predecessor Plan ID is empty")
	}
	if input.SupersededAt == "" {
		return 0, fmt.Errorf("replan canonical v19 Plan: superseded_at is empty")
	}
	if err := validateCanonicalV19PlanCreateInput("replan canonical v19 Plan", input.Successor); err != nil {
		return 0, err
	}

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = sqlDB.Close() }()

	observed, err := observeCanonicalV19PlanBasis(ctx, sqlDB, input.Successor)
	if err != nil {
		return 0, fmt.Errorf("replan canonical v19 Plan: %w", err)
	}
	if err := verifyCanonicalV19PlanGitBasis(homeDir, observed); err != nil {
		return 0, fmt.Errorf("replan canonical v19 Plan: %w", err)
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, canonicalV19PlanWriteError("replan canonical v19 Plan", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return 0, fmt.Errorf("replan canonical v19 Plan: %w", err)
	}
	current, err := observeCanonicalV19PlanBasis(ctx, tx, input.Successor)
	if err != nil {
		return 0, fmt.Errorf("replan canonical v19 Plan: %w", err)
	}
	if current != observed {
		return 0, fmt.Errorf("replan canonical v19 Plan: %w: captured basis changed after Git observation", ErrCanonicalV19PlanNotCurrent)
	}
	if err := requireCanonicalV19ActivePredecessor(ctx, tx, input.Successor.TaskID, input.PredecessorPlanID); err != nil {
		return 0, fmt.Errorf("replan canonical v19 Plan: %w", err)
	}

	ordinal, err := nextCanonicalV19PlanOrdinal(ctx, tx, input.Successor.TaskID)
	if err != nil {
		return 0, canonicalV19PlanWriteError("replan canonical v19 Plan", "allocate ordinal", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE plan
		SET lifecycle='superseded', terminal_at=?
		WHERE id=? AND task_id=? AND lifecycle='active' AND terminal_at=''`,
		input.SupersededAt, input.PredecessorPlanID, input.Successor.TaskID)
	if err != nil {
		return 0, canonicalV19PlanWriteError("replan canonical v19 Plan", "supersede exact predecessor", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, canonicalV19PlanWriteError("replan canonical v19 Plan", "count superseded predecessor", err)
	}
	if changed != 1 {
		return 0, fmt.Errorf("replan canonical v19 Plan: %w: predecessor changed %d rows, want 1", ErrCanonicalV19PlanNotCurrent, changed)
	}
	if err := insertCanonicalV19Plan(ctx, tx, input.Successor, ordinal, "replan", input.PredecessorPlanID); err != nil {
		return 0, fmt.Errorf("replan canonical v19 Plan: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, canonicalV19PlanWriteError("replan canonical v19 Plan", "commit writer", err)
	}
	committed = true
	return ordinal, nil
}

func validateCanonicalV19PlanCreateInput(action string, input CanonicalV19PlanCreateInput) error {
	for name, value := range map[string]string{
		"Plan ID":              input.ID,
		"Task ID":              input.TaskID,
		"brief digest":         input.BriefDigest,
		"WorkspaceBinding ID": input.WorkspaceBindingID,
		"PolicyRevision ID":   input.PolicyRevisionID,
		"created_at":           input.CreatedAt,
	} {
		if value == "" {
			return fmt.Errorf("%s: %s is empty", action, name)
		}
	}
	if input.Intent != "explore" && input.Intent != "execute" {
		return fmt.Errorf("%s: intent %q is not canonical", action, input.Intent)
	}
	switch input.Judgment {
	case "mechanical", "bounded", "substantial":
	default:
		return fmt.Errorf("%s: judgment %q is not canonical", action, input.Judgment)
	}
	return nil
}

func observeCanonicalV19PlanBasis(ctx context.Context, q canonicalV19PlanQueryer, input CanonicalV19PlanCreateInput) (canonicalV19PlanBasisObservation, error) {
	var observed canonicalV19PlanBasisObservation
	err := q.QueryRowContext(ctx, `SELECT t.project_id,w.repository_locator,w.common_git_dir,w.revision
		FROM task t
		JOIN project project_current ON project_current.id=t.project_id AND project_current.retired_at=''
		JOIN workspace_binding w ON w.id=? AND w.project_id=t.project_id AND w.superseded_at=''
		JOIN policy_revision p ON p.id=? AND p.project_id=t.project_id AND p.superseded_at=''
		WHERE t.id=? AND t.lifecycle='active' AND t.terminal_at=''`,
		input.WorkspaceBindingID, input.PolicyRevisionID, input.TaskID,
	).Scan(&observed.ProjectID, &observed.RepositoryLocator, &observed.CommonGitDir, &observed.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return canonicalV19PlanBasisObservation{}, fmt.Errorf("%w: Task %q with exact current binding/policy", ErrCanonicalV19PlanNotCurrent, input.TaskID)
	}
	if err != nil {
		return canonicalV19PlanBasisObservation{}, canonicalV19PlanWriteError("canonical v19 Plan", "read exact current basis", err)
	}
	return observed, nil
}

func verifyCanonicalV19PlanGitBasis(homeDir string, observed canonicalV19PlanBasisObservation) error {
	if err := validateCanonicalV19CommitID(observed.Revision); err != nil {
		return fmt.Errorf("%w: %v", ErrCanonicalV19PlanGitBasis, err)
	}
	repositoryPath := canonicalV19ObservedPath(homeDir, observed.RepositoryLocator)
	root, err := handgit.ResolveRoot(repositoryPath)
	if err != nil {
		return fmt.Errorf("%w: resolve repository %q: %v", ErrCanonicalV19PlanGitBasis, observed.RepositoryLocator, err)
	}
	if !handgit.SamePath(root, repositoryPath) {
		return fmt.Errorf("%w: locator %q resolves inside a different repository root", ErrCanonicalV19PlanGitBasis, observed.RepositoryLocator)
	}
	commonDir, err := handgit.CommonDir(repositoryPath)
	if err != nil {
		return fmt.Errorf("%w: resolve common Git directory: %v", ErrCanonicalV19PlanGitBasis, err)
	}
	if !handgit.SamePath(commonDir, canonicalV19ObservedPath(homeDir, observed.CommonGitDir)) {
		return fmt.Errorf("%w: exact WorkspaceBinding common Git directory changed", ErrCanonicalV19PlanGitBasis)
	}
	resolved, err := handgit.Run(repositoryPath, "rev-parse", "--verify", observed.Revision+"^{commit}")
	if err != nil {
		return fmt.Errorf("%w: exact revision %s is absent: %v", ErrCanonicalV19PlanGitBasis, observed.Revision, err)
	}
	if strings.TrimSpace(resolved) != observed.Revision {
		return fmt.Errorf("%w: exact revision resolved as %q", ErrCanonicalV19PlanGitBasis, strings.TrimSpace(resolved))
	}
	return nil
}

func validateCanonicalV19CommitID(revision string) error {
	if (len(revision) != 40 && len(revision) != 64) || revision != strings.ToLower(revision) {
		return fmt.Errorf("revision must be exactly 40 or 64 lowercase hex characters")
	}
	if _, err := hex.DecodeString(revision); err != nil {
		return fmt.Errorf("revision is not hexadecimal: %w", err)
	}
	return nil
}

func canonicalV19ObservedPath(homeDir, locator string) string {
	path := filepath.FromSlash(locator)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(homeDir, path)
}

func nextCanonicalV19PlanOrdinal(ctx context.Context, tx *sql.Tx, taskID string) (int64, error) {
	var ordinal int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ordinal),0)+1 FROM plan WHERE task_id=?`, taskID).Scan(&ordinal); err != nil {
		return 0, err
	}
	return ordinal, nil
}

func requireCanonicalV19ActivePredecessor(ctx context.Context, tx *sql.Tx, taskID, planID string) error {
	var ordinal int64
	if err := tx.QueryRowContext(ctx, `SELECT ordinal FROM plan
		WHERE id=? AND task_id=? AND lifecycle='active' AND terminal_at=''`, planID, taskID).Scan(&ordinal); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: predecessor Plan %q is not the exact active predecessor", ErrCanonicalV19PlanNotCurrent, planID)
	} else if err != nil {
		return canonicalV19PlanWriteError("canonical v19 Plan", "read exact predecessor", err)
	}
	return nil
}

func insertCanonicalV19Plan(ctx context.Context, tx *sql.Tx, input CanonicalV19PlanCreateInput, ordinal int64, lineageKind, predecessorID string) error {
	var predecessor any
	if predecessorID != "" {
		predecessor = predecessorID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO plan(
		id,task_id,ordinal,lineage_kind,predecessor_plan_id,intent,judgment,basis,brief,
		brief_digest,workspace_binding_id,policy_revision_id,lifecycle,created_at,terminal_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?, 'active',?, '')`,
		input.ID, input.TaskID, ordinal, lineageKind, predecessor,
		input.Intent, input.Judgment, input.Basis, input.Brief, input.BriefDigest,
		input.WorkspaceBindingID, input.PolicyRevisionID, input.CreatedAt)
	if err != nil {
		if isSQLiteConstraint(err) {
			return fmt.Errorf("%w: Plan %q", ErrCanonicalV19PlanConflict, input.ID)
		}
		return canonicalV19PlanWriteError("canonical v19 Plan", "insert Plan", err)
	}
	return nil
}

func canonicalV19PlanWriteError(subject, action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("%s: %s: %w", subject, action, ErrContention)
	}
	return fmt.Errorf("%s: %s: %w", subject, action, err)
}
