package cli

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const historyCeiling = 16 << 10

var historyCommands = map[string]handler{
	"search": cmdHistorySearch,
	"import": cmdHistoryImport,
}

func init() {
	commands["history"] = func(r *runner, args []string) error { return sub(r, args, "history", historyCommands) }
}

var historyRefArg = regexp.MustCompile(`^[iry][1-9][0-9]*$`)

func cmdHistorySearch(r *runner, args []string) error {
	fs := flags("history search")
	limit := fs.Int("limit", 5, "hits to print, 1 to 50")
	since := fs.String("since", "", "only entries at or after this, like 7d, 36h or 2026-11-01")
	who := fs.String("who", "", "only operator messages (operator), supervisor replies (supervisor) or reports (report)")
	task := fs.String("task", "", "only this task: its reports, and messages and replies that name it, like t12")
	around := fs.Int("context", 0, "entries before and after each message or reply hit, 0 to 5")
	full := fs.String("full", "", "print this entry whole, iN, rN or yN, capped at 16 KiB; no QUERY needed")
	var words []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return helpOr(fs, err, usageError{fmt.Sprintf("%s: %v", fs.Name(), err)})
		}
		if fs.NArg() == 0 {
			break
		}
		words, rest = append(words, fs.Arg(0)), fs.Args()[1:]
	}
	if *full != "" {
		if len(words) > 0 {
			return usageError{"history search: --full takes a ref, not a QUERY"}
		}
		return historyFull(r, *full)
	}
	if len(words) == 0 {
		return usageError{"usage: hand history search QUERY [--limit N] [--since 7d] [--who operator|supervisor|report] [--task tN] [--context N] [--full REF]"}
	}
	q := state.HistoryQuery{Q: strings.Join(words, " "), Who: *who, Limit: *limit, Context: *around}
	var err error
	if q.Since, err = state.ParseSince(*since, r.env.Now()); err != nil {
		return err
	}
	if *task != "" {
		if q.TaskID, err = parseID("t", *task); err != nil {
			return err
		}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	res, err := st.SearchHistory(context.Background(), q)
	if err != nil {
		return err
	}
	return r.print(historyDoc(res, r.env.command()))
}

func historyDoc(res state.HistoryResult, command string) *toon.Doc {
	return fitRows(len(res.Hits), func(n int) *toon.Doc { return historyPage(res, n, command) })
}

func fitRows(total int, page func(n int) *toon.Doc) *toon.Doc {
	lo, hi := 0, total
	for lo < hi {
		if mid := (lo + hi + 1) / 2; len(page(mid).String()) <= historyCeiling {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return page(lo)
}

func historyPage(res state.HistoryResult, n int, command string) *toon.Doc {
	var d toon.Doc
	d.Field("total", strconv.Itoa(res.Total))
	rows := make([][]string, 0, n)
	var near [][]string
	for _, h := range res.Hits[:n] {
		rows = append(rows, []string{h.Ref, h.At, h.Who, h.Task, h.Supervisor, h.Text})
		for _, c := range h.Context {
			near = append(near, []string{h.Ref, c.Ref, c.At, c.Who, c.Text})
		}
	}
	d.Rows("hits", []string{"ref", "at", "who", "task", "supervisor", "text"}, rows)
	if len(near) > 0 {
		d.Rows("context", []string{"hit", "ref", "at", "who", "text"}, near)
	}
	switch {
	case res.Total == 0:
		d.Help("No match. Words are ANDed; try fewer or `a OR b` or `pre*`, or loosen --since, --who or --task")
	case res.Total > n:
		d.Help(fmt.Sprintf("%d more; narrow with --since, --who or --task, or raise --limit. `%s history search --full REF` prints one entry whole", res.Total-n, command))
	default:
		d.Help("`" + command + " history search --full REF` prints one entry whole")
	}
	return &d
}

func historyFull(r *runner, ref string) error {
	if !historyRefArg.MatchString(ref) {
		return usageError{fmt.Sprintf("history search: --full %q is not a ref; want iN, rN or yN", ref)}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	e, err := st.HistoryEntry(context.Background(), ref)
	if err != nil {
		return err
	}
	return r.print(historyFullDoc(e))
}

func historyFullDoc(e state.HistoryEntry) *toon.Doc {
	text, omitted := e.Text, e.Omitted
	for {
		var d toon.Doc
		d.Field("ref", e.Ref)
		d.Field("who", e.Who)
		d.Field("at", e.At)
		if e.Supervisor != "" {
			d.Field("supervisor", e.Supervisor)
		}
		if e.Task != "" {
			d.Field("task", e.Task)
		}
		shown := text
		if omitted > 0 {
			shown += fmt.Sprintf("\n… %d bytes omitted at 16 KiB", omitted)
		}
		d.Text("text", shown)
		size := len(d.String())
		if size <= historyCeiling || text == "" {
			return &d
		}
		cut := min(len(text)*(historyCeiling-64)/size, len(text)-1)
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		omitted += len(text) - cut
		text = text[:cut]
	}
}
