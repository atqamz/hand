package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/board"
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
	"deliver": cmdTaskDeliver,
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
	waits, err := st.Waits(context.Background())
	if err != nil {
		return err
	}
	if w, ok := waits[id]; ok {
		detail := state.TimerRef(w.ID) + " until " + w.Due.Format(time.RFC3339)
		if w.Note != "" {
			detail += ": " + w.Note
		}
		d.Field("waits", detail)
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
	reports, err := st.Reports(context.Background(), state.ReportFilter{TaskID: id}, 20)
	if err != nil {
		return err
	}
	events, err := st.DeliveredEvents(context.Background(), id)
	if err != nil {
		return err
	}
	var delivered [][]string
	for _, dl := range board.Newest(reports, events) {
		delivered = append(delivered, []string{dl.Kind, dl.Ref})
	}
	d.Rows("delivered", []string{"kind", "ref"}, delivered)
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
	status := fs.String("status", "", "comma-separated statuses (inbox, active, done, abandoned, or finished for done and abandoned), or all alone; default inbox,active, or all with --q")
	q := fs.String("q", "", "words that must each appear in the title or goal, or be the task ref")
	since := fs.String("since", "", "on or after this UTC date, YYYY-MM-DD; the day a task finished, else its last update")
	until := fs.String("until", "", "on or before this UTC date, YYYY-MM-DD; the day a task finished, else its last update")
	limit := fs.Int("limit", 50, "maximum rows")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	if *status == "" {
		*status = "inbox,active"
		if strings.TrimSpace(*q) != "" {
			*status = "all"
		}
	}
	want, err := state.StatusWords(*status)
	if err != nil {
		return err
	}
	tasks, _, err := st.SearchTasks(context.Background(), state.TaskQuery{Q: *q, Statuses: want, Since: *since, Until: *until, Limit: *limit})
	if err != nil {
		return err
	}
	dated := want == nil || slices.Contains(want, state.StatusDone) || slices.Contains(want, state.StatusAbandoned)
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
		fs := flags("task " + name)
		var delivered []string
		if to == state.StatusDone {
			fs.Func("deliverable", "a file or link the supervisor produced: an http(s) URL or an absolute path; repeat for each", func(ref string) error {
				if board.DeliverableKind(ref) == "" {
					return errors.New("want an http(s) URL or an absolute path")
				}
				delivered = append(delivered, ref)
				return nil
			})
		}
		if err := fs.Parse(args); err != nil {
			return usageError{fmt.Sprintf("%s: %v", fs.Name(), err)}
		}
		pos := fs.Args()
		if len(pos) > 0 {
			if err := fs.Parse(pos[1:]); err != nil {
				return usageError{fmt.Sprintf("%s: %v", fs.Name(), err)}
			}
			pos = append(pos[:1:1], fs.Args()...)
		}
		if len(pos) != 1 {
			return usageError{fmt.Sprintf("%s: want 1 argument(s) after flags, got %d", fs.Name(), len(pos))}
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
		t, err := st.Transition(context.Background(), id, to, delivered...)
		if err != nil {
			if len(delivered) > 0 {
				err = fmt.Errorf("%w; no deliverable was recorded", err)
			}
			return err
		}
		var d toon.Doc
		d.Field("task", state.TaskRef(t.ID))
		d.Field("status", t.Status)
		if len(delivered) > 0 {
			d.List("delivered", delivered)
		}
		return r.print(&d)
	}
}

func cmdTaskDeliver(r *runner, args []string) error {
	fs := flags("task deliver")
	if err := fs.Parse(args); err != nil {
		return usageError{fmt.Sprintf("%s: %v", fs.Name(), err)}
	}
	pos := fs.Args()
	if len(pos) < 2 {
		return usageError{fmt.Sprintf("%s: want a task and at least 1 PATH_OR_URL after flags, got %d argument(s)", fs.Name(), len(pos))}
	}
	id, err := parseID("t", pos[0])
	if err != nil {
		return err
	}
	for _, ref := range pos[1:] {
		if board.DeliverableKind(ref) == "" {
			return usageError{fmt.Sprintf("%s: invalid value %q: want an http(s) URL or an absolute path; no deliverable was recorded", fs.Name(), ref)}
		}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	t, added, err := st.Deliver(context.Background(), id, pos[1:]...)
	if err != nil {
		return fmt.Errorf("%w; no deliverable was recorded", err)
	}
	var skipped []string
	for _, ref := range pos[1:] {
		if !slices.Contains(added, ref) && !slices.Contains(skipped, ref) {
			skipped = append(skipped, ref)
		}
	}
	var d toon.Doc
	d.Field("task", state.TaskRef(t.ID))
	d.Field("status", t.Status)
	d.List("delivered", added)
	if len(skipped) > 0 {
		d.List("already recorded", skipped)
	}
	d.Help("See them: `hand task show " + state.TaskRef(t.ID) + "`")
	return r.print(&d)
}
