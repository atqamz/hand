package orient

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/inbox"
	"github.com/atqamz/hand/internal/memory"
	"github.com/atqamz/hand/internal/retention"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

type Budget struct {
	Active      int
	Inbox       int
	Decisions   int
	Reports     int
	Events      int
	Changes     int
	ShortBytes  int
	MemoryBytes int
	TitleBytes  int
	Bytes       int
}

const (
	folderBytes = 100 << 20
	folderFiles = 500
)

var DefaultBudget = Budget{Active: 15, Inbox: 10, Decisions: 15, Reports: 10, Events: 10, Changes: 20, ShortBytes: 3000, MemoryBytes: 4000, TitleBytes: 50, Bytes: 6000}

type section struct {
	name   string
	fields []string
	rows   [][]string
	total  int
	cmd    string
}

func Build(ctx context.Context, st *state.Store, home, pane string, watch, managed bool, b Budget) (*toon.Doc, error) {
	f, err := st.Fleet(ctx)
	if err != nil {
		return nil, err
	}
	move := moveWarning(ctx, st, f, home)
	counts, err := st.CountTasks(ctx)
	if err != nil {
		return nil, err
	}
	cursor, err := st.LastEventSeq(ctx)
	if err != nil {
		return nil, err
	}
	sup, hasSup, err := st.LatestSupervisor(ctx)
	if err != nil {
		return nil, err
	}
	supervisor, you, resumable := "none", "", false
	if hasSup {
		origin, err := st.SupervisorOrigin(ctx, sup)
		if err != nil {
			return nil, err
		}
		supervisor = strings.Join(strings.Fields(state.SupervisorRef(sup.ID)+" "+sup.Harness+" "+Clip(sup.Model, 200)+" "+Clip(sup.Effort, 32)+" "+sup.Status), " ")
		if origin != sup.ID {
			supervisor += " (resumes " + state.SupervisorRef(origin) + ")"
		}
		if _, held, err := st.SupervisorHold(ctx, sup); err != nil {
			return nil, err
		} else if held && sup.Status == state.AttemptRunning {
			supervisor += " (holds messages; " + state.NoteLost + ")"
		}
		if pane != "" && sup.Status == state.AttemptRunning {
			you = "not the supervisor"
			if pane == sup.PaneID {
				you = state.SupervisorRef(sup.ID)
			}
		}
		resumable = sup.Status == state.AttemptInterrupted || sup.Status == state.AttemptExited
	}
	active, err := st.Tasks(ctx, []string{state.StatusActive}, b.Active)
	if err != nil {
		return nil, err
	}
	signals, err := st.AttemptSignals(ctx)
	if err != nil {
		return nil, err
	}
	waits, err := st.Waits(ctx)
	if err != nil {
		return nil, err
	}
	activeNested := state.Nested(active)
	if activeNested {
		active = state.Tree(active)
	}
	activeRows := make([][]string, 0, len(active))
	for _, t := range active {
		c, err := taskCells(ctx, st, t, signals, waits)
		if err != nil {
			return nil, err
		}
		row := []string{state.TaskRef(t.ID), t.Project, Clip(t.Title, b.TitleBytes), c.plan, c.attempt, c.report, strconv.Itoa(c.open)}
		if activeNested {
			row = append(row, parentCell(t))
		}
		activeRows = append(activeRows, row)
	}

	decisionRows, openTotal, reportRows, unacked, err := queues(ctx, st, b)
	if err != nil {
		return nil, err
	}

	inbox, err := st.Tasks(ctx, []string{state.StatusInbox}, b.Inbox)
	if err != nil {
		return nil, err
	}
	inboxNested := state.Nested(inbox)
	if inboxNested {
		inbox = state.Tree(inbox)
	}
	inboxRows := make([][]string, 0, len(inbox))
	for _, t := range inbox {
		row := []string{state.TaskRef(t.ID), t.Project, Clip(t.Title, b.TitleBytes)}
		if inboxNested {
			row = append(row, parentCell(t))
		}
		inboxRows = append(inboxRows, row)
	}

	events, err := st.RecentEvents(ctx, b.Events)
	if err != nil {
		return nil, err
	}
	eventRows := make([][]string, 0, len(events))
	for _, e := range events {
		task := ""
		if e.TaskID != 0 {
			task = state.TaskRef(e.TaskID)
		}
		eventRows = append(eventRows, []string{strconv.FormatInt(e.Seq, 10), e.Kind, task})
	}

	unwatched, err := st.Unwatched(ctx, managed)
	if err != nil {
		return nil, err
	}
	unwatchedTotal := len(unwatched.Tasks)
	unwatched.Tasks = unwatched.Tasks[:min(unwatchedTotal, b.Active)]

	timers, err := st.TimersAt(ctx, unwatched.At)
	if err != nil {
		return nil, err
	}
	timerRows := make([][]string, 0, len(timers))
	for _, t := range timers {
		note := Clip(t.Note, b.TitleBytes)
		if t.Waits {
			note = strings.TrimSpace("(waits " + state.TaskRef(t.TaskID) + ") " + note)
		}
		switch {
		case !managed:
			note = strings.TrimSpace("(no managed supervisor: not fired) " + note)
		case t.Beyond && !t.Waits:
			note = strings.TrimSpace("(beyond the horizon) " + note)
		}
		timerRows = append(timerRows, []string{state.TimerRef(t.ID), t.Due.Format(time.RFC3339), note})
	}

	folder := inboxFolder(home)
	artifacts, gcCommand := artifactWarning(home)

	mem, truncated, err := memory.Read(home, memory.OperatorFile, b.MemoryBytes)
	if err != nil {
		return nil, err
	}
	memLines := lines(mem)

	activeFields := []string{"id", "project", "title", "plan", "attempt", "report", "open_decisions"}
	if activeNested {
		activeFields = append(activeFields, "parent")
	}
	inboxFields := []string{"id", "project", "title"}
	if inboxNested {
		inboxFields = append(inboxFields, "parent")
	}
	sections := []*section{
		{"active", activeFields, activeRows, counts[state.StatusActive], "hand task list --status active --limit 500"},
		{"open_decisions", []string{"id", "task", "question"}, decisionRows, openTotal, "hand decision list --limit 500"},
		{"unacked_reports", []string{"id", "attempt", "status", "summary"}, reportRows, unacked, "hand report list --unacked --limit 500"},
		{"inbox", inboxFields, inboxRows, counts[state.StatusInbox], "hand task list --status inbox --limit 500"},
		{"recent", []string{"seq", "kind", "task"}, eventRows, len(eventRows), ""},
	}
	for {
		d := render(home, f, move, supervisor, you, watch, resumable, counts, cursor, unwatched, unwatchedTotal, timerRows, folder, artifacts, gcCommand, sections, memLines, truncated)
		if len(d.String()) < b.Bytes {
			return d, nil
		}
		if dropLast(sections) {
			continue
		}
		if len(memLines) == 0 {
			return d, nil
		}
		memLines, truncated = memLines[:len(memLines)-1], true
	}
}

