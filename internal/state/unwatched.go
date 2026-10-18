package state

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"time"
)

type Unwatched struct {
	Tasks  []int64
	Since  time.Time
	Idle   time.Duration
	Stalls int
	Last   time.Time
	Far    Timer
	FarIn  time.Duration
}

func (s *Store) Unwatched(ctx context.Context, managed bool) (Unwatched, error) {
	var live, open int
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM attempt WHERE status IN (?, ?)), (SELECT count(*) FROM decision WHERE status = ?)`, AttemptLaunching, AttemptRunning, DecisionOpen).Scan(&live, &open)
	if err != nil || live > 0 || open > 0 {
		return Unwatched{}, err
	}
	var timers []Timer
	if managed {
		if timers, err = s.Timers(ctx); err != nil || slices.ContainsFunc(timers, func(t Timer) bool { return !t.Beyond }) {
			return Unwatched{}, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM task WHERE status = ? ORDER BY id`, StatusActive)
	if err != nil {
		return Unwatched{}, err
	}
	defer rows.Close()
	var u Unwatched
	if len(timers) > 0 {
		u.Far = timers[0]
		u.FarIn = u.Far.Due.Sub(s.now())
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return Unwatched{}, err
		}
		u.Tasks = append(u.Tasks, id)
	}
	if err := rows.Err(); err != nil || len(u.Tasks) == 0 {
		return Unwatched{}, err
	}
	var seq int64
	var since string
	if err := s.db.QueryRowContext(ctx, `SELECT seq, at FROM event WHERE kind NOT IN ('supervisor.stalled', 'supervisor.delivered') ORDER BY seq DESC LIMIT 1`).Scan(&seq, &since); err != nil {
		return Unwatched{}, err
	}
	if u.Since, err = time.Parse(time.RFC3339Nano, since); err != nil {
		return Unwatched{}, err
	}
	var last string
	err = s.db.QueryRowContext(ctx, `SELECT at, (SELECT count(*) FROM event WHERE kind = 'supervisor.stalled' AND seq > ?1) FROM event WHERE kind = 'supervisor.stalled' AND seq > ?1 ORDER BY seq DESC LIMIT 1`, seq).Scan(&last, &u.Stalls)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return Unwatched{}, err
	default:
		if u.Last, err = time.Parse(time.RFC3339Nano, last); err != nil {
			return Unwatched{}, err
		}
	}
	u.Idle = s.now().Sub(u.Since)
	return u, nil
}

func (u Unwatched) IdleFor() string {
	m := int(u.Idle / time.Minute)
	switch {
	case m < 1:
		return "<1m"
	case m < 60:
		return strconv.Itoa(m) + "m"
	case m%60 == 0:
		return strconv.Itoa(m/60) + "h"
	}
	return strconv.Itoa(m/60) + "h" + strconv.Itoa(m%60) + "m"
}
