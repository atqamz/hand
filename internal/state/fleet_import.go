package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/flock"
)

type ImportOptions struct {
	SourceID   string
	SourceHome string
	DestID     string
	DestHome   string
	DryRun     bool
}

type ImportLock struct {
	file *os.File
}

func ImportMarker(home string) string { return filepath.Join(home, "imported-into") }

func LockImport(home string) (*ImportLock, error) {
	f, err := os.OpenFile(filepath.Join(home, "hand.db.import.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	ok, err := flock.Lock(f, false)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !ok {
		_ = f.Close()
		return nil, fmt.Errorf("%w: another fleet import is running in %s", ErrConflict, home)
	}
	return &ImportLock{file: f}, nil
}

func (l *ImportLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return flock.Release(l.file)
}

type ImportHealth struct {
	Supervisor string
	Attempts   []int64
}

type ImportProject struct {
	Name, Repo, Source, Action string
}

type ImportRef struct {
	Table, Key, Column, From, To string
}

type ImportAmbiguous struct {
	Table, Key, Column, Token, Context string
}

type ImportReportRow struct {
	ID, TaskID, AttemptID, NewTaskID   int64
	Status, CreatedAt, Body, TaskTitle string
}

type ImportResult struct {
	Offset                 int64
	NewTaskMin, NewTaskMax int64
	Projects               []ImportProject
	Tasks                  map[string]int
	Refs                   []ImportRef
	Left                   []ImportRef
	Ambiguous              []ImportAmbiguous
	Reports                []ImportReportRow
	TextSHA256             string
	EventSeq               int64
}

type importPlan struct {
	result   ImportResult
	projects []Project
	tasks    []Task
}

var importRefPattern = regexp.MustCompile(`[tardiw][0-9]+`)

func openImport(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+uriPath.Replace(path)+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func ImportSchema(home string) (int, error) {
	db, err := openImport(filepath.Join(home, "hand.db"))
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var v int
	err = db.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v, err
}

func ReadFleet(home string) (Fleet, error) {
	db, err := openImport(filepath.Join(home, "hand.db"))
	if err != nil {
		return Fleet{}, err
	}
	defer db.Close()
	return scanFleetDB(db)
}

func ImportHealthAt(home string) (ImportHealth, error) {
	db, err := openImport(filepath.Join(home, "hand.db"))
	if err != nil {
		return ImportHealth{}, err
	}
	defer db.Close()
	return importHealth(db)
}

func ImportRecorded(ctx context.Context, home, sourceID string) (bool, error) {
	db, err := openImport(filepath.Join(home, "hand.db"))
	if err != nil {
		return false, err
	}
	defer db.Close()
	var n int
	err = db.QueryRowContext(ctx, `SELECT count(*) FROM event WHERE kind = 'fleet.imported' AND detail LIKE ?`, sourceID+" %").Scan(&n)
	return n > 0, err
}

func ImportFleet(ctx context.Context, o ImportOptions) (ImportResult, error) {
	for _, p := range []struct{ what, home string }{{"source", o.SourceHome}, {"destination", o.DestHome}} {
		v, err := ImportSchema(p.home)
		if err != nil {
			return ImportResult{}, err
		}
		if v != SchemaVersion {
			return ImportResult{}, fmt.Errorf("%w: %s %s has schema %d, not the current %d; open that fleet with this hand first", ErrInvalid, p.what, p.home, v, SchemaVersion)
		}
		h, err := ImportHealthAt(p.home)
		if err != nil {
			return ImportResult{}, err
		}
		if h.Supervisor != "" || len(h.Attempts) > 0 {
			live := refsOf(h.Attempts)
			if h.Supervisor != "" {
				live = append([]string{h.Supervisor}, live...)
			}
			return ImportResult{}, fmt.Errorf("%w: %s %s is live (%s)", ErrConflict, p.what, p.home, strings.Join(live, ", "))
		}
	}
	src, err := openImport(filepath.Join(o.SourceHome, "hand.db"))
	if err != nil {
		return ImportResult{}, err
	}
	defer src.Close()
	if o.DryRun {
		dst, err := openImport(filepath.Join(o.DestHome, "hand.db"))
		if err != nil {
			return ImportResult{}, err
		}
		defer dst.Close()
		p, err := planImport(ctx, src, dst, o)
		return p.result, err
	}
	store, err := Open(filepath.Join(o.DestHome, "hand.db"), time.Now)
	if err != nil {
		return ImportResult{}, err
	}
	defer store.Close()
	var out ImportResult
	err = store.tx(ctx, func(tx *sql.Tx) error {
		if err := checkImportRecorded(ctx, tx, o); err != nil {
			return err
		}
		p, err := planImport(ctx, src, tx, o)
		if err != nil {
			return err
		}
		if err := insertImport(ctx, tx, store.stamp(), o, p); err != nil {
			return err
		}
		if err := checkImport(tx); err != nil {
			return err
		}
		out = p.result
		out.EventSeq, err = lastEventSeq(tx)
		return err
	})
	return out, err
}

func checkImportRecorded(ctx context.Context, tx *sql.Tx, o ImportOptions) error {
	marker := ImportMarker(o.SourceHome)
	if _, err := os.Stat(marker); err == nil {
		return fmt.Errorf("%w: fleet %s was already imported (marker %s)", ErrConflict, o.SourceID, marker)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM event WHERE kind = 'fleet.imported' AND detail LIKE ?`, o.SourceID+" %").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: fleet %s was already imported into %s (fleet.imported event)", ErrConflict, o.SourceID, o.DestID)
	}
	return nil
}

type importQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scanFleetDB(q importQueryer) (Fleet, error) {
	var f Fleet
	err := q.QueryRowContext(context.Background(), `SELECT id, name FROM fleet`).Scan(&f.ID, &f.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Fleet{}, errNoFleet
	}
	return f, err
}

func importHealth(q importQueryer) (ImportHealth, error) {
	var h ImportHealth
	var id int64
	var status string
	err := q.QueryRowContext(context.Background(), `SELECT id, status FROM supervisor WHERE status IN ('launching', 'running') ORDER BY id DESC LIMIT 1`).Scan(&id, &status)
	if err == nil {
		h.Supervisor = SupervisorRef(id) + " " + status
	} else if !errors.Is(err, sql.ErrNoRows) {
		return h, err
	}
	rows, err := q.QueryContext(context.Background(), `SELECT id FROM attempt WHERE status IN ('launching', 'running') ORDER BY id`)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return h, err
		}
		h.Attempts = append(h.Attempts, id)
	}
	return h, rows.Err()
}

func refsOf(ids []int64) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, AttemptRef(id))
	}
	return out
}

func planImport(ctx context.Context, src *sql.DB, dst importQueryer, o ImportOptions) (importPlan, error) {
	var p importPlan
	if err := dst.QueryRowContext(ctx, `SELECT COALESCE(max(id), 0) FROM task`).Scan(&p.result.Offset); err != nil {
		return p, err
	}
	srcProjects, err := importProjects(ctx, src)
	if err != nil {
		return p, err
	}
	dstProjects, err := importProjects(ctx, dst)
	if err != nil {
		return p, err
	}
	byName := map[string]Project{}
	byRepo := map[string]Project{}
	for _, pr := range dstProjects {
		byName[pr.Name] = pr
		byRepo[importCanonical(o.DestHome, pr.Repo)] = pr
	}
	projectOf := map[string]string{}
	for _, pr := range srcProjects {
		repo := importRepo(o.SourceHome, pr.Repo)
		canonical := importCanonical(o.SourceHome, pr.Repo)
		if have, ok := byName[pr.Name]; ok {
			if importCanonical(o.DestHome, have.Repo) != canonical {
				return p, fmt.Errorf("%w: project %s exists on both sides with a different repo (%s and %s); rename it in the source fleet or import into a fleet without it", ErrConflict, pr.Name, have.Repo, repo)
			}
			projectOf[pr.Name] = pr.Name
			p.result.Projects = append(p.result.Projects, ImportProject{Name: pr.Name, Repo: have.Repo, Source: pr.Name, Action: "reused"})
			continue
		}
		if have, ok := byRepo[canonical]; ok {
			projectOf[pr.Name] = have.Name
			p.result.Projects = append(p.result.Projects, ImportProject{Name: pr.Name, Repo: repo, Source: pr.Name, Action: "reused " + have.Name})
			continue
		}
		stored := storedPath(o.DestHome, repo)
		p.projects = append(p.projects, Project{Name: pr.Name, Repo: stored, Settings: pr.Settings, CreatedAt: pr.CreatedAt})
		byName[pr.Name] = Project{Name: pr.Name, Repo: repo}
		byRepo[canonical] = Project{Name: pr.Name, Repo: repo}
		projectOf[pr.Name] = pr.Name
		p.result.Projects = append(p.result.Projects, ImportProject{Name: pr.Name, Repo: repo, Source: pr.Name, Action: "added"})
	}
	srcTasks, err := importTasks(ctx, src)
	if err != nil {
		return p, err
	}
	ids := map[int64]bool{}
	for _, t := range srcTasks {
		ids[t.ID] = true
	}
	p.result.Tasks = map[string]int{}
	for _, t := range srcTasks {
		project, ok := projectOf[t.Project]
		if !ok {
			return p, fmt.Errorf("%w: task %s names project %s, which is not on the destination", ErrConflict, TaskRef(t.ID), t.Project)
		}
		title, refs, left, amb := rewriteImportText(t.Title, ids, p.result.Offset, TaskRef(t.ID), "title")
		goal, grefs, gleft, gamb := rewriteImportText(t.Goal, ids, p.result.Offset, TaskRef(t.ID), "goal")
		if t.ID+p.result.Offset < p.result.NewTaskMin || p.result.NewTaskMin == 0 {
			p.result.NewTaskMin = t.ID + p.result.Offset
		}
		if t.ID+p.result.Offset > p.result.NewTaskMax {
			p.result.NewTaskMax = t.ID + p.result.Offset
		}
		p.tasks = append(p.tasks, Task{ID: t.ID + p.result.Offset, Project: project, Title: title, Goal: goal, Status: t.Status, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt})
		p.result.Refs = append(p.result.Refs, refs...)
		p.result.Refs = append(p.result.Refs, grefs...)
		p.result.Left = append(p.result.Left, left...)
		p.result.Left = append(p.result.Left, gleft...)
		p.result.Ambiguous = append(p.result.Ambiguous, amb...)
		p.result.Ambiguous = append(p.result.Ambiguous, gamb...)
		p.result.Tasks[t.Status]++
	}
	reports, err := importReports(ctx, src)
	if err != nil {
		return p, err
	}
	for i := range reports {
		reports[i].NewTaskID = reports[i].TaskID + p.result.Offset
	}
	p.result.Reports = reports
	p.result.TextSHA256 = importTextHash(p.projects, p.tasks)
	return p, nil
}

func importProjects(ctx context.Context, q importQueryer) ([]Project, error) {
	rows, err := q.QueryContext(ctx, `SELECT name, repo, settings, created_at FROM project ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.Name, &p.Repo, &p.Settings, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func importTasks(ctx context.Context, q importQueryer) ([]Task, error) {
	rows, err := q.QueryContext(ctx, taskSelect+` ORDER BY id`)
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

func importReports(ctx context.Context, q importQueryer) ([]ImportReportRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT r.id, r.attempt_id, r.task_id, r.status, r.body, r.created_at, t.title FROM report r JOIN task t ON t.id = r.task_id ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImportReportRow
	for rows.Next() {
		var r ImportReportRow
		if err := rows.Scan(&r.ID, &r.AttemptID, &r.TaskID, &r.Status, &r.Body, &r.CreatedAt, &r.TaskTitle); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func importRepo(home, repo string) string {
	if filepath.IsAbs(repo) {
		return filepath.Clean(repo)
	}
	return filepath.Clean(filepath.Join(home, repo))
}

func importCanonical(home, repo string) string {
	path := importRepo(home, repo)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

func importTextHash(projects []Project, tasks []Task) string {
	h := sha256.New()
	for _, p := range projects {
		fmt.Fprintf(h, "project %q %q %q %q\n", p.Name, p.Repo, p.Settings, p.CreatedAt)
	}
	for _, t := range tasks {
		fmt.Fprintf(h, "task %d %q %q %q %q %q %q\n", t.ID, t.Project, t.Title, t.Goal, t.Status, t.CreatedAt, t.UpdatedAt)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func rewriteImportText(s string, ids map[int64]bool, offset int64, key, column string) (string, []ImportRef, []ImportRef, []ImportAmbiguous) {
	if s == "" {
		return s, nil, nil, nil
	}
	var b strings.Builder
	var refs, left []ImportRef
	var ambiguous []ImportAmbiguous
	seen := map[[2]int]bool{}
	pos := 0
	for _, loc := range importRefPattern.FindAllStringIndex(s, -1) {
		if loc[0] > 0 && importGlue(s[loc[0]-1]) {
			start, end := importRunStart(s, loc[0]), importRunEnd(s, loc[1])
			if !seen[[2]int{start, end}] {
				seen[[2]int{start, end}] = true
				ambiguous = append(ambiguous, ImportAmbiguous{Table: "task", Key: key, Column: column, Token: s[start:end], Context: importContext(s, start, end)})
			}
			continue
		}
		tok := s[loc[0]:loc[1]]
		if loc[0] > 0 && importRisky(s[loc[0]-1]) || loc[1] < len(s) && !importWholeEnd(s, loc[1]) {
			start, end := importRunStart(s, loc[0]), importRunEnd(s, loc[1])
			if !seen[[2]int{start, end}] {
				seen[[2]int{start, end}] = true
				ambiguous = append(ambiguous, ImportAmbiguous{Table: "task", Key: key, Column: column, Token: s[start:end], Context: importContext(s, start, end)})
			}
			continue
		}
		b.WriteString(s[pos:loc[0]])
		if tok[0] == 't' {
			if id, err := strconv.ParseInt(tok[1:], 10, 64); err == nil && ids[id] {
				next := TaskRef(id + offset)
				b.WriteString(next)
				refs = append(refs, ImportRef{Table: "task", Key: key, Column: column, From: tok, To: next})
				pos = loc[1]
				continue
			}
		}
		if tok[0] != 't' {
			left = append(left, ImportRef{Table: "task", Key: key, Column: column, From: tok})
		}
		b.WriteString(tok)
		pos = loc[1]
	}
	b.WriteString(s[pos:])
	return b.String(), refs, left, ambiguous
}

func importGlue(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '/' || c == '#' || c == '-'
}

func importPunct(c byte) bool { return c == '.' || c == ',' || c == ';' || c == ')' }

func importRisky(c byte) bool {
	return strings.ContainsRune(`|\^$*+?[]{}`, rune(c))
}

func importWholeEnd(s string, i int) bool {
	c := s[i]
	if importRisky(c) {
		return false
	}
	if !importPunct(c) {
		return !importGlue(c)
	}
	if i+1 == len(s) {
		return true
	}
	switch s[i+1] {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

func importRunStart(s string, start int) int {
	for start > 0 && importGlue(s[start-1]) {
		start--
	}
	return start
}

func importRunEnd(s string, end int) int {
	for end < len(s) && importGlue(s[end]) {
		end++
	}
	return end
}

func importContext(s string, start, end int) string {
	lo, hi := start-24, end+24
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	return strings.Join(strings.Fields(s[lo:hi]), " ")
}

func insertImport(ctx context.Context, tx *sql.Tx, at string, o ImportOptions, p importPlan) error {
	for _, pr := range p.projects {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project(name, repo, settings, created_at) VALUES (?, ?, ?, ?)`, pr.Name, pr.Repo, pr.Settings, pr.CreatedAt); err != nil {
			return err
		}
	}
	for _, t := range p.tasks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, t.ID, t.Project, t.Title, t.Goal, t.Status, t.CreatedAt, t.UpdatedAt); err != nil {
			return err
		}
	}
	detail := fmt.Sprintf("%s projects %d tasks %d offset %d text %s", o.SourceID, len(p.projects), len(p.tasks), p.result.Offset, p.result.TextSHA256)
	return emit(tx, at, "fleet.imported", 0, detail)
}

func checkImport(tx *sql.Tx) error {
	rows, err := tx.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	broken := false
	for rows.Next() {
		broken = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if broken {
		return fmt.Errorf("%w: the import would break a foreign key", ErrInvalid)
	}
	var integrity string
	if err := tx.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("%w: integrity check after the import: %s", ErrInvalid, integrity)
	}
	return nil
}

func lastEventSeq(tx *sql.Tx) (int64, error) {
	var seq int64
	if err := tx.QueryRow(`SELECT COALESCE(max(seq), 0) FROM event`).Scan(&seq); err != nil {
		return 0, err
	}
	return seq, nil
}
