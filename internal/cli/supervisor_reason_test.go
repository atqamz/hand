package cli_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/state"
)

func inputState(t *testing.T, h *harness, id int64) state.SupervisorInput {
	t.Helper()
	in, err := openStore(t, h).SupervisorInput(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func refuseAtPrompt(rt *fakeRuntime) {
	rt.srv.Handle("agent.prompt", func(json.RawMessage) (any, error) {
		return nil, fakeuhp.Fail{Code: "agent_not_ready", Message: "input is fenced"}
	})
}

func TestAQueuedMessageNamesBlockedWithTheHintAndTheKeysThatClearIt(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Trust this folder?" })
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no", "reason: blocked")
	in := inputState(t, h, 1)
	if in.Status() != "queued" || in.Reason != state.ReasonBlocked || in.ReasonDetail != "Trust this folder?" || !slices.Equal(in.Keys(), state.SupervisorKeys) {
		t.Fatalf("input = %+v keys %v", in, in.Keys())
	}
	has(t, "show", h.ok("supervisor", "show"), "messages[1]{input,status,at,reason}:", "i1,queued,", `"blocked: Trust this folder?"`)
}

func TestABlockedMessageIsTypedByTheWatcherOnceTheScreenClearsAndLosesItsReason(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	fx := &attemptFixture{h: h, rt: rt}
	pane := supervisorPane(t, startClaudeSupervisor(h))
	rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Trust this folder?" })
	h.ok("supervisor", "send", "--text", "hello")
	if got := rt.prompts(); len(got) != 0 {
		t.Fatalf("typed while blocked: %q", got)
	}
	stop := startWatch(t, fx, "--every", "1h")
	publishStatus(fx, pane, "idle")
	eventually(t, func() bool { return slices.Equal(rt.prompts(), []string{"hello"}) })
	stop()
	in := inputState(t, h, 1)
	if in.Status() != "delivered" || in.Reason != "" || in.ReasonDetail != "" || in.Keys() != nil {
		t.Fatalf("after the screen cleared = %+v", in)
	}
	has(t, "show", h.ok("supervisor", "show"), "pending: 0", "i1,delivered,")
}

func TestAMessageTheHarnessRefusesWhileItWorksNamesTheTurnSince(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status = "working" })
	refuseAtPrompt(rt)
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no", "reason: turn")
	in := inputState(t, h, 1)
	if _, err := time.Parse(time.RFC3339Nano, in.ReasonDetail); err != nil || in.Reason != state.ReasonTurn {
		t.Fatalf("input = %+v, want a turn reason whose detail is when the turn started", in)
	}
	has(t, "show", h.ok("supervisor", "show"), "i1,queued,", "working since last delivery ", " UTC")
}

func TestAMessageTheHarnessRefusesAtAnIdlePromptIsHeldNotATurn(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	refuseAtPrompt(rt)
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no", "reason: held")
	if in := inputState(t, h, 1); in.Reason != state.ReasonHeld || in.ReasonDetail != "supervisor is not at a prompt" {
		t.Fatalf("input = %+v", in)
	}
}

func TestAMessageRefusedUnderAUsageLimitNamesWhenItResets(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status, rt.screen = "idle", claudeLimit })
	refuseAtPrompt(rt)
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no", "reason: limited")
	if in := inputState(t, h, 1); in.Reason != state.ReasonLimited || in.ReasonDetail != "You've hit your session limit · resets 6:10am (Asia/Jakarta)" {
		t.Fatalf("input = %+v", in)
	}
}

func TestAMessageWaitsForAPendingSwitchAndNamesIt(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	h.ok("supervisor", "switch", "--model", "opus", "--effort", "high")
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no", "reason: switching")
	if in := inputState(t, h, 1); in.Reason != state.ReasonSwitching || in.ReasonDetail != "opus high" {
		t.Fatalf("input = %+v", in)
	}
	if got := rt.prompts(); len(got) != 0 {
		t.Fatalf("typed while a switch waits: %q", got)
	}
}

func TestAMessageWithNoSupervisorWaitsAsNoSupervisor(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no", "reason: no_supervisor")
	if in := inputState(t, h, 1); in.Reason != state.ReasonNoSupervisor || in.Status() != "queued" {
		t.Fatalf("input = %+v", in)
	}
}

func TestAMessageWaitsAsLostWhenTheResumeNoteWasNotTaken(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	if err := openStore(t, h).NoteSupervisor(context.Background(), 1, "blocked", state.NoteLost); err != nil {
		t.Fatal(err)
	}
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no", "reason: lost")
	if in := inputState(t, h, 1); in.Reason != state.ReasonLost || in.ReasonDetail != state.NoteLost {
		t.Fatalf("input = %+v", in)
	}
}

func TestTwoWaitingMessagesShareOneReasonAndOneEvent(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Trust this folder?" })
	h.ok("supervisor", "send", "--text", "one")
	h.ok("supervisor", "send", "--text", "two")
	h.ok("supervisor", "send", "--text", "three")
	events, err := openStore(t, h).RecentEventsOf(context.Background(), []string{"supervisor.held"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	for _, e := range events {
		details = append(details, e.Detail)
	}
	want := []string{"i1: blocked: Trust this folder?", "i2: blocked: Trust this folder?", "i3: blocked: Trust this folder?"}
	if !slices.Equal(details, want) {
		t.Fatalf("held events = %q, want %q", details, want)
	}
	for id := int64(1); id <= 3; id++ {
		if in := inputState(t, h, id); in.Reason != state.ReasonBlocked {
			t.Fatalf("input %d = %+v", id, in)
		}
	}
}
