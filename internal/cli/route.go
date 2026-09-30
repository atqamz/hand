package cli

import (
	"strings"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var routeCommands = map[string]handler{
	"list": cmdRouteList,
}

func init() {
	commands["route"] = func(r *runner, args []string) error { return sub(r, args, "route", routeCommands) }
}

func cmdRouteList(r *runner, args []string) error {
	if _, err := parse(flags("route list"), args, 0); err != nil {
		return err
	}
	if err := r.needHome(); err != nil {
		return err
	}
	p, err := harness.LoadPolicy(r.home)
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(p.Profiles))
	for _, name := range p.Names() {
		s := p.Profiles[name]
		valid := "yes"
		if err := harness.Validate(s, harness.CodexHome(r.env.Getenv)); err != nil {
			valid = err.Error()
		}
		rows = append(rows, []string{name, s.Harness, s.Model, s.Effort, valid})
	}
	var models [][]string
	for _, name := range state.Harnesses {
		list, err := harness.Models(name, harness.CodexHome(r.env.Getenv))
		if err != nil {
			models = append(models, []string{name, "", "unavailable: " + err.Error()})
			continue
		}
		for _, m := range list {
			models = append(models, []string{name, m.Name, strings.Join(m.Efforts, " ")})
		}
	}
	var d toon.Doc
	d.Rows("profiles", []string{"name", "harness", "model", "effort", "valid"}, rows)
	d.Field("harnesses", strings.Join(state.Harnesses, " "))
	d.Rows("models", []string{"harness", "model", "efforts"}, models)
	d.Help("Edit profiles in " + r.home + "/" + harness.PolicyFile + "; start with one: `hand attempt start --profile NAME --prompt-file BRIEF.md tN`")
	return r.print(&d)
}
