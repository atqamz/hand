package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const screenMax = 2048

var terminalEscape = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b[ -/]*[0-~]?`)

func screenTail(s string) string {
	s = terminalEscape.ReplaceAllString(strings.ToValidUTF8(s, ""), "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t':
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if cut := len(s) - screenMax; cut > 0 {
		for !utf8.RuneStart(s[cut]) {
			cut++
		}
		s = strings.TrimSpace(s[cut:])
	}
	return s
}

const (
	AttemptLaunching   = "launching"
	AttemptRunning     = "running"
	AttemptExited      = "exited"
	AttemptStopped     = "stopped"
	AttemptInterrupted = "interrupted"
	AttemptFailed      = "failed"
)

var Harnesses = []string{"claude", "codex", "opencode", "agy", "copilot", "omp"}

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
	Socket           string
}

type AttemptSpec struct {
	TaskID    int64
	Harness   string
	Model     string
	Effort    string
	Argv      []string
	ArgvFor   func(worktree string) ([]string, error)
	Continues int64
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
	Continues int64
	CreatedAt string
	EndedAt   string
	CleanedAt string
	Kept      string
}

func (a Attempt) Live() bool { return a.Status == AttemptLaunching || a.Status == AttemptRunning }

const attemptColumns = `id, task_id, harness, model, effort, %sworktree, branch, status, server_generation, terminal_id, pane_id, pid, start_marker, socket, reason, continues, created_at, ended_at, cleaned_at, kept FROM attempt`

var (
	attemptSelect    = `SELECT ` + fmt.Sprintf(attemptColumns, "argv, ")
	attemptBriefless = `SELECT ` + fmt.Sprintf(attemptColumns, "")
)

func scanAttempt(row scanner) (Attempt, error) {
	var argv string
	a, err := scanAttemptFields(row, &argv)
	if err != nil {
		return a, err
	}
	return a, json.Unmarshal([]byte(argv), &a.Argv)
}

func scanAttemptFields(row scanner, argv *string) (Attempt, error) {
	var a Attempt
	dest := []any{&a.ID, &a.TaskID, &a.Harness, &a.Model, &a.Effort}
	if argv != nil {
		dest = append(dest, argv)
	}
	dest = append(dest, &a.Worktree, &a.Branch, &a.Status,
		&a.ServerGeneration, &a.TerminalID, &a.PaneID, &a.PID, &a.StartMarker, &a.Socket, &a.Reason, &a.Continues, &a.CreatedAt, &a.EndedAt, &a.CleanedAt, &a.Kept)
	return a, row.Scan(dest...)
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
	a := Attempt{TaskID: spec.TaskID, Harness: spec.Harness, Model: spec.Model, Effort: spec.Effort, Argv: spec.Argv, Status: AttemptLaunching, CreatedAt: s.stamp()}
	err := s.tx(ctx, func(tx *sql.Tx) error {
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
		var branch string
		if spec.Continues != 0 {
			prev, err := getAttempt(tx, spec.Continues)
			if err != nil {
				return err
			}
			switch {
			case prev.TaskID != spec.TaskID:
				return fmt.Errorf("%w: attempt %s belongs to task %s, not %s; continue one of %s's own attempts", ErrInvalid, AttemptRef(prev.ID), TaskRef(prev.TaskID), TaskRef(spec.TaskID), TaskRef(spec.TaskID))
			case prev.Live():
				return fmt.Errorf("%w: attempt %s is %s; stop it first with `hand attempt stop %s`", ErrConflict, AttemptRef(prev.ID), prev.Status, AttemptRef(prev.ID))
			case prev.CleanedAt == "":
				return fmt.Errorf("%w: attempt %s is not cleaned; run `hand attempt clean %s` first", ErrConflict, AttemptRef(prev.ID), AttemptRef(prev.ID))
			}
			branch = prev.Branch
			a.Continues = prev.ID
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
		if branch == "" {
			branch = "hand/" + fleet + "/" + name
		}
		a.Worktree, a.Branch = filepath.Join(worktreeRoot, name), branch
		if spec.ArgvFor != nil {
			if a.Argv, err = spec.ArgvFor(a.Worktree); err != nil {
				return err
			}
		}
		if len(a.Argv) == 0 {
			return fmt.Errorf("%w: attempt needs a launch command", ErrInvalid)
		}
		argv, err := json.Marshal(a.Argv)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, continues, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.ID, a.TaskID, a.Harness, a.Model, a.Effort, string(argv), a.Worktree, a.Branch, a.Status, a.Continues, a.CreatedAt); err != nil {
			return err
		}
		if err := cancelWaits(tx, a.TaskID, a.CreatedAt); err != nil {
			return err
		}
		if err := emit(tx, a.CreatedAt, "attempt."+AttemptLaunching, a.TaskID, AttemptRef(a.ID)); err != nil {
			return err
		}
		if a.Harness == "opencode" && a.Model != "" {
			return emit(tx, a.CreatedAt, "model.requested", a.TaskID, AttemptRef(a.ID)+": "+askedModel(a.Model, a.Effort))
		}
		return nil
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
		if _, err := tx.Exec(`UPDATE attempt SET status = ?, server_generation = ?, terminal_id = ?, pane_id = ?, pid = ?, start_marker = ?, socket = ? WHERE id = ?`,
			AttemptRunning, t.ServerGeneration, t.TerminalID, t.PaneID, t.PID, t.StartMarker, t.Socket, id); err != nil {
			return err
		}
		a.Status, a.Terminal = AttemptRunning, t
		out = a
		return emit(tx, s.stamp(), "attempt."+AttemptRunning, a.TaskID, AttemptRef(id)+": pane "+t.PaneID)
	})
	return out, err
}

func (s *Store) StampAttemptSocket(ctx context.Context, id int64, socket string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		a, err := getAttempt(tx, id)
		if err != nil || a.Status != AttemptRunning || a.Socket != "" {
			return err
		}
		if _, err := tx.Exec(`UPDATE attempt SET socket = ? WHERE id = ?`, socket, id); err != nil {
			return err
		}
		return emit(tx, s.stamp(), "attempt.socket", a.TaskID, AttemptRef(id)+": "+socket)
	})
}

func (s *Store) AttemptSwitched(ctx context.Context, id int64, t Terminal, to, model, effort string, requested bool) (Attempt, error) {
	var out Attempt
	err := s.tx(ctx, func(tx *sql.Tx) error {
		a, err := getAttempt(tx, id)
		if err != nil {
			return err
		}
		if a.Status != AttemptRunning {
			return fmt.Errorf("%w: attempt %s is %s and cannot be switched", ErrConflict, AttemptRef(id), a.Status)
		}
		if _, err := tx.Exec(`UPDATE attempt SET server_generation = ?, terminal_id = ?, pane_id = ?, pid = ?, start_marker = ?, socket = ? WHERE id = ?`,
			t.ServerGeneration, t.TerminalID, t.PaneID, t.PID, t.StartMarker, t.Socket, id); err != nil {
			return err
		}
		a.Terminal = t
		out = a
		now := s.stamp()
		if err := emit(tx, now, "attempt.switched", a.TaskID, AttemptRef(id)+": "+to+", pane "+t.PaneID); err != nil {
			return err
		}
		if err := setAttemptModel(tx, now, a, model, effort); err != nil {
			return err
		}
		out.Model, out.Effort = model, effort
		if requested && a.Harness == "opencode" && model != "" {
			return emit(tx, now, "model.requested", a.TaskID, AttemptRef(id)+": "+askedModel(model, effort))
		}
		return nil
	})
	return out, err
}

func (s *Store) EndAttempt(ctx context.Context, id int64, to, reason string) (Attempt, error) {
	return s.EndAttemptWithScreen(ctx, id, to, reason, "")
}

func (s *Store) EndAttemptWithScreen(ctx context.Context, id int64, to, reason, screen string) (Attempt, error) {
	var out Attempt
	var tail any
	if t := screenTail(screen); t != "" {
		tail = t
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		a, err := getAttempt(tx, id)
		if err != nil {
			return err
		}
		if !slices.Contains(attemptEnds[a.Status], to) {
			return fmt.Errorf("%w: attempt %s is %s and cannot become %s", ErrConflict, AttemptRef(id), a.Status, to)
		}
		now := s.stamp()
		if _, err := tx.Exec(`UPDATE attempt SET status = ?, reason = ?, ended_at = ?, screen = ? WHERE id = ?`, to, reason, now, tail, id); err != nil {
			return err
		}
		a.Status, a.Reason, a.EndedAt = to, reason, now
		out = a
		return emit(tx, now, "attempt."+to, a.TaskID, AttemptRef(id)+": "+reason)
	})
	return out, err
}

func (s *Store) CleanAttempt(ctx context.Context, id int64) (Attempt, error) {
	return s.cleanAttempt(ctx, id, "")
}

func (s *Store) CleanAttemptAfter(ctx context.Context, id int64, window string) (Attempt, error) {
	return s.cleanAttempt(ctx, id, "removed by retention after "+window)
}

func (s *Store) KeepAttempt(ctx context.Context, id int64, reason string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		a, err := getAttempt(tx, id)
		if err != nil {
			return err
		}
		if a.Kept == reason || a.CleanedAt != "" {
			return nil
		}
		if _, err := tx.Exec(`UPDATE attempt SET kept = ? WHERE id = ?`, reason, id); err != nil {
			return err
		}
		return emit(tx, s.stamp(), "attempt.kept", a.TaskID, AttemptRef(id)+": "+reason)
	})
}

func (s *Store) KeptAttempts(ctx context.Context) ([]Attempt, error) {
	return s.queryAttempts(ctx, attemptSelect+` WHERE kept != '' AND cleaned_at = '' ORDER BY id`)
}

func (s *Store) cleanAttempt(ctx context.Context, id int64, why string) (Attempt, error) {
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
		detail := AttemptRef(id)
		if why != "" {
			detail += ": " + why
		}
		return emit(tx, now, "attempt.cleaned", a.TaskID, detail)
	})
	return out, err
}

func (s *Store) NoteAttempt(ctx context.Context, id int64, kind, detail string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		a, err := getAttempt(tx, id)
		if err != nil {
			return err
		}
		if !a.Live() && slices.Contains([]string{"blocked", "working", "limited", "long"}, kind) {
			return fmt.Errorf("%w: attempt %s is %s", ErrConflict, AttemptRef(id), a.Status)
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

func (s *Store) AttemptScreen(ctx context.Context, id int64) (string, error) {
	var screen sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT screen FROM attempt WHERE id = ?`, id).Scan(&screen)
	return screen.String, err
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

func (s *Store) LatestAttemptsBriefless(ctx context.Context) (map[int64]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, attemptBriefless+` WHERE id IN (SELECT MAX(id) FROM attempt GROUP BY task_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attempts []Attempt
	for rows.Next() {
		a, err := scanAttemptFields(rows, nil)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[int64]Attempt, len(attempts))
	for _, a := range attempts {
		out[a.TaskID] = a
	}
	return out, nil
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

func (s *Store) SetAttemptModel(ctx context.Context, id int64, model, effort string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		a, err := getAttempt(tx, id)
		if err != nil || !a.Live() {
			return err
		}
		return setAttemptModel(tx, s.stamp(), a, model, effort)
	})
}

func setAttemptModel(tx *sql.Tx, now string, a Attempt, model, effort string) error {
	if a.Model == model && a.Effort == effort {
		return nil
	}
	if _, err := tx.Exec(`UPDATE attempt SET model = ?, effort = ? WHERE id = ?`, model, effort, a.ID); err != nil {
		return err
	}
	return emit(tx, now, "model.changed", a.TaskID, strings.Join(strings.Fields(AttemptRef(a.ID)+": "+model+" "+effort), " "))
}
