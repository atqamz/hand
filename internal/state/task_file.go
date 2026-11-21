package state

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/atqamz/hand/internal/inbox"
)

type TaskFile struct {
	Name    string
	AddedAt string
}

func (s *Store) AttachFiles(ctx context.Context, id int64, names []string) (added []string, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		added = nil
		if err := taskExists(tx, id); err != nil {
			return err
		}
		now := s.stamp()
		for _, name := range names {
			res, err := tx.Exec(`INSERT OR IGNORE INTO task_file(task_id, name, added_at) VALUES (?, ?, ?)`, id, name, now)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				continue
			}
			added = append(added, name)
			if err := emit(tx, now, "task.attached", id, inbox.Label(name)); err != nil {
				return err
			}
		}
		return nil
	})
	return added, err
}

func (s *Store) DetachFiles(ctx context.Context, id int64, names []string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := taskExists(tx, id); err != nil {
			return err
		}
		now := s.stamp()
		for _, name := range names {
			res, err := tx.Exec(`DELETE FROM task_file WHERE task_id = ? AND name = ?`, id, name)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("%w: %s is not attached to %s", ErrNotFound, inbox.Label(name), TaskRef(id))
			}
			if err := emit(tx, now, "task.detached", id, inbox.Label(name)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) TaskFiles(ctx context.Context, id int64) ([]TaskFile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, added_at FROM task_file WHERE task_id = ? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskFile
	for rows.Next() {
		var f TaskFile
		if err := rows.Scan(&f.Name, &f.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) ProtectedFiles(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT f.name FROM task_file f JOIN task t ON t.id = f.task_id WHERE t.status IN ('inbox', 'active')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func taskExists(tx *sql.Tx, id int64) error {
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM task WHERE id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: task %s", ErrNotFound, TaskRef(id))
	}
	return nil
}
