package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

var (
	preemptEvery                   = 5 * time.Second
	interruptSettle, interruptPoll = 5 * time.Second, 200 * time.Millisecond
)

const minAnswerWithin = 5 * time.Second

func (w *watcher) preempt(ctx context.Context, c luvus.Client, caps luvus.Capabilities) {
	inputs, err := w.st.DeliveredSupervisorInputs(ctx, answerLookback)
	if err != nil {
		return
	}
	now := w.r.env.Now()
	stale := func(in state.SupervisorInput, bound time.Duration) bool {
		at, err := time.Parse(time.RFC3339Nano, in.DeliveredAt)
		age := now.Sub(at)
		return err == nil && in.AnsweredAt == "" && in.Reason != state.ReasonInterrupted && !w.interrupted[in.ID] && age > bound && age <= answerHorizon
	}
	if !slices.ContainsFunc(inputs, func(in state.SupervisorInput) bool { return stale(in, minAnswerWithin) }) {
		w.calls = nil
		return
	}
	p, err := harness.LoadPolicy(w.r.home)
	if err != nil {
		if !w.interruptWarn {
			w.interruptWarn = true
			w.say(harness.PolicyFile + " unreadable, no message interrupts the supervisor: " + err.Error())
		}
		return
	}
	sup, ok, err := w.st.LiveSupervisor(ctx)
	sups, err2 := w.st.Supervisors(ctx)
	if err != nil || err2 != nil || !ok || !sup.Managed(caps.ServerGeneration) || sup.Switching() {
		return
	}
	var due []state.SupervisorInput
	var sent []transcript.Sent
	slices.Reverse(inputs)
	for _, in := range inputs {
		at, _ := time.Parse(time.RFC3339Nano, in.DeliveredAt)
		if to, ok := deliveredTo(sups, at); !ok || to.ID != sup.ID {
			continue
		}
		sent = append(sent, transcript.Sent{ID: in.ID, Body: in.Body, CreatedAt: in.CreatedAt})
		if stale(in, time.Duration(p.Supervisor.AnswerWithin)) {
			due = append(due, in)
		}
	}
	if len(due) == 0 {
		return
	}
	unlock, locked, err := w.r.supervisorLock(false)
	if err != nil || !locked {
		return
	}
	defer unlock()
	if !w.running(ctx, c, sup) {
		return
	}
	switch {
	case !p.InterruptOn():
		w.waited(ctx, due, state.WaitOff)
	case sup.Harness != "claude":
		w.waited(ctx, due, state.WaitUnknown)
	default:
		w.judge(ctx, c, sup, sent, due)
	}
}

func (w *watcher) running(ctx context.Context, c luvus.Client, sup state.Supervisor) bool {
	ag, err := c.Explain(ctx, sup.PaneID)
	if err != nil || ag.Status != "working" {
		return false
	}
	if line, err := supervisorLimit(ctx, c, sup); err != nil || line != "" {
		return false
	}
	_, held, err := w.st.SupervisorHold(ctx, sup)
	return err == nil && !held
}

func (w *watcher) waited(ctx context.Context, due []state.SupervisorInput, why string) {
	if err := w.st.WaitInputs(ctx, inputIDs(due), why); err != nil {
		w.say("supervisor interrupt: " + err.Error())
	}
}

func inputIDs(in []state.SupervisorInput) []int64 {
	ids := make([]int64, len(in))
	for i, x := range in {
		ids[i] = x.ID
	}
	return ids
}

func (w *watcher) judge(ctx context.Context, c luvus.Client, sup state.Supervisor, sent []transcript.Sent, due []state.SupervisorInput) {
	if w.calls == nil {
		w.calls = &transcript.Reader{Paths: w.r.transcriptPaths()}
	}
	entries, err := w.calls.Read(ctx, sup.Harness, sup.Session, w.r.home)
	if err != nil {
		w.waited(ctx, due, state.WaitUnknown)
		return
	}
	taken := transcript.Pair(entries, sent)
	if due = slices.DeleteFunc(due, func(in state.SupervisorInput) bool { _, ok := taken[in.ID]; return ok }); len(due) == 0 {
		return
	}
	typed, err := w.typed(ctx, c, sup, due[0])
	if err != nil {
		w.waited(ctx, due, state.WaitUnknown)
		return
	}
	if typed {
		w.waited(ctx, due, state.WaitTyped)
		return
	}
	calls, err := w.calls.Running(ctx, sup.Harness, sup.Session, w.r.home)
	switch {
	case err != nil || len(calls) == 0:
		w.waited(ctx, due, state.WaitUnknown)
	case slices.ContainsFunc(calls, func(call transcript.Call) bool { return call.Tool != "Bash" || !harness.KnownWait(call.Command) }):
		w.waited(ctx, due, state.WaitNotWait)
	default:
		w.interrupt(ctx, c, sup, due)
	}
}

func (w *watcher) typed(ctx context.Context, c luvus.Client, sup state.Supervisor, first state.SupervisorInput) (bool, error) {
	e, ok, err := w.st.LatestEvent(ctx, "supervisor.keys")
	if err != nil {
		return false, err
	}
	pressed, _ := time.Parse(time.RFC3339Nano, e.At)
	delivered, _ := time.Parse(time.RFC3339Nano, first.DeliveredAt)
	if ok && strings.HasPrefix(e.Detail, state.SupervisorRef(sup.ID)+": ") && pressed.After(delivered) {
		return true, nil
	}
	s, err := c.Read(ctx, sup.PaneID, 40)
	return err == nil && !emptyPrompt(s.Text), err
}

func (w *watcher) interrupt(ctx context.Context, c luvus.Client, sup state.Supervisor, due []state.SupervisorInput) {
	if w.interrupted == nil {
		w.interrupted = map[int64]bool{}
	}
	for _, in := range due {
		w.interrupted[in.ID] = true
	}
	if _, err := interruptSupervisor(ctx, c, sup); err != nil {
		w.say("supervisor interrupt: " + err.Error())
		w.waited(ctx, due, state.WaitFailed)
		return
	}
	n, why := 0, "the turn did not stop"
	if status := settled(ctx, c, sup); status != "" {
		for ; n < len(due); n++ {
			if _, err := sendToSupervisor(ctx, c, sup, status, due[n].Body); err != nil {
				why = err.Error()
				break
			}
			if cur, err := c.Explain(ctx, sup.PaneID); err == nil {
				status = cur.Status
			}
		}
	}
	for _, r := range []struct {
		ids    []int64
		detail string
	}{{inputIDs(due[:n]), ""}, {inputIDs(due[n:]), "not sent again: " + why}} {
		if err := w.st.InterruptInputs(ctx, r.ids, r.detail); err != nil {
			w.say("supervisor interrupt: " + err.Error())
		}
	}
	if n > 0 {
		w.say(fmt.Sprintf("supervisor: interrupted a wait and sent %d queued message(s) again", n))
	}
}

func settled(ctx context.Context, c luvus.Client, sup state.Supervisor) string {
	for range int(interruptSettle / interruptPoll) {
		if ag, err := c.Explain(ctx, sup.PaneID); err == nil && (ag.Status == "idle" || ag.Status == "done") {
			return ag.Status
		}
		time.Sleep(interruptPoll)
	}
	return ""
}
