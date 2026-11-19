package state

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/redact"
)

const (
	MaxHistoryBody  = 16 << 10
	MaxHistoryQuery = 1024
	MaxHistoryLimit = 50
	MaxHistoryNear  = 5
	historySnippet  = 300
)

type HistoryQuery struct {
	Q       string
	Who     string
	Since   time.Time
	TaskID  int64
	Limit   int
	Context int
}

type HistoryEntry struct {
	Ref        string
	Who        string
	At         string
	Supervisor string
	Task       string
	Text       string
	Truncated  bool
	Omitted    int
}

type HistoryHit struct {
	HistoryEntry
	Context []HistoryEntry
}

type HistoryResult struct {
	Total int
	Hits  []HistoryHit
}

type historySource struct {
	who, fts, table, at, sup, task string
}

var historySources = []historySource{
	{"operator", "supervisor_input_fts", "supervisor_input", "created_at", "0", "0"},
	{"supervisor", "supervisor_reply_fts", "supervisor_reply", "at", "t.supervisor_id", "0"},
	{"report", "report_fts", "report", "created_at", "0", "t.task_id"},
	{"decision", "decision_fts", "history_decision", "created_at", "0", "t.task_id"},
	{"task", "task_fts", "history_task", "created_at", "0", "t.task_id"},
}

var historyWho = map[string]string{"operator": "i", "supervisor": "y", "report": "r", "decision": "d", "task": "t"}

var historyRef = regexp.MustCompile(`^([irydt])([1-9][0-9]*)$`)

var historyMarked = regexp.MustCompile(`«([^»]*)»`)

