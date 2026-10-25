package cli_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/state"
)

func supervisorTarget(t *testing.T, harness string, set func(*fakeRuntime)) (luvus.Client, state.Supervisor, *fakeRuntime) {
	t.Helper()
	rt := startRuntime(t, fakeuhp.ShortSocket(t))
	c := luvus.Client{Socket: rt.srv.Socket}
	term, err := c.Create(context.Background(), t.TempDir(), "hand-supervisor", []string{exe(fakeBin(t), harness)})
	if err != nil {
		t.Fatal(err)
	}
	rt.set(set)
	return c, state.Supervisor{Harness: harness, Terminal: state.Terminal{PaneID: term.PaneID, TerminalID: term.TerminalID}}, rt
}

func quickSubmitCheck(t *testing.T) {
	t.Helper()
	check := *cli.SubmitCheck
	*cli.SubmitCheck = 300 * time.Millisecond
	t.Cleanup(func() { *cli.SubmitCheck = check })
}

func TestCodexInputThatStaysUnsubmittedGetsAnEnter(t *testing.T) {
	quickSubmitCheck(t)
	c, sup, rt := supervisorTarget(t, "codex", func(rt *fakeRuntime) { rt.ready, rt.status = true, "idle" })
	typed, err := cli.SendToSupervisor(context.Background(), c, sup, "idle", "[hand v1 wake]\nreport r1")
	if err != nil || typed {
		t.Fatalf("typed = %v, err = %v", typed, err)
	}
	if !slices.Equal(rt.prompts(), []string{"[hand v1 wake]\nreport r1"}) || !slices.Equal(rt.keysSent(), []string{"enter"}) {
		t.Fatalf("prompts = %q, keys = %q", rt.prompts(), rt.keysSent())
	}
}

func TestCodexInputThatStartedATurnGetsNoEnter(t *testing.T) {
	quickSubmitCheck(t)
	c, sup, rt := supervisorTarget(t, "codex", func(rt *fakeRuntime) {
		rt.ready, rt.status = true, "idle"
		rt.onPrompt = func() { rt.status = "working" }
	})
	if _, err := cli.SendToSupervisor(context.Background(), c, sup, "idle", "report r1"); err != nil {
		t.Fatal(err)
	}
	if len(rt.keysSent()) != 0 {
		t.Fatalf("keys = %q", rt.keysSent())
	}
}

func TestCodexInputSentDuringATurnGetsNoEnter(t *testing.T) {
	quickSubmitCheck(t)
	c, sup, rt := supervisorTarget(t, "codex", func(rt *fakeRuntime) { rt.ready, rt.status = true, "working" })
	if _, err := cli.SendToSupervisor(context.Background(), c, sup, "working", "report r1"); err != nil {
		t.Fatal(err)
	}
	if len(rt.keysSent()) != 0 {
		t.Fatalf("keys = %q", rt.keysSent())
	}
}

func TestClaudeInputLuvusRefusedIsTypedAtAnEmptyPrompt(t *testing.T) {
	c, sup, rt := supervisorTarget(t, "claude", func(rt *fakeRuntime) {
		rt.ready, rt.status, rt.screen = false, "done", "────────\n❯\n────────\n  ⏵⏵ bypass permissions on"
	})
	typed, err := cli.SendToSupervisor(context.Background(), c, sup, "done", "wake\nline")
	if err != nil || !typed {
		t.Fatalf("typed = %v, err = %v", typed, err)
	}
	want := []string{"w", "a", "k", "e", `\`, "enter", "l", "i", "n", "e", "enter"}
	if len(rt.prompts()) != 0 || !slices.Equal(rt.keysSent(), want) {
		t.Fatalf("prompts = %q, keys = %q, want %q", rt.prompts(), rt.keysSent(), want)
	}
}

func TestClaudeInputLuvusRefusedIsNotTypedIntoAMenu(t *testing.T) {
	c, sup, rt := supervisorTarget(t, "claude", func(rt *fakeRuntime) {
		rt.ready, rt.status, rt.screen = false, "done", "Select a model\n❯ 1. Sonnet\n  2. Opus"
	})
	typed, err := cli.SendToSupervisor(context.Background(), c, sup, "done", "wake")
	if typed || luvus.Code(err) != "agent_not_ready" || len(rt.keysSent()) != 0 {
		t.Fatalf("typed = %v, err = %v, keys = %q", typed, err, rt.keysSent())
	}
}

func TestClaudeInputLuvusRefusedDuringATurnIsNotTyped(t *testing.T) {
	c, sup, rt := supervisorTarget(t, "claude", func(rt *fakeRuntime) {
		rt.ready, rt.status, rt.screen = false, "working", "────────\n❯\n────────\n"
	})
	typed, err := cli.SendToSupervisor(context.Background(), c, sup, "working", "wake")
	if typed || luvus.Code(err) != "agent_not_ready" || len(rt.keysSent()) != 0 {
		t.Fatalf("typed = %v, err = %v, keys = %q", typed, err, rt.keysSent())
	}
}

func TestQueuedMessagesAreNotTypedOnceTheFirstStartedATurn(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status, rt.ready = "blocked", false })
	h.ok("supervisor", "send", "--text", "first")
	h.ok("supervisor", "send", "--text", "second")
	rt.set(func(rt *fakeRuntime) {
		rt.status, rt.ready, rt.screen = "idle", true, "────────\n❯\n────────\n"
		rt.onPrompt = func() { rt.status, rt.ready = "working", false }
	})
	has(t, "force", h.ok("supervisor", "force"), "supervisor is not at a prompt")
	if !slices.Equal(rt.prompts(), []string{"first"}) || len(rt.keysSent()) != 0 {
		t.Fatalf("prompts = %q, keys = %q", rt.prompts(), rt.keysSent())
	}
	has(t, "show", h.ok("supervisor", "show"), "pending: 1")
}
