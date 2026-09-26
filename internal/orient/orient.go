package orient

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/memory"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

type Budget struct {
	Active      int
	Inbox       int
	Decisions   int
	Reports     int
	Events      int
	MemoryBytes int
	TitleBytes  int
	Bytes       int
}

var DefaultBudget = Budget{Active: 15, Inbox: 10, Decisions: 15, Reports: 10, Events: 10, MemoryBytes: 1200, TitleBytes: 50, Bytes: 6000}

type section struct {
	name   string
	fields []string
	rows   [][]string
	total  int
	cmd    string
}

func Build(ctx context.Context, st *state.Store, home string, b Budget) (*toon.Doc, error) {
	counts, err := st.CountTasks(ctx)
	if err != nil {
		return nil, err
	}
	cursor, err := st.LastEventSeq(ctx)
	if err != nil {
		return nil, err
	}
	active, err := st.Tasks(ctx, []string{state.StatusActive}, b.Active)
	if err != nil {
		return nil, err
	}
	activeRows := make([][]string, 0, len(active))
	for _, t := range active {
		plan := "none"
		p, ok, err := st.CurrentPlan(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			plan = state.PlanRef(p.Revision)
		}
		n, err := st.OpenDecisionCount(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		attempt := "none"
		a, ok, err := st.LatestAttempt(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			attempt = state.AttemptRef(a.ID) + " " + a.Status
		}
		report := "none"
		if ok {
			rep, found, err := st.LatestReport(ctx, state.ReportFilter{AttemptID: a.ID})
			if err != nil {
				return nil, err
			}
			if found {
				report = state.ReportRef(rep.ID) + " " + rep.Status
			}
		}
		activeRows = append(activeRows, []string{state.TaskRef(t.ID), t.Project, clip(t.Title, b.TitleBytes), plan, attempt, report, strconv.Itoa(n)})
	}

	decisions, err := st.OpenDecisions(ctx, 0, b.Decisions)
	if err != nil {
		return nil, err
	}
	openTotal, err := st.OpenDecisionCount(ctx, 0)
	if err != nil {
		return nil, err
	}
	decisionRows := make([][]string, 0, len(decisions))
	for _, dec := range decisions {
		decisionRows = append(decisionRows, []string{state.DecisionRef(dec.ID), state.TaskRef(dec.TaskID), clip(dec.Question, b.TitleBytes)})
	}

	var reports []state.Report
	if b.Reports > 0 {
		if reports, err = st.Reports(ctx, state.ReportFilter{Unacked: true}, b.Reports); err != nil {
			return nil, err
		}
	}
	unacked, err := st.UnackedReportCount(ctx)
	if err != nil {
		return nil, err
	}
	reportRows := make([][]string, 0, len(reports))
	for _, rep := range reports {
		reportRows = append(reportRows, []string{state.ReportRef(rep.ID), state.AttemptRef(rep.AttemptID), rep.Status, clip(rep.Summary(), b.TitleBytes)})
	}

	inbox, err := st.Tasks(ctx, []string{state.StatusInbox}, b.Inbox)
	if err != nil {
		return nil, err
	}
	inboxRows := make([][]string, 0, len(inbox))
	for _, t := range inbox {
		inboxRows = append(inboxRows, []string{state.TaskRef(t.ID), t.Project, clip(t.Title, b.TitleBytes)})
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

	mem, truncated, err := memory.Read(home, memory.OperatorFile, b.MemoryBytes)
	if err != nil {
		return nil, err
	}
	memLines := lines(mem)

	sections := []*section{
		{"active", []string{"id", "project", "title", "plan", "attempt", "report", "open_decisions"}, activeRows, counts[state.StatusActive], "hand task list --status active --limit 500"},
		{"open_decisions", []string{"id", "task", "question"}, decisionRows, openTotal, "hand decision list --limit 500"},
		{"unacked_reports", []string{"id", "attempt", "status", "summary"}, reportRows, unacked, "hand report list --unacked --limit 500"},
		{"inbox", []string{"id", "project", "title"}, inboxRows, counts[state.StatusInbox], "hand task list --status inbox --limit 500"},
		{"recent", []string{"seq", "kind", "task"}, eventRows, len(eventRows), ""},
	}
	for {
		d := render(home, counts, cursor, sections, memLines, truncated)
		if len(d.String()) < b.Bytes {
			return d, nil
		}
		if len(memLines) > 0 {
			memLines, truncated = memLines[:len(memLines)-1], true
			continue
		}
		if !dropLast(sections) {
			return d, nil
		}
	}
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

func render(home string, counts map[string]int, cursor int64, sections []*section, memLines []string, truncated bool) *toon.Doc {
	var d toon.Doc
	d.Field("home", home)
	d.Field("tasks", fmt.Sprintf("inbox=%d active=%d done=%d abandoned=%d",
		counts[state.StatusInbox], counts[state.StatusActive], counts[state.StatusDone], counts[state.StatusAbandoned]))
	d.Field("cursor", strconv.FormatInt(cursor, 10))
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
	d.Help("Capture every new request first: `hand task add --goal TEXT PROJECT TITLE`",
		"Ask the operator only through `hand decision ask TASK QUESTION`",
		"Wait for workers without polling: `hand wait --after CURSOR` (needs `hand watch` running)")
	return &d
}

func clip(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(toon.Value(string(r))) <= n {
		return string(r)
	}
	for k := min(len(r), n); k > 0; k-- {
		if out := string(r[:k]) + "…"; len(toon.Value(out)) <= n {
			return out
		}
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