func ParseSince(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if n, unit := len(s)-1, s[len(s)-1]; n > 0 && (unit == 'd' || unit == 'h') {
		if v, err := strconv.Atoi(s[:n]); err == nil && v >= 1 && v <= 99999 {
			d := time.Hour
			if unit == 'd' {
				d = 24 * time.Hour
			}
			return now.UTC().Add(-time.Duration(v) * d), nil
		}
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%w: since %q is not Nd, Nh or a date; want 7d, 36h or YYYY-MM-DD", ErrInvalid, s)
}

func quoteWords(q string) string {
	words := strings.Fields(q)
	for i, w := range words {
		words[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	return strings.Join(words, " ")
}

func (q HistoryQuery) validate() error {
	switch {
	case strings.TrimSpace(q.Q) == "":
		return fmt.Errorf("%w: a search needs words to look for", ErrInvalid)
	case len(q.Q) > MaxHistoryQuery:
		return fmt.Errorf("%w: a search takes at most %d bytes", ErrInvalid, MaxHistoryQuery)
	case q.Who != "" && historyWho[q.Who] == "":
		return fmt.Errorf("%w: who %q must be operator, supervisor, report, decision or task", ErrInvalid, q.Who)
	case q.Limit < 1 || q.Limit > MaxHistoryLimit:
		return fmt.Errorf("%w: limit must be a number from 1 to %d", ErrInvalid, MaxHistoryLimit)
	case q.Context < 0 || q.Context > MaxHistoryNear:
		return fmt.Errorf("%w: context must be a number from 0 to %d", ErrInvalid, MaxHistoryNear)
	case q.TaskID < 0:
		return fmt.Errorf("%w: task must be a task ref like t12", ErrInvalid)
	}
	return nil
}

func (src historySource) where(since string, taskID int64) (string, []any) {
	w := ` WHERE ` + src.fts + ` MATCH ?`
	var args []any
	if since != "" {
		w += ` AND t.` + src.at + ` >= ?`
		args = append(args, since)
	}
	switch {
	case taskID == 0:
	case src.task != "0":
		w += ` AND ` + src.task + ` = ?`
		args = append(args, taskID)
	default:
		w += ` AND t.id IN (SELECT rowid FROM ` + src.fts + ` WHERE ` + src.fts + ` MATCH ?)`
		args = append(args, `"`+TaskRef(taskID)+`"`)
	}
	return w, args
}

func (s *Store) SearchHistory(ctx context.Context, q HistoryQuery) (HistoryResult, error) {
	if err := q.validate(); err != nil {
		return HistoryResult{}, err
	}
	since := ""
	if !q.Since.IsZero() {
		since = q.Since.UTC().Format("2006-01-02T15:04:05")
	}
	var sources []historySource
	for _, src := range historySources {
		if q.Who == "" || q.Who == src.who {
			sources = append(sources, src)
		}
	}
	match := strings.TrimSpace(q.Q)
	total, err := s.historyCount(ctx, sources, match, since, q.TaskID)
	if err != nil {
		if ctx.Err() != nil {
			return HistoryResult{}, ctx.Err()
		}
		match = quoteWords(q.Q)
		if total, err = s.historyCount(ctx, sources, match, since, q.TaskID); err != nil {
			return HistoryResult{}, err
		}
	}
	res := HistoryResult{Total: total}
	if total == 0 {
		return res, nil
	}
	var parts []string
	var args []any
	for _, src := range sources {
		where, wargs := src.where(since, q.TaskID)
		parts = append(parts, `SELECT '`+src.who+`' AS who, t.id AS id, t.`+src.at+` AS at, `+src.sup+` AS sup, `+src.task+` AS task, `+src.fts+`.rank AS rank FROM `+src.fts+` JOIN `+src.table+` t ON t.id = `+src.fts+`.rowid`+where)
		args = append(args, match)
		args = append(args, wargs...)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT who, id, at, sup, task FROM (`+strings.Join(parts, ` UNION ALL `)+`) ORDER BY rank, at DESC, who, id LIMIT ?`, append(args, q.Limit)...)
	if err != nil {
		return HistoryResult{}, err
	}
	type key struct {
		src       historySource
		id        int64
		sup, task int64
		at        string
	}
	var found []key
	for rows.Next() {
		var k key
		var who string
		if err := rows.Scan(&who, &k.id, &k.at, &k.sup, &k.task); err != nil {
			_ = rows.Close()
			return HistoryResult{}, err
		}
		k.src = historySourceOf(who)
		found = append(found, k)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return HistoryResult{}, err
	}
	if err := rows.Close(); err != nil {
		return HistoryResult{}, err
	}
	for _, k := range found {
		src := k.src
		var snip, body string
		if err := s.db.QueryRowContext(ctx, `SELECT snippet(`+src.fts+`, 0, '«', '»', '…', 24), t.body FROM `+src.fts+` JOIN `+src.table+` t ON t.id = `+src.fts+`.rowid WHERE `+src.fts+` MATCH ? AND `+src.fts+`.rowid = ?`, match, k.id).Scan(&snip, &body); err != nil {
			return HistoryResult{}, err
		}
		hit := HistoryHit{HistoryEntry: historyEntry(src.who, k.id, k.at, k.sup, k.task)}
		hit.Text = clipLine(redactSnippet(snip, body))
		if q.Context > 0 && src.task == "0" {
			if hit.Context, err = s.historyNear(ctx, src.who, k.id, k.at, q.Context); err != nil {
				return HistoryResult{}, err
			}
		}
		res.Hits = append(res.Hits, hit)
	}
	return res, nil
}

func historySourceOf(who string) historySource {
	for _, src := range historySources {
		if src.who == who {
			return src
		}
	}
	return historySource{}
}

func historyEntry(who string, id int64, at string, sup, task int64) HistoryEntry {
	e := HistoryEntry{Ref: historyWho[who] + strconv.FormatInt(id, 10), Who: who, At: at}
	if sup != 0 {
		e.Supervisor = SupervisorRef(sup)
	}
	if task != 0 {
		e.Task = TaskRef(task)
	}
	return e
}

func (s *Store) historyCount(ctx context.Context, sources []historySource, match, since string, taskID int64) (int, error) {
	total := 0
	for _, src := range sources {
		where, wargs := src.where(since, taskID)
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+src.fts+` JOIN `+src.table+` t ON t.id = `+src.fts+`.rowid`+where, append([]any{match}, wargs...)...).Scan(&n); err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

func redactSnippet(snip, body string) string {
	clean := redact.Text(body)
	if clean == body {
		return redact.Text(snip)
	}
	pos, word := 0, ""
	for _, m := range historyMarked.FindAllStringSubmatch(snip, -1) {
		if i := strings.Index(clean, m[1]); m[1] != "" && i >= 0 && (word == "" || i < pos) {
			pos, word = i, m[1]
		}
	}
	start, end := max(0, pos-80), min(len(clean), pos+len(word)+160)
	for start > 0 && !utf8.RuneStart(clean[start]) {
		start++
	}
	for end < len(clean) && !utf8.RuneStart(clean[end]) {
		end--
	}
	out := clean[start:end]
	if word != "" {
		i := pos - start
		out = out[:i] + "«" + word + "»" + out[i+len(word):]
	}
	if start > 0 {
		out = "…" + out
	}
	if end < len(clean) {
		out += "…"
	}
	return out
}

func clipLine(s string) string {
	s = strings.Join(strings.FieldsFunc(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s), unicode.IsSpace), " ")
	if len(s) <= historySnippet {
		return s
	}
	cut := historySnippet - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func (s *Store) historyNear(ctx context.Context, who string, id int64, at string, n int) ([]HistoryEntry, error) {
	kind := 0
	if who == "supervisor" {
		kind = 1
	}
	const feed = `SELECT k, who, id, at, sup, body FROM (
		SELECT 0 AS k, 'operator' AS who, id, created_at AS at, 0 AS sup, body FROM supervisor_input
		UNION ALL SELECT 1, 'supervisor', id, at, supervisor_id, body FROM supervisor_reply)`
	var out []HistoryEntry
	for _, side := range []struct{ cmp, order string }{{"<", "DESC"}, {">", "ASC"}} {
		rows, err := s.db.QueryContext(ctx, feed+` WHERE (at, k, id) `+side.cmp+` (?, ?, ?) ORDER BY at `+side.order+`, k `+side.order+`, id `+side.order+` LIMIT ?`, at, kind, id, n)
		if err != nil {
			return nil, err
		}
		var part []HistoryEntry
		for rows.Next() {
			var k int
			var w, eat, body string
			var eid, sup int64
			if err := rows.Scan(&k, &w, &eid, &eat, &sup, &body); err != nil {
				_ = rows.Close()
				return nil, err
			}
			e := historyEntry(w, eid, eat, sup, 0)
			e.Text = clipLine(redact.Text(body))
			part = append(part, e)
		}
		err = rows.Err()
		if cerr := rows.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		if side.cmp == "<" {
			for i, j := 0, len(part)-1; i < j; i, j = i+1, j-1 {
				part[i], part[j] = part[j], part[i]
			}
		}
		out = append(out, part...)
	}
	return out, nil
}

func (s *Store) HistoryEntry(ctx context.Context, ref string) (HistoryEntry, error) {
	m := historyRef.FindStringSubmatch(ref)
	if m == nil {
		return HistoryEntry{}, fmt.Errorf("%w: %q is not a history ref; want iN, yN, rN, dN or tN", ErrNotFound, ref)
	}
	id, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return HistoryEntry{}, fmt.Errorf("%w: %q is not a history ref; want iN, yN, rN, dN or tN", ErrNotFound, ref)
	}
	var src historySource
	for who, letter := range historyWho {
		if letter == m[1] {
			src = historySourceOf(who)
		}
	}
	var at, body string
	var sup, task int64
	err = s.db.QueryRowContext(ctx, `SELECT t.`+src.at+`, `+src.sup+`, `+src.task+`, t.body FROM `+src.table+` t WHERE t.id = ?`, id).Scan(&at, &sup, &task, &body)
	if err == sql.ErrNoRows {
		return HistoryEntry{}, fmt.Errorf("%w: no entry %s", ErrNotFound, ref)
	}
	if err != nil {
		return HistoryEntry{}, err
	}
	e := historyEntry(src.who, id, at, sup, task)
	e.Text = redact.Text(body)
	if len(e.Text) > MaxHistoryBody {
		cut := MaxHistoryBody
		for cut > 0 && !utf8.RuneStart(e.Text[cut]) {
			cut--
		}
		e.Truncated, e.Omitted, e.Text = true, len(e.Text)-cut, e.Text[:cut]
	}
	return e, nil
}
