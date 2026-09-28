package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type SupervisorSpec struct {
	Harness    string
	Model      string
	Effort     string
	Argv       []string
	Session    string
	WakeCursor int64
}

type Supervisor struct {
	ID      int64
	Harness string
	Model   string
	Effort  string
	Argv    []string
	Session string
	Status  string
	Terminal
	WakeCursor int64
	Reason     string
	CreatedAt  string
	EndedAt    string
}

func (s Supervisor) Live() bool { return s.Status == AttemptLaunching || s.Status == AttemptRunning }

type SupervisorInput struct {
	ID          int64
	Body        string
	CreatedAt   string
	DeliveredAt string
}

func SupervisorRef(id int64) string { return "s" + strconv.FormatInt(id, 10) }

func inputRef(id int64) string { return "i" + strconv.FormatInt(id, 10) }

const supervisorSelect = `SELECT id, harness, model, effort, argv, session, status, server_generation, terminal_id, pane_id, pid, start_marker, wake_cursor, reason, created_at, ended_at FROM supervisor`

func scanSupervisor(row scanner) (Supervisor, error) {
	var s Supervisor
	var argv string
	err := row.Scan(&s.ID, &s.Harness, &s.Model, &s.Effort, &argv, &s.Session, &s.Status, &s.ServerGeneration, &s.TerminalID, &s.PaneID, &s.PID, &s.StartMarker, &s.WakeCursor, &s.Reason, &s.CreatedAt, &s.EndedAt)
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal([]byte(argv), &s.Argv)
}

func getSupervisor(tx *sql.Tx, id int64) (Supervisor, error) {
	s, err := scanSupervisor(tx.QueryRow(supervisorSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Supervisor{}, fmt.Errorf("%w: supervisor %s", ErrNotFound, SupervisorRef(id))
	}
	return s, err
}

func (s *Store) AddSupervisor(ctx context.Context, spec SupervisorSpec) (Supervisor, error) {
	if !slices.Contains(Harnesses, spec.Harness) {
		return Supervisor{}, fmt.Errorf("%w: harness %q must be one of %s", ErrInvalid, spec.Harness, strings.Join(Harnesses, ", "))
	}
	if len(spec.Argv) == 0 {
		return Supervisor{}, fmt.Errorf("%w: supervisor needs a launch command", ErrInvalid)
	}
	argv, err := json.Marshal(spec.Argv)
	if err != nil {
		return Supervisor{}, err
	}
	out := Supervisor{Harness: spec.Harness, Model: spec.Model, Effort: spec.Effort, Argv: spec.Argv, Session: spec.Session, Status: AttemptLaunching, WakeCursor: spec.WakeCursor, CreatedAt: s.stamp()}
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var live int64
		err := tx.QueryRow(`SELECT id FROM supervisor WHERE status IN (?, ?)`, AttemptLaunching, AttemptRunning).Scan(&live)
		if err == nil {
			return fmt.Errorf("%w: supervisor %s is already live", ErrConflict, SupervisorRef(live))
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		res, err := tx.Exec(`INSERT INTO supervisor(harness, model, effort, argv, session, status, wake_cursor, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			out.Harness, out.Model, out.Effort, string(argv), out.Session, out.Status, out.WakeCursor, out.CreatedAt)
		if err != nil {
			return err
		}
		if out.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return emit(tx, out.CreatedAt, "supervisor.launching", 0, SupervisorRef(out.ID)+": "+out.Harness)
	})
	return out, err
}

func (s *Store) SupervisorRunning(ctx context.Context, id int64, t Terminal) (Supervisor, error) {
	var out Supervisor
	err := s.tx(ctx, func(tx *sql.Tx) error {
		sup, err := getSupervisor(tx, id)
		if err != nil {
			return err
		}
		if sup.Status != AttemptLaunching {
			return fmt.Errorf("%w: supervisor %s is %s and cannot become %s", ErrConflict, SupervisorRef(id), sup.Status, AttemptRunning)
		}
		if _, err := tx.Exec(`UPDATE supervisor SET status = ?, server_generation = ?, terminal_id = ?, pane_id = ?, pid = ?, start_marker = ? WHERE id = ?`,
			AttemptRunning, t.ServerGeneration, t.TerminalID, t.PaneID, t.PID, t.StartMarker, id); err != nil {
			return err
		}
		sup.Status, sup.Terminal = AttemptRunning, t
		out = sup
		return emit(tx, s.stamp(), "supervisor.started", 0, SupervisorRef(id)+": pane "+t.PaneID)
	})
	return out, err
}

var supervisorEnds = map[string][]string{
	AttemptLaunching: {AttemptFailed},
	AttemptRunning:   {AttemptExited, AttemptInterrupted, AttemptStopped},
}

func (s *Store) EndSupervisor(ctx context.Context, id int64, to, reason string) (Supervisor, error) {
	var out Supervisor
	err := s.tx(ctx, func(tx *sql.Tx) error {
		sup, err := getSupervisor(tx, id)
		if err != nil {
			return err
		}
		if !slices.Contains(supervisorEnds[sup.Status], to) {
			return fmt.Errorf("%w: supervisor %s is %s and cannot become %s", ErrConflict, SupervisorRef(id), sup.Status, to)
		}
		now := s.stamp()
		if _, err := tx.Exec(`UPDATE supervisor SET status = ?, reason = ?, ended_at = ? WHERE id = ?`, to, reason, now, id); err != nil {
			return err
		}
		sup.Status, sup.Reason, sup.EndedAt = to, reason, now
		out = sup
		return emit(tx, now, "supervisor."+to, 0, SupervisorRef(id)+": "+reason)
	})
	return out, err
}

func (s *Store) SetSupervisorSession(ctx context.Context, id int64, session string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := getSupervisor(tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE supervisor SET session = ? WHERE id = ?`, session, id); err != nil {
			return err
		}
		return emit(tx, s.stamp(), "supervisor.session", 0, SupervisorRef(id)+": "+session)
	})
}

