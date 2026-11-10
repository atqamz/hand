package cli

import (
	"context"
	"flag"

	"github.com/atqamz/hand/internal/orient"
)

func init() {
	commands["orient"] = cmdOrient
}

func cmdOrient(r *runner, args []string) error {
	fs := flags("orient")
	since := fs.Int64("since", -1, "print only what changed after this event cursor")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == "since" })
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	watch, err := r.watchHeld()
	if err != nil {
		return err
	}
	managed, err := r.managedSupervisor(context.Background(), st)
	if err != nil {
		return err
	}
	pane := r.env.Getenv("LUVUS_PANE_ID")
	if given {
		doc, err := orient.Short(context.Background(), st, *since, pane, watch, managed, orient.DefaultBudget)
		if err != nil {
			return err
		}
		return r.print(doc)
	}
	doc, err := orient.Build(context.Background(), st, r.home, pane, watch, managed, orient.DefaultBudget)
	if err != nil {
		return err
	}
	return r.print(doc)
}
