package state

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

const (
	MaxAlertText = 200
	AlertKeep    = 7 * 24 * time.Hour
	AlertWindow  = 24 * time.Hour
)

type Alert struct {
	Seq       int64
	At        time.Time
	Kind      string
	Ref       string
	TaskID    int64
	Text      string
	Link      string
	FetchedAt time.Time
	AckedAt   time.Time
}

func (a Alert) Delay() (time.Duration, bool) {
	if a.AckedAt.IsZero() {
		return 0, false
	}
	return a.AckedAt.Sub(a.At), true
}

func (s *Store) RecordAlert(ctx context.Context, kind, ref, text, link string) error {
	now := s.now().UTC()
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM alert WHERE at < ?`, now.Add(-AlertKeep).Format(time.RFC3339Nano)); err != nil {
			return err
		}
		var task sql.NullInt64
		if len(ref) > 1 {
			var q string
			switch ref[0] {
			case 't':
				q = `SELECT id FROM task WHERE id = ?`
			case 'd':
				q = `SELECT task_id FROM decision WHERE id = ?`
			case 'a':
				q = `SELECT task_id FROM attempt WHERE id = ?`
			}
			if id, err := strconv.ParseInt(ref[1:], 10, 64); q != "" && err == nil {
				if err := tx.QueryRow(q, id).Scan(&task); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
		}
		_, err := tx.Exec(`INSERT INTO alert(at, kind, ref, task_id, text, link) VALUES (?, ?, ?, ?, ?, ?)`,
			now.Format(time.RFC3339Nano), kind, ref, task, cleanDeviceText(text, MaxAlertText), link)
		return err
	})
}

func (s *Store) LatestAlertSeq(ctx context.Context) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'alert'), 0)`).Scan(&seq)
	return seq, err
}

func (s *Store) AlertsAfter(ctx context.Context, after int64, limit int) ([]Alert, error) {
	return s.alerts(ctx, `WHERE seq > ? AND at >= ? ORDER BY seq LIMIT ?`, after, s.now().UTC().Add(-AlertWindow).Format(time.RFC3339Nano), limit)
}

func (s *Store) Alerts(ctx context.Context) ([]Alert, error) {
	return s.alerts(ctx, `WHERE at >= ? ORDER BY seq DESC`, s.now().UTC().Add(-AlertKeep).Format(time.RFC3339Nano))
}

func (s *Store) alerts(ctx context.Context, tail string, args ...any) ([]Alert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, at, kind, ref, COALESCE(task_id, 0), text, link, fetched_at, acked_at FROM alert `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		var a Alert
		var at, fetched, acked string
		if err := rows.Scan(&a.Seq, &at, &a.Kind, &a.Ref, &a.TaskID, &a.Text, &a.Link, &fetched, &acked); err != nil {
			return nil, err
		}
		a.At, _ = time.Parse(time.RFC3339Nano, at)
		a.FetchedAt, _ = time.Parse(time.RFC3339Nano, fetched)
		a.AckedAt, _ = time.Parse(time.RFC3339Nano, acked)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) FetchedAlerts(ctx context.Context, first, last int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE alert SET fetched_at = ? WHERE seq BETWEEN ? AND ? AND fetched_at = ''`, s.stamp(), first, last)
		return err
	})
}

func (s *Store) AckAlerts(ctx context.Context, upTo int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE alert SET acked_at = ? WHERE seq <= ? AND fetched_at != '' AND acked_at = ''`, s.stamp(), upTo)
		return err
	})
}
