package cli

import (
	"context"

	"github.com/atqamz/hand/internal/toon"
)

var projectCommands = map[string]handler{
	"add":  cmdProjectAdd,
	"list": cmdProjectList,
}

func init() {
	commands["project"] = func(r *runner, args []string) error { return sub(r, args, "project", projectCommands) }
}

func cmdProjectAdd(r *runner, args []string) error {
	pos, err := parse(flags("project add"), args, 2)
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	p, err := st.AddProject(context.Background(), pos[0], pos[1])
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("project", p.Name)
	d.Field("repo", p.Repo)
	d.Help("Capture work: `hand task add --goal TEXT " + p.Name + " TITLE`")
	return r.print(&d)
}

func cmdProjectList(r *runner, args []string) error {
	if _, err := parse(flags("project list"), args, 0); err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	ps, err := st.Projects(context.Background())
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(ps))
	for _, p := range ps {
		rows = append(rows, []string{p.Name, p.Repo})
	}
	var d toon.Doc
	d.Rows("projects", []string{"name", "repo"}, rows)
	return r.print(&d)
}
