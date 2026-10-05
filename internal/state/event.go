package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
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

func (s *Store) RecentEventsOf(ctx context.Context, kinds []string, limit int) ([]Event, error) {
	if len(kinds) == 0 || limit < 1 {
		return nil, fmt.Errorf("%w: give kinds and a limit of at least 1", ErrInvalid)
	}
	args := make([]any, 0, len(kinds)+1)
	for _, k := range kinds {
		args = append(args, k)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq, at, kind, task_id, detail FROM (
		SELECT seq, at, kind, COALESCE(task_id, 0) AS task_id, detail FROM event WHERE kind IN (`+strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")+`) ORDER BY seq DESC LIMIT ?
	) ORDER BY seq`, append(args, limit)...)
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

func (s *Store) LatestEvent(ctx context.Context, kind string) (Event, bool, error) {
	var e Event
	err := s.db.QueryRowContext(ctx, `SELECT seq, at, kind, COALESCE(task_id, 0), detail FROM event WHERE kind = ? ORDER BY seq DESC LIMIT 1`, kind).Scan(&e.Seq, &e.At, &e.Kind, &e.TaskID, &e.Detail)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, nil
	}
	return e, err == nil, err
}

func (s *Store) LastAttemptEvent(ctx context.Context, attemptID int64) (string, error) {
	e, _, err := lastAttemptEvent(ctx, s.db, attemptID)
	return e.Kind, err
}

func (s *Store) LastLimit(ctx context.Context, attemptID int64) (string, error) {
	prefix := AttemptRef(attemptID) + ": "
	var detail string
	err := s.db.QueryRowContext(ctx, `SELECT detail FROM event WHERE kind = 'attempt.limited' AND detail LIKE ? ORDER BY seq DESC LIMIT 1`, prefix+"%").Scan(&detail)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return strings.TrimPrefix(detail, prefix), err
}

func (s *Store) AttemptSignals(ctx context.Context) (map[int64]Event, error) {
	live, err := s.LiveAttempts(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]Event, len(live))
	for _, a := range live {
		e, ok, err := lastAttemptEvent(ctx, s.db, a.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			out[a.ID] = e
		}
	}
	return out, nil
}

func (s *Store) LastKeys(ctx context.Context, ref string) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM event WHERE kind IN ('attempt.keys', 'supervisor.keys') AND detail LIKE ?`, ref+": %").Scan(&seq)
	return seq, err
}

func lastAttemptEvent(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, attemptID int64) (Event, bool, error) {
	ref := AttemptRef(attemptID)
	var e Event
	err := q.QueryRowContext(ctx, `SELECT seq, at, kind, COALESCE(task_id, 0), detail FROM event WHERE kind LIKE 'attempt.%' AND (detail = ? OR detail LIKE ?) ORDER BY seq DESC LIMIT 1`, ref, ref+": %").Scan(&e.Seq, &e.At, &e.Kind, &e.TaskID, &e.Detail)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, nil
	}
	return e, err == nil, err
}

func (s *Store) TurnStart(ctx context.Context, attemptID int64) (time.Time, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at, kind FROM event WHERE kind IN ('attempt.running', 'attempt.quiet', 'attempt.idle', 'attempt.blocked', 'attempt.limited', 'attempt.sent', 'attempt.keys', 'attempt.long') AND detail LIKE ? ORDER BY seq DESC`, AttemptRef(attemptID)+": %")
	if err != nil {
		return time.Time{}, false, err
	}
	defer rows.Close()
	var start string
	var long bool
scan:
	for rows.Next() {
		var at, kind string
		if err := rows.Scan(&at, &kind); err != nil {
			return time.Time{}, false, err
		}
		switch kind {
		case "attempt.long":
			long = true
		case "attempt.sent", "attempt.keys":
			start = at
		case "attempt.running":
			if start == "" {
				start = at
			}
			break scan
		default:
			break scan
		}
	}
	if err := rows.Err(); err != nil || start == "" {
		return time.Time{}, long, err
	}
	t, err := time.Parse(time.RFC3339Nano, start)
	return t, long, err
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

func (s *Store) TaskEvents(ctx context.Context, taskID int64, limit int) ([]Event, error) {
	if limit < 1 {
		return nil, fmt.Errorf("%w: limit must be at least 1", ErrInvalid)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq, at, kind, task_id, detail FROM (
		SELECT seq, at, kind, COALESCE(task_id, 0) AS task_id, detail FROM event WHERE task_id = ? ORDER BY seq DESC LIMIT ?
	) ORDER BY seq`, taskID, limit)
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
