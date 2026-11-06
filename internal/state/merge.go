package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type FleetHealth struct {
	Fleet        Fleet
	Supervisor   string
	LiveAttempts []int64
	PendingInput int
	ArmedTimers  []int64
}

type MergeOptions struct {
	SourceHome    string
	DestHome      string
	CancelTimers  bool
	RenameProject map[string]string
	Preflight     bool
}

var mergeAfterSourceCounts func()

type MergeCount struct {
	Table                string
	Source, Dest, Merged int64
	Offset               int64
}

type MergeRef struct {
	Kind               string
	Changed, Protected int64
}

type MergeCell struct {
	Table, Key, Column, Refs string
}

type MergeReport struct {
	Source, Dest Fleet
	Offsets      map[string]int64
	Counts       []MergeCount
	Refs         []MergeRef
	Cells        []MergeCell
	TextSHA256   string
	Cancelled    int64
	Warnings     []string
}

func openReadOnly(path string) (*sql.DB, error) {
	return openSQLite("file:" + uriPath.Replace(path) + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(10000)")
}

func openSnapshot(path string) (*sql.DB, error) {
	return openSQLite("file:" + uriPath.Replace(path) + "?mode=ro&_pragma=busy_timeout(10000)")
}

func openSQLite(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func SchemaAt(path string) (int, error) {
	db, err := openReadOnly(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var v int
	err = db.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v, err
}

func CheckCurrent(path string) error {
	v, err := SchemaAt(path)
	if err != nil {
		return err
	}
	if v != SchemaVersion {
		return fmt.Errorf("%w: %s has schema %d, not the current %d; open that fleet with this hand first, which migrates it", ErrInvalid, path, v, SchemaVersion)
	}
	return nil
}

func InspectFleet(path string) (FleetHealth, error) {
	var h FleetHealth
	db, err := openReadOnly(path)
	if err != nil {
		return h, err
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return h, err
	}
	if version != SchemaVersion {
		return h, fmt.Errorf("%w: %s has schema %d, not the current %d", ErrInvalid, path, version, SchemaVersion)
	}
	if h.Fleet, err = scanFleet(db, context.Background()); err != nil {
		return h, err
	}
	var id int64
	var status string
	err = db.QueryRow(`SELECT id, status FROM supervisor WHERE status IN (?, ?) ORDER BY id DESC LIMIT 1`, AttemptLaunching, AttemptRunning).Scan(&id, &status)
	if err == nil {
		h.Supervisor = SupervisorRef(id) + " " + status
	} else if !errors.Is(err, sql.ErrNoRows) {
		return h, err
	}
	rows, err := db.Query(`SELECT id FROM attempt WHERE status IN (?, ?) ORDER BY id`, AttemptLaunching, AttemptRunning)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return h, err
		}
		h.LiveAttempts = append(h.LiveAttempts, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return h, err
	}
	if err := rows.Close(); err != nil {
		return h, err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM supervisor_input WHERE delivered_at = ''`).Scan(&h.PendingInput); err != nil {
		return h, err
	}
	rows, err = db.Query(`SELECT id FROM timer WHERE status = ? ORDER BY id`, TimerArmed)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return h, err
		}
		h.ArmedTimers = append(h.ArmedTimers, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return h, err
	}
	return h, rows.Close()
}

func scanFleet(q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, ctx context.Context) (Fleet, error) {
	var f Fleet
	err := q.QueryRowContext(ctx, `SELECT id, name FROM fleet`).Scan(&f.ID, &f.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Fleet{}, errNoFleet
	}
	return f, err
}

func CopyDatabase(srcPath, dstPath string) error {
	db, err := openSnapshot(srcPath)
	if err != nil {
		return err
	}
	defer db.Close()
	_ = os.Remove(dstPath)
	_, err = db.Exec(`VACUUM INTO ?`, dstPath)
	return err
}

func copySnapshot(path string) (string, func(), error) {
	f, err := os.CreateTemp("", "hand-merge-src-*.db")
	if err != nil {
		return "", nil, err
	}
	tmp := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", nil, err
	}
	_ = os.Remove(tmp)
	if err := CopyDatabase(path, tmp); err != nil {
		_ = os.Remove(tmp)
		return "", nil, err
	}
	return tmp, func() { _ = os.Remove(tmp) }, nil
}

func DatabaseHash(path string) (string, error) {
	snapshot, cleanup, err := copySnapshot(path)
	if err != nil {
		return "", err
	}
	defer cleanup()
	db, err := openSnapshot(snapshot)
	if err != nil {
		return "", err
	}
	defer db.Close()
	return canonicalDigest(context.Background(), db, false)
}

func canonicalDigest(ctx context.Context, q queryer, textOnly bool) (string, error) {
	rows, err := q.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return "", err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return "", err
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return "", err
	}
	if err := rows.Close(); err != nil {
		return "", err
	}
	h := sha256.New()
	for _, table := range tables {
		rows, err := q.QueryContext(ctx, `SELECT * FROM "`+strings.ReplaceAll(table, `"`, `""`)+`"`)
		if err != nil {
			return "", err
		}
		cols, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			return "", err
		}
		_, _ = io.WriteString(h, "table "+table+"\ncolumns "+strings.Join(cols, "|")+"\n")
		var lines []string
		for rows.Next() {
			raw := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range raw {
				dest[i] = &raw[i]
			}
			if err := rows.Scan(dest...); err != nil {
				_ = rows.Close()
				return "", err
			}
			var line strings.Builder
			for _, v := range raw {
				if textOnly && !isTextValue(v) {
					line.WriteString("-\x1f")
					continue
				}
				line.WriteString(digestValue(v))
				line.WriteByte('\x1f')
			}
			lines = append(lines, line.String())
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return "", err
		}
		if err := rows.Close(); err != nil {
			return "", err
		}
		sort.Strings(lines)
		for _, line := range lines {
			_, _ = io.WriteString(h, line+"\n")
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isTextValue(v any) bool {
	switch v.(type) {
	case string, []byte:
		return true
	}
	return false
}

func digestValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "n"
	case int64:
		return "i" + strconv.FormatInt(x, 10)
	case float64:
		return "f" + strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return "b" + strconv.FormatBool(x)
	case string:
		return "s" + strconv.Itoa(len(x)) + ":" + x
	case []byte:
		return "b" + strconv.Itoa(len(x)) + ":" + string(x)
	default:
		return fmt.Sprintf("?%v", x)
	}
}

func Merge(ctx context.Context, srcPath, dstPath string, o MergeOptions) (MergeReport, error) {
	var rep MergeReport
	snapshot, cleanup, err := copySnapshot(srcPath)
	if err != nil {
		return rep, err
	}
	defer cleanup()
	src, err := openReadOnly(snapshot)
	if err != nil {
		return rep, err
	}
	defer src.Close()
	var v int
	if err := src.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return rep, err
	}
	if v != SchemaVersion {
		return rep, fmt.Errorf("%w: source has schema %d, not the current %d", ErrInvalid, v, SchemaVersion)
	}
	if err := CheckCurrent(dstPath); err != nil {
		return rep, err
	}
	dst, err := Open(dstPath, time.Now)
	if err != nil {
		return rep, err
	}
	defer dst.Close()
	err = dst.tx(ctx, func(tx *sql.Tx) error {
		return mergeTx(ctx, tx, src, o, &rep)
	})
	return rep, err
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type mergeRefs struct {
	ids          map[string]map[int64]bool
	off          map[string]int64
	srcID, dstID string
}

var (
	mergeRef     = regexp.MustCompile(`[tardiw][0-9]+`)
	mergeShield  = regexp.MustCompile(`(?i)https?://[^\s)>\]]+|\b(?:hand|worktrees)/[0-9a-f]{13}/t[0-9]+-a[0-9]+(?:-[A-Za-z0-9._-]+)?|\bt[0-9]+-a[0-9]+\b|\b(?:reports|briefs|shots)/t[0-9]+(?:[./][A-Za-z0-9._-]+)*|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	mergeBranch  = regexp.MustCompile(`(?i)\b(?:hand|worktrees)/[0-9a-f]{13}/t[0-9]+-a[0-9]+|\bt[0-9]+-a[0-9]+\b`)
	mergeFleetID = regexp.MustCompile(`f[0-9a-f]{12}`)
)

type textRewriter struct {
	refs       *mergeRefs
	srcHome    string
	dstHome    string
	changed    map[string]int64
	protected  map[string]int64
	cells      map[string]*MergeCell
	table, key string
	column     string
}

func newTextRewriter(refs *mergeRefs, srcHome, dstHome string) *textRewriter {
	return &textRewriter{refs: refs, srcHome: srcHome, dstHome: dstHome, changed: map[string]int64{}, protected: map[string]int64{}, cells: map[string]*MergeCell{}}
}

func (w *textRewriter) cell(table, key, column string) {
	w.table, w.key, w.column = table, key, column
}

func (w *textRewriter) text(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	for {
		open := strings.IndexByte(s, '`')
		if open < 0 {
			b.WriteString(w.outside(s))
			return b.String()
		}
		b.WriteString(w.outside(s[:open]))
		n := 1
		for open+n < len(s) && s[open+n] == '`' {
			n++
		}
		fence := strings.Repeat("`", n)
		rest := s[open+n:]
		close := strings.Index(rest, fence)
		if close < 0 {
			b.WriteString(w.outside(s[open:]))
			return b.String()
		}
		end := open + n + close + n
		b.WriteString(w.code(s[open:end]))
		s = s[end:]
	}
}

func (w *textRewriter) outside(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	pos := 0
	for _, loc := range mergeShield.FindAllStringIndex(s, -1) {
		b.WriteString(w.plain(s[pos:loc[0]]))
		span := s[loc[0]:loc[1]]
		if strings.HasPrefix(strings.ToLower(span), "http") {
			b.WriteString(w.url(span))
		} else {
			w.countProtected(span)
			b.WriteString(w.home(span))
		}
		pos = loc[1]
	}
	b.WriteString(w.plain(s[pos:]))
	return b.String()
}

func (w *textRewriter) code(s string) string {
	for _, loc := range mergeRef.FindAllStringIndex(s, -1) {
		tok := s[loc[0]:loc[1]]
		if !w.ambiguous(s, loc, tok) {
			w.protected[tok[:1]]++
		}
	}
	return w.home(s)
}

func (w *textRewriter) destination(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	pos := 0
	for _, loc := range mergeShield.FindAllStringIndex(s, -1) {
		b.WriteString(s[pos:loc[0]])
		span := s[loc[0]:loc[1]]
		if strings.HasPrefix(strings.ToLower(span), "http") {
			b.WriteString(w.url(span))
		} else {
			b.WriteString(span)
		}
		pos = loc[1]
	}
	b.WriteString(s[pos:])
	return b.String()
}

func (w *textRewriter) plain(s string) string {
	var b strings.Builder
	pos := 0
	for _, loc := range mergeRef.FindAllStringIndex(s, -1) {
		tok := s[loc[0]:loc[1]]
		if (loc[0] > 0 && refAdjacent(s[loc[0]-1])) || (loc[1] < len(s) && !refEndAt(s, loc[1])) {
			if !w.ambiguous(s, loc, tok) {
				w.protected[tok[:1]]++
			}
			continue
		}
		b.WriteString(s[pos:loc[0]])
		if w.ambiguous(s, loc, tok) {
			b.WriteString(tok)
		} else {
			b.WriteString(w.mapRef(tok))
		}
		pos = loc[1]
	}
	b.WriteString(s[pos:])
	return w.home(b.String())
}

func refAdjacent(c byte) bool {
	return isRefByte(c) || c == '.' || c == '/' || c == '#' || c == '-' || c == '|' || c == '"' || c == '\''
}

func refEndAt(s string, end int) bool {
	if end == len(s) {
		return true
	}
	c := s[end]
	if !refAdjacent(c) {
		return true
	}
	switch c {
	case '.', ',', ';', ')':
		return end+1 == len(s) || isSpace(s[end+1])
	}
	return false
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isRefByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

func (w *textRewriter) mapRef(tok string) string {
	out, ok := w.mapped(tok)
	if ok {
		w.changed[tok[:1]]++
	}
	return out
}

func (w *textRewriter) mapped(tok string) (string, bool) {
	kind, n := tok[:1], tok[1:]
	id, err := strconv.ParseInt(n, 10, 64)
	if err != nil {
		return tok, false
	}
	if !w.refs.ids[kind][id] || id <= 0 {
		return tok, false
	}
	return kind + strconv.FormatInt(id+w.refs.off[kind], 10), true
}

func (w *textRewriter) ambiguous(s string, loc []int, tok string) bool {
	kind := tok[:1]
	if kind != "a" && kind != "d" {
		return false
	}
	if kind == "d" && len(tok) == 2 {
		w.cellAmbiguous(tok)
		return true
	}
	if loc[1] < len(s) {
		c := s[loc[1]]
		if c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' {
			end := loc[1]
			for end < len(s) && isHex(s[end]) {
				end++
			}
			run := s[loc[0]:end]
			if len(run) >= 7 && len(run) <= 40 {
				w.cellAmbiguous(run)
				return true
			}
		}
	}
	if len(tok) >= 8 {
		w.cellAmbiguous(tok)
		return true
	}
	return false
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func (w *textRewriter) cellAmbiguous(ref string) {
	k := w.table + "\x00" + w.key + "\x00" + w.column
	c, ok := w.cells[k]
	if !ok {
		c = &MergeCell{Table: w.table, Key: w.key, Column: w.column}
		w.cells[k] = c
	}
	if !strings.Contains(" "+c.Refs+" ", " "+ref+" ") {
		if c.Refs != "" {
			c.Refs += " "
		}
		c.Refs += ref
	}
}

func (w *textRewriter) countProtected(s string) {
	s = mergeFleetID.ReplaceAllString(s, "")
	for _, tok := range mergeRef.FindAllString(s, -1) {
		if len(tok) > 1 {
			w.protected[tok[:1]]++
		}
	}
}

func (w *textRewriter) url(s string) string {
	if mergeBranch.MatchString(s) {
		w.countProtected(s)
		return s
	}
	srcPath, dstPath := "/"+w.refs.srcID+"/", "/"+w.refs.dstID+"/"
	if !strings.Contains(s, srcPath) {
		return s
	}
	s = strings.ReplaceAll(s, srcPath, dstPath)
	var b strings.Builder
	pos := 0
	for _, loc := range mergeFleetID.FindAllStringIndex(s, -1) {
		b.WriteString(w.mapURLRefs(s[pos:loc[0]]))
		b.WriteString(s[loc[0]:loc[1]])
		pos = loc[1]
	}
	b.WriteString(w.mapURLRefs(s[pos:]))
	w.changed["url"]++
	return b.String()
}

func (w *textRewriter) mapURLRefs(s string) string {
	return mergeRef.ReplaceAllStringFunc(s, func(tok string) string {
		out, _ := w.mapped(tok)
		return out
	})
}

func (w *textRewriter) home(s string) string {
	if w.srcHome == w.dstHome {
		return s
	}
	var n int
	s, n = replacePath(s, w.srcHome, w.dstHome)
	w.changed["home"] += int64(n)
	base, dbase := filepath.Base(w.srcHome), filepath.Base(w.dstHome)
	if base != dbase {
		s, n = replacePath(s, "~/"+base, "~/"+dbase)
		w.changed["home"] += int64(n)
	}
	return s
}

func replacePath(s, from, to string) (string, int) {
	if from == "" || from == to {
		return s, 0
	}
	var b strings.Builder
	pos, n := 0, 0
	for {
		i := strings.Index(s[pos:], from)
		if i < 0 {
			break
		}
		start, end := pos+i, pos+i+len(from)
		if (start > 0 && isPathByte(s[start-1])) || !pathEndAt(s, end) {
			b.WriteString(s[pos:end])
			pos = end
			continue
		}
		b.WriteString(s[pos:start])
		b.WriteString(to)
		n++
		pos = end
	}
	b.WriteString(s[pos:])
	return b.String(), n
}

func pathEndAt(s string, end int) bool {
	if end == len(s) {
		return true
	}
	c := s[end]
	if !isPathByte(c) {
		return true
	}
	switch c {
	case '.', ',', ';', ':', '!', '?', ')', ']', '}':
		return end+1 == len(s) || isSpace(s[end+1])
	}
	return false
}

func isPathByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-'
}

func mergeTx(ctx context.Context, tx *sql.Tx, src *sql.DB, o MergeOptions, rep *MergeReport) error {
	var err error
	if rep.Dest, err = scanFleet(tx, ctx); err != nil {
		return err
	}
	var srcFleet Fleet
	if srcFleet, err = scanFleet(src, ctx); err != nil {
		return err
	}
	rep.Source = srcFleet
	var mergedAt string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MIN(at), '') FROM event WHERE kind = 'fleet.merged' AND detail LIKE ?`, srcFleet.ID+" %").Scan(&mergedAt); err != nil {
		return err
	}
	if mergedAt != "" {
		return fmt.Errorf("%w: source %s was already merged into this fleet at %s; a second merge is a non-operation", ErrConflict, srcFleet.ID, mergedAt)
	}
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt WHERE status IN (?, ?)`, AttemptLaunching, AttemptRunning).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return fmt.Errorf("%w: destination has %d live attempt(s)", ErrConflict, live)
	}
	if err := src.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt WHERE status IN (?, ?)`, AttemptLaunching, AttemptRunning).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return fmt.Errorf("%w: source has %d live attempt(s); the operator must stop them", ErrConflict, live)
	}
	if err := src.QueryRowContext(ctx, `SELECT COUNT(*) FROM supervisor_input WHERE delivered_at = ''`).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return fmt.Errorf("%w: source has %d pending input(s); deliver or drop them first", ErrConflict, live)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM supervisor_input WHERE delivered_at = ''`).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return fmt.Errorf("%w: destination has %d pending input(s)", ErrConflict, live)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM supervisor WHERE status IN (?, ?)`, AttemptLaunching, AttemptRunning).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return fmt.Errorf("%w: destination has a live supervisor", ErrConflict)
	}
	if !o.Preflight {
		if err := src.QueryRowContext(ctx, `SELECT COUNT(*) FROM supervisor WHERE status IN (?, ?)`, AttemptLaunching, AttemptRunning).Scan(&live); err != nil {
			return err
		}
		if live > 0 {
			return fmt.Errorf("%w: source has a live supervisor", ErrConflict)
		}
	}
	if !o.CancelTimers {
		if err := src.QueryRowContext(ctx, `SELECT COUNT(*) FROM timer WHERE status = ?`, TimerArmed).Scan(&live); err != nil {
			return err
		}
		if live > 0 {
			return fmt.Errorf("%w: source has %d armed timer(s); cancel them with --cancel-timers", ErrConflict, live)
		}
	}

	off := map[string]int64{}
	for k, spec := range map[string]struct {
		table string
	}{"t": {"task"}, "a": {"attempt"}, "r": {"report"}, "d": {"decision"}, "w": {"timer"}, "i": {"supervisor_input"}} {
		var n int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM `+spec.table).Scan(&n); err != nil {
			return err
		}
		off[k] = n
	}
	var seqOff int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM event`).Scan(&seqOff); err != nil {
		return err
	}
	rep.Offsets = map[string]int64{"t": off["t"], "a": off["a"], "r": off["r"], "d": off["d"], "w": off["w"], "i": off["i"], "event": seqOff}

	ids := map[string]map[int64]bool{}
	for k, table := range map[string]string{"t": "task", "a": "attempt", "r": "report", "d": "decision", "w": "timer", "i": "supervisor_input"} {
		set, err := idSet(ctx, src, table)
		if err != nil {
			return err
		}
		ids[k] = set
	}
	refs := &mergeRefs{ids: ids, off: map[string]int64{"t": off["t"], "a": off["a"], "r": off["r"], "d": off["d"], "w": off["w"], "i": off["i"]}, srcID: srcFleet.ID, dstID: rep.Dest.ID}
	w := newTextRewriter(refs, o.SourceHome, o.DestHome)

	destCounts, err := tableCounts(ctx, tx)
	if err != nil {
		return err
	}
	sourceCounts, err := tableCounts(ctx, src)
	if err != nil {
		return err
	}
	if mergeAfterSourceCounts != nil {
		mergeAfterSourceCounts()
	}
	destMax := map[string]int64{}
	for k, table := range map[string]string{"task": "task", "attempt": "attempt", "report": "report", "decision": "decision", "timer": "timer", "supervisor_input": "supervisor_input", "event": "event"} {
		col := "id"
		if k == "event" {
			col = "seq"
		}
		var n int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(`+col+`), 0) FROM `+table).Scan(&n); err != nil {
			return err
		}
		destMax[k] = n
	}

	projectRows, err := rowsOf(ctx, src, `SELECT name, repo, settings, created_at FROM project ORDER BY name`)
	if err != nil {
		return err
	}
	destProjects, err := projectMap(ctx, tx)
	if err != nil {
		return err
	}
	rename := map[string]string{}
	for old, newName := range o.RenameProject {
		rename[old] = newName
	}
	for old, newName := range rename {
		if _, ok := sourceCountsProject(projectRows, old); !ok {
			return fmt.Errorf("%w: source has no project %s to rename", ErrInvalid, old)
		}
		if !projectName.MatchString(newName) {
			return fmt.Errorf("%w: project name %q must match %s", ErrInvalid, newName, projectName)
		}
		if newName == old {
			continue
		}
		if _, ok := sourceCountsProject(projectRows, newName); ok {
			return fmt.Errorf("%w: source already has project %s", ErrConflict, newName)
		}
		if _, ok := destProjects[newName]; ok {
			return fmt.Errorf("%w: destination already has project %s", ErrConflict, newName)
		}
	}
	mergedProject := map[string]string{}
	for _, row := range projectRows {
		old := text(row, "name")
		newName := old
		if v, ok := rename[old]; ok {
			newName = v
		}
		repo := w.home(text(row, "repo"))
		if destRepo, ok := destProjects[newName]; ok {
			if newName != old {
				return fmt.Errorf("%w: destination already has project %s", ErrConflict, newName)
			}
			if !sameRepo(text(row, "repo"), repo, destRepo, o.SourceHome, o.DestHome) {
				return fmt.Errorf("%w: project %s exists on both sides with a different repo; pass --rename-project %s=NEW", ErrConflict, old, old)
			}
			if text(row, "settings") != "" && destSettings(ctx, tx, newName) != text(row, "settings") {
				rep.Warnings = append(rep.Warnings, "kept destination settings for project "+newName)
			}
			mergedProject[old] = newName
			continue
		}
		if err := insertRow(tx, "project", []string{"name", "repo", "settings", "created_at"},
			[]any{newName, repo, text(row, "settings"), text(row, "created_at")}); err != nil {
			return err
		}
		mergedProject[old] = newName
	}

	taskRows, err := rowsOf(ctx, src, `SELECT id, project, title, goal, status, created_at, updated_at FROM task ORDER BY id`)
	if err != nil {
		return err
	}
	for _, row := range taskRows {
		id := num(row, "id")
		key := strconv.FormatInt(id+off["t"], 10)
		w.cell("task", key, "title")
		title := w.text(text(row, "title"))
		w.cell("task", key, "goal")
		goal := w.text(text(row, "goal"))
		if err := insertRow(tx, "task", []string{"id", "project", "title", "goal", "status", "created_at", "updated_at"},
			[]any{id + off["t"], projectNameOf(mergedProject, text(row, "project")), title, goal, text(row, "status"), text(row, "created_at"), text(row, "updated_at")}); err != nil {
			return err
		}
	}

	planRows, err := rowsOf(ctx, src, `SELECT task_id, revision, body, created_at FROM plan ORDER BY task_id, revision`)
	if err != nil {
		return err
	}
	for _, row := range planRows {
		tid := num(row, "task_id")
		key := strconv.FormatInt(tid+off["t"], 10) + "/" + strconv.FormatInt(num(row, "revision"), 10)
		w.cell("plan", key, "body")
		if err := insertRow(tx, "plan", []string{"task_id", "revision", "body", "created_at"},
			[]any{tid + off["t"], num(row, "revision"), w.text(text(row, "body")), text(row, "created_at")}); err != nil {
			return err
		}
	}

	decisionRows, err := rowsOf(ctx, src, `SELECT id, task_id, question, status, answer, answered_by, created_at, closed_at FROM decision ORDER BY id`)
	if err != nil {
		return err
	}
	for _, row := range decisionRows {
		id := num(row, "id")
		key := strconv.FormatInt(id+off["d"], 10)
		w.cell("decision", key, "question")
		question := w.text(text(row, "question"))
		w.cell("decision", key, "answer")
		answer := w.text(text(row, "answer"))
		if err := insertRow(tx, "decision", []string{"id", "task_id", "question", "status", "answer", "answered_by", "created_at", "closed_at"},
			[]any{id + off["d"], num(row, "task_id") + off["t"], question, text(row, "status"), answer, text(row, "answered_by"), text(row, "created_at"), text(row, "closed_at")}); err != nil {
			return err
		}
	}

	attemptRows, err := rowsOf(ctx, src, `SELECT id, task_id, harness, model, effort, argv, worktree, branch, status, reason, created_at, ended_at, cleaned_at, continues, screen, approve, access, kept FROM attempt ORDER BY id`)
	if err != nil {
		return err
	}
	for _, row := range attemptRows {
		id := num(row, "id")
		key := strconv.FormatInt(id+off["a"], 10)
		w.cell("attempt", key, "argv")
		argv := w.text(text(row, "argv"))
		continues := int64(0)
		if v, ok := optionalNum(row, "continues"); ok && v != 0 {
			continues = v + off["a"]
		}
		if err := insertRow(tx, "attempt", []string{"id", "task_id", "harness", "model", "effort", "argv", "worktree", "branch", "status", "server_generation", "terminal_id", "pane_id", "pid", "start_marker", "reason", "created_at", "ended_at", "cleaned_at", "continues", "screen", "approve", "access", "kept"},
			[]any{id + off["a"], num(row, "task_id") + off["t"], text(row, "harness"), text(row, "model"), text(row, "effort"), argv, text(row, "worktree"), text(row, "branch"), text(row, "status"), "", "", "", int64(0), "", text(row, "reason"), text(row, "created_at"), text(row, "ended_at"), text(row, "cleaned_at"), continues, optional(row, "screen"), text(row, "approve"), text(row, "access"), text(row, "kept")}); err != nil {
			return err
		}
	}

	reportRows, err := rowsOf(ctx, src, `SELECT id, attempt_id, task_id, status, body, created_at, acked_at, acked_by FROM report ORDER BY id`)
	if err != nil {
		return err
	}
	for _, row := range reportRows {
		id := num(row, "id")
		key := strconv.FormatInt(id+off["r"], 10)
		w.cell("report", key, "body")
		if err := insertRow(tx, "report", []string{"id", "attempt_id", "task_id", "status", "body", "created_at", "acked_at", "acked_by"},
			[]any{id + off["r"], num(row, "attempt_id") + off["a"], num(row, "task_id") + off["t"], text(row, "status"), w.text(text(row, "body")), text(row, "created_at"), text(row, "acked_at"), text(row, "acked_by")}); err != nil {
			return err
		}
	}

	mergeAt := mergeTime(ctx, src, tx)
	timerRows, err := rowsOf(ctx, src, `SELECT id, due_at, every, task_id, note, status, created_at, ended_at, waits FROM timer ORDER BY id`)
	if err != nil {
		return err
	}
	var cancelled []int64
	for _, row := range timerRows {
		id := num(row, "id")
		key := strconv.FormatInt(id+off["w"], 10)
		w.cell("timer", key, "note")
		note := w.text(text(row, "note"))
		status, ended := text(row, "status"), text(row, "ended_at")
		if status == TimerArmed && o.CancelTimers {
			status, ended = TimerCancelled, mergeAt
			cancelled = append(cancelled, id+off["w"])
		}
		var task any
		if v, ok := optionalNum(row, "task_id"); ok {
			task = v + off["t"]
		}
		if err := insertRow(tx, "timer", []string{"id", "due_at", "every", "task_id", "note", "status", "created_at", "ended_at", "waits"},
			[]any{id + off["w"], text(row, "due_at"), num(row, "every"), task, note, status, text(row, "created_at"), ended, num(row, "waits")}); err != nil {
			return err
		}
	}
	var maxSourceSeq int64
	if err := src.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM event`).Scan(&maxSourceSeq); err != nil {
		return err
	}
	cancelSeq := seqOff + maxSourceSeq
	for _, id := range cancelled {
		var task any
		if err := tx.QueryRowContext(ctx, `SELECT task_id FROM timer WHERE id = ?`, id).Scan(&task); err != nil {
			return err
		}
		cancelSeq++
		if _, err := tx.ExecContext(ctx, `INSERT INTO event(seq, at, kind, task_id, detail) VALUES (?, ?, ?, ?, ?)`, cancelSeq, mergeAt, "timer.cancelled", task, TimerRef(id)); err != nil {
			return err
		}
	}
	rep.Cancelled = int64(len(cancelled))

	inputRows, err := rowsOf(ctx, src, `SELECT id, body, created_at, delivered_at FROM supervisor_input ORDER BY id`)
	if err != nil {
		return err
	}
	for _, row := range inputRows {
		id := num(row, "id")
		key := strconv.FormatInt(id+off["i"], 10)
		w.cell("supervisor_input", key, "body")
		if err := insertRow(tx, "supervisor_input", []string{"id", "body", "created_at", "delivered_at"},
			[]any{id + off["i"], w.text(text(row, "body")), text(row, "created_at"), text(row, "delivered_at")}); err != nil {
			return err
		}
	}

	eventRows, err := rowsOf(ctx, src, `SELECT seq, at, kind, task_id, detail FROM event ORDER BY seq`)
	if err != nil {
		return err
	}
	for _, row := range eventRows {
		seq := num(row, "seq")
		key := strconv.FormatInt(seq+seqOff, 10)
		w.cell("event", key, "detail")
		var task any
		if v, ok := optionalNum(row, "task_id"); ok {
			task = v + off["t"]
		}
		if err := insertRow(tx, "event", []string{"seq", "at", "kind", "task_id", "detail"},
			[]any{seq + seqOff, text(row, "at"), text(row, "kind"), task, w.text(text(row, "detail"))}); err != nil {
			return err
		}
	}

	grantRows, err := rowsOf(ctx, src, `SELECT project, harness, pattern, created_at FROM project_grant ORDER BY id`)
	if err != nil {
		return err
	}
	for _, row := range grantRows {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_grant(project, harness, pattern, created_at) VALUES (?, ?, ?, ?) ON CONFLICT(project, harness, pattern) DO UPDATE SET created_at = min(project_grant.created_at, excluded.created_at)`,
			projectNameOf(mergedProject, text(row, "project")), text(row, "harness"), text(row, "pattern"), text(row, "created_at")); err != nil {
			return err
		}
	}
	if err := rewriteDestinationText(ctx, tx, w, destMax, destProjects); err != nil {
		return err
	}

	detail := fmt.Sprintf("%s %s: task +%d, attempt +%d, report +%d, decision +%d, timer +%d, input +%d, event +%d; t+%d a+%d r+%d d+%d w+%d i+%d",
		srcFleet.ID, srcFleet.Name, sourceCounts["task"], sourceCounts["attempt"], sourceCounts["report"], sourceCounts["decision"], sourceCounts["timer"], sourceCounts["supervisor_input"], sourceCounts["event"],
		off["t"], off["a"], off["r"], off["d"], off["w"], off["i"])
	if _, err := tx.ExecContext(ctx, `INSERT INTO event(at, kind, task_id, detail) VALUES (?, ?, NULL, ?)`, mergeAt, "fleet.merged", detail); err != nil {
		return err
	}

	if err := renumberEvents(ctx, tx); err != nil {
		return err
	}
	if err := checkMerged(ctx, tx); err != nil {
		return err
	}
	if rep.TextSHA256, err = textDigest(ctx, tx); err != nil {
		return err
	}

	mergedCounts, err := tableCounts(ctx, tx)
	if err != nil {
		return err
	}
	rep.Counts = mergeCounts(sourceCounts, destCounts, mergedCounts, rep.Offsets)
	for _, k := range []string{"t", "a", "r", "d", "w", "i", "url", "home"} {
		rep.Refs = append(rep.Refs, MergeRef{Kind: k, Changed: w.changed[k], Protected: w.protected[k]})
	}
	keys := make([]string, 0, len(w.cells))
	for k := range w.cells {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rep.Cells = append(rep.Cells, *w.cells[k])
	}
	return nil
}

