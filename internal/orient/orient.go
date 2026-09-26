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
	Events      int
	MemoryBytes int
	TitleRunes  int
}

var DefaultBudget = Budget{Active: 15, Inbox: 10, Decisions: 15, Events: 10, MemoryBytes: 1200, TitleRunes: 50}

func Build(ctx context.Context, st *state.Store, home string, b Budget) (*toon.Doc, error) {
	var d toon.Doc
	counts, err := st.CountTasks(ctx)
	if err != nil {
		return nil, err
	}
	d.Field("home", home)
	d.Field("tasks", fmt.Sprintf("inbox=%d active=%d done=%d abandoned=%d",
		counts[state.StatusInbox], counts[state.StatusActive], counts[state.StatusDone], counts[state.StatusAbandoned]))

	active, err := st.Tasks(ctx, []string{state.StatusActive}, b.Active)
	if err != nil {
		return nil, err
	}
	rows := make([][]string, 0, len(active))
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
		rows = append(rows, []string{state.TaskRef(t.ID), t.Project, clip(t.Title, b.TitleRunes), plan, strconv.Itoa(n)})
	}
	d.Rows("active", []string{"id", "project", "title", "plan", "open_decisions"}, rows)
	more(&d, "active", len(active), counts[state.StatusActive], "hand task list --status active --limit 500")

	decisions, err := st.OpenDecisions(ctx, 0, b.Decisions)
	if err != nil {
		return nil, err
	}
	openTotal, err := st.OpenDecisionCount(ctx, 0)
	if err != nil {
		return nil, err
	}
	rows = make([][]string, 0, len(decisions))
	for _, dec := range decisions {
		rows = append(rows, []string{state.DecisionRef(dec.ID), state.TaskRef(dec.TaskID), clip(dec.Question, b.TitleRunes)})
	}
	d.Rows("open_decisions", []string{"id", "task", "question"}, rows)
	more(&d, "open_decisions", len(decisions), openTotal, "hand decision list --limit 500")

	inbox, err := st.Tasks(ctx, []string{state.StatusInbox}, b.Inbox)
	if err != nil {
		return nil, err
	}
	rows = make([][]string, 0, len(inbox))
	for _, t := range inbox {
		rows = append(rows, []string{state.TaskRef(t.ID), t.Project, clip(t.Title, b.TitleRunes)})
	}
	d.Rows("inbox", []string{"id", "project", "title"}, rows)
	more(&d, "inbox", len(inbox), counts[state.StatusInbox], "hand task list --status inbox --limit 500")

	events, err := st.RecentEvents(ctx, b.Events)
	if err != nil {
		return nil, err
	}
	rows = make([][]string, 0, len(events))
	for _, e := range events {
		task := ""
		if e.TaskID != 0 {
			task = state.TaskRef(e.TaskID)
		}
		rows = append(rows, []string{strconv.FormatInt(e.Seq, 10), e.Kind, task})
	}
	d.Rows("recent", []string{"seq", "kind", "task"}, rows)

	mem, truncated, err := memory.Read(home, memory.OperatorFile, b.MemoryBytes)
	if err != nil {
		return nil, err
	}
	d.List("operator_memory", lines(mem))
	if truncated {
		d.Field("operator_memory_truncated", "read "+filepath.Join(home, "memory", memory.OperatorFile)+" for the rest")
	}
	d.Help("Capture every new request first: `hand task add --goal TEXT PROJECT TITLE`",
		"Ask the operator only through `hand decision ask TASK QUESTION`")
	return &d, nil
}

func more(d *toon.Doc, name string, shown, total int, cmd string) {
	if shown < total {
		d.Field(name+"_more", fmt.Sprintf("%d of %d shown; run `%s`", shown, total, cmd))
	}
}

func clip(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
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
