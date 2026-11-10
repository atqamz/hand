package state

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	StatusInbox     = "inbox"
	StatusActive    = "active"
	StatusDone      = "done"
	StatusAbandoned = "abandoned"
)

var statuses = []string{StatusInbox, StatusActive, StatusDone, StatusAbandoned}

var transitions = map[string][]string{
	StatusInbox:  {StatusActive, StatusAbandoned},
	StatusActive: {StatusDone, StatusAbandoned},
}

type Task struct {
	ID         int64
	Project    string
	Title      string
	Goal       string
	Status     string
	CreatedAt  string
	UpdatedAt  string
	FinishedAt string
	ParentID   int64
}

const taskSelect = `SELECT t.id, t.project, t.title, t.goal, t.status, t.created_at, t.updated_at, CASE WHEN t.status IN ('done', 'abandoned') THEN COALESCE((SELECT max(e.at) FROM event e WHERE e.task_id = t.id AND e.kind IN ('task.done', 'task.abandoned')), t.updated_at) ELSE '' END, COALESCE(t.parent_id, 0) FROM task t`

type scanner interface{ Scan(...any) error }

func scanTask(row scanner) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.Project, &t.Title, &t.Goal, &t.Status, &t.CreatedAt, &t.UpdatedAt, &t.FinishedAt, &t.ParentID)
	return t, err
}

func (s *Store) AddTask(ctx context.Context, project, title, goal string) (Task, error) {
	return s.AddTaskUnder(ctx, 0, project, title, goal)
}

func (s *Store) AddTaskUnder(ctx context.Context, parent int64, project, title, goal string) (Task, error) {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 200 || strings.ContainsAny(title, "\r\n") {
		return Task{}, fmt.Errorf("%w: task title must be one line of 1-200 characters", ErrInvalid)
	}
	now := s.stamp()
	t := Task{Project: project, Title: title, Goal: goal, Status: StatusInbox, CreatedAt: now, UpdatedAt: now, ParentID: parent}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM project WHERE name = ?`, project).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: project %s", ErrNotFound, project)
		}
		var up any
		detail := ""
		if parent != 0 {
			if err := tx.QueryRow(`SELECT count(*) FROM task WHERE id = ?`, parent).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("%w: task %s", ErrNotFound, TaskRef(parent))
			}
			up, detail = parent, TaskRef(parent)
		}
		res, err := tx.Exec(`INSERT INTO task(project, title, goal, status, created_at, updated_at, parent_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			t.Project, t.Title, t.Goal, t.Status, t.CreatedAt, t.UpdatedAt, up)
		if err != nil {
			return err
		}
		if t.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return emit(tx, now, "task.added", t.ID, detail)
	})
	return t, err
}