func queues(ctx context.Context, st *state.Store, b Budget) (decisionRows [][]string, openTotal int, reportRows [][]string, unacked int, err error) {
	decisions, err := st.OpenDecisions(ctx, 0, b.Decisions)
	if err != nil {
		return nil, 0, nil, 0, err
	}
	if openTotal, err = st.OpenDecisionCount(ctx, 0); err != nil {
		return nil, 0, nil, 0, err
	}
	decisionRows = make([][]string, 0, len(decisions))
	for _, dec := range decisions {
		decisionRows = append(decisionRows, []string{state.DecisionRef(dec.ID), state.TaskRef(dec.TaskID), Clip(dec.Question, b.TitleBytes)})
	}
	var reports []state.Report
	if b.Reports > 0 {
		if reports, err = st.Reports(ctx, state.ReportFilter{Unacked: true}, b.Reports); err != nil {
			return nil, 0, nil, 0, err
		}
	}
	if unacked, err = st.UnackedReportCount(ctx); err != nil {
		return nil, 0, nil, 0, err
	}
	reportRows = make([][]string, 0, len(reports))
	for _, rep := range reports {
		reportRows = append(reportRows, []string{state.ReportRef(rep.ID), state.AttemptRef(rep.AttemptID), rep.Status, Clip(rep.Summary(), b.TitleBytes)})
	}
	return decisionRows, openTotal, reportRows, unacked, nil
}

type cells struct {
	plan, attempt, report string
	open                  int
}

