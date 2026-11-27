package state

import (
	"context"
	"fmt"
)

type Closable struct {
	ID    int64
	Title string
}

func (s *Store) ClosableTasks(ctx context.Context, limit int) ([]Closable, int, error) {
	if limit < 1 {
		return nil, 0, fmt.Errorf("%w: limit must be at least 1", ErrInvalid)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.title, count(*) OVER () FROM task t
WHERE t.status = ?
AND (SELECT r.status FROM report r WHERE r.task_id = t.id ORDER BY r.id DESC LIMIT 1) = 'done'
AND NOT EXISTS (SELECT 1 FROM attempt a WHERE a.task_id = t.id AND a.status IN (?, ?))
AND NOT EXISTS (SELECT 1 FROM report r WHERE r.task_id = t.id AND r.acked_at = '')
AND NOT EXISTS (SELECT 1 FROM decision d WHERE d.task_id = t.id AND d.status = ?)
AND NOT EXISTS (SELECT 1 FROM timer m WHERE m.task_id = t.id AND m.status = 'armed')
AND NOT EXISTS (SELECT 1 FROM task c WHERE c.parent_id = t.id AND c.status IN (?, ?))
ORDER BY t.id LIMIT ?`, StatusActive, AttemptLaunching, AttemptRunning, DecisionOpen, StatusInbox, StatusActive, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Closable
	total := 0
	for rows.Next() {
		var c Closable
		if err := rows.Scan(&c.ID, &c.Title, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}
