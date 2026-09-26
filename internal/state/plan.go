package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Plan struct {
	TaskID    int64
	Revision  int
	Body      string
	CreatedAt string
}

func PlanRef(rev int) string { return "p" + strconv.Itoa(rev) }

func requireOpenTask(tx *sql.Tx, id int64) error {
	var status string
	err := tx.QueryRow(`SELECT status FROM task WHERE id = ?`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: task %s", ErrNotFound, TaskRef(id))
	}
	if err != nil {
		return err
	}
	if status != StatusInbox && status != StatusActive {
		return fmt.Errorf("%w: task %s is %s", ErrConflict, TaskRef(id), status)
	}
	return nil
}

func (s *Store) SetPlan(ctx context.Context, taskID int64, body string) (Plan, error) {
	if strings.TrimSpace(body) == "" {
		return Plan{}, fmt.Errorf("%w: plan body must not be empty", ErrInvalid)
	}
	p := Plan{TaskID: taskID, Body: body, CreatedAt: s.stamp()}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := requireOpenTask(tx, taskID); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT COALESCE(MAX(revision), 0) + 1 FROM plan WHERE task_id = ?`, taskID).Scan(&p.Revision); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO plan(task_id, revision, body, created_at) VALUES (?, ?, ?, ?)`,
			p.TaskID, p.Revision, p.Body, p.CreatedAt); err != nil {
			return err
		}
		return emit(tx, p.CreatedAt, "plan.set", taskID, PlanRef(p.Revision))
	})
	return p, err
}

func (s *Store) CurrentPlan(ctx context.Context, taskID int64) (Plan, bool, error) {
	var p Plan
	err := s.db.QueryRowContext(ctx, `SELECT task_id, revision, body, created_at FROM plan WHERE task_id = ? ORDER BY revision DESC LIMIT 1`, taskID).
		Scan(&p.TaskID, &p.Revision, &p.Body, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Plan{}, false, nil
	}
	if err != nil {
		return Plan{}, false, err
	}
	return p, true, nil
}
