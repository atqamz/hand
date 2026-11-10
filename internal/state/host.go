package state

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Store) HostHandover(ctx context.Context, kind, detail, reason string, interrupt []int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		now := s.stamp()
		for _, id := range interrupt {
			var task int64
			err := tx.QueryRow(`UPDATE attempt SET status = ?, reason = ?, ended_at = ? WHERE id = ? AND status IN (?, ?) RETURNING task_id`,
				AttemptInterrupted, reason, now, id, AttemptLaunching, AttemptRunning).Scan(&task)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if err := emit(tx, now, "attempt.interrupted", task, AttemptRef(id)+": "+reason); err != nil {
				return err
			}
		}
		return emit(tx, now, kind, 0, detail)
	})
}
