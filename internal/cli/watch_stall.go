package cli

import (
	"context"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/update"
)

const stallAlert = time.Hour

type rest struct {
	sup    int64
	rev    int64
	since  time.Time
	worked time.Time
}

func (w *watcher) stall(ctx context.Context, c luvus.Client, caps luvus.Capabilities) error {
	p, err := harness.LoadPolicy(w.r.home)
	if err != nil {
		p.Stall = harness.DefaultStall
		if !w.policyWarned {
			w.policyWarned = true
			w.say(harness.PolicyFile + " unreadable, stall wakes use the defaults: " + err.Error())
		}
	}
	sup, ok, err := w.st.LiveSupervisor(ctx)
	if err != nil {
		return err
	}
	if p.Stall.Off || !ok || !sup.Managed(caps.ServerGeneration) || sup.Switching() {
		w.rest = rest{}
		return nil
	}
	if w.rest.sup != sup.ID {
		w.rest = rest{sup: sup.ID}
	}
	u, err := w.st.Unwatched(ctx, true)
	if err != nil || len(u.Tasks) == 0 {
		return err
	}
	now := w.r.env.Now()
	ag, err := c.Explain(ctx, sup.PaneID)
	if err != nil {
		return nil
	}
	if ag.Status == "working" {
		w.rest.worked = now
	}
	if ag.Status != "idle" && ag.Status != "done" {
		return nil
	}
	s, err := c.Read(ctx, sup.PaneID, luvus.ScreenLines)
	if err != nil {
		return nil
	}
	if w.rest.since.IsZero() || s.ContentRevision != w.rest.rev {
		w.rest.rev, w.rest.since = s.ContentRevision, now
	}
	if _, limited := harness.Limit(sup.Harness, s.Text); limited || now.Sub(w.rest.since) < update.StillFor {
		return nil
	}
	pending, err := w.st.PendingSupervisorInputs(ctx)
	if err != nil || len(pending) > 0 {
		return err
	}
	due := u.Since.Add(p.Stall.After)
	if u.Stalls > 0 {
		due = u.Last.Add(p.Stall.Delay(u.Stalls))
	}
	if now.Before(due) || now.Before(w.rest.worked.Add(p.Stall.After)) {
		return nil
	}
	text := stalled(u)
	if err := w.st.NoteSupervisor(ctx, sup.ID, "stalled", text+". Start a worker, arm a timer with `"+w.r.env.command()+" wake`, or finish the task."); err != nil {
		return err
	}
	line := state.SupervisorRef(sup.ID) + " stalled: " + text
	if u.Idle >= stallAlert && (u.Stalls == 0 || u.Last.Sub(u.Since) < stallAlert) {
		w.alert(ctx, "supervisor.stalled", "supervisor", line)
	} else {
		w.say(line)
	}
	w.deliver(ctx, c, caps)
	return nil
}

func stalled(u state.Unwatched) string {
	refs := make([]string, len(u.Tasks))
	for i, id := range u.Tasks {
		refs[i] = state.TaskRef(id)
	}
	names, verb := refs[0], " is active"
	if n := len(refs); n > 1 {
		names, verb = strings.Join(refs[:n-1], ", ")+" and "+refs[n-1], " are active"
	}
	text := names + verb + ", no attempt is running, idle " + u.IdleFor()
	if u.Far.ID != 0 {
		text += "; " + state.TimerRef(u.Far.ID) + " is armed but due in " + state.Ahead(u.FarIn)
	}
	return text
}

func (w *watcher) timers(ctx context.Context, c luvus.Client, caps luvus.Capabilities) error {
	sup, ok, err := w.st.LiveSupervisor(ctx)
	if err != nil || !ok || !sup.Managed(caps.ServerGeneration) {
		return err
	}
	fired, err := w.st.FireTimers(ctx)
	if err != nil || len(fired) == 0 {
		return err
	}
	for _, line := range fired {
		w.say("timer.fired " + line)
	}
	w.deliver(ctx, c, caps)
	return nil
}
