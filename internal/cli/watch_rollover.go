package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/update"
)

const (
	rolloverHeader   = "[hand v1 rollover]"
	rolloverCooldown = time.Hour
)

var rolloverWait, rolloverPoll, rolloverStill = 5 * time.Minute, 2 * time.Second, update.StillFor

type contextRead struct {
	sup     int64
	session string
	tokens  int64
}

type stillness struct {
	sup   int64
	rev   int64
	since time.Time
}

func handoffPath(home string) string { return filepath.Join(home, "memory", "handoff.md") }

func rolloverHarness(name string) bool { return name == "claude" || name == "omp" }

func tokens(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprint(n)
}

func (w *watcher) rollover(ctx context.Context, c luvus.Client, caps luvus.Capabilities) {
	p, err := harness.LoadPolicy(w.r.home)
	if err != nil || p.Supervisor.RolloverTokens <= 0 {
		return
	}
	sup, ok, err := w.st.LiveSupervisor(ctx)
	read, _ := w.supContext.Load().(contextRead)
	if err != nil || !ok || sup.Status != state.AttemptRunning || !sup.Managed(caps.ServerGeneration) || sup.Switching() ||
		!rolloverHarness(sup.Harness) ||
		read.sup != sup.ID || read.session != sup.Session || read.tokens < p.Supervisor.RolloverTokens {
		w.still = stillness{}
		return
	}
	if !w.rolloverQuiet(ctx, c, sup) {
		return
	}
	if !w.rolloverDue(ctx, sup) {
		return
	}
	if !w.rollBusy.CompareAndSwap(false, true) {
		return
	}
	w.pending.Add(1)
	go func() {
		defer w.pending.Done()
		defer w.rollBusy.Store(false)
		if w.rolled(ctx, c, sup, read.tokens, p.Supervisor.RolloverTokens) {
			w.deliver(ctx, c, caps)
		}
	}()
}

func (w *watcher) rolloverDue(ctx context.Context, sup state.Supervisor) bool {
	if sup.ID == w.rollFailed.Load() {
		return false
	}
	last, found, err := w.st.LatestEvent(ctx, "supervisor.rolled_over")
	if err != nil {
		return false
	}
	at, _ := time.Parse(time.RFC3339Nano, last.At)
	return !found || w.r.env.Now().Sub(at) >= rolloverCooldown
}

func (w *watcher) rolloverQuiet(ctx context.Context, c luvus.Client, sup state.Supervisor) bool {
	root, err := w.r.rootDir()
	if err != nil {
		return false
	}
	if running, err := update.Running(root); err != nil || running {
		return false
	}
	ag, err := c.Explain(ctx, sup.PaneID)
	if err != nil || (ag.Status != "idle" && ag.Status != "done") {
		w.still = stillness{}
		return false
	}
	s, err := c.Read(ctx, sup.PaneID, luvus.ScreenLines)
	if err != nil {
		return false
	}
	now := w.r.env.Now()
	if w.still.sup != sup.ID || s.ContentRevision != w.still.rev {
		w.still = stillness{sup.ID, s.ContentRevision, now}
	}
	if _, limited := harness.Limit(sup.Harness, s.Text); limited || now.Sub(w.still.since) < rolloverStill {
		return false
	}
	return w.waitingNothing(ctx, sup)
}

func (w *watcher) waitingNothing(ctx context.Context, sup state.Supervisor) bool {
	pending, err := w.st.PendingSupervisorInputs(ctx)
	if err != nil || len(pending) > 0 {
		return false
	}
	events, err := wakeEvents(ctx, w.st, sup.WakeCursor)
	return err == nil && len(events) == 0
}

type handoff struct {
	exists bool
	mod    time.Time
	size   int64
}

func readHandoff(path string) handoff {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return handoff{}
	}
	return handoff{true, info.ModTime(), info.Size()}
}

func (h handoff) written(before handoff) bool {
	return h.exists && h.size > 0 && (!before.exists || !h.mod.Equal(before.mod) || h.size != before.size)
}

