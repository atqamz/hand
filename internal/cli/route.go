package cli

import (
	"fmt"
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
		if err := harness.Validate(s, harness.EnvOf(r.env.Getenv)); err != nil {
			valid = err.Error()
		}
		rows = append(rows, []string{name, s.Harness, s.Model, s.Effort, valid})
	}
	var models [][]string
	for _, name := range state.Harnesses {
		list, err := harness.Models(name, harness.EnvOf(r.env.Getenv))
		if err != nil {
			models = append(models, []string{name, "", "unavailable: " + err.Error()})
			continue
		}
		for _, m := range list {
			models = append(models, []string{name, m.Name, strings.Join(m.Efforts, " ")})
		}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	tracks, err := st.TrackRecord(r.ctx())
	if err != nil {
		return err
	}
	track := make([][]string, 0, len(tracks))
	for _, t := range tracks {
		n := float64(t.N)
		median := ""
		if t.Timed {
			median = fmt.Sprintf("%.1f", t.Median)
		}
		track = append(track, []string{t.Harness, t.Model, t.Effort, fmt.Sprint(t.N),
			fmt.Sprintf("%.2f", float64(t.FirstTry)/n), fmt.Sprintf("%.2f", float64(t.Reattempt)/n), fmt.Sprintf("%.2f", float64(t.Stuck)/n),
			fmt.Sprintf("%.2f", float64(t.Sent)/n), fmt.Sprintf("%.2f", float64(t.Wakes)/n), median})
	}
	var d toon.Doc
	d.Rows("profiles", []string{"name", "harness", "model", "effort", "valid"}, rows)
	d.Rows("track", []string{"harness", "model", "effort", "n", "first_try", "reattempt", "stuck", "sent_per", "wakes_per", "median_min"}, track)
	d.Field("harnesses", strings.Join(state.Harnesses, " "))
	d.Rows("models", []string{"harness", "model", "efforts"}, models)
	d.Help("Edit profiles in " + r.home + "/" + harness.PolicyFile + "; start with one: `hand attempt start --profile NAME --prompt-file BRIEF.md tN`")
	return r.print(&d)
}
