package cli

import (
	"context"
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
	ds, err := st.OpenDecisions(context.Background(), id, 20)
	if err != nil {
		return err
	}
	d.Rows("open_decisions", []string{"id", "task", "question"}, decisionRows(ds))
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
	limit := fs.Int("limit", 50, "maximum rows")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	tasks, err := st.Tasks(context.Background(), strings.Split(*status, ","), *limit)
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(tasks))
	for _, t := range tasks {
		rows = append(rows, []string{state.TaskRef(t.ID), t.Project, t.Status, t.Title})
	}
	var d toon.Doc
	d.Rows("tasks", []string{"id", "project", "status", "title"}, rows)
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
