package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const (
	AttemptLaunching   = "launching"
	AttemptRunning     = "running"
	AttemptExited      = "exited"
	AttemptStopped     = "stopped"
	AttemptInterrupted = "interrupted"
	AttemptFailed      = "failed"
)

var Harnesses = []string{"claude", "codex", "opencode"}

var attemptEnds = map[string][]string{
	AttemptLaunching: {AttemptFailed},
	AttemptRunning:   {AttemptExited, AttemptStopped, AttemptInterrupted},
}

type Terminal struct {
	ServerGeneration string
	TerminalID       string
	PaneID           string
	PID              int
	StartMarker      string
}

type AttemptSpec struct {
	TaskID  int64
	Harness string
	Model   string
	Effort  string
	Argv    []string
}

type Attempt struct {
	ID       int64
	TaskID   int64
	Harness  string
	Model    string
	Effort   string
	Argv     []string
	Worktree string
	Branch   string
	Status   string
	Terminal
	Reason    string
	CreatedAt string
	EndedAt   string
	CleanedAt string
}

func (a Attempt) Live() bool { return a.Status == AttemptLaunching || a.Status == AttemptRunning }

const attemptSelect = `SELECT id, task_id, harness, model, effort, argv, worktree, branch, status, server_generation, terminal_id, pane_id, pid, start_marker, reason, created_at, ended_at, cleaned_at FROM attempt`

func scanAttempt(row scanner) (Attempt, error) {
	var a Attempt
	var argv string
	err := row.Scan(&a.ID, &a.TaskID, &a.Harness, &a.Model, &a.Effort, &argv, &a.Worktree, &a.Branch, &a.Status,
		&a.ServerGeneration, &a.TerminalID, &a.PaneID, &a.PID, &a.StartMarker, &a.Reason, &a.CreatedAt, &a.EndedAt, &a.CleanedAt)
	if err != nil {
		return a, err
	}
	return a, json.Unmarshal([]byte(argv), &a.Argv)
}