func (w *watcher) rolled(ctx context.Context, c luvus.Client, sup state.Supervisor, used, limit int64) bool {
	r := w.r
	unlock, ok, err := r.supervisorLock(false)
	if err != nil {
		w.say("supervisor rollover: " + err.Error())
		return false
	}
	if !ok {
		return false
	}
	defer unlock()
	ref := state.SupervisorRef(sup.ID)
	if cur, ok, err := w.st.LiveSupervisor(ctx); err != nil || !ok || cur.ID != sup.ID || cur.Status != state.AttemptRunning || cur.Switching() || !w.rolloverDue(ctx, cur) || !w.waitingNothing(ctx, cur) {
		return false
	}
	fail := func(why string) bool {
		w.rollFailed.Store(sup.ID)
		w.alert(ctx, "supervisor.rollover", "supervisor", fmt.Sprintf("%s holds %s tokens, over the rollover limit of %s, and %s; it keeps running and Hand will not ask it again", ref, tokens(used), tokens(limit), why))
		return false
	}
	path := handoffPath(r.home)
	before := readHandoff(path)
	ask := fmt.Sprintf("%s Your context holds %s tokens, over the %s Hand keeps. Hand will end this session and start a fresh one. Write your handoff note to %s now: the tasks in flight and the next step for each, the decisions waiting on the operator, the timers that matter, and anything not in Hand's state. Keep it short. Then answer `done` and start no other work.", rolloverHeader, tokens(used), tokens(limit), path)
	if _, err := sendToSupervisor(ctx, c, sup, "idle", ask); err != nil {
		return fail("Hand could not send the request for a handoff note: " + err.Error())
	}
	deadline := time.Now().Add(rolloverWait)
	wrote := false
	for settled := 0; settled < 2; {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(rolloverPoll):
		}
		if time.Now().After(deadline) {
			why := "it wrote no handoff note in " + rolloverWait.String()
			if wrote {
				why = "it did not finish its turn in " + rolloverWait.String()
			}
			return fail(why)
		}
		wrote = wrote || readHandoff(path).written(before)
		ag, err := c.Explain(ctx, sup.PaneID)
		if wrote && err == nil && (ag.Status == "idle" || ag.Status == "done") {
			settled++
		} else {
			settled = 0
		}
	}
	cur, ok, err := w.st.LiveSupervisor(ctx)
	if err != nil || !ok || cur.ID != sup.ID || !w.waitingNothing(ctx, cur) {
		return false
	}
	next, err := w.replace(ctx, c, cur, used)
	if err != nil {
		w.alert(ctx, "supervisor.rollover", "supervisor", fmt.Sprintf("%s wrote its handoff note but Hand could not start the fresh session: %v; continue %s with `%s supervisor resume`", ref, err, ref, r.env.command()))
		return false
	}
	w.say(fmt.Sprintf("%s rolled over to %s at %s tokens", ref, state.SupervisorRef(next.ID), tokens(used)))
	return true
}

func (w *watcher) replace(ctx context.Context, c luvus.Client, sup state.Supervisor, used int64) (state.Supervisor, error) {
	r := w.r
	unlock, ok, err := r.guardGeneration(ctx, w.st, sup)
	if err != nil {
		return state.Supervisor{}, err
	}
	defer unlock()
	if !ok {
		return state.Supervisor{}, r.staleGeneration(sup)
	}
	who := r.requester()
	if rootAlive(sup.PID, sup.StartMarker) {
		if _, who, err = stopWorker(ctx, c, terminal(sup.Terminal), who); err != nil {
			return state.Supervisor{}, err
		}
	}
	_ = r.findSession(ctx, w.st, &sup)
	ref := state.SupervisorRef(sup.ID)
	if _, err := w.st.EndSupervisor(ctx, sup.ID, state.AttemptStopped, who.note("rolled over at "+tokens(used)+" tokens")); err != nil {
		return state.Supervisor{}, err
	}
	cmd := r.env.command()
	note := "You continue " + ref + ", which Hand ended at " + tokens(used) + " tokens of context: run the full `" + cmd + " orient`, then read `" + handoffPath(r.home) + "`, the handoff note " + ref + " wrote for you."
	next, err := r.startSupervisor(ctx, w.st, c, lastSpec(sup), note, sup.WakeCursor)
	if err != nil {
		return next, err
	}
	session := next.Session
	if session == "" {
		session = "found once it starts"
	}
	if err := w.st.NoteSupervisor(ctx, next.ID, "rolled_over", fmt.Sprintf("continues %s (session %s, %d tokens); new session %s", ref, sup.Session, used, session)); err != nil {
		w.say("could not record the rollover: " + err.Error())
	}
	if _, _, err := r.settleLaunch(r.ctx(), w.st, c, next); err != nil {
		w.say("supervisor rollover: " + err.Error())
	}
	return next, nil
}
