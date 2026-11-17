package cli

import (
	"context"
	"flag"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/orient"
	"github.com/atqamz/hand/internal/state"
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
		doc, err := orient.Short(context.Background(), st, *since, pane, watch, managed, r.supervisorTurn(context.Background(), st, managed), orient.DefaultBudget)
		if err != nil {
			return err
		}
		return r.print(doc)
	}
	doc, err := orient.Build(context.Background(), st, r.home, pane, watch, managed, r.supervisorTurn(context.Background(), st, managed), orient.DefaultBudget)
	if err != nil {
		return err
	}
	return r.print(doc)
}

func (r *runner) supervisorTurn(ctx context.Context, st *state.Store, managed bool) orient.Turn {
	if !managed {
		return orient.Turn{}
	}
	sup, ok, err := st.LiveSupervisor(ctx)
	if err != nil || !ok {
		return orient.Turn{}
	}
	if ag, err := r.client(fleet.Session(r.fleet.ID)).Explain(ctx, sup.PaneID); err != nil || ag.Status != "working" {
		return orient.Turn{}
	}
	began, _ := time.Parse(time.RFC3339Nano, sup.CreatedAt)
	if e, ok, err := st.LatestEvent(ctx, "supervisor.delivered"); err == nil && ok {
		if at, err := time.Parse(time.RFC3339Nano, e.At); err == nil && !at.Before(began) {
			began = at
		}
	}
	bound := harness.DefaultAnswerWithin
	if p, err := harness.LoadPolicy(r.home); err == nil {
		bound = time.Duration(p.Supervisor.AnswerWithin)
	}
	return orient.Turn{Ref: state.SupervisorRef(sup.ID), Age: r.env.Now().Sub(began), Bound: bound}
}
