// Package store holds hand's machine state in a sqlite database at
// state/hand.db and owns the one-way migration from the state/<id>.json files
// that used to hold it. The prose corpus stays in files; see index.go.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	KindShip  = "ship"
	KindScout = "scout"
)

const (
	HoldKindOperator = "operator"
	HoldKindBlocked  = "blocked"
)

// Wrapped by DeleteTask, rendered by callers as `task "<id>" not found`.
var ErrTaskNotFound = errors.New("not found")

// Wrapped by ClearHold, rendered by callers as `hold "<id>" not found`.
var ErrHoldNotFound = errors.New("not found")

// Wrapped by AddProject so a caller importing a registry that already lists a
// name can tell a duplicate from a real write fault.
var ErrProjectExists = errors.New("already registered")

type Herdr struct {
	Session     string `json:"session"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	PaneID      string `json:"pane_id"`
}

type Task struct {
	ID       string `json:"id"`
	Project  string `json:"project"`
	Kind     string `json:"kind"`
	Harness  string `json:"harness"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
	Worktree string `json:"worktree"`
	Brief    string `json:"brief"`
	Herdr    Herdr  `json:"herdr"`
	PR       string `json:"pr"`
	// hand itself ran the merge, not that the PR is merged: one merged by other
	// means leaves this false and is what MergeAnnounced records instead.
	MergeExecuted   bool   `json:"merged"`
	MergeExecutedAt string `json:"merged_at"`
	// Durable so a watcher restart resumes exactly where it stopped instead of
	// replaying every line the previous run already surfaced.
	ReportOffset int64 `json:"report_offset"`
	// A merge hand observed rather than performed. Distinct from MergeExecuted:
	// a restarted watcher needs to know the announcement went out even when
	// hand itself never ran the merge.
	MergeAnnounced bool `json:"pr_merged_observed"`
	// Durable for the same reason MergeAnnounced is: evidence can land while the
	// watcher is down, and a restart that re-derived this from current evidence
	// would conclude the line had already gone out and never print it.
	DoneVerified bool   `json:"done_verified"`
	CreatedAt    string `json:"created_at"`
	// Durable because a dwell clock reseeded to "now" on every restart never
	// accumulates past a threshold. Trustworthy only while StatusChangedFor
	// still matches the observed status; empty seeds the dwell from CreatedAt.
	StatusChangedAt  string `json:"status_changed_at"`
	StatusChangedFor string `json:"status_changed_for"`
	// Durable so a restart resumes the scout's deferred-done bookkeeping without
	// re-reading report history it has already consumed past ReportOffset.
	LastReportState string `json:"last_report_state"`
	LastReportNote  string `json:"last_report_note"`
	// Durable so a hand send message with no evidence it reached the pane -
	// a composer still busy past the --wait bound, a failed send, a failed
	// submit - leaves a trace instead of vanishing with the process that
	// attempted it; the operator who ran that send is the only one who would
	// otherwise know it was ever tried. Cleared on the next send that actually
	// reaches the pane, whatever message that send carries.
	SendUndeliveredMessage string `json:"send_undelivered_message"`
	SendUndeliveredAt      string `json:"send_undelivered_at"`
}

type Project struct {
	Name string
	URL  string
	Mode string
}

// Hold is its own row keyed by an arbitrary id, not a foreign key into task:
// the case it exists for is a question left open by work that has no task row
// behind it any more, because hand teardown already removed it. BlockedOn
// carries the id a HoldKindBlocked hold waits on; empty for HoldKindOperator.
type Hold struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason"`
	BlockedOn string `json:"blocked_on"`
	SetAt     string `json:"set_at"`
}

// Every machine-state file lives here, database included, so a human looking
// for fleet state has one directory to look in.
func Dir(homeDir string) string {
	return filepath.Join(homeDir, "state")
}

func Path(homeDir string) string {
	return filepath.Join(Dir(homeDir), "hand.db")
}

type DB struct {
	sql  *sql.DB
	home string
}

