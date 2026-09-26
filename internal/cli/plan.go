package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var planCommands = map[string]handler{
	"set":  cmdPlanSet,
	"show": cmdPlanShow,
}

func init() {
	commands["plan"] = func(r *runner, args []string) error { return sub(r, args, "plan", planCommands) }
}

func cmdPlanSet(r *runner, args []string) error {
	fs := flags("plan set")
	body := fs.String("body", "", "plan text")
	file := fs.String("body-file", "", "path to plan text")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	if (*body == "") == (*file == "") {
		return usageError{"plan set: give exactly one of --body or --body-file"}
	}
	if *file != "" {
		b, err := os.ReadFile(*file)
		if err != nil {
			return fmt.Errorf("%w: %v", state.ErrInvalid, err)
		}
		*body = string(b)
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
	p, err := st.SetPlan(context.Background(), id, *body)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("task", state.TaskRef(id))
	d.Field("plan", state.PlanRef(p.Revision))
	return r.print(&d)
}

func cmdPlanShow(r *runner, args []string) error {
	pos, err := parse(flags("plan show"), args, 1)
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
	if _, err := st.Task(context.Background(), id); err != nil {
		return err
	}
	p, ok, err := st.CurrentPlan(context.Background(), id)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("task", state.TaskRef(id))
	if !ok {
		d.Field("plan", "none")
		d.Help("Write one: `hand plan set --body-file PATH " + state.TaskRef(id) + "`")
		return r.print(&d)
	}
	d.Field("plan", state.PlanRef(p.Revision))
	d.Field("body", p.Body)
	return r.print(&d)
}