func taskCells(ctx context.Context, st *state.Store, t state.Task, signals map[int64]state.Event, waits map[int64]state.Timer) (cells, error) {
	c := cells{plan: "none", attempt: "none", report: "none"}
	p, ok, err := st.CurrentPlan(ctx, t.ID)
	if err != nil {
		return c, err
	}
	if ok {
		c.plan = state.PlanRef(p.Revision)
	}
	if c.open, err = st.OpenDecisionCount(ctx, t.ID); err != nil {
		return c, err
	}
	a, ok, err := st.LatestAttempt(ctx, t.ID)
	if err != nil {
		return c, err
	}
	if ok {
		c.attempt = state.AttemptRef(a.ID) + " " + a.Status
		switch e := signals[a.ID]; e.Kind {
		case "attempt.quiet", "attempt.idle":
			c.attempt += " quiet"
		case "attempt.blocked":
			c.attempt += " blocked"
			if hint := strings.TrimPrefix(e.Detail, state.AttemptRef(a.ID)+": "); hint != "" {
				c.attempt += ": " + Clip(hint, 40)
			}
		}
	}
	if w, waiting := waits[t.ID]; waiting {
		c.attempt += " (waits until " + w.Due.Format(time.DateOnly) + ")"
	}
	if ok {
		rep, found, err := st.LatestReport(ctx, state.ReportFilter{AttemptID: a.ID})
		if err != nil {
			return c, err
		}
		if found {
			c.report = state.ReportRef(rep.ID) + " " + rep.Status
		}
	}
	return c, nil
}

func Short(ctx context.Context, st *state.Store, since int64, pane string, watch, managed bool, b Budget) (*toon.Doc, error) {
	cursor, err := st.LastEventSeq(ctx)
	if err != nil {
		return nil, err
	}
	if since < 0 || since > cursor {
		return nil, fmt.Errorf("%w: --since %d is not a cursor of this fleet (last %d); run `hand orient`", state.ErrInvalid, since, cursor)
	}
	touched, touchedTotal, err := st.TouchedAfter(ctx, since, b.Active)
	if err != nil {
		return nil, err
	}
	events, eventsTotal, wakeTotal, err := st.NewestEventsAfter(ctx, since, state.WakeKinds, b.Changes)
	if err != nil {
		return nil, err
	}
	eventRows := make([][]string, 0, len(events))
	wakes := 0
	for _, e := range events {
		task := ""
		if e.TaskID != 0 {
			task = state.TaskRef(e.TaskID)
		}
		if slices.Contains(state.WakeKinds, e.Kind) {
			wakes++
		}
		eventRows = append(eventRows, []string{strconv.FormatInt(e.Seq, 10), e.Kind, task})
	}
	counts, err := st.CountTasks(ctx)
	if err != nil {
		return nil, err
	}
	signals, err := st.AttemptSignals(ctx)
	if err != nil {
		return nil, err
	}
	waits, err := st.Waits(ctx)
	if err != nil {
		return nil, err
	}
	taskRows := make([][]string, 0, len(touched))
	for _, id := range touched {
		t, err := st.Task(ctx, id)
		if err != nil {
			return nil, err
		}
		c, err := taskCells(ctx, st, t, signals, waits)
		if err != nil {
			return nil, err
		}
		taskRows = append(taskRows, []string{state.TaskRef(t.ID), t.Project, Clip(t.Title, b.TitleBytes), t.Status, c.plan, c.attempt, c.report, strconv.Itoa(c.open)})
	}
	decisionRows, openTotal, reportRows, unacked, err := queues(ctx, st, b)
	if err != nil {
		return nil, err
	}
	unwatched, err := st.Unwatched(ctx, managed)
	if err != nil {
		return nil, err
	}
	unwatchedTotal := len(unwatched.Tasks)
	unwatched.Tasks = unwatched.Tasks[:min(unwatchedTotal, b.Active)]
	sup, hasSup, err := st.LatestSupervisor(ctx)
	if err != nil {
		return nil, err
	}
	you := ""
	if hasSup && pane != "" && sup.Status == state.AttemptRunning {
		you = "not the supervisor"
		if pane == sup.PaneID {
			you = state.SupervisorRef(sup.ID)
		}
	}
	sections := []*section{
		{"open_decisions", []string{"id", "task", "question"}, decisionRows, openTotal, "hand decision list --limit 500"},
		{"unacked_reports", []string{"id", "attempt", "status", "summary"}, reportRows, unacked, "hand report list --unacked --limit 500"},
		{"changed", []string{"id", "project", "title", "status", "plan", "attempt", "report", "open_decisions"}, taskRows, touchedTotal, "hand orient"},
		{"events", []string{"seq", "kind", "task"}, eventRows, eventsTotal, "hand orient"},
	}
	for {
		d := renderShort(since, cursor, you, watch, counts, unwatched, unwatchedTotal, sections, wakeTotal)
		if len(d.String()) < b.ShortBytes {
			return d, nil
		}
		if events := sections[3]; len(events.rows) > wakes {
			events.rows = events.rows[:len(events.rows)-1]
		} else if !dropLast(sections[:3]) && !dropLast(sections[3:]) {
			return d, nil
		}
	}
}

