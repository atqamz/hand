package cli

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var taskCommands = map[string]handler{
	"add":     cmdTaskAdd,
	"show":    cmdTaskShow,
	"list":    cmdTaskList,
	"start":   transitionCommand("start", state.StatusActive),
	"done":    transitionCommand("done", state.StatusDone),
	"abandon": transitionCommand("abandon", state.StatusAbandoned),
}

func init() {
	commands["task"] = func(r *runner, args []string) error { return sub(r, args, "task", taskCommands) }
}

func cmdTaskAdd(r *runner, args []string) error {
	fs := flags("task add")
	goal := fs.String("goal", "", "what done looks like")
	pos, err := parse(fs, args, 2)
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	t, err := st.AddTask(context.Background(), pos[0], pos[1], *goal)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("task", state.TaskRef(t.ID))
	d.Field("status", t.Status)
	d.Help("Start it when work begins: `hand task start " + state.TaskRef(t.ID) + "`")
	return r.print(&d)
}

func cmdTaskShow(r *runner, args []string) error {
	pos, err := parse(flags("task show"), args, 1)
	if err != nil {
		return err
	}
	id, err := parseID("t", pos[0])
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	t, err := st.Task(context.Background(), id)
	if err != nil {
		return err
	}
	var d toon.Doc
	taskFields(&d, t)
	p, ok, err := st.CurrentPlan(context.Background(), id)
	if err != nil {
		return err
	}
	if ok {
		d.Field("plan", state.PlanRef(p.Revision))
	} else {
		d.Field("plan", "none")
	}
	a, ok, err := st.LatestAttempt(context.Background(), id)
	if err != nil {
		return err
	}
	if ok {
		d.Field("attempt", state.AttemptRef(a.ID)+" "+a.Status)
	} else {
		d.Field("attempt", "none")
	}
	var rep state.Report
	found := false
	if ok {
		if rep, found, err = st.LatestReport(context.Background(), state.ReportFilter{AttemptID: a.ID}); err != nil {
			return err
		}
	}
	if !found {
		d.Field("report", "none")
	} else if rep.AckedAt == "" {
		d.Field("report", state.ReportRef(rep.ID)+" "+rep.Status+" unacked")
	} else {
		d.Field("report", state.ReportRef(rep.ID)+" "+rep.Status)
	}
	ds, err := st.Decisions(context.Background(), id, 20)
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(ds))
	answered := false
	for _, dec := range ds {
		rows = append(rows, []string{state.DecisionRef(dec.ID), dec.Status, dec.Question})
		answered = answered || dec.Status == state.DecisionAnswered
	}
	d.Rows("decisions", []string{"id", "status", "question"}, rows)
	if answered {
		d.Help("Read an answer: `hand decision show dN`")
	}
	return r.print(&d)
}

func taskFields(d *toon.Doc, t state.Task) {
	d.Field("task", state.TaskRef(t.ID))
	d.Field("project", t.Project)
	d.Field("title", t.Title)
	d.Field("status", t.Status)
	d.Field("goal", t.Goal)
}

func cmdTaskList(r *runner, args []string) error {
	fs := flags("task list")
	status := fs.String("status", "inbox,active", "comma-separated statuses")
	q := fs.String("q", "", "words that must all appear in the title, goal or ref")
	since := fs.String("since", "", "finished on or after this UTC date, YYYY-MM-DD")
	until := fs.String("until", "", "finished on or before this UTC date, YYYY-MM-DD")
	limit := fs.Int("limit", 50, "maximum rows")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	want := strings.Split(*status, ",")
	tasks, _, err := st.SearchTasks(context.Background(), state.TaskQuery{Q: *q, Statuses: want, Since: *since, Until: *until, Limit: *limit})
	if err != nil {
		return err
	}
	dated := slices.Contains(want, state.StatusDone) || slices.Contains(want, state.StatusAbandoned)
	header := []string{"id", "project", "status", "title"}
	if dated {
		header = append(header, "finished")
	}
	rows := make([][]string, 0, len(tasks))
	for _, t := range tasks {
		row := []string{state.TaskRef(t.ID), t.Project, t.Status, t.Title}
		if dated {
			day := ""
			if t.Status == state.StatusDone || t.Status == state.StatusAbandoned {
				day = t.FinishedAt[:10]
			}
			row = append(row, day)
		}
		rows = append(rows, row)
	}
	var d toon.Doc
	d.Rows("tasks", header, rows)
	if len(tasks) == *limit {
		d.Help("More may exist: rerun with `--limit " + strconv.Itoa(*limit*2) + "`")
	}
	return r.print(&d)
}

func transitionCommand(name, to string) handler {
	return func(r *runner, args []string) error {
		pos, err := parse(flags("task "+name), args, 1)
		if err != nil {
			return err
		}
		id, err := parseID("t", pos[0])
		if err != nil {
			return err
		}
		st, err := r.store()
		if err != nil {
			return err
		}
		defer st.Close()
		t, err := st.Transition(context.Background(), id, to)
		if err != nil {
			return err
		}
		var d toon.Doc
		d.Field("task", state.TaskRef(t.ID))
		d.Field("status", t.Status)
		return r.print(&d)
	}
}