func sourceCountsProject(rows []map[string]any, name string) (map[string]any, bool) {
	for _, row := range rows {
		if text(row, "name") == name {
			return row, true
		}
	}
	return nil, false
}

func projectNameOf(m map[string]string, old string) string {
	if v, ok := m[old]; ok {
		return v
	}
	return old
}

func destSettings(ctx context.Context, tx *sql.Tx, name string) string {
	var s string
	_ = tx.QueryRowContext(ctx, `SELECT settings FROM project WHERE name = ?`, name).Scan(&s)
	return s
}

func sameRepo(srcRepo, rewritten, dstRepo, srcHome, dstHome string) bool {
	one := rewritten
	if !filepath.IsAbs(srcRepo) {
		one = filepath.Join(dstHome, srcRepo)
	} else if rel, err := filepath.Rel(srcHome, srcRepo); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		one = filepath.Join(dstHome, rel)
	}
	two := dstRepo
	if !filepath.IsAbs(dstRepo) {
		two = filepath.Join(dstHome, dstRepo)
	}
	x, err := filepath.Abs(one)
	if err != nil {
		return false
	}
	y, err := filepath.Abs(two)
	if err != nil {
		return false
	}
	return filepath.Clean(x) == filepath.Clean(y)
}

func projectMap(ctx context.Context, tx *sql.Tx) (map[string]string, error) {
	out := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT name, repo FROM project`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, repo string
		if err := rows.Scan(&name, &repo); err != nil {
			return nil, err
		}
		out[name] = repo
	}
	return out, rows.Err()
}

func tableCounts(ctx context.Context, q queryer) (map[string]int64, error) {
	out := map[string]int64{}
	for _, table := range []string{"project", "task", "plan", "decision", "event", "attempt", "report", "timer", "supervisor_input", "project_grant", "supervisor", "device", "fleet"} {
		var n int64
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
			return nil, err
		}
		out[table] = n
	}
	return out, nil
}

func mergeCounts(src, dst, merged map[string]int64, off map[string]int64) []MergeCount {
	order := []string{"project", "task", "plan", "decision", "event", "attempt", "report", "timer", "supervisor_input", "project_grant", "supervisor", "device", "fleet"}
	keys := map[string]string{"task": "t", "attempt": "a", "report": "r", "decision": "d", "timer": "w", "supervisor_input": "i", "event": "event"}
	out := make([]MergeCount, 0, len(order))
	for _, table := range order {
		out = append(out, MergeCount{Table: table, Source: src[table], Dest: dst[table], Merged: merged[table], Offset: off[keys[table]]})
	}
	return out
}

func idSet(ctx context.Context, q queryer, table string) (map[int64]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM `+table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func rowsOf(ctx context.Context, q queryer, query string, args ...any) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		raw := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = normalized(raw[i])
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func normalized(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

func text(row map[string]any, key string) string {
	if v, ok := row[key].(string); ok {
		return v
	}
	return ""
}

func num(row map[string]any, key string) int64 {
	v, _ := optionalNum(row, key)
	return v
}

func optionalNum(row map[string]any, key string) (int64, bool) {
	switch v := row[key].(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case nil:
		return 0, false
	}
	return 0, false
}

func optional(row map[string]any, key string) any {
	if v, ok := row[key]; ok {
		return v
	}
	return nil
}

func insertRow(tx *sql.Tx, table string, cols []string, vals []any) error {
	q := `INSERT INTO ` + table + `(` + strings.Join(cols, ", ") + `) VALUES (` + strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",") + `)`
	_, err := tx.Exec(q, vals...)
	return err
}

func rewriteDestinationText(ctx context.Context, tx *sql.Tx, w *textRewriter, max map[string]int64, projects map[string]string) error {
	type item struct {
		query string
		args  []any
		cols  []string
		key   []string
		table string
	}
	items := []item{
		{`SELECT id, title, goal FROM task WHERE id <= ? ORDER BY id`, []any{max["task"]}, []string{"title", "goal"}, []string{"id"}, "task"},
		{`SELECT task_id, revision, body FROM plan WHERE task_id <= ? ORDER BY task_id, revision`, []any{max["task"]}, []string{"body"}, []string{"task_id", "revision"}, "plan"},
		{`SELECT id, body FROM report WHERE id <= ? ORDER BY id`, []any{max["report"]}, []string{"body"}, []string{"id"}, "report"},
		{`SELECT id, question, answer FROM decision WHERE id <= ? ORDER BY id`, []any{max["decision"]}, []string{"question", "answer"}, []string{"id"}, "decision"},
		{`SELECT id, note FROM timer WHERE id <= ? ORDER BY id`, []any{max["timer"]}, []string{"note"}, []string{"id"}, "timer"},
		{`SELECT seq, detail FROM event WHERE seq <= ? ORDER BY seq`, []any{max["event"]}, []string{"detail"}, []string{"seq"}, "event"},
		{`SELECT id, body FROM supervisor_input WHERE id <= ? ORDER BY id`, []any{max["supervisor_input"]}, []string{"body"}, []string{"id"}, "supervisor_input"},
		{`SELECT id, argv FROM attempt WHERE id <= ? ORDER BY id`, []any{max["attempt"]}, []string{"argv"}, []string{"id"}, "attempt"},
	}
	for _, it := range items {
		rows, err := rowsOf(ctx, tx, it.query, it.args...)
		if err != nil {
			return err
		}
		for _, row := range rows {
			var sets []string
			var args []any
			for _, col := range it.cols {
				old := text(row, col)
				next := w.destination(old)
				if next == old {
					continue
				}
				sets = append(sets, col+" = ?")
				args = append(args, next)
			}
			if len(sets) == 0 {
				continue
			}
			var where []string
			for _, k := range it.key {
				where = append(where, k+" = ?")
				args = append(args, optional(row, k))
			}
			if _, err := tx.ExecContext(ctx, `UPDATE `+it.table+` SET `+strings.Join(sets, ", ")+` WHERE `+strings.Join(where, " AND "), args...); err != nil {
				return err
			}
		}
	}
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var repo string
		if err := tx.QueryRowContext(ctx, `SELECT repo FROM project WHERE name = ?`, name).Scan(&repo); err != nil {
			return err
		}
		if next := w.destination(repo); next != repo {
			if _, err := tx.ExecContext(ctx, `UPDATE project SET repo = ? WHERE name = ?`, next, name); err != nil {
				return err
			}
		}
	}
	return nil
}

