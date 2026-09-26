package cli

import (
	"path/filepath"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/toon"
)

var fleetCommands = map[string]handler{
	"list": cmdFleetList,
}

func init() {
	commands["fleet"] = func(r *runner, args []string) error { return sub(r, args, "fleet", fleetCommands) }
}

func cmdFleetList(r *runner, args []string) error {
	if _, err := parse(flags("fleet list"), args, 0); err != nil {
		return err
	}
	root, err := fleet.Root(r.env.Getenv)
	if err != nil {
		return err
	}
	entries, err := fleet.List(root)
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{e.ID, e.Name, e.Home, e.State})
	}
	var d toon.Doc
	d.Rows("fleets", []string{"id", "name", "home", "state"}, rows)
	d.Help("Forget a dead fleet by deleting its link in " + filepath.Join(root, "fleets"))
	return r.print(&d)
}