func renderShort(since, cursor int64, you string, watch bool, counts map[string]int, unwatched state.Unwatched, unwatchedTotal int, sections []*section, wakeTotal int) *toon.Doc {
	var d toon.Doc
	d.Field("since", strconv.FormatInt(since, 10))
	d.Field("cursor", strconv.FormatInt(cursor, 10))
	if you != "" {
		d.Field("you", you)
	}
	if !watch {
		d.Field("watch", "missing")
	}
	d.Field("tasks", fmt.Sprintf("inbox=%d active=%d done=%d abandoned=%d",
		counts[state.StatusInbox], counts[state.StatusActive], counts[state.StatusDone], counts[state.StatusAbandoned]))
	if len(unwatched.Tasks) > 0 {
		refs := make([]string, len(unwatched.Tasks))
		for i, id := range unwatched.Tasks {
			refs[i] = state.TaskRef(id)
		}
		d.List("unwatched", refs)
		if len(refs) < unwatchedTotal {
			d.Field("unwatched_more", fmt.Sprintf("%d of %d shown; run `hand task list --status active --limit 500`", len(refs), unwatchedTotal))
		}
		d.Field("unwatched_idle", unwatched.IdleFor())
	}
	cut := false
	for _, s := range sections {
		if len(s.rows) > 0 {
			d.Rows(s.name, s.fields, s.rows)
		}
		if len(s.rows) < s.total {
			d.Field(s.name+"_more", fmt.Sprintf("%d of %d shown; run `%s`", len(s.rows), s.total, s.cmd))
			cut = cut || s.name != "events"
		}
	}
	kept := 0
	for _, r := range sections[len(sections)-1].rows {
		if slices.Contains(state.WakeKinds, r[1]) {
			kept++
		}
	}
	cut = cut || kept < wakeTotal
	if cut {
		d.Field("more", "rows were cut; run `hand orient`")
	}
	return &d
}

func moveWarning(ctx context.Context, st *state.Store, f state.Fleet, home string) string {
	from, to, pending, err := st.PendingMove(ctx)
	if err != nil {
		return "the pending move is unreadable: " + Clip(err.Error(), 200) + "; `hand fleet move` refuses until the `fleet.moving` event is fixed"
	}
	if pending {
		return "interrupted: the database names " + to + "; run `hand fleet move --yes " + toon.ShellQuote(to) + "` in " + toon.ShellQuote(from) + " or in " + toon.ShellQuote(to)
	}
	if f.Home == "" {
		return ""
	}
	same := filepath.Clean(f.Home) == filepath.Clean(home)
	if runtime.GOOS == "windows" {
		same = strings.EqualFold(filepath.Clean(f.Home), filepath.Clean(home))
	}
	if same {
		return ""
	}
	return "the database names " + f.Home + "; run `hand fleet adopt` here"
}

func inboxFolder(home string) string {
	u, err := inbox.Size(home)
	if err != nil || (u.Bytes <= folderBytes && u.Files <= folderFiles) {
		return ""
	}
	return inbox.Count(u.Files, u.Bytes)
}

