package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/inbox"
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
	"parent":  cmdTaskParent,
	"attach":  cmdTaskAttach,
	"detach":  cmdTaskDetach,
}

func init() {
	commands["task"] = func(r *runner, args []string) error { return sub(r, args, "task", taskCommands) }
}

func cmdTaskAdd(r *runner, args []string) error {
	fs := flags("task add")
	goal := fs.String("goal", "", "what done looks like")
	parent := fs.String("parent", "", "task this one belongs under")
	pos, err := parse(fs, args, 2)
	if err != nil {
		return err
	}
	var parentID int64
	if *parent != "" {
		if parentID, err = parseID("t", *parent); err != nil {
			return err
		}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	t, err := st.AddTaskUnder(context.Background(), parentID, pos[0], pos[1], *goal)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("task", state.TaskRef(t.ID))
	d.Field("status", t.Status)
	if t.ParentID != 0 {
		d.Field("parent", state.TaskRef(t.ParentID))
	}
	d.Help("Start it when work begins: `hand task start " + state.TaskRef(t.ID) + "`")
	return r.print(&d)
}

func cmdTaskParent(r *runner, args []string) error {
	fs := flags("task parent")
	parent := fs.String("parent", "", "task this one belongs under")
	root := fs.Bool("root", false, "make it a root task")
	if err := fs.Parse(args); err != nil {
		return helpOr(fs, err, usageError{fmt.Sprintf("%s: %v", fs.Name(), err)})
	}
	pos := fs.Args()
	if len(pos) > 0 {
		if err := fs.Parse(pos[1:]); err != nil {
			return helpOr(fs, err, usageError{fmt.Sprintf("%s: %v", fs.Name(), err)})
		}
		pos = append(pos[:1:1], fs.Args()...)
	}
	if len(pos) != 1 {
		return usageError{fmt.Sprintf("%s: want 1 argument(s) after flags, got %d", fs.Name(), len(pos))}
	}
	if (*parent == "") == !*root {
		return usageError{"task parent: give exactly one of --parent tM or --root"}
	}
	id, err := parseID("t", pos[0])
	if err != nil {
		return err
	}
	var parentID int64
	if !*root {
		if parentID, err = parseID("t", *parent); err != nil {
			return err
		}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	t, err := st.SetParent(context.Background(), id, parentID)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("task", state.TaskRef(t.ID))
	if t.ParentID == 0 {
		d.Field("parent", "none")
	} else {
		d.Field("parent", state.TaskRef(t.ParentID))
	}
	return r.print(&d)
}

func cmdTaskShow(r *runner, args []string) error {
	fs := flags("task show")
	full := fs.Bool("full", false, "print the whole goal, not its first 4 KB")
	pos, err := parse(fs, args, 1)
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
	if !*full {
		t.Goal = elide(t.Goal, goalShowBytes, r.env.command()+" task show --full "+state.TaskRef(id))
	}
	taskFields(&d, t)
	if t.ParentID != 0 {
		d.Field("parent", state.TaskRef(t.ParentID))
	} else {
		d.Field("parent", "none")
	}
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
	files, err := st.TaskFiles(context.Background(), id)
	if err != nil {
		return err
	}
	r.fileRows(&d, files)
	kids, err := st.Children(context.Background(), id)
	if err != nil {
		return err
	}
	if len(kids) > 0 {
		kidRows := make([][]string, 0, len(kids))
		for _, k := range kids {
			kidRows = append(kidRows, []string{state.TaskRef(k.ID), k.Status, k.Title})
		}
		d.Rows("children", []string{"id", "status", "title"}, kidRows)
	}
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
	flat := fs.Bool("flat", false, "one table, newest first, children not placed under their parent")
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
	nested := !*flat && state.Nested(tasks)
	if nested {
		tasks = state.Tree(tasks)
	}
	dated := want == nil || slices.Contains(want, state.StatusDone) || slices.Contains(want, state.StatusAbandoned)
	header := []string{"id", "project", "status", "title"}
	if dated {
		header = append(header, "finished")
	}
	if nested {
		header = append(header, "parent")
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
		if nested {
			row = append(row, parentRef(t))
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

func parentRef(t state.Task) string {
	if t.ParentID == 0 {
		return ""
	}
	return state.TaskRef(t.ParentID)
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
			return helpOr(fs, err, usageError{fmt.Sprintf("%s: %v", fs.Name(), err)})
		}
		pos := fs.Args()
		if len(pos) > 0 {
			if err := fs.Parse(pos[1:]); err != nil {
				return helpOr(fs, err, usageError{fmt.Sprintf("%s: %v", fs.Name(), err)})
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
		if to == state.StatusDone || to == state.StatusAbandoned {
			open, err := st.OpenChildCount(context.Background(), id)
			if err != nil {
				return err
			}
			if open > 0 {
				d.Field("warn", fmt.Sprintf("%s has %d open sub-task(s); they stay as they are: `hand task list`", state.TaskRef(t.ID), open))
			}
		}
		if len(delivered) > 0 {
			d.List("delivered", delivered)
		}
		return r.print(&d)
	}
}

func cmdTaskDeliver(r *runner, args []string) error {
	fs := flags("task deliver")
	if err := fs.Parse(args); err != nil {
		return helpOr(fs, err, usageError{fmt.Sprintf("%s: %v", fs.Name(), err)})
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

func (r *runner) inboxFile(arg string, mustExist bool) (string, error) {
	dir := filepath.Join(r.home, inbox.Folder)
	name := arg
	if filepath.IsAbs(arg) {
		parent, err := filepath.EvalSymlinks(filepath.Dir(arg))
		want, werr := filepath.EvalSymlinks(dir)
		switch {
		case err == nil && werr == nil && parent == want:
			name = filepath.Base(arg)
		case mustExist:
			return r.copyIn(arg)
		default:
			return "", fmt.Errorf("%w: %s is not a file directly in this fleet's inbox/", state.ErrInvalid, arg)
		}
	}
	if !inbox.Stored.MatchString(name) {
		return "", fmt.Errorf("%w: %q is not the name of a file Hand stored in inbox/ (YYYYMMDD-HHMMSS-N-label)", state.ErrInvalid, name)
	}
	if !mustExist {
		return name, nil
	}
	fi, err := os.Lstat(filepath.Join(dir, name))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("%w: %s is not in inbox/", state.ErrNotFound, name)
	case err != nil:
		return "", err
	case !fi.Mode().IsRegular():
		return "", fmt.Errorf("%w: %s in inbox/ is not a regular file", state.ErrInvalid, name)
	}
	return name, nil
}

func (r *runner) copyIn(arg string) (string, error) {
	fi, err := os.Lstat(arg)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("%w: %s does not exist", state.ErrNotFound, arg)
	case err != nil:
		return "", err
	case !fi.Mode().IsRegular():
		return "", fmt.Errorf("%w: %s is not a regular file (a symlink, folder or device is not copied)", state.ErrInvalid, arg)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(arg))
	if err != nil {
		return "", err
	}
	home, err := filepath.EvalSymlinks(r.home)
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(home, filepath.Join(parent, filepath.Base(arg))); err == nil && filepath.IsLocal(rel) && !strings.HasPrefix(rel, "reports"+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s is inside the fleet folder, which holds Hand's own state; only a file in reports/ or outside the folder is copied into inbox/", state.ErrInvalid, arg)
	}
	f, err := os.Open(arg)
	if err != nil {
		return "", err
	}
	defer f.Close()
	name, err := inbox.Save(filepath.Join(r.home, inbox.Folder), r.env.Now(), filepath.Base(arg), f)
	if inbox.TooBig(err) {
		return "", fmt.Errorf("%w: %s: %v", state.ErrInvalid, arg, err)
	}
	return name, err
}

func (r *runner) fileRows(d *toon.Doc, files []state.TaskFile) {
	if len(files) == 0 {
		return
	}
	rows := make([][]string, len(files))
	for i, f := range files {
		held := "ok"
		if fi, err := os.Lstat(filepath.Join(r.home, inbox.Folder, f.Name)); err != nil || !fi.Mode().IsRegular() {
			held = "missing"
		}
		rows[i] = []string{inbox.Line(r.home, i+1, f.Name), held}
	}
	d.Rows("files", []string{"file", "state"}, rows)
}

func (r *runner) taskFileChange(name string, args []string, mustExist bool, apply func(context.Context, *state.Store, int64, []string) (int, error)) error {
	fs := flags("task " + name)
	if err := fs.Parse(args); err != nil {
		return helpOr(fs, err, usageError{fmt.Sprintf("%s: %v", fs.Name(), err)})
	}
	pos := fs.Args()
	if len(pos) < 2 {
		return usageError{fmt.Sprintf("usage: hand task %s tN FILE...; FILE is a name in inbox/, or an absolute path (attach copies a file outside inbox/ into it)", name)}
	}
	id, err := parseID("t", pos[0])
	if err != nil {
		return err
	}
	if err := r.needHome(); err != nil {
		return err
	}
	if name == "attach" {
		unlock, _, err := r.lock("inbox", true)
		if err != nil {
			return err
		}
		defer unlock()
	}
	names := make([]string, len(pos)-1)
	for i, arg := range pos[1:] {
		if names[i], err = r.inboxFile(arg, mustExist); err != nil {
			return err
		}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	n, err := apply(r.ctx(), st, id, names)
	if err != nil {
		return err
	}
	files, err := st.TaskFiles(r.ctx(), id)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("task", state.TaskRef(id))
	d.Field(name+"ed", strconv.Itoa(n))
	r.fileRows(&d, files)
	return r.print(&d)
}

func cmdTaskAttach(r *runner, args []string) error {
	return r.taskFileChange("attach", args, true, func(ctx context.Context, st *state.Store, id int64, names []string) (int, error) {
		added, err := st.AttachFiles(ctx, id, names)
		return len(added), err
	})
}

func cmdTaskDetach(r *runner, args []string) error {
	return r.taskFileChange("detach", args, false, func(ctx context.Context, st *state.Store, id int64, names []string) (int, error) {
		return len(names), st.DetachFiles(ctx, id, names)
	})
}
