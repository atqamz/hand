package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

var ErrCanonicalV19DecisionConflict = errors.New("canonical v19 Decision evidence conflict")
var ErrCanonicalV19DecisionNotCurrent = errors.New("canonical v19 Decision is not current")

const canonicalV19DecisionMaxTextBytes = 64 << 10

// CanonicalV19DecisionCreateInput names one immutable authority question, not a Hold or Answer.
// Callers retain its stable ID and exact evidence for retries; a new ID means a new question.
type CanonicalV19DecisionCreateInput struct {
	ID                       string
	TaskID                   string
	PlanID                   string
	AttemptID                string
	ScopeKind                string
	TriggeringWorkerReportID string
	Question                 string
	ChoicesDigest            string
	CreatedAt                string
}

// CreateCanonicalV19Decision records one exact question without acknowledging its trigger.
// Exact replay remains historical even after the owner or Decision becomes terminal.
func CreateCanonicalV19Decision(ctx context.Context, homeDir string, input CanonicalV19DecisionCreateInput) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19DecisionCreateInput(input); err != nil {
		return err
	}
	db, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19DecisionWriteError("begin question writer", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return canonicalV19DecisionWriteError("validate question writer", err)
	}
	existing, found, err := loadCanonicalV19Decision(ctx, tx, input.ID)
	if err != nil {
		return err
	}
	if found {
		if existing == input {
			return nil
		}
		return fmt.Errorf("%w: Decision %q already has different evidence", ErrCanonicalV19DecisionConflict, input.ID)
	}
	if err := requireCanonicalV19DecisionCurrent(ctx, tx, input); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision(
		id,task_id,plan_id,attempt_id,scope_kind,triggering_worker_report_id,question,choices_digest,created_at
	) VALUES(?,?,NULLIF(?,''),NULLIF(?,''),?,NULLIF(?,''),?,?,?)`,
		input.ID, input.TaskID, input.PlanID, input.AttemptID, input.ScopeKind,
		input.TriggeringWorkerReportID, input.Question, input.ChoicesDigest, input.CreatedAt); err != nil {
		return canonicalV19DecisionWriteError("insert question", err)
	}
	return canonicalV19DecisionWriteError("commit question", tx.Commit())
}

func validateCanonicalV19DecisionCreateInput(input CanonicalV19DecisionCreateInput) error {
	if input.ID == "" || input.TaskID == "" || input.CreatedAt == "" {
		return fmt.Errorf("%w: Decision ID, Task ID and created_at are required", ErrCanonicalV19DecisionConflict)
	}
	validScope := input.ScopeKind == "task" && input.PlanID == "" && input.AttemptID == "" ||
		input.ScopeKind == "plan" && input.PlanID != "" && input.AttemptID == "" ||
		input.ScopeKind == "attempt" && input.PlanID != "" && input.AttemptID != ""
	if !validScope {
		return fmt.Errorf("%w: Decision scope must name its exact Task/Plan/Attempt lineage", ErrCanonicalV19DecisionConflict)
	}
	if err := validateCanonicalV19DecisionText("question", input.Question); err != nil {
		return err
	}
	if input.ChoicesDigest != "" && !validCanonicalV19DecisionDigest(input.ChoicesDigest) {
		return fmt.Errorf("%w: choices digest must be empty or lowercase SHA-256", ErrCanonicalV19DecisionConflict)
	}
	return nil
}

func loadCanonicalV19Decision(ctx context.Context, tx *sql.Tx, id string) (CanonicalV19DecisionCreateInput, bool, error) {
	var result CanonicalV19DecisionCreateInput
	err := tx.QueryRowContext(ctx, `SELECT id,task_id,COALESCE(plan_id,''),COALESCE(attempt_id,''),scope_kind,
		COALESCE(triggering_worker_report_id,''),question,choices_digest,created_at FROM decision WHERE id=?`, id).Scan(
		&result.ID, &result.TaskID, &result.PlanID, &result.AttemptID, &result.ScopeKind,
		&result.TriggeringWorkerReportID, &result.Question, &result.ChoicesDigest, &result.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19DecisionCreateInput{}, false, nil
	}
	if err != nil {
		return CanonicalV19DecisionCreateInput{}, false, canonicalV19DecisionWriteError("read exact question", err)
	}
	return result, true, nil
}

func requireCanonicalV19DecisionCurrent(ctx context.Context, tx *sql.Tx, decision CanonicalV19DecisionCreateInput) error {
	// Task-scoped questions may concern terminal/archived history. Execution-scoped
	// authority instead requires the exact active lineage, never a successor lookup.
	var current bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM task t JOIN project pr ON pr.id=t.project_id AND pr.retired_at=''
		WHERE t.id=? AND (?='task' OR (
			t.lifecycle='active' AND t.terminal_at='' AND EXISTS (
				SELECT 1 FROM plan p WHERE p.id=? AND p.task_id=t.id AND p.lifecycle='active' AND p.terminal_at=''
				AND (?='plan' OR EXISTS (
					SELECT 1 FROM attempt a WHERE a.id=? AND a.plan_id=p.id AND a.lifecycle='active' AND a.terminal_at=''
				))
			)
		))
	)`, decision.TaskID, decision.ScopeKind, decision.PlanID, decision.ScopeKind, decision.AttemptID).Scan(&current)
	if err != nil {
		return canonicalV19DecisionWriteError("read exact question lineage", err)
	}
	if !current {
		return fmt.Errorf("%w: Decision %q lacks exact current ownership", ErrCanonicalV19DecisionNotCurrent, decision.ID)
	}
	if decision.TriggeringWorkerReportID == "" {
		return nil
	}
	var related bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM worker_report r JOIN attempt a ON a.id=r.attempt_id JOIN plan p ON p.id=a.plan_id
		WHERE r.id=? AND p.task_id=? AND (?='' OR p.id=?) AND (?='' OR a.id=?)
	)`, decision.TriggeringWorkerReportID, decision.TaskID, decision.PlanID, decision.PlanID,
		decision.AttemptID, decision.AttemptID).Scan(&related)
	if err != nil {
		return canonicalV19DecisionWriteError("read exact triggering report lineage", err)
	}
	if !related {
		return fmt.Errorf("%w: Decision %q triggering report does not belong to its exact lineage", ErrCanonicalV19DecisionConflict, decision.ID)
	}
	report, _, err := loadCanonicalV19WorkerReportByID(ctx, tx, decision.TriggeringWorkerReportID)
	if err != nil {
		return err
	}
	if err := requireCanonicalV19WorkerReportCurrent(ctx, tx, report); err != nil {
		if errors.Is(err, ErrCanonicalV19WorkerReportNotCurrent) {
			return fmt.Errorf("%w: Decision %q triggering execution is no longer current", ErrCanonicalV19DecisionNotCurrent, decision.ID)
		}
		return err
	}
	return nil
}

func validateCanonicalV19DecisionText(field, text string) error {
	if strings.TrimSpace(text) == "" || !utf8.ValidString(text) || len(text) > canonicalV19DecisionMaxTextBytes {
		return fmt.Errorf("%w: %s must be nonblank UTF-8 of at most %d bytes", ErrCanonicalV19DecisionConflict, field, canonicalV19DecisionMaxTextBytes)
	}
	return nil
}

func validCanonicalV19DecisionDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for _, c := range digest {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func canonicalV19DecisionWriteError(action string, err error) error {
	if err == nil {
		return nil
	}
	if isSQLiteConstraint(err) {
		return fmt.Errorf("canonical v19 Decision %s: %w: %v", action, ErrCanonicalV19DecisionConflict, err)
	}
	if isSQLiteBusy(err) {
		return fmt.Errorf("canonical v19 Decision %s: %w", action, ErrContention)
	}
	return fmt.Errorf("canonical v19 Decision %s: %w", action, err)
}
