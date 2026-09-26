package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	DecisionOpen      = "open"
	DecisionAnswered  = "answered"
	DecisionWithdrawn = "withdrawn"
)

type Decision struct {
	ID         int64
	TaskID     int64
	Question   string
	Status     string
	Answer     string
	AnsweredBy string
	CreatedAt  string
	ClosedAt   string
}

const decisionSelect = `SELECT id, task_id, question, status, answer, answered_by, created_at, closed_at FROM decision`

func scanDecision(row scanner) (Decision, error) {
	var d Decision
	err := row.Scan(&d.ID, &d.TaskID, &d.Question, &d.Status, &d.Answer, &d.AnsweredBy, &d.CreatedAt, &d.ClosedAt)
	return d, err
}

func (s *Store) Ask(ctx context.Context, taskID int64, question string) (Decision, error) {
	question = strings.TrimSpace(question)
	if question == "" || utf8.RuneCountInString(question) > 2000 {
		return Decision{}, fmt.Errorf("%w: question must be 1-2000 characters", ErrInvalid)
	}
	d := Decision{TaskID: taskID, Question: question, Status: DecisionOpen, CreatedAt: s.stamp()}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := requireOpenTask(tx, taskID); err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO decision(task_id, question, status, created_at) VALUES (?, ?, ?, ?)`,
			d.TaskID, d.Question, d.Status, d.CreatedAt)
		if err != nil {
			return err
		}
		if d.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return emit(tx, d.CreatedAt, "decision.asked", taskID, DecisionRef(d.ID))
	})
	return d, err
}

func (s *Store) Answer(ctx context.Context, id int64, answer, by string) (Decision, error) {
	if strings.TrimSpace(answer) == "" {
		return Decision{}, fmt.Errorf("%w: answer must not be empty", ErrInvalid)
	}
	if strings.TrimSpace(by) == "" || strings.ContainsAny(by, "\r\n") {
		return Decision{}, fmt.Errorf("%w: answered-by must be one non-empty line", ErrInvalid)
	}
	return s.closeDecision(ctx, id, DecisionAnswered, answer, by)
}

func (s *Store) Withdraw(ctx context.Context, id int64) (Decision, error) {
	return s.closeDecision(ctx, id, DecisionWithdrawn, "", "")
}

func (s *Store) closeDecision(ctx context.Context, id int64, to, answer, by string) (Decision, error) {
	var out Decision
	err := s.tx(ctx, func(tx *sql.Tx) error {
		d, err := scanDecision(tx.QueryRow(decisionSelect+` WHERE id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: decision %s", ErrNotFound, DecisionRef(id))
		}
		if err != nil {
			return err
		}
		if d.Status != DecisionOpen {
			return fmt.Errorf("%w: decision %s is %s", ErrConflict, DecisionRef(id), d.Status)
		}
		now := s.stamp()
		res, err := tx.Exec(`UPDATE decision SET status = ?, answer = ?, answered_by = ?, closed_at = ? WHERE id = ? AND status = ?`,
			to, answer, by, now, id, DecisionOpen)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("%w: decision %s changed concurrently", ErrConflict, DecisionRef(id))
		}
		d.Status, d.Answer, d.AnsweredBy, d.ClosedAt = to, answer, by, now
		out = d
		return emit(tx, now, "decision."+to, d.TaskID, DecisionRef(id))
	})
	return out, err
}

func withdrawOpen(tx *sql.Tx, taskID int64, now string) error {
	rows, err := tx.Query(`UPDATE decision SET status = ?, closed_at = ? WHERE task_id = ? AND status = ? RETURNING id`,
		DecisionWithdrawn, now, taskID, DecisionOpen)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	slices.Sort(ids)
	for _, id := range ids {
		if err := emit(tx, now, "decision."+DecisionWithdrawn, taskID, DecisionRef(id)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) OpenDecisions(ctx context.Context, taskID int64, limit int) ([]Decision, error) {
	if limit < 1 {
		return nil, fmt.Errorf("%w: limit must be at least 1", ErrInvalid)
	}
	q, args := decisionSelect+` WHERE status = ?`, []any{DecisionOpen}
	if taskID != 0 {
		q += ` AND task_id = ?`
		args = append(args, taskID)
	}
	q += ` ORDER BY id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Decision
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) OpenDecisionCount(ctx context.Context, taskID int64) (int, error) {
	q, args := `SELECT count(*) FROM decision WHERE status = ?`, []any{DecisionOpen}
	if taskID != 0 {
		q += ` AND task_id = ?`
		args = append(args, taskID)
	}
	var n int
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

func (s *Store) Decision(ctx context.Context, id int64) (Decision, error) {
	d, err := scanDecision(s.db.QueryRowContext(ctx, decisionSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Decision{}, fmt.Errorf("%w: decision %s", ErrNotFound, DecisionRef(id))
	}
	return d, err
}