func artifactWarning(home string) (string, string) {
	p, err := harness.LoadPolicy(home)
	if err != nil || (p.Retention.WarnSize == 0 && p.Retention.WarnDays == 0) {
		return "", ""
	}
	files, err := retention.Scan(home)
	if err != nil {
		return "could not check reports/ and shots/: " + Clip(err.Error(), 160), retention.GcCommand(p.Retention.WarnDays)
	}
	text := retention.Warning(files, time.Now(), p.Retention.WarnSize, p.Retention.WarnDays)
	return text, retention.GcCommand(p.Retention.WarnDays)
}

func parentCell(t state.Task) string {
	if t.ParentID == 0 {
		return ""
	}
	return state.TaskRef(t.ParentID)
}

func dropLast(sections []*section) bool {
	for i := len(sections) - 1; i >= 0; i-- {
		if s := sections[i]; len(s.rows) > 0 {
			s.rows = s.rows[:len(s.rows)-1]
			return true
		}
	}
	return false
}

func render(home string, f state.Fleet, move, supervisor, you string, watch, resumable bool, counts map[string]int, cursor int64, unwatched state.Unwatched, unwatchedTotal int, timers [][]string, folder, artifacts, gcCommand string, sections []*section, memLines []string, truncated bool) *toon.Doc {
	var d toon.Doc
	d.Field("home", home)
	d.Field("fleet", f.Name+" ("+f.ID+")")
	if move != "" {
		d.Field("move", move)
	}
	d.Field("supervisor", supervisor)
	if you != "" {
		d.Field("you", you)
	}
	watchState := "missing"
	if watch {
		watchState = "running"
	}
	d.Field("watch", watchState)
	d.Field("tasks", fmt.Sprintf("inbox=%d active=%d done=%d abandoned=%d",
		counts[state.StatusInbox], counts[state.StatusActive], counts[state.StatusDone], counts[state.StatusAbandoned]))
	d.Field("cursor", strconv.FormatInt(cursor, 10))
	if len(unwatched.Tasks) > 0 {
		refs := make([]string, len(unwatched.Tasks))
		for i, id := range unwatched.Tasks {
			refs[i] = state.TaskRef(id)
		}
		d.List("unwatched", refs)
		if len(refs) < unwatchedTotal {
			d.Field("unwatched_more", fmt.Sprintf("%d of %d shown; run `hand task list --status active --limit 500`", len(refs), unwatchedTotal))
		}
		d.Field("unwatched_idle", unwatched.IdleFor())
	}
	if len(timers) > 0 {
		d.Rows("timers", []string{"id", "due", "note"}, timers)
	}
	if folder != "" {
		d.Field("inbox_folder", folder)
	}
	if artifacts != "" {
		d.Field("artifacts", artifacts)
	}
	for _, s := range sections {
		d.Rows(s.name, s.fields, s.rows)
		if s.cmd != "" && len(s.rows) < s.total {
			d.Field(s.name+"_more", fmt.Sprintf("%d of %d shown; run `%s`", len(s.rows), s.total, s.cmd))
		}
	}
	d.List("operator_memory", memLines)
	if truncated {
		d.Field("operator_memory_truncated", "read "+filepath.Join(home, "memory", memory.OperatorFile)+" for the rest")
	}
	help := []string{"Capture every new request first: `hand task add --goal TEXT PROJECT TITLE`",
		"Ask the operator only through `hand decision ask TASK QUESTION`",
		"Wait for workers without polling: `hand wait --after CURSOR` (needs `hand watch` running; the managed supervisor gets wakes as messages instead)"}
	if resumable {
		help = append(help, "Resume the supervisor: `hand supervisor resume`")
	}
	if !watch {
		help = append(help, "Start the watcher: `hand watch`")
	}
	if artifacts != "" {
		help = append(help, "Old reports and shots: ask the operator through the `secondhand-cleanup` skill, or list them with `"+gcCommand+"`")
	}
	if folder != "" {
		help = append(help, "See what fills the inbox folder: `hand inbox size`")
	}
	d.Help(help...)
	return &d
}

func Clip(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(toon.Value(string(r))) <= n {
		return string(r)
	}
	for k := min(len(r), n); k > 0; k-- {
		cut := string(r[:k])
		if len(toon.Value(cut+"…")) > n {
			continue
		}
		if k < len(r) && r[k] != ' ' {
			if i := strings.LastIndexByte(cut, ' '); i > 0 {
				cut = cut[:i]
			}
		}
		return cut + "…"
	}
	return "…"
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
