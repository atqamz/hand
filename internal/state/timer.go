package state

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	TimerArmed     = "armed"
	TimerFired     = "fired"
	TimerCancelled = "cancelled"

	MaxTimers    = 20
	MaxTimerNote = 200
	MinTimer     = time.Minute
	MaxWait      = 30 * 24 * time.Hour

	timerWatchHorizon = 6 * time.Hour
)

type TimerSpec struct {
	At     time.Time
	After  time.Duration
	Every  time.Duration
	TaskID int64
	Note   string
	Waits  bool
}

type Timer struct {
	ID     int64
	Due    time.Time
	Every  time.Duration
	TaskID int64
	Note   string
	Status string
	Beyond bool
	Waits  bool
}

const timerSelect = `SELECT id, due_at, every, COALESCE(task_id, 0), note, status, waits FROM timer`

func scanTimer(row scanner) (Timer, error) {
	var t Timer
	var due string
	var every int64
	if err := row.Scan(&t.ID, &due, &every, &t.TaskID, &t.Note, &t.Status, &t.Waits); err != nil {
		return t, err
	}
	t.Every = time.Duration(every) * time.Second
	var err error
	t.Due, err = time.Parse(time.RFC3339Nano, due)
	return t, err
}

func Span(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

func (t Timer) detail(extra string) string {
	d := TimerRef(t.ID) + extra
	if t.Note != "" {
		d += ": " + t.Note
	}
	return d
}

func (s *Store) AddTimer(ctx context.Context, spec TimerSpec) (Timer, error) {
	now := s.now()
	t := Timer{Due: spec.At, Every: spec.Every.Truncate(time.Second), TaskID: spec.TaskID, Note: strings.TrimSpace(spec.Note), Status: TimerArmed, Waits: spec.Waits}
	if t.Due.IsZero() {
		t.Due = now.Add(cmp.Or(spec.After, spec.Every))
	}
	t.Due = t.Due.UTC()
	switch {
	case spec.Every != 0 && spec.Every < MinTimer:
		return Timer{}, fmt.Errorf("%w: a timer repeats at most once a minute", ErrInvalid)
	case t.Due.Sub(now) < MinTimer:
		return Timer{}, fmt.Errorf("%w: a timer must be due at least 1m from now", ErrInvalid)
	case !utf8.ValidString(t.Note) || utf8.RuneCountInString(t.Note) > MaxTimerNote:
		return Timer{}, fmt.Errorf("%w: a timer note is at most %d characters of UTF-8", ErrInvalid, MaxTimerNote)
	case strings.ContainsFunc(t.Note, unicode.IsControl):
		return Timer{}, fmt.Errorf("%w: a timer note is one line without control characters", ErrInvalid)
	case t.Waits && t.TaskID == 0:
		return Timer{}, fmt.Errorf("%w: a waits timer needs a task: give --while tN", ErrInvalid)
	case t.Waits && spec.Every != 0:
		return Timer{}, fmt.Errorf("%w: a waits timer fires once: it cannot repeat", ErrInvalid)
	case t.Waits && t.Due.Sub(now) > MaxWait:
		return Timer{}, fmt.Errorf("%w: a waits timer is due within %s from now", ErrInvalid, Ahead(MaxWait))
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var task any
		if t.TaskID != 0 {
			var status string
			err := tx.QueryRow(`SELECT status FROM task WHERE id = ?`, t.TaskID).Scan(&status)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: task %s", ErrNotFound, TaskRef(t.TaskID))
			}
			if err != nil {
				return err
			}
			if status == StatusDone || status == StatusAbandoned {
				return fmt.Errorf("%w: task %s is already %s", ErrConflict, TaskRef(t.TaskID), status)
			}
			task = t.TaskID
		}
		if t.Waits {
			var other int64
			err := tx.QueryRow(`SELECT id FROM timer WHERE task_id = ? AND waits = 1 AND status = ?`, t.TaskID, TimerArmed).Scan(&other)
			if err == nil {
				return fmt.Errorf("%w: task %s already waits (%s); cancel it first: `hand wake cancel %s`", ErrConflict, TaskRef(t.TaskID), TimerRef(other), TimerRef(other))
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		var armed int
		if err := tx.QueryRow(`SELECT count(*) FROM timer WHERE status = ?`, TimerArmed).Scan(&armed); err != nil {
			return err
		}
		if armed >= MaxTimers {
			return fmt.Errorf("%w: %d timers are armed, the most a fleet keeps; cancel one first: `hand wake cancel wN`", ErrConflict, armed)
		}
		at := s.stamp()
		res, err := tx.Exec(`INSERT INTO timer(due_at, every, task_id, note, status, created_at, waits) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			t.Due.Format(time.RFC3339Nano), int64(t.Every/time.Second), task, t.Note, TimerArmed, at, t.Waits)
		if err != nil {
			return err
		}
		if t.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		extra := ", due " + t.Due.Format(time.RFC3339)
		if t.Every > 0 {
			extra += ", every " + Span(t.Every)
		}
		return emit(tx, at, "timer.armed", t.TaskID, t.detail(extra))
	})
	return t, err
}

func (s *Store) Timers(ctx context.Context) ([]Timer, error) {
	return s.TimersAt(ctx, s.now())
}

func (s *Store) TimersAt(ctx context.Context, now time.Time) ([]Timer, error) {
	timers, err := armedTimers(ctx, s.db)
	horizon := now.Add(timerWatchHorizon)
	for i := range timers {
		timers[i].Beyond = timers[i].Due.After(horizon)
	}
	return timers, err
}

func (s *Store) Waits(ctx context.Context) (map[int64]Timer, error) {
	timers, err := armedTimers(ctx, s.db)
	if err != nil {
		return nil, err
	}
	out := map[int64]Timer{}
	for _, t := range timers {
		if t.Waits {
			out[t.TaskID] = t
		}
	}
	return out, nil
}

func Ahead(d time.Duration) string {
	if d >= 24*time.Hour {
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	}
	return Span(d.Truncate(time.Minute))
}

func armedTimers(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]Timer, error) {
	rows, err := q.QueryContext(ctx, timerSelect+` WHERE status = ? ORDER BY id`, TimerArmed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Timer
	for rows.Next() {
		t, err := scanTimer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	slices.SortStableFunc(out, func(a, b Timer) int { return a.Due.Compare(b.Due) })
	return out, rows.Err()
}

func (s *Store) FireTimers(ctx context.Context) ([]string, error) {
	now := s.now()
	due := func(t Timer) bool { return !t.Due.After(now) }
	armed, err := armedTimers(ctx, s.db)
	if err != nil || !slices.ContainsFunc(armed, due) {
		return nil, err
	}
	var fired []string
	err = s.tx(ctx, func(tx *sql.Tx) error {
		fired = nil
		armed, err := armedTimers(ctx, tx)
		if err != nil {
			return err
		}
		at := now.UTC().Format(time.RFC3339Nano)
		for _, t := range slices.DeleteFunc(armed, func(t Timer) bool { return !due(t) }) {
			extra := ""
			if late := now.Sub(t.Due).Truncate(time.Minute); late >= MinTimer {
				extra = ", " + Span(late) + " late"
			}
			if t.Every > 0 {
				next := t.Due.Add(t.Every)
				if !next.After(now) {
					next = now.Add(t.Every)
				}
				_, err = tx.Exec(`UPDATE timer SET due_at = ? WHERE id = ?`, next.UTC().Format(time.RFC3339Nano), t.ID)
			} else {
				_, err = tx.Exec(`UPDATE timer SET status = ?, ended_at = ? WHERE id = ?`, TimerFired, at, t.ID)
			}
			if err != nil {
				return err
			}
			if err := emit(tx, at, "timer.fired", t.TaskID, t.detail(extra)); err != nil {
				return err
			}
			fired = append(fired, t.detail(extra))
		}
		return nil
	})
	return fired, err
}

func (s *Store) CancelTimer(ctx context.Context, id int64) (Timer, error) {
	var t Timer
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		t, err = scanTimer(tx.QueryRow(timerSelect+` WHERE id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: timer %s", ErrNotFound, TimerRef(id))
		}
		if err != nil {
			return err
		}
		if t.Status != TimerArmed {
			return fmt.Errorf("%w: timer %s is already %s", ErrConflict, TimerRef(id), t.Status)
		}
		at := s.stamp()
		if _, err := tx.Exec(`UPDATE timer SET status = ?, ended_at = ? WHERE id = ?`, TimerCancelled, at, id); err != nil {
			return err
		}
		t.Status = TimerCancelled
		return emit(tx, at, "timer.cancelled", t.TaskID, TimerRef(id))
	})
	return t, err
}

func cancelWaits(tx *sql.Tx, taskID int64, now string) error {
	return closeAll(tx, taskID, now, "timer.cancelled", TimerRef, `UPDATE timer SET status = ?, ended_at = ? WHERE task_id = ? AND status = ? AND waits = 1 RETURNING id`,
		TimerCancelled, now, taskID, TimerArmed)
}

func cancelTimers(tx *sql.Tx, taskID int64, now string) error {
	return closeAll(tx, taskID, now, "timer.cancelled", TimerRef, `UPDATE timer SET status = ?, ended_at = ? WHERE task_id = ? AND status = ? RETURNING id`,
		TimerCancelled, now, taskID, TimerArmed)
}
