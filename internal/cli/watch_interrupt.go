package cli

import (
	"context"
	"errors"
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

var errTurnMovedOn = errors.New("the supervisor's running call changed before the key")

const (
	minAnswerWithin = 5 * time.Second
	maxAnswerWithin = time.Hour
)

func (w *watcher) preempt(ctx context.Context, c luvus.Client, caps luvus.Capabilities) {
	inputs, err := w.st.DeliveredSupervisorInputs(ctx, answerLookback)
	if err != nil {
		return
	}
	now := w.r.env.Now()
	stale := func(in state.SupervisorInput, bound time.Duration, gate bool) bool {
		at, err := time.Parse(time.RFC3339Nano, in.DeliveredAt)
		age := now.Sub(at)
		return err == nil && in.AnsweredAt == "" && in.Reason != state.ReasonInterrupted && !w.interrupted[in.ID] && age > bound && (gate && age <= maxAnswerWithin+answerHorizon || age <= bound+answerHorizon)
	}
	if !slices.ContainsFunc(inputs, func(in state.SupervisorInput) bool { return stale(in, minAnswerWithin, true) }) {
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
	var due, mine []state.SupervisorInput
	slices.Reverse(inputs)
	for _, in := range inputs {
		at, _ := time.Parse(time.RFC3339Nano, in.DeliveredAt)
		if to, ok := deliveredTo(sups, at); !ok || to.ID != sup.ID {
			continue
		}
		mine = append(mine, in)
		if stale(in, time.Duration(p.Supervisor.AnswerWithin), false) {
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
	case !p.Supervisor.Interrupt:
		w.waited(ctx, due, state.WaitOff)
	case sup.Harness != "claude":
		w.waited(ctx, due, state.WaitUnknown)
	default:
		w.judge(ctx, c, sup, mine, due)
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

func (w *watcher) judge(ctx context.Context, c luvus.Client, sup state.Supervisor, mine, due []state.SupervisorInput) {
	if w.calls == nil {
		w.calls = &transcript.Reader{Paths: w.r.transcriptPaths()}
	}
	entries, err := w.calls.Read(ctx, sup.Harness, sup.Session, w.r.home)
	if err != nil {
		w.waited(ctx, due, state.WaitUnknown)
		return
	}
	sent := make([]transcript.Sent, len(mine))
	for i, in := range mine {
		sent[i] = transcript.Sent{ID: in.ID, Body: in.Body, CreatedAt: in.CreatedAt}
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
	if why := w.verdict(ctx, sup); why != "" {
		w.waited(ctx, due, why)
		return
	}
	again := slices.DeleteFunc(slices.Clone(mine[slices.IndexFunc(mine, func(in state.SupervisorInput) bool { return in.ID == due[0].ID }):]), func(in state.SupervisorInput) bool {
		_, ok := taken[in.ID]
		return ok || in.AnsweredAt != ""
	})
	w.interrupt(ctx, c, sup, due, again)
}

func (w *watcher) verdict(ctx context.Context, sup state.Supervisor) string {
	calls, err := w.calls.Running(ctx, sup.Harness, sup.Session, w.r.home)
	switch {
	case err != nil || len(calls) == 0:
		return state.WaitUnknown
	case slices.ContainsFunc(calls, func(call transcript.Call) bool { return call.Tool != "Bash" || !harness.KnownWait(call.Command) }):
		return state.WaitNotWait
	}
	return ""
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
	return promptHoldsText(ctx, c, sup)
}

func promptHoldsText(ctx context.Context, c luvus.Client, sup state.Supervisor) (bool, error) {
	s, err := c.Read(ctx, sup.PaneID, 40)
	return err == nil && !emptyPrompt(s.Text), err
}

func (w *watcher) interrupt(ctx context.Context, c luvus.Client, sup state.Supervisor, due, again []state.SupervisorInput) {
	if w.interrupted == nil {
		w.interrupted = map[int64]bool{}
	}
	for _, in := range again {
		w.interrupted[in.ID] = true
	}
	var moved string
	recheck := func() error {
		if moved = w.verdict(ctx, sup); moved != "" {
			return errTurnMovedOn
		}
		return nil
	}
	if _, err := interruptSupervisor(ctx, c, sup, recheck); err != nil {
		if errors.Is(err, errTurnMovedOn) {
			for _, in := range again {
				delete(w.interrupted, in.ID)
			}
			w.waited(ctx, due, moved)
			return
		}
		w.say("supervisor interrupt: " + err.Error())
		w.waited(ctx, due, state.WaitFailed)
		return
	}
	n, why := 0, "the turn did not stop"
	if status := settled(ctx, c, sup); status != "" {
		for ; n < len(again); n++ {
			if held, err := promptHoldsText(ctx, c, sup); err != nil || held {
				why = "the prompt holds text"
				if err != nil {
					why = err.Error()
				}
				break
			}
			if _, err := sendToSupervisor(ctx, c, sup, status, again[n].Body); err != nil {
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
	}{{inputIDs(again[:n]), ""}, {inputIDs(again[n:]), "not sent again: " + why}} {
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