// The version-0 baseline plus every registered migration folded in: a column
// added here also needs its ALTER TABLE appended to migrations in
// schemaversion.go, or no database that already exists ever gains it.
const schema = `
CREATE TABLE IF NOT EXISTS task (
	id                 TEXT PRIMARY KEY,
	project            TEXT NOT NULL DEFAULT '',
	kind               TEXT NOT NULL DEFAULT '',
	harness            TEXT NOT NULL DEFAULT '',
	model              TEXT NOT NULL DEFAULT '',
	effort             TEXT NOT NULL DEFAULT '',
	worktree           TEXT NOT NULL DEFAULT '',
	brief              TEXT NOT NULL DEFAULT '',
	herdr_session      TEXT NOT NULL DEFAULT '',
	herdr_workspace_id TEXT NOT NULL DEFAULT '',
	herdr_tab_id       TEXT NOT NULL DEFAULT '',
	herdr_pane_id      TEXT NOT NULL DEFAULT '',
	pr                 TEXT NOT NULL DEFAULT '',
	merge_executed     INTEGER NOT NULL DEFAULT 0,
	merge_executed_at  TEXT NOT NULL DEFAULT '',
	report_offset      INTEGER NOT NULL DEFAULT 0,
	merge_announced    INTEGER NOT NULL DEFAULT 0,
	done_verified      INTEGER NOT NULL DEFAULT 0,
	created_at         TEXT NOT NULL DEFAULT '',
	status_changed_at  TEXT NOT NULL DEFAULT '',
	status_changed_for TEXT NOT NULL DEFAULT '',
	last_report_state  TEXT NOT NULL DEFAULT '',
	last_report_note   TEXT NOT NULL DEFAULT '',
	send_undelivered_message TEXT NOT NULL DEFAULT '',
	send_undelivered_at      TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS project (
	name     TEXT PRIMARY KEY,
	url      TEXT NOT NULL,
	mode     TEXT NOT NULL,
	position INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS hold (
	id         TEXT PRIMARY KEY,
	kind       TEXT NOT NULL DEFAULT '',
	reason     TEXT NOT NULL DEFAULT '',
	blocked_on TEXT NOT NULL DEFAULT '',
	set_at     TEXT NOT NULL DEFAULT ''
);
`

// Shared by the machine-state database and the derived index. The busy timeout
// matters because several hand commands can run at once, and sqlite's default
// is to fail the second write immediately rather than wait.
func open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	// The pragmas need a file: URI, and a URI means sqlite reads `%`, `#` and
	// `?` in the fleet home's path as syntax rather than as filename.
	uri := "file:" + (&url.URL{Path: path}).EscapedPath() + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// One connection, because every writer here is a short-lived CLI process and
	// a pool would let sqlite fail a second connection's write against the first.
	db.SetMaxOpenConns(1)
	return db, nil
}

// Safe to call on every command: creating the database and importing any
// pre-sqlite state are both idempotent.
func Open(homeDir string) (*DB, error) {
	sqlDB, err := open(Path(homeDir))
	if err != nil {
		return nil, err
	}
	db := &DB{sql: sqlDB, home: homeDir}
	if err := db.migrateSchema(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if err := db.migrateLegacy(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

func (db *DB) meta(key string) (string, error) {
	var value string
	err := db.sql.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read meta %q: %w", key, err)
	}
	return value, nil
}

func (db *DB) setMeta(key, value string) error {
	_, err := db.sql.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("write meta %q: %w", key, err)
	}
	return nil
}

const taskColumns = `id, project, kind, harness, model, effort, worktree, brief,
	herdr_session, herdr_workspace_id, herdr_tab_id, herdr_pane_id, pr,
	merge_executed, merge_executed_at, report_offset, merge_announced, done_verified,
	created_at, status_changed_at, status_changed_for, last_report_state, last_report_note,
	send_undelivered_message, send_undelivered_at`

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.Project, &t.Kind, &t.Harness, &t.Model, &t.Effort, &t.Worktree, &t.Brief,
		&t.Herdr.Session, &t.Herdr.WorkspaceID, &t.Herdr.TabID, &t.Herdr.PaneID, &t.PR,
		&t.MergeExecuted, &t.MergeExecutedAt, &t.ReportOffset, &t.MergeAnnounced, &t.DoneVerified,
		&t.CreatedAt, &t.StatusChangedAt, &t.StatusChangedFor, &t.LastReportState, &t.LastReportNote,
		&t.SendUndeliveredMessage, &t.SendUndeliveredAt)
	return t, err
}