func (s *Store) Task(ctx context.Context, id int64) (Task, error) {
	t, err := scanTask(s.db.QueryRowContext(ctx, taskSelect+` WHERE t.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, fmt.Errorf("%w: task %s", ErrNotFound, TaskRef(id))
	}
	return t, err
}

func (s *Store) Tasks(ctx context.Context, want []string, limit int) ([]Task, error) {
	if limit < 1 {
		return nil, fmt.Errorf("%w: limit must be at least 1", ErrInvalid)
	}
	for _, st := range want {
		if !slices.Contains(statuses, st) {
			return nil, fmt.Errorf("%w: unknown task status %q; want one of %s", ErrInvalid, st, strings.Join(statuses, ", "))
		}
	}
	q, args := taskSelect, []any{}
	if len(want) > 0 {
		q += ` WHERE t.status IN (` + strings.TrimSuffix(strings.Repeat("?,", len(want)), ",") + `)`
		for _, st := range want {
			args = append(args, st)
		}
	}
	q += ` ORDER BY t.id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type TaskQuery struct {
	Q        string
	Statuses []string
	Project  string
	Since    string
	Until    string
	Limit    int
	Offset   int
}

func StatusWords(list string) ([]string, error) {
	var out []string
	words := strings.Split(list, ",")
	for _, w := range words {
		switch w = strings.TrimSpace(w); w {
		case "all":
			if len(words) > 1 {
				return nil, fmt.Errorf("%w: all stands alone, not in a list; got %q", ErrInvalid, list)
			}
			return nil, nil
		case "finished":
			out = append(out, StatusDone, StatusAbandoned)
		default:
			out = append(out, w)
		}
	}
	return out, nil
}

const finishedAt = `COALESCE(f.at, t.updated_at)`

func (s *Store) SearchTasks(ctx context.Context, q TaskQuery) ([]Task, int, error) {
	if q.Limit < 1 {
		return nil, 0, fmt.Errorf("%w: limit must be at least 1", ErrInvalid)
	}
	for _, st := range q.Statuses {
		if !slices.Contains(statuses, st) {
			return nil, 0, fmt.Errorf("%w: unknown task status %q; want one of %s", ErrInvalid, st, strings.Join(statuses, ", "))
		}
	}
	var where []string
	var args []any
	if len(q.Statuses) > 0 {
		where = append(where, `t.status IN (`+strings.TrimSuffix(strings.Repeat("?,", len(q.Statuses)), ",")+`)`)
		for _, st := range q.Statuses {
			args = append(args, st)
		}
	}
	if q.Project != "" {
		where = append(where, `t.project = ?`)
		args = append(args, q.Project)
	}
	for _, d := range []struct{ name, value, op string }{{"since", q.Since, ">="}, {"until", q.Until, "<="}} {
		if d.value == "" {
			continue
		}
		if _, err := time.Parse(time.DateOnly, d.value); err != nil {
			return nil, 0, fmt.Errorf("%w: %s %q is not a date; want YYYY-MM-DD", ErrInvalid, d.name, d.value)
		}
		where = append(where, `substr(`+finishedAt+`, 1, 10) `+d.op+` ?`)
		args = append(args, d.value)
	}
	for _, w := range strings.Fields(strings.ToLower(q.Q)) {
		where = append(where, `(instr(lower(t.title), ?) > 0 OR instr(lower(t.goal), ?) > 0 OR 't' || t.id = ?)`)
		args = append(args, w, w, w)
	}
	from := ` FROM task t LEFT JOIN (SELECT task_id, max(at) AS at FROM event WHERE kind IN ('task.done', 'task.abandoned') GROUP BY task_id) f ON f.task_id = t.id`
	if len(where) > 0 {
		from += ` WHERE ` + strings.Join(where, ` AND `)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*)`+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.project, t.title, t.goal, t.status, t.created_at, t.updated_at, CASE WHEN t.status IN ('done', 'abandoned') THEN `+finishedAt+` ELSE '' END, COALESCE(t.parent_id, 0)`+from+` ORDER BY `+finishedAt+` DESC, t.id DESC LIMIT ? OFFSET ?`,
		append(args, q.Limit, max(q.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Project, &t.Title, &t.Goal, &t.Status, &t.CreatedAt, &t.UpdatedAt, &t.FinishedAt, &t.ParentID); err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

func (s *Store) CountTasks(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, count(*) FROM task GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

func (s *Store) Transition(ctx context.Context, id int64, to string, delivered ...string) (Task, error) {
	if !slices.Contains(statuses, to) {
		return Task{}, fmt.Errorf("%w: unknown task status %q", ErrInvalid, to)
	}
	if len(delivered) > 0 && to != StatusDone {
		return Task{}, fmt.Errorf("%w: only a task becoming done takes deliverables", ErrInvalid)
	}
	var out Task
	err := s.tx(ctx, func(tx *sql.Tx) error {
		cur, err := scanTask(tx.QueryRow(taskSelect+` WHERE t.id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: task %s", ErrNotFound, TaskRef(id))
		}
		if err != nil {
			return err
		}
		if !slices.Contains(transitions[cur.Status], to) {
			return fmt.Errorf("%w: task %s is %s and cannot become %s", ErrConflict, TaskRef(id), cur.Status, to)
		}
		if to == StatusDone || to == StatusAbandoned {
			live, err := liveAttempt(tx, id)
			if err != nil {
				return err
			}
			if live != 0 {
				return fmt.Errorf("%w: task %s has live attempt %s; stop it first", ErrConflict, TaskRef(id), AttemptRef(live))
			}
			if to == StatusDone {
				var unread int64
				err := tx.QueryRow(`SELECT id FROM report WHERE task_id = ? AND acked_at = '' ORDER BY id LIMIT 1`, id).Scan(&unread)
				if err == nil {
					return fmt.Errorf("%w: task %s has unread report %s; read and ack it first", ErrConflict, TaskRef(id), ReportRef(unread))
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
		}
		from, now := cur.Status, s.stamp()
		res, err := tx.Exec(`UPDATE task SET status = ?, updated_at = ? WHERE id = ? AND status = ?`, to, now, id, from)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("%w: task %s changed concurrently", ErrConflict, TaskRef(id))
		}
		cur.Status, cur.UpdatedAt = to, now
		if to == StatusDone || to == StatusAbandoned {
			cur.FinishedAt = now
		}
		out = cur
		if err := emit(tx, now, "task."+to, id, from+"->"+to); err != nil {
			return err
		}
		for _, ref := range delivered {
			if err := emit(tx, now, "task.delivered", id, ref); err != nil {
				return err
			}
		}
		if to == StatusDone || to == StatusAbandoned {
			if err := withdrawOpen(tx, id, now); err != nil {
				return err
			}
			return cancelTimers(tx, id, now)
		}
		return nil
	})
	return out, err
}

func (s *Store) Deliver(ctx context.Context, id int64, refs ...string) (Task, []string, error) {
	var out Task
	var added []string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		cur, err := scanTask(tx.QueryRow(taskSelect+` WHERE t.id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: task %s", ErrNotFound, TaskRef(id))
		}
		if err != nil {
			return err
		}
		if cur.Status != StatusActive && cur.Status != StatusDone {
			return fmt.Errorf("%w: task %s is %s; only an active or done task takes a deliverable", ErrConflict, TaskRef(id), cur.Status)
		}
		out = cur
		now := s.stamp()
		for _, ref := range refs {
			var have int
			if err := tx.QueryRow(`SELECT count(*) FROM event WHERE kind = 'task.delivered' AND task_id = ? AND detail = ?`, id, ref).Scan(&have); err != nil {
				return err
			}
			if have > 0 {
				continue
			}
			if err := emit(tx, now, "task.delivered", id, ref); err != nil {
				return err
			}
			added = append(added, ref)
		}
		return nil
	})
	if err != nil {
		added = nil
	}
	return out, added, err
}

func (s *Store) SetParent(ctx context.Context, id, parent int64) (Task, error) {
	var out Task
	err := s.tx(ctx, func(tx *sql.Tx) error {
		cur, err := scanTask(tx.QueryRow(taskSelect+` WHERE t.id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: task %s", ErrNotFound, TaskRef(id))
		}
		if err != nil {
			return err
		}
		out = cur
		if parent != 0 {
			var n int
			if err := tx.QueryRow(`SELECT count(*) FROM task WHERE id = ?`, parent).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("%w: task %s", ErrNotFound, TaskRef(parent))
			}
			if parent == id {
				return fmt.Errorf("%w: task %s cannot sit under itself", ErrConflict, TaskRef(id))
			}
			chain, err := ancestry(tx, parent, id)
			if err != nil {
				return err
			}
			if len(chain) > 0 {
				return fmt.Errorf("%w: task %s cannot sit under %s: %s is already below it (%s)", ErrConflict, TaskRef(id), TaskRef(parent), TaskRef(parent), chain)
			}
		}
		if cur.ParentID == parent {
			return nil
		}
		now := s.stamp()
		var up any
		detail := "root"
		if parent != 0 {
			up, detail = parent, TaskRef(parent)
		}
		if _, err := tx.Exec(`UPDATE task SET parent_id = ?, updated_at = ? WHERE id = ?`, up, now, id); err != nil {
			return err
		}
		out.ParentID, out.UpdatedAt = parent, now
		return emit(tx, now, "task.parented", id, detail)
	})
	return out, err
}

func ancestry(tx *sql.Tx, from, target int64) (string, error) {
	var found int
	err := tx.QueryRow(`WITH RECURSIVE up(id) AS (SELECT ? UNION SELECT t.parent_id FROM task t JOIN up ON t.id = up.id WHERE t.parent_id IS NOT NULL) SELECT 1 FROM up WHERE id = ?`, from, target).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	chain := []string{TaskRef(from)}
	for cur := from; cur != target && len(chain) < 1000; {
		if err := tx.QueryRow(`SELECT COALESCE(parent_id, 0) FROM task WHERE id = ?`, cur).Scan(&cur); err != nil {
			return "", err
		}
		if cur == 0 {
			break
		}
		chain = append(chain, TaskRef(cur))
	}
	return strings.Join(chain, " > "), nil
}

func (s *Store) Children(ctx context.Context, id int64) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, taskSelect+` WHERE t.parent_id = ? ORDER BY t.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ChildCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT parent_id, count(*) FROM task WHERE parent_id IS NOT NULL GROUP BY parent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func (s *Store) OpenChildCount(ctx context.Context, id int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM task WHERE parent_id = ? AND status IN ('inbox', 'active')`, id).Scan(&n)
	return n, err
}

func Tree(tasks []Task) []Task {
	ids := make(map[int64]bool, len(tasks))
	for _, t := range tasks {
		ids[t.ID] = true
	}
	kids := map[int64][]Task{}
	var roots []Task
	for _, t := range tasks {
		if t.ParentID != 0 && ids[t.ParentID] && t.ParentID != t.ID {
			kids[t.ParentID] = append(kids[t.ParentID], t)
		} else {
			roots = append(roots, t)
		}
	}
	byID := func(a, b Task) int { return cmp.Compare(a.ID, b.ID) }
	slices.SortFunc(roots, byID)
	for _, k := range kids {
		slices.SortFunc(k, byID)
	}
	out := make([]Task, 0, len(tasks))
	seen := make(map[int64]bool, len(tasks))
	var walk func(Task)
	walk = func(t Task) {
		if seen[t.ID] {
			return
		}
		seen[t.ID] = true
		out = append(out, t)
		for _, c := range kids[t.ID] {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	for _, t := range tasks {
		walk(t)
	}
	return out
}

func Nested(tasks []Task) bool {
	for _, t := range tasks {
		if t.ParentID != 0 {
			return true
		}
	}
	return false
}
