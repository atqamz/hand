package state

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type Event struct {
	Seq    int64
	At     string
	Kind   string
	TaskID int64
	Detail string
}

func emit(tx *sql.Tx, at, kind string, taskID int64, detail string) error {
	var task any
	if taskID != 0 {
		task = taskID
	}
	_, err := tx.Exec(`INSERT INTO event(at, kind, task_id, detail) VALUES (?, ?, ?, ?)`, at, kind, task, detail)
	return err
}

func (s *Store) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, at, kind, task_id, detail FROM (
		SELECT seq, at, kind, COALESCE(task_id, 0) AS task_id, detail FROM event ORDER BY seq DESC LIMIT ?
	) ORDER BY seq`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Seq, &e.At, &e.Kind, &e.TaskID, &e.Detail); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) LastEventSeq(ctx context.Context) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM event`).Scan(&seq)
	return seq, err
}

func (s *Store) EventsAfter(ctx context.Context, after int64, kinds []string, limit int) ([]Event, error) {
	if limit < 1 {
		return nil, fmt.Errorf("%w: limit must be at least 1", ErrInvalid)
	}
	q, args := `SELECT seq, at, kind, COALESCE(task_id, 0), detail FROM event WHERE seq > ?`, []any{after}
	if len(kinds) > 0 {
		q += ` AND kind IN (` + strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",") + `)`
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY seq LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Seq, &e.At, &e.Kind, &e.TaskID, &e.Detail); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
