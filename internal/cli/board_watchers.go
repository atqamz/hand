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
	r.noteWatcher(ctx, failed, "", err)
	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}
		if e.State == "ok" {
			r.noteWatcher(ctx, failed, e.ID, r.keepWatcher(ctx, root, e.ID))
		}
	}
}

func (r *runner) noteWatcher(ctx context.Context, failed map[string]string, id string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if ctx.Err() != nil || msg == failed[id] {
		return
	}
	if msg == "" {
		delete(failed, id)
		return
	}
	failed[id] = msg
	label := "watcher " + id
	if id == "" {
		label = "fleet list"
	}
	fmt.Fprintf(r.env.Stderr, "%s: %s\n", label, msg)
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