func (s *Store) LiveSupervisor(ctx context.Context) (Supervisor, bool, error) {
	return s.oneSupervisor(ctx, supervisorSelect+` WHERE status IN (?, ?) ORDER BY id DESC LIMIT 1`, AttemptLaunching, AttemptRunning)
}

func (s *Store) LatestSupervisor(ctx context.Context) (Supervisor, bool, error) {
	return s.oneSupervisor(ctx, supervisorSelect+` ORDER BY id DESC LIMIT 1`)
}

func (s *Store) SupervisorOrigin(ctx context.Context, sup Supervisor) (int64, error) {
	if sup.Session == "" {
		return sup.ID, nil
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT MIN(id) FROM supervisor WHERE session = ?`, sup.Session).Scan(&id)
	return id, err
}

func (s *Store) SupervisorSessions(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT session FROM supervisor WHERE session != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var session string
		if err := rows.Scan(&session); err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

func (s *Store) oneSupervisor(ctx context.Context, q string, args ...any) (Supervisor, bool, error) {
	sup, err := scanSupervisor(s.db.QueryRowContext(ctx, q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Supervisor{}, false, nil
	}
	return sup, err == nil, err
}

func (s *Store) AddSupervisorInput(ctx context.Context, body string) (SupervisorInput, error) {
	in := SupervisorInput{Body: body, CreatedAt: s.stamp()}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO supervisor_input(body, created_at) VALUES (?, ?)`, body, in.CreatedAt)
		if err != nil {
			return err
		}
		if in.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return emit(tx, in.CreatedAt, "supervisor.input", 0, inputRef(in.ID)+": "+strconv.Itoa(len(body))+" bytes")
	})
	return in, err
}

func (s *Store) PendingSupervisorInputs(ctx context.Context) ([]SupervisorInput, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, body, created_at, delivered_at FROM supervisor_input WHERE delivered_at = '' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SupervisorInput
	for rows.Next() {
		var in SupervisorInput
		if err := rows.Scan(&in.ID, &in.Body, &in.CreatedAt, &in.DeliveredAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (s *Store) DeliverSupervisorInput(ctx context.Context, inputID int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		now := s.stamp()
		res, err := tx.Exec(`UPDATE supervisor_input SET delivered_at = ? WHERE id = ? AND delivered_at = ''`, now, inputID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return fmt.Errorf("%w: input %s is already delivered or does not exist", ErrConflict, inputRef(inputID))
		}
		return emit(tx, now, "supervisor.delivered", 0, inputRef(inputID))
	})
}

func (s *Store) AdvanceWakeCursor(ctx context.Context, supervisorID, seq int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		sup, err := getSupervisor(tx, supervisorID)
		if err != nil {
			return err
		}
		if seq <= sup.WakeCursor {
			return fmt.Errorf("%w: wake cursor of %s is %d, not before %d", ErrConflict, SupervisorRef(supervisorID), sup.WakeCursor, seq)
		}
		if _, err := tx.Exec(`UPDATE supervisor SET wake_cursor = ? WHERE id = ?`, seq, supervisorID); err != nil {
			return err
		}
		return emit(tx, s.stamp(), "supervisor.delivered", 0, "wake to "+strconv.FormatInt(seq, 10))
	})
}
