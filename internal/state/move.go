package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type MoveCell struct {
	Table, Column, Ref string
	Absolute, Tilde    int
}

type MoveReport struct {
	From, To        string
	Cells           []MoveCell
	Absolute, Tilde int
	Hits            []MoveCell
	AfterAbsolute   int
	AfterTilde      int
	ForeignKeys     int
	Integrity       string
	MarkerSeq       int64
	EventSeq        int64
}

type moveTextColumn struct{ table, column string }

var moveRefs = map[string]string{
	"attempt":          "'a' || id",
	"decision":         "'d' || id",
	"device":           "'dev' || id",
	"event":            "'e' || seq",
	"fleet":            "'fleet'",
	"plan":             "'t' || task_id || '/' || revision",
	"project":          "name",
	"project_grant":    "'g' || id",
	"report":           "'r' || id",
	"supervisor":       "'s' || id",
	"supervisor_input": "'i' || id",
	"task":             "'t' || id",
	"timer":            "'w' || id",
}

type moveQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type moveWriter interface {
	moveQueryer
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func OpenRead(path string) (*Store, error) {
	if v, ok := headerVersion(path); ok && v > SchemaVersion {
		return nil, fmt.Errorf("%w: %s has schema %d, newer than this hand (%d)", ErrInvalid, path, v, SchemaVersion)
	} else if ok && v != 0 && v < MinSchemaVersion {
		return nil, tooOld(path+" has", v)
	}
	_, walErr := os.Stat(path + "-wal")
	_, shmErr := os.Stat(path + "-shm")
	db, err := sql.Open("sqlite", "file:"+uriPath.Replace(path)+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	return &Store{db: db, now: time.Now, home: filepath.Dir(path), cleanup: func() {
		if errors.Is(walErr, fs.ErrNotExist) {
			if info, err := os.Stat(path + "-wal"); err == nil && info.Size() == 0 {
				_ = os.Remove(path + "-wal")
			}
		}
		if errors.Is(shmErr, fs.ErrNotExist) {
			_ = os.Remove(path + "-shm")
		}
	}}, nil
}

func (s *Store) PlanHome(ctx context.Context, from, to string) (MoveReport, error) {
	rep := MoveReport{From: from, To: to}
	pass, err := moveScan(ctx, s.db, from, to, userHome(), false)
	rep.Cells, rep.Absolute, rep.Tilde = pass.Cells, pass.Absolute, pass.Tilde
	rep.Hits = pass.Hits
	rep.AfterAbsolute = len(rep.Hits)
	return rep, err
}

func (s *Store) MoveHome(ctx context.Context, from, to string) (MoveReport, error) {
	return s.moveHome(ctx, from, to, false, nil)
}

func (s *Store) RewriteHome(ctx context.Context, from, to string) (MoveReport, error) {
	return s.moveHome(ctx, from, to, true, nil)
}

func (s *Store) moveHome(ctx context.Context, from, to string, inPlace bool, after func(moveWriter) error) (MoveReport, error) {
	rep := MoveReport{From: from, To: to}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var recorded string
		if err := tx.QueryRowContext(ctx, `SELECT home FROM fleet`).Scan(&recorded); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errNoFleet
			}
			return err
		}
		if recorded != "" && !sameHome(recorded, from) {
			return fmt.Errorf("%w: the database records this fleet's home as %s, not %s; nothing changed", ErrConflict, recorded, from)
		}
		pass, err := moveScan(ctx, tx, from, to, userHome(), true)
		if err != nil {
			return err
		}
		rep.Cells, rep.Absolute, rep.Tilde = pass.Cells, pass.Absolute, pass.Tilde
		if after != nil {
			if err := after(tx); err != nil {
				return err
			}
		}
		hits, err := moveHits(ctx, tx, from, to, userHome())
		if err != nil {
			return err
		}
		if len(hits) != 0 {
			return fmt.Errorf("%w: the move verification found %d old home path(s) after the rewrite: %s; edit them to name %s by hand in %s, then run again; nothing changed", ErrConflict, len(hits), moveHitList(hits), to, filepath.Join(s.home, "hand.db"))
		}
		rep.Hits, rep.AfterAbsolute = hits, len(hits)
		if err := moveChecks(ctx, tx, &rep); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE fleet SET home = ? WHERE home = ''`, to); err != nil {
			return err
		}
		if inPlace {
			rep.EventSeq, err = moveEvent(tx, s.stamp(), from, to)
		} else {
			rep.MarkerSeq, err = moveMarker(tx, s.stamp(), from, to)
		}
		return err
	})
	return rep, err
}

func moveMarker(tx *sql.Tx, at, from, to string) (int64, error) {
	res, err := tx.Exec(`INSERT INTO event(at, kind, task_id, detail) VALUES (?, 'fleet.moving', NULL, ?)`, at, moveDetail(from, to))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func moveEvent(tx *sql.Tx, at, from, to string) (int64, error) {
	res, err := tx.Exec(`INSERT INTO event(at, kind, task_id, detail) VALUES (?, 'fleet.moved', NULL, ?)`, at, moveDetail(from, to))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func moveDetail(from, to string) string {
	b, _ := json.Marshal(struct {
		From string `json:"from"`
		To   string `json:"to"`
	}{from, to})
	return string(b)
}

func moveSplit(detail string) (string, string, bool) {
	var v struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal([]byte(detail), &v); err != nil || v.From == "" || v.To == "" {
		return "", "", false
	}
	return v.From, v.To, true
}

func (s *Store) PendingMove(ctx context.Context) (from, to string, ok bool, err error) {
	return pendingMove(ctx, s.db)
}

func pendingMove(ctx context.Context, q moveQueryer) (from, to string, ok bool, err error) {
	rows, err := q.QueryContext(ctx, `SELECT seq, kind, detail FROM event WHERE kind IN ('fleet.moving', 'fleet.moved') ORDER BY seq DESC`)
	if err != nil {
		return "", "", false, err
	}
	defer rows.Close()
	var moving, moved int64
	var detail string
	for rows.Next() {
		var seq int64
		var kind, d string
		if err := rows.Scan(&seq, &kind, &d); err != nil {
			return "", "", false, err
		}
		switch {
		case kind == "fleet.moved" && moved == 0:
			moved = seq
		case kind == "fleet.moving" && moving == 0:
			moving, detail = seq, d
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", false, err
	}
	if moving == 0 || moving < moved {
		return "", "", false, nil
	}
	from, to, ok = moveSplit(detail)
	if !ok {
		return "", "", false, fmt.Errorf("%w: event e%d (fleet.moving) does not name a move: %q", ErrInvalid, moving, detail)
	}
	return from, to, true, nil
}

func (s *Store) ConfirmMove(ctx context.Context, from, to string) (int64, error) {
	var seq int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		pf, pt, pending, err := pendingMove(ctx, tx)
		if err != nil {
			return err
		}
		if !pending {
			var detail string
			if err := tx.QueryRowContext(ctx, `SELECT seq, detail FROM event WHERE kind = 'fleet.moved' ORDER BY seq DESC LIMIT 1`).Scan(&seq, &detail); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w: no move is pending", ErrConflict)
				}
				return err
			}
			pf, pt, ok := moveSplit(detail)
			if !ok || !sameHome(pf, from) || !sameHome(pt, to) {
				return fmt.Errorf("%w: no move from %s to %s is pending", ErrConflict, from, to)
			}
			return nil
		}
		if !sameHome(pf, from) || !sameHome(pt, to) {
			return fmt.Errorf("%w: the pending move is %s -> %s, not %s -> %s; nothing changed", ErrConflict, pf, pt, from, to)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO event(at, kind, task_id, detail) VALUES (?, 'fleet.moved', NULL, ?)`, s.stamp(), moveDetail(from, to))
		if err != nil {
			return err
		}
		if seq, err = res.LastInsertId(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE fleet SET home = ?`, to)
		return err
	})
	return seq, err
}

func (s *Store) VerifyHome(ctx context.Context, from, to string) ([]MoveCell, error) {
	return moveHits(ctx, s.db, from, to, userHome())
}

func moveChecks(ctx context.Context, tx *sql.Tx, rep *MoveReport) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	broken := 0
	for rows.Next() {
		broken++
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if broken != 0 {
		return fmt.Errorf("%w: the move left %d broken foreign key(s); nothing changed", ErrConflict, broken)
	}
	rep.ForeignKeys = broken
	var check string
	if err := tx.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("%w: the move verification integrity_check: %s; nothing changed", ErrConflict, check)
	}
	rep.Integrity = check
	return nil
}

func moveColumns(ctx context.Context, q moveQueryer) ([]moveTextColumn, error) {
	rows, err := q.QueryContext(ctx, `SELECT m.name, p.name FROM sqlite_master AS m, pragma_table_info(m.name) AS p
		WHERE m.type = 'table' AND p.type = 'TEXT' AND m.name NOT LIKE 'sqlite_%'
		ORDER BY m.name, p.cid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []moveTextColumn
	for rows.Next() {
		var c moveTextColumn
		if err := rows.Scan(&c.table, &c.column); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

type moveRow struct {
	rowid int64
	ref   string
	value string
}

type movePass struct {
	Cells           []MoveCell
	Absolute, Tilde int
	Hits            []MoveCell
}

func moveScan(ctx context.Context, q moveWriter, from, to, home string, apply bool) (movePass, error) {
	var pass movePass
	cols, err := moveColumns(ctx, q)
	if err != nil {
		return pass, err
	}
	patterns := moveHitPatterns(from, to, home)
	fold := foldHome()
	for _, c := range cols {
		rows, err := q.QueryContext(ctx, `SELECT rowid, `+moveRef(c.table)+`, `+moveNullSafe(c.column)+` FROM `+quoteIdent(c.table)+moveHistory(c.table)+` ORDER BY rowid`)
		if err != nil {
			return pass, err
		}
		var found []moveRow
		for rows.Next() {
			var r moveRow
			if err := rows.Scan(&r.rowid, &r.ref, &r.value); err != nil {
				_ = rows.Close()
				return pass, err
			}
			found = append(found, r)
		}
		if err := rows.Close(); err != nil {
			return pass, err
		}
		if err := rows.Err(); err != nil {
			return pass, err
		}
		for _, r := range found {
			out, absolute, tilde := rewriteHome(r.value, from, to, home)
			if out != r.value {
				if apply {
					if _, err := q.ExecContext(ctx, `UPDATE `+quoteIdent(c.table)+` SET `+quoteIdent(c.column)+` = ? WHERE rowid = ?`, out, r.rowid); err != nil {
						return pass, err
					}
				}
				pass.Cells = append(pass.Cells, MoveCell{Table: c.table, Column: c.column, Ref: r.ref, Absolute: absolute, Tilde: tilde})
				pass.Absolute += absolute
				pass.Tilde += tilde
			}
			if !moveIsTo(out, to) && countHits(out, patterns, fold) > 0 {
				pass.Hits = append(pass.Hits, MoveCell{Table: c.table, Column: c.column, Ref: r.ref})
			}
		}
	}
	return pass, nil
}

func moveHits(ctx context.Context, q moveQueryer, from, to, home string) ([]MoveCell, error) {
	cols, err := moveColumns(ctx, q)
	if err != nil {
		return nil, err
	}
	patterns := moveHitPatterns(from, to, home)
	fold := foldHome()
	var hits []MoveCell
	for _, c := range cols {
		where := make([]string, 0, len(patterns))
		args := make([]any, 0, len(patterns))
		for _, p := range patterns {
			if fold {
				where = append(where, `instr(lower(`+moveNullSafe(c.column)+`), lower(?)) > 0`)
			} else {
				where = append(where, `instr(`+moveNullSafe(c.column)+`, ?) > 0`)
			}
			args = append(args, p)
		}
		query := `SELECT rowid, ` + moveRef(c.table) + `, ` + moveNullSafe(c.column) + ` FROM ` + quoteIdent(c.table)
		if c.table == "event" {
			query += ` WHERE kind NOT IN ('fleet.moved', 'fleet.moving') AND (`
		} else {
			query += ` WHERE (`
		}
		query += strings.Join(where, " OR ") + ") ORDER BY rowid"
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		var found []moveRow
		for rows.Next() {
			var r moveRow
			if err := rows.Scan(&r.rowid, &r.ref, &r.value); err != nil {
				_ = rows.Close()
				return nil, err
			}
			found = append(found, r)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for _, r := range found {
			if moveIsTo(r.value, to) {
				continue
			}
			if countHits(r.value, patterns, fold) > 0 {
				hits = append(hits, MoveCell{Table: c.table, Column: c.column, Ref: r.ref})
			}
		}
	}
	return hits, nil
}

func moveHitList(hits []MoveCell) string {
	const max = 20
	parts := make([]string, 0, min(len(hits), max))
	for i, h := range hits {
		if i == max {
			parts = append(parts, fmt.Sprintf("and %d more", len(hits)-max))
			break
		}
		parts = append(parts, h.Table+"."+h.Column+" "+h.Ref)
	}
	return strings.Join(parts, ", ")
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func moveNullSafe(column string) string { return `COALESCE(` + quoteIdent(column) + `, '')` }

func moveRef(table string) string {
	if ref := moveRefs[table]; ref != "" {
		return ref
	}
	return "'rowid ' || rowid"
}

func moveHistory(table string) string {
	if table == "event" {
		return ` WHERE kind NOT IN ('fleet.moved', 'fleet.moving')`
	}
	return ""
}

type movePattern struct {
	text, target string
	tilde        bool
}

func moveHitPatterns(from, to, home string) []string {
	ps := movePatterns(from, to, home, true)
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.text
	}
	return out
}

func movePatterns(from, to, home string, tilde bool) []movePattern {
	var ps []movePattern
	add := func(text, target string, tilde bool) {
		if text == "" || text == target {
			return
		}
		for _, have := range ps {
			if have.text == text {
				return
			}
		}
		ps = append(ps, movePattern{text: text, target: target, tilde: tilde})
	}
	var addSpellings func(path, target string)
	addSpellings = func(path, target string) {
		add(path, target, false)
		if escaped := escapeBackslashes(path); escaped != path {
			add(escaped, escapeBackslashes(target), false)
		}
		slash, slashTarget := filepath.ToSlash(path), filepath.ToSlash(target)
		if slash != path {
			add(slash, slashTarget, false)
			if escaped := escapeBackslashes(slash); escaped != slash {
				add(escaped, escapeBackslashes(slashTarget), false)
			}
		}
		if strings.Contains(path, "/") {
			add(strings.ReplaceAll(path, "/", `\u002f`), strings.ReplaceAll(target, "/", `\u002f`), false)
			add(strings.ReplaceAll(path, "/", `\u002F`), strings.ReplaceAll(target, "/", `\u002F`), false)
			add(strings.ReplaceAll(path, "/", `\/`), strings.ReplaceAll(target, "/", `\/`), false)
		}
		if slash != path && strings.Contains(slash, "/") {
			add(strings.ReplaceAll(slash, "/", `\u002f`), strings.ReplaceAll(slashTarget, "/", `\u002f`), false)
			add(strings.ReplaceAll(slash, "/", `\u002F`), strings.ReplaceAll(slashTarget, "/", `\u002F`), false)
			add(strings.ReplaceAll(slash, "/", `\/`), strings.ReplaceAll(slashTarget, "/", `\/`), false)
		}
		add("file://"+path, "file://"+target, false)
		if slash != path {
			add("file://"+slash, "file://"+slashTarget, false)
			add("file:///"+slash, "file:///"+slashTarget, false)
		}
	}
	addSpellings(from, to)
	if alias, ok := homeAlias(from, home); ok {
		addSpellings(alias, to)
	}
	if base := moveBase(from); tilde && base != "" {
		target := to
		if newBase, ok := homeChild(to, home); ok {
			target = "~/" + newBase
		}
		add("~/"+base, target, true)
		add(`~\/`+base, slashEscaped(target, `\/`), true)
		add(`~\u002f`+base, slashEscaped(target, `\u002f`), true)
		add(`~\u002F`+base, slashEscaped(target, `\u002F`), true)
		if foldHome() {
			add(`~\`+base, target, true)
			add(`~\\`+base, escapeBackslashes(target), true)
		}
	}
	return ps
}

func slashEscaped(target, escape string) string {
	return strings.ReplaceAll(escapeBackslashes(target), "/", escape)
}

func moveBase(p string) string {
	b := filepath.Base(filepath.Clean(p))
	if b == "" || b == "." || b == string(filepath.Separator) {
		return ""
	}
	return b
}

func rewriteHome(s, from, to, home string) (string, int, int) {
	if s == "" || from == "" || moveIsTo(s, to) {
		return s, 0, 0
	}
	out := s
	absolute, tilde := 0, 0
	fold := foldHome()
	_, named := homeChild(from, home)
	for _, p := range movePatterns(from, to, home, named) {
		var n int
		out, n = replacePathFold(out, p.text, p.target, fold)
		if p.tilde {
			tilde += n
		} else {
			absolute += n
		}
	}
	return out, absolute, tilde
}

func moveIsTo(v, to string) bool {
	if v == to {
		return true
	}
	return foldHome() && sameHome(v, to)
}

func replacePath(s, from, to string) (string, int) {
	return replacePathFold(s, from, to, false)
}

func replacePathFold(s, from, to string, fold bool) (string, int) {
	if s == "" || from == "" {
		return s, 0
	}
	hay, needle := s, from
	if fold {
		hay, needle = foldASCII(s), foldASCII(from)
	}
	var b strings.Builder
	b.Grow(len(s))
	last, n := 0, 0
	for i := 0; ; {
		j := strings.Index(hay[i:], needle)
		if j < 0 {
			break
		}
		j += i
		end := j + len(needle)
		if (j == 0 || !moveLead(s[j-1])) && pathEnd(s, end) {
			b.WriteString(s[last:j])
			b.WriteString(to)
			last, n = end, n+1
		}
		i = end
	}
	b.WriteString(s[last:])
	return b.String(), n
}

func countHits(s string, patterns []string, fold bool) int {
	if fold {
		s = foldASCII(s)
	}
	n := 0
	for _, p := range patterns {
		if fold {
			p = foldASCII(p)
		}
		n += countPath(s, p)
	}
	return n
}

func countPath(s, pattern string) int {
	if s == "" || pattern == "" {
		return 0
	}
	n := 0
	for i := 0; ; {
		j := strings.Index(s[i:], pattern)
		if j < 0 {
			return n
		}
		j += i
		end := j + len(pattern)
		if (j == 0 || !moveLead(s[j-1])) && pathEnd(s, end) {
			n++
		}
		i = end
	}
}

func moveName(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '_' || c == '-' || c == '.' || c == '+':
		return true
	}
	return c >= 0x80
}

func moveGlue(c byte) bool { return moveName(c) || c == ' ' }

func pathEnd(s string, end int) bool {
	if end == len(s) {
		return true
	}
	if c := s[end]; c != '.' {
		return !moveGlue(c)
	}
	if end+1 == len(s) {
		return true
	}
	switch s[end+1] {
	case ' ', '\t', '\n', '\r':
		return true
	}
	c := s[end+1]
	return !moveGlue(c) && c != '/' && c != '\\'
}

func moveLead(c byte) bool {
	return moveName(c) || c == '/' || c == '\\' || c == '~'
}

func foldHome() bool { return runtime.GOOS == "windows" }

func foldASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

func escapeBackslashes(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }

func sameHome(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if foldHome() {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func userHome() string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(h); err == nil {
		h = real
	}
	return filepath.Clean(h)
}

func homeChild(p, home string) (string, bool) {
	if p == "" || home == "" {
		return "", false
	}
	p, home = filepath.Clean(p), filepath.Clean(home)
	base := filepath.Base(p)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", false
	}
	if !sameHome(filepath.Dir(p), home) {
		return "", false
	}
	return base, true
}

func homeAlias(from, home string) (string, bool) {
	raw, err := os.UserHomeDir()
	if err != nil || raw == "" || !filepath.IsAbs(raw) || sameHome(raw, home) {
		return "", false
	}
	rel, err := filepath.Rel(home, from)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Join(raw, rel), true
}
