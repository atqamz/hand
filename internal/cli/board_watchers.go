package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
)

var watcherSweep = 30 * time.Second

func (r *runner) keepWatchers(ctx context.Context, root string) {
	failed := map[string]string{}
	for {
		r.sweepWatchers(ctx, root, failed)
		select {
		case <-ctx.Done():
			return
		case <-time.After(watcherSweep):
		}
	}
}

func (r *runner) sweepWatchers(ctx context.Context, root string, failed map[string]string) {
	entries, err := fleet.List(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}
		if e.State != "ok" {
			continue
		}
		msg := ""
		if err := r.keepWatcher(ctx, root, e.ID); err != nil {
			msg = err.Error()
		}
		if msg != failed[e.ID] && ctx.Err() == nil {
			if msg == "" {
				delete(failed, e.ID)
			} else {
				failed[e.ID] = msg
				fmt.Fprintf(r.env.Stderr, "watcher %s: %s\n", e.ID, msg)
			}
		}
	}
}

func (r *runner) keepWatcher(ctx context.Context, root, id string) error {
	home, err := boardHome(root, id)
	if err != nil {
		return nil
	}
	w := &runner{env: r.env, home: home, root: root}
	st, err := w.store()
	if err != nil {
		return nil
	}
	defer st.Close()
	want, err := watcherWanted(ctx, st, home)
	if err != nil || !want {
		return err
	}
	_, err = w.ensureWatcher(ctx)
	return err
}

func watcherWanted(ctx context.Context, st *state.Store, home string) (bool, error) {
	if _, live, err := st.LiveSupervisor(ctx); live || err != nil {
		return live, err
	}
	if p, err := harness.LoadPolicy(home); err != nil || !p.Supervisor.Autoresume {
		return false, nil
	}
	last, ok, err := lastStarted(ctx, st)
	return ok && (last.Status == state.AttemptInterrupted || last.Status == state.AttemptExited), err
}