func mergeTime(ctx context.Context, src *sql.DB, tx *sql.Tx) string {
	at := ""
	for _, q := range []queryer{src, tx} {
		var v string
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(at), '') FROM event`).Scan(&v); err == nil && v > at {
			at = v
		}
	}
	if at == "" {
		at = time.Unix(0, 0).UTC().Format(time.RFC3339Nano)
	}
	return at
}

func renumberEvents(ctx context.Context, tx *sql.Tx) error {
	var shift int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM event`).Scan(&shift); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS merge_seq`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE merge_seq(old INTEGER PRIMARY KEY, new INTEGER)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO merge_seq(old, new) SELECT seq, row_number() OVER (ORDER BY at, seq) FROM event`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE event SET seq = seq + ?`, shift); err != nil {
		return fmt.Errorf("renumber shift %d: %w", shift, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE event SET seq = (SELECT new FROM merge_seq WHERE old = event.seq - ?)`, shift); err != nil {
		return fmt.Errorf("renumber shift %d: %w", shift, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE supervisor SET wake_cursor = CASE WHEN wake_cursor = 0 THEN 0 ELSE COALESCE((SELECT new FROM merge_seq WHERE old = wake_cursor), (SELECT COALESCE(MAX(new), 0) FROM merge_seq)) END`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sqlite_sequence SET seq = (SELECT COALESCE(MAX(seq), 0) FROM event) WHERE name = 'event'`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO sqlite_sequence(name, seq) SELECT 'event', (SELECT COALESCE(MAX(seq), 0) FROM event) WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'event')`)
	return err
}

func rowsAny(rows *sql.Rows) (bool, error) {
	var any bool
	for rows.Next() {
		any = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, err
	}
	return any, rows.Close()
}

func checkMerged(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	broken, err := rowsAny(rows)
	if err != nil {
		return err
	}
	if broken {
		return fmt.Errorf("%w: merge would break a foreign key; nothing was written", ErrInvalid)
	}
	var result string
	if err := tx.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("%w: merged database failed integrity_check: %s", ErrInvalid, result)
	}
	return nil
}

func textDigest(ctx context.Context, q queryer) (string, error) {
	return canonicalDigest(ctx, q, true)
}