func getAttempt(tx *sql.Tx, id int64) (Attempt, error) {
	a, err := scanAttempt(tx.QueryRow(attemptSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, fmt.Errorf("%w: attempt %s", ErrNotFound, AttemptRef(id))
	}
	return a, err
}

func liveAttempt(tx *sql.Tx, taskID int64) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM attempt WHERE task_id = ? AND status IN (?, ?)`, taskID, AttemptLaunching, AttemptRunning).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *Store) AddAttempt(ctx context.Context, spec AttemptSpec, worktreeRoot string) (Attempt, error) {
	if !slices.Contains(Harnesses, spec.Harness) {
		return Attempt{}, fmt.Errorf("%w: harness %q must be one of %s", ErrInvalid, spec.Harness, strings.Join(Harnesses, ", "))
	}
	if len(spec.Argv) == 0 {
		return Attempt{}, fmt.Errorf("%w: attempt needs a launch command", ErrInvalid)
	}
	argv, err := json.Marshal(spec.Argv)
	if err != nil {
		return Attempt{}, err
	}
	a := Attempt{TaskID: spec.TaskID, Harness: spec.Harness, Model: spec.Model, Effort: spec.Effort, Argv: spec.Argv, Status: AttemptLaunching, CreatedAt: s.stamp()}
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var status string
		err := tx.QueryRow(`SELECT status FROM task WHERE id = ?`, spec.TaskID).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: task %s", ErrNotFound, TaskRef(spec.TaskID))
		}
		if err != nil {
			return err
		}
		if status != StatusActive {
			return fmt.Errorf("%w: task %s is %s; start it first", ErrConflict, TaskRef(spec.TaskID), status)
		}
		live, err := liveAttempt(tx, spec.TaskID)
		if err != nil {
			return err
		}
		if live != 0 {
			return fmt.Errorf("%w: task %s already has live attempt %s", ErrConflict, TaskRef(spec.TaskID), AttemptRef(live))
		}
		var fleet string
		if err := tx.QueryRow(`SELECT id FROM fleet`).Scan(&fleet); errors.Is(err, sql.ErrNoRows) {
			return errNoFleet
		} else if err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT COALESCE(MAX(id), 0) + 1 FROM attempt`).Scan(&a.ID); err != nil {
			return err
		}
		name := TaskRef(spec.TaskID) + "-" + AttemptRef(a.ID)
		a.Worktree, a.Branch = filepath.Join(worktreeRoot, name), "hand/"+fleet+"/"+name
		if _, err := tx.Exec(`INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.ID, a.TaskID, a.Harness, a.Model, a.Effort, string(argv), a.Worktree, a.Branch, a.Status, a.CreatedAt); err != nil {
			return err
		}
		return emit(tx, a.CreatedAt, "attempt."+AttemptLaunching, a.TaskID, AttemptRef(a.ID))
	})
	return a, err
}

func (s *Store) AttemptRunning(ctx context.Context, id int64, t Terminal) (Attempt, error) {
	var out Attempt
	err := s.tx(ctx, func(tx *sql.Tx) error {
		a, err := getAttempt(tx, id)
		if err != nil {
			return err
		}
		if a.Status != AttemptLaunching {
			return fmt.Errorf("%w: attempt %s is %s and cannot become %s", ErrConflict, AttemptRef(id), a.Status, AttemptRunning)
		}
		if _, err := tx.Exec(`UPDATE attempt SET status = ?, server_generation = ?, terminal_id = ?, pane_id = ?, pid = ?, start_marker = ? WHERE id = ?`,
			AttemptRunning, t.ServerGeneration, t.TerminalID, t.PaneID, t.PID, t.StartMarker, id); err != nil {
			return err
		}
		a.Status, a.Terminal = AttemptRunning, t
		out = a
		return emit(tx, s.stamp(), "attempt."+AttemptRunning, a.TaskID, AttemptRef(id)+": pane "+t.PaneID)
	})
	return out, err
}

func (s *Store) EndAttempt(ctx context.Context, id int64, to, reason string) (Attempt, error) {
	var out Attempt
	err := s.tx(ctx, func(tx *sql.Tx) error {
		a, err := getAttempt(tx, id)
		if err != nil {
			return err
		}
		if !slices.Contains(attemptEnds[a.Status], to) {
			return fmt.Errorf("%w: attempt %s is %s and cannot become %s", ErrConflict, AttemptRef(id), a.Status, to)
		}
		now := s.stamp()
		if _, err := tx.Exec(`UPDATE attempt SET status = ?, reason = ?, ended_at = ? WHERE id = ?`, to, reason, now, id); err != nil {
			return err
		}
		a.Status, a.Reason, a.EndedAt = to, reason, now
		out = a
		return emit(tx, now, "attempt."+to, a.TaskID, AttemptRef(id)+": "+reason)
	})
	return out, err
}

func (s *Store) CleanAttempt(ctx context.Context, id int64) (Attempt, error) {
	var out Attempt
	err := s.tx(ctx, func(tx *sql.Tx) error {
		a, err := getAttempt(tx, id)
		if err != nil {
			return err
		}
		switch {
		case a.Live():
			return fmt.Errorf("%w: attempt %s is %s; stop it first", ErrConflict, AttemptRef(id), a.Status)
		case a.CleanedAt != "":
			return fmt.Errorf("%w: attempt %s is already cleaned", ErrConflict, AttemptRef(id))
		}
		now := s.stamp()
		if _, err := tx.Exec(`UPDATE attempt SET cleaned_at = ? WHERE id = ?`, now, id); err != nil {
			return err
		}
		a.CleanedAt = now
		out = a
		return emit(tx, now, "attempt.cleaned", a.TaskID, AttemptRef(id))
	})
	return out, err
}

func (s *Store) NoteAttempt(ctx context.Context, id int64, kind, detail string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		a, err := getAttempt(tx, id)
		if err != nil {
			return err
		}
		return emit(tx, s.stamp(), "attempt."+kind, a.TaskID, AttemptRef(id)+": "+detail)
	})
}

func (s *Store) Attempt(ctx context.Context, id int64) (Attempt, error) {
	a, err := scanAttempt(s.db.QueryRowContext(ctx, attemptSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, fmt.Errorf("%w: attempt %s", ErrNotFound, AttemptRef(id))
	}
	return a, err
}

func (s *Store) Attempts(ctx context.Context, taskID int64, limit int) ([]Attempt, error) {
	if limit < 1 {
		return nil, fmt.Errorf("%w: limit must be at least 1", ErrInvalid)
	}
	q, args := attemptSelect, []any{}
	if taskID != 0 {
		q += ` WHERE task_id = ?`
		args = append(args, taskID)
	}
	args = append(args, limit)
	return s.queryAttempts(ctx, `SELECT * FROM (`+q+` ORDER BY id DESC LIMIT ?) ORDER BY id`, args...)
}

func (s *Store) UncleanedAttempts(ctx context.Context) ([]Attempt, error) {
	return s.queryAttempts(ctx, attemptSelect+` WHERE cleaned_at = '' ORDER BY id`)
}

func (s *Store) LiveAttempts(ctx context.Context) ([]Attempt, error) {
	return s.queryAttempts(ctx, attemptSelect+` WHERE status IN (?, ?) ORDER BY id`, AttemptLaunching, AttemptRunning)
}

func (s *Store) LatestAttempt(ctx context.Context, taskID int64) (Attempt, bool, error) {
	a, err := scanAttempt(s.db.QueryRowContext(ctx, attemptSelect+` WHERE task_id = ? ORDER BY id DESC LIMIT 1`, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, false, nil
	}
	return a, err == nil, err
}

func (s *Store) queryAttempts(ctx context.Context, q string, args ...any) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