func (db *DB) ReadTask(id string) (Task, bool, error) {
	row := db.sql.QueryRow(`SELECT `+taskColumns+` FROM task WHERE id = ?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, fmt.Errorf("read task %q: %w", id, err)
	}
	return t, true, nil
}

func (db *DB) WriteTask(t Task) error {
	_, err := db.sql.Exec(`INSERT INTO task (`+taskColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			project = excluded.project, kind = excluded.kind, harness = excluded.harness,
			model = excluded.model, effort = excluded.effort, worktree = excluded.worktree,
			brief = excluded.brief, herdr_session = excluded.herdr_session,
			herdr_workspace_id = excluded.herdr_workspace_id, herdr_tab_id = excluded.herdr_tab_id,
			herdr_pane_id = excluded.herdr_pane_id, pr = excluded.pr,
			merge_executed = excluded.merge_executed, merge_executed_at = excluded.merge_executed_at,
			report_offset = excluded.report_offset, merge_announced = excluded.merge_announced,
			done_verified = excluded.done_verified, created_at = excluded.created_at,
			status_changed_at = excluded.status_changed_at, status_changed_for = excluded.status_changed_for,
			last_report_state = excluded.last_report_state, last_report_note = excluded.last_report_note,
			send_undelivered_message = excluded.send_undelivered_message,
			send_undelivered_at = excluded.send_undelivered_at`,
		t.ID, t.Project, t.Kind, t.Harness, t.Model, t.Effort, t.Worktree, t.Brief,
		t.Herdr.Session, t.Herdr.WorkspaceID, t.Herdr.TabID, t.Herdr.PaneID, t.PR,
		t.MergeExecuted, t.MergeExecutedAt, t.ReportOffset, t.MergeAnnounced, t.DoneVerified,
		t.CreatedAt, t.StatusChangedAt, t.StatusChangedFor, t.LastReportState, t.LastReportNote,
		t.SendUndeliveredMessage, t.SendUndeliveredAt)
	if err != nil {
		return fmt.Errorf("write task %q: %w", t.ID, err)
	}
	return nil
}

func (db *DB) ListTasks() ([]Task, error) {
	rows, err := db.sql.Query(`SELECT ` + taskColumns + ` FROM task ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("list tasks: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return tasks, nil
}

func (db *DB) TaskExists(id string) (bool, error) {
	var one int
	err := db.sql.QueryRow(`SELECT 1 FROM task WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check task %q: %w", id, err)
	}
	return true, nil
}

func (db *DB) DeleteTask(id string) error {
	res, err := db.sql.Exec(`DELETE FROM task WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete task %q: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete task %q: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("task %q %w", id, ErrTaskNotFound)
	}
	return nil
}

func (db *DB) ListProjects() ([]Project, error) {
	rows, err := db.sql.Query(`SELECT name, url, mode FROM project ORDER BY position, name`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var projects []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.Name, &p.URL, &p.Mode); err != nil {
			return nil, fmt.Errorf("list projects: %w", err)
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	return projects, nil
}

func (db *DB) AddProject(p Project) error {
	_, err := db.sql.Exec(`INSERT INTO project (name, url, mode, position)
		VALUES (?, ?, ?, (SELECT COALESCE(MAX(position), -1) + 1 FROM project))`, p.Name, p.URL, p.Mode)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("project %q %w", p.Name, ErrProjectExists)
		}
		return fmt.Errorf("add project %q: %w", p.Name, err)
	}
	return nil
}

// RemoveProject reports whether a row was actually removed, leaving the
// not-registered wording to the caller that already owns it.
func (db *DB) RemoveProject(name string) (bool, error) {
	res, err := db.sql.Exec(`DELETE FROM project WHERE name = ?`, name)
	if err != nil {
		return false, fmt.Errorf("remove project %q: %w", name, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("remove project %q: %w", name, err)
	}
	return affected > 0, nil
}

const holdColumns = `id, kind, reason, blocked_on, set_at`

func scanHold(row interface{ Scan(...any) error }) (Hold, error) {
	var h Hold
	err := row.Scan(&h.ID, &h.Kind, &h.Reason, &h.BlockedOn, &h.SetAt)
	return h, err
}

// SetHold upserts, so an operator narrowing down a reason re-runs the same
// command rather than needing a clear first - SetAt then reads as when the
// hold was last set, not when it was first raised.
func (db *DB) SetHold(h Hold) error {
	_, err := db.sql.Exec(`INSERT INTO hold (`+holdColumns+`)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind = excluded.kind, reason = excluded.reason,
			blocked_on = excluded.blocked_on, set_at = excluded.set_at`,
		h.ID, h.Kind, h.Reason, h.BlockedOn, h.SetAt)
	if err != nil {
		return fmt.Errorf("write hold %q: %w", h.ID, err)
	}
	return nil
}

func (db *DB) ReadHold(id string) (Hold, bool, error) {
	row := db.sql.QueryRow(`SELECT `+holdColumns+` FROM hold WHERE id = ?`, id)
	h, err := scanHold(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Hold{}, false, nil
	}
	if err != nil {
		return Hold{}, false, fmt.Errorf("read hold %q: %w", id, err)
	}
	return h, true, nil
}

// ListHolds surfaces every row, whatever it holds: a caller that filtered
// here on kind or on BlockedOn being consistent with kind would let a row an
// external write left inconsistent silently disappear from "what is held"
// instead of being reported as a hold that needs attention.
func (db *DB) ListHolds() ([]Hold, error) {
	rows, err := db.sql.Query(`SELECT ` + holdColumns + ` FROM hold ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list holds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var holds []Hold
	for rows.Next() {
		h, err := scanHold(rows)
		if err != nil {
			return nil, fmt.Errorf("list holds: %w", err)
		}
		holds = append(holds, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list holds: %w", err)
	}
	return holds, nil
}

func (db *DB) ClearHold(id string) error {
	res, err := db.sql.Exec(`DELETE FROM hold WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("clear hold %q: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("clear hold %q: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("hold %q %w", id, ErrHoldNotFound)
	}
	return nil
}
