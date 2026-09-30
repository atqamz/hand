package board_test

import (
	"context"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestTheWorkingRowNamesWhatItWorksOn(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	h := fx.handler()
	contains(t, "fresh", region(get(t, h, "/"), "timeline"), "data-working", "s1 working", "data-since", "since it started")
	ctx := context.Background()
	in, err := fx.st.AddSupervisorInput(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.st.DeliverSupervisorInput(ctx, in.ID, false); err != nil {
		t.Fatal(err)
	}
	contains(t, "message", region(get(t, h, "/"), "timeline"), `on your <a href="#msg-i1">`, "data-clock")
	seq, err := fx.st.LastEventSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.st.AdvanceWakeCursor(ctx, 1, seq); err != nil {
		t.Fatal(err)
	}
	contains(t, "wake", region(get(t, h, "/"), "timeline"), "on a wake")
	fx.mu.Lock()
	fx.status = "idle"
	fx.mu.Unlock()
	lacks(t, "idle", region(get(t, h, "/"), "timeline"), "data-working")
}

func TestTheComposerHintFollowsTheState(t *testing.T) {
	cases := []struct {
		name, agent, gen, want, data string
		sup, switching, launching    bool
	}{
		{name: "none", want: "No supervisor is running; your message waits until one starts.", data: "none"},
		{name: "ready", agent: "idle", gen: "gen-1", sup: true, want: "s1 is ready; it reads your message now.", data: "ready"},
		{name: "working", agent: "working", gen: "gen-1", sup: true, want: "s1 is working; your message goes to it right away.", data: "working"},
		{name: "blocked", agent: "blocked", gen: "gen-1", sup: true, want: "s1 is waiting on a screen in Needs; your message waits too.", data: "blocked"},
		{name: "unreachable", agent: "working", gen: "gen-0", sup: true, want: "Luvus restarted; your message waits until hand watch settles it.", data: "none"},
		{name: "switching", agent: "working", gen: "gen-1", sup: true, switching: true, want: "s1 switches to opus high after this turn; your message waits for the new session.", data: "working"},
		{name: "launching", launching: true, want: "s1 is starting; your message waits.", data: "none"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newFixture(t)
			fx.status = c.agent
			ctx := context.Background()
			if c.sup {
				fx.supervisor(t, state.AttemptRunning, c.gen)
			}
			if c.launching {
				if _, err := fx.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude"}}); err != nil {
					t.Fatal(err)
				}
			}
			if c.switching {
				if _, err := fx.st.SetSupervisorSwitch(ctx, 1, "opus", "high"); err != nil {
					t.Fatal(err)
				}
			}
			body := get(t, fx.handler(), "/")
			contains(t, c.name, body, `<span class="hint">`+c.want+`</span>`)
			contains(t, c.name, region(body, "status"), `data-agent="`+c.data+`"`, `data-hint="`+c.want+`"`)
			if c.switching {
				contains(t, c.name, region(body, "console"), "next turn: opus · high")
			}
		})
	}
}
