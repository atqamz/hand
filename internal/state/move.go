package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	AfterAbsolute   int
	AfterTilde      int
	ForeignKeys     int
	Integrity       string
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
	cells, absolute, tilde, err := moveScan(ctx, s.db, from, to, false)
	rep.Cells, rep.Absolute, rep.Tilde = cells, absolute, tilde
	return rep, err
}

func (s *Store) MoveHome(ctx context.Context, from, to string) (MoveReport, error) {
	return s.moveHome(ctx, from, to, nil)
}

func (s *Store) moveHome(ctx context.Context, from, to string, after func(moveWriter) error) (MoveReport, error) {
	rep := MoveReport{From: from, To: to}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		cells, absolute, tilde, err := moveScan(ctx, tx, from, to, true)
		if err != nil {
			return err
		}
		rep.Cells, rep.Absolute, rep.Tilde = cells, absolute, tilde
		if after != nil {
			if err := after(tx); err != nil {
				return err
			}
		}
		absolute, tilde, first, err := moveRemaining(ctx, tx, from, to)
		if err != nil {
			return err
		}
		if absolute+tilde != 0 {
			return fmt.Errorf("%w: the move verification found %d old home path(s) after the rewrite, first in %s.%s %s; nothing changed", ErrConflict, absolute+tilde, first.Table, first.Column, first.Ref)
		}
		rep.AfterAbsolute, rep.AfterTilde = absolute, tilde
		if err := moveChecks(ctx, tx, &rep); err != nil {
			return err
		}
		rep.EventSeq, err = moveEvent(tx, s.stamp(), from, to)
		return err
	})
	return rep, err
}

func moveEvent(tx *sql.Tx, at, from, to string) (int64, error) {
	res, err := tx.Exec(`INSERT INTO event(at, kind, task_id, detail) VALUES (?, 'fleet.moved', NULL, ?)`, at, from+" -> "+to)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
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

func moveScan(ctx context.Context, q moveWriter, from, to string, apply bool) ([]MoveCell, int, int, error) {
	cols, err := moveColumns(ctx, q)
	if err != nil {
		return nil, 0, 0, err
	}
	patterns := movePatterns(from, to)
	var cells []MoveCell
	absolute, tilde := 0, 0
	for _, c := range cols {
		ref := moveRefs[c.table]
		if ref == "" {
			ref = "'rowid ' || rowid"
		}
		col := quoteIdent(c.column)
		query := fmt.Sprintf(`SELECT rowid, %s, %s FROM %s WHERE instr(%s, ?) > 0`, ref, col, quoteIdent(c.table), col)
		args := []any{patterns[0]}
		for _, p := range patterns[1:] {
			query += fmt.Sprintf(` OR instr(%s, ?) > 0`, col)
			args = append(args, p)
		}
		query += ` ORDER BY rowid`
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, 0, 0, err
		}
		var found []moveRow
		for rows.Next() {
			var r moveRow
			if err := rows.Scan(&r.rowid, &r.ref, &r.value); err != nil {
				_ = rows.Close()
				return nil, 0, 0, err
			}
			found = append(found, r)
		}
		if err := rows.Close(); err != nil {
			return nil, 0, 0, err
		}
		if err := rows.Err(); err != nil {
			return nil, 0, 0, err
		}
		for _, r := range found {
			out, abs, t := rewriteHome(r.value, from, to)
			if out == r.value {
				continue
			}
			if apply {
				if _, err := q.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET %s = ? WHERE rowid = ?`, quoteIdent(c.table), col), out, r.rowid); err != nil {
					return nil, 0, 0, err
				}
			}
			cells = append(cells, MoveCell{Table: c.table, Column: c.column, Ref: r.ref, Absolute: abs, Tilde: t})
			absolute += abs
			tilde += t
		}
	}
	return cells, absolute, tilde, nil
}

func moveRemaining(ctx context.Context, q moveQueryer, from, to string) (int, int, *MoveCell, error) {
	cols, err := moveColumns(ctx, q)
	if err != nil {
		return 0, 0, nil, err
	}
	patterns := movePatterns(from, to)
	absolute, tilde := 0, 0
	var first *MoveCell
	for _, c := range cols {
		ref := moveRefs[c.table]
		if ref == "" {
			ref = "'rowid ' || rowid"
		}
		col := quoteIdent(c.column)
		query := fmt.Sprintf(`SELECT rowid, %s, %s FROM %s WHERE instr(%s, ?) > 0`, ref, col, quoteIdent(c.table), col)
		args := []any{patterns[0]}
		for _, p := range patterns[1:] {
			query += fmt.Sprintf(` OR instr(%s, ?) > 0`, col)
			args = append(args, p)
		}
		query += ` ORDER BY rowid`
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return 0, 0, nil, err
		}
		var found []moveRow
		for rows.Next() {
			var r moveRow
			if err := rows.Scan(&r.rowid, &r.ref, &r.value); err != nil {
				_ = rows.Close()
				return 0, 0, nil, err
			}
			found = append(found, r)
		}
		if err := rows.Close(); err != nil {
			return 0, 0, nil, err
		}
		if err := rows.Err(); err != nil {
			return 0, 0, nil, err
		}
		for _, r := range found {
			out, abs, t := rewriteHome(r.value, from, to)
			if out == r.value {
				continue
			}
			absolute += abs
			tilde += t
			if first == nil {
				first = &MoveCell{Table: c.table, Column: c.column, Ref: r.ref, Absolute: abs, Tilde: t}
			}
		}
	}
	return absolute, tilde, first, nil
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func movePatterns(from, to string) []string {
	patterns := []string{from}
	if escaped := strings.ReplaceAll(from, `\`, `\\`); escaped != from {
		patterns = append(patterns, escaped)
	}
	if oldBase, newBase := filepath.Base(from), filepath.Base(to); oldBase != newBase && oldBase != "" && oldBase != "." && oldBase != string(filepath.Separator) {
		patterns = append(patterns, "~/"+oldBase)
	}
	return patterns
}

func rewriteHome(s, from, to string) (string, int, int) {
	out, absolute := replacePath(s, from, to)
	if escaped := strings.ReplaceAll(from, `\`, `\\`); escaped != from {
		var n int
		out, n = replacePath(out, escaped, strings.ReplaceAll(to, `\`, `\\`))
		absolute += n
	}
	oldBase, newBase := filepath.Base(from), filepath.Base(to)
	tilde := 0
	if oldBase != newBase && oldBase != "" && oldBase != "." && oldBase != string(filepath.Separator) {
		out, tilde = replacePath(out, "~/"+oldBase, "~/"+newBase)
	}
	return out, absolute, tilde
}

func replacePath(s, from, to string) (string, int) {
	if s == "" || from == "" {
		return s, 0
	}
	var b strings.Builder
	b.Grow(len(s))
	n := 0
	for {
		i := strings.Index(s, from)
		if i < 0 {
			b.WriteString(s)
			break
		}
		end := i + len(from)
		if (i == 0 || !moveLead(s[i-1])) && (end == len(s) || !moveGlue(s[end])) {
			b.WriteString(s[:i])
			b.WriteString(to)
			n++
		} else {
			b.WriteString(s[:end])
		}
		s = s[end:]
	}
	return b.String(), n
}

func moveGlue(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return c == '_' || c == '-'
}

func moveLead(c byte) bool {
	return moveGlue(c) || c == '.' || c == '/' || c == '\\' || c == '~'
}
