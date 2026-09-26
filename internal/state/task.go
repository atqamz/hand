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
	StatusInbox     = "inbox"
	StatusActive    = "active"
	StatusDone      = "done"
	StatusAbandoned = "abandoned"
)

var statuses = []string{StatusInbox, StatusActive, StatusDone, StatusAbandoned}

var transitions = map[string][]string{
	StatusInbox:  {StatusActive, StatusAbandoned},
	StatusActive: {StatusDone, StatusAbandoned},
}

type Task struct {
	ID        int64
	Project   string
	Title     string
	Goal      string
	Status    string
	CreatedAt string
	UpdatedAt string
}

const taskSelect = `SELECT id, project, title, goal, status, created_at, updated_at FROM task`

type scanner interface{ Scan(...any) error }

func scanTask(row scanner) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.Project, &t.Title, &t.Goal, &t.Status, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func (s *Store) AddTask(ctx context.Context, project, title, goal string) (Task, error) {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 200 || strings.ContainsAny(title, "\r\n") {
		return Task{}, fmt.Errorf("%w: task title must be one line of 1-200 characters", ErrInvalid)
	}
	now := s.stamp()
	t := Task{Project: project, Title: title, Goal: goal, Status: StatusInbox, CreatedAt: now, UpdatedAt: now}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM project WHERE name = ?`, project).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: project %s", ErrNotFound, project)
		}
		res, err := tx.Exec(`INSERT INTO task(project, title, goal, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			t.Project, t.Title, t.Goal, t.Status, t.CreatedAt, t.UpdatedAt)
		if err != nil {
			return err
		}
		if t.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return emit(tx, now, "task.added", t.ID, "")
	})
	return t, err
}

func (s *Store) Task(ctx context.Context, id int64) (Task, error) {
	t, err := scanTask(s.db.QueryRowContext(ctx, taskSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, fmt.Errorf("%w: task %s", ErrNotFound, TaskRef(id))
	}
	return t, err
}

func (s *Store) Tasks(ctx context.Context, want []string, limit int) ([]Task, error) {
	if limit < 1 {
		return nil, fmt.Errorf("%w: limit must be at least 1", ErrInvalid)
	}
	for _, st := range want {
		if !slices.Contains(statuses, st) {
			return nil, fmt.Errorf("%w: unknown task status %q; want one of %s", ErrInvalid, st, strings.Join(statuses, ", "))
		}
	}
	q, args := taskSelect, []any{}
	if len(want) > 0 {
		q += ` WHERE status IN (` + strings.TrimSuffix(strings.Repeat("?,", len(want)), ",") + `)`
		for _, st := range want {
			args = append(args, st)
		}
	}
	q += ` ORDER BY id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) CountTasks(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, count(*) FROM task GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

func (s *Store) Transition(ctx context.Context, id int64, to string) (Task, error) {
	if !slices.Contains(statuses, to) {
		return Task{}, fmt.Errorf("%w: unknown task status %q", ErrInvalid, to)
	}
	var out Task
	err := s.tx(ctx, func(tx *sql.Tx) error {
		cur, err := scanTask(tx.QueryRow(taskSelect+` WHERE id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: task %s", ErrNotFound, TaskRef(id))
		}
		if err != nil {
			return err
		}
		if !slices.Contains(transitions[cur.Status], to) {
			return fmt.Errorf("%w: task %s is %s and cannot become %s", ErrConflict, TaskRef(id), cur.Status, to)
		}
		if to == StatusDone || to == StatusAbandoned {
			live, err := liveAttempt(tx, id)
			if err != nil {
				return err
			}
			if live != 0 {
				return fmt.Errorf("%w: task %s has live attempt %s; stop it first", ErrConflict, TaskRef(id), AttemptRef(live))
			}
		}
		from, now := cur.Status, s.stamp()
		res, err := tx.Exec(`UPDATE task SET status = ?, updated_at = ? WHERE id = ? AND status = ?`, to, now, id, from)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("%w: task %s changed concurrently", ErrConflict, TaskRef(id))
		}
		cur.Status, cur.UpdatedAt = to, now
		out = cur
		if err := emit(tx, now, "task."+to, id, from+"->"+to); err != nil {
			return err
		}
		if to == StatusDone || to == StatusAbandoned {
			return withdrawOpen(tx, id, now)
		}
		return nil
	})
	return out, err
}
