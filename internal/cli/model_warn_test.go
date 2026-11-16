package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/state"
)

func TestSupervisorStartWarnsAboutAnUnverifiedModelAndLaunchesIt(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		warn string
		flag string
	}{
		{"claude gateway name", []string{"--harness", "claude", "--model", "acme-opus", "--effort", "high"}, `warn: "claude model acme-opus is not an alias (opus, sonnet, haiku, fable), a claude-* name or in Claude Code's model list; starting anyway"`, "acme-opus"},
		{"claude bedrock id", []string{"--harness", "claude", "--model", "us.anthropic.claude-opus-5-5-v1:0", "--effort", "high"}, "warn:", "us.anthropic.claude-opus-5-5-v1:0"},
		{"codex without a cache", []string{"--harness", "codex", "--model", "acme-coder-1", "--effort", "high"}, `warn: "codex model acme-coder-1 could not be checked`, "acme-coder-1"},
		{"agy unlisted", []string{"--harness", "agy", "--model", "gemini-9"}, "warn: agy model gemini-9 is not in `agy models`", "gemini-9"},
		{"agy without a list", []string{"--harness", "agy", "--model", "gemini-9"}, "warn: \"agy model gemini-9 could not be checked: no model list yet; run `hand models --refresh agy`", "gemini-9"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, rt := newSupervisorFixture(t)
			if c.name == "agy unlisted" {
				h.ok("models", "--refresh", "agy")
			}
			out := h.ok(append([]string{"supervisor", "start"}, c.args...)...)
			has(t, "start", out, c.warn, "status: running")
			if !slices.Contains(rt.lastCreate().Command, c.flag) {
				t.Fatalf("argv = %q", rt.lastCreate().Command)
			}
		})
	}
}

func TestSupervisorStartPrintsNoWarnForAVerifiedModel(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	if out := startClaudeSupervisor(h); strings.Contains(out, "warn") {
		t.Fatalf("start = %q", out)
	}
}

func TestSupervisorStartLeavesTheEffortToTheHarness(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	out := h.ok("supervisor", "start", "--harness", "claude", "--model", "sonnet")
	has(t, "start", out, "status: running")
	if strings.Contains(out, "warn") {
		t.Fatalf("start = %q", out)
	}
	argv := rt.lastCreate().Command
	if !slices.Contains(argv, "sonnet") || slices.ContainsFunc(argv, func(a string) bool { return strings.Contains(a, "--effort") }) {
		t.Fatalf("argv = %q", argv)
	}
}

func TestSupervisorSwitchRefusesAnotherSupervisorsRef(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	if _, errOut, code := h.run("supervisor", "switch", "--expect", "s2", "--model", "opus", "--effort", "high"); code != 3 || !strings.Contains(errOut, "s2 is on screen, but s1 is running now; refresh and try again") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if out := h.ok("supervisor", "switch", "--expect", "s1", "--model", "opus", "--effort", "high"); !strings.Contains(out, "switch: pending") {
		t.Fatalf("switch = %q", out)
	}
}

func TestAStaleRefLeavesThePendingSwitchAlone(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	has(t, "pending", h.ok("supervisor", "switch", "--expect", "s1", "--model", "opus", "--effort", "high"), "switch: pending", "model: opus")
	if _, errOut, code := h.run("supervisor", "switch", "--expect", "s2", "--model", "sonnet", "--effort", "low"); code != 3 || !strings.Contains(errOut, "s2 is on screen, but s1 is running now; refresh and try again") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	has(t, "show", h.ok("supervisor", "show"), "switch: opus high")
}

func TestSupervisorSwitchChecksItsRefUnderTheDeliveryLock(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	ctx := context.Background()
	st, err := state.Open(filepath.Join(h.home, "hand.db"), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	lock, err := os.OpenFile(filepath.Join(h.home, "locks", "supervisor.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if _, err := flock.Lock(lock, true); err != nil {
		t.Fatal(err)
	}
	type result struct {
		out, errOut string
		code        int
	}
	done := make(chan result, 1)
	go func() {
		out, errOut, code := h.run("supervisor", "switch", "--expect", "s1", "--model", "opus", "--effort", "high")
		done <- result{out, errOut, code}
	}()
	var first state.Supervisor
	for deadline := time.Now().Add(5 * time.Second); ; {
		latest, found, err := st.LatestSupervisor(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if found && latest.Switching() {
			first = latest
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the switch never reached its pending write")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := st.EndSupervisor(ctx, first.ID, state.AttemptStopped, "stopped between the check and the delivery"); err != nil {
		t.Fatal(err)
	}
	next, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}, Session: "ses_hand0123456789abcdef01234567"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupervisorRunning(ctx, next.ID, state.Terminal{ServerGeneration: "gen-old", TerminalID: "t9", PaneID: "9", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()
	got := <-done
	want := state.SupervisorRef(first.ID) + " is on screen, but " + state.SupervisorRef(next.ID) + " is running now; refresh and try again"
	if got.code != 3 || !strings.Contains(got.errOut, want) {
		t.Fatalf("switch = %q %q, code %d; want %q", got.out, got.errOut, got.code, want)
	}
}

func TestSupervisorStartRefusesWhatIsMalformed(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--harness", "claude", "--model", "my model", "--effort", "high"}, `claude model "my model"`},
		{[]string{"--harness", "claude", "--model", "-x", "--effort", "high"}, `claude model "-x"`},
		{[]string{"--harness", "claude", "--model", "opus", "--effort", "Very High"}, "claude effort"},
		{[]string{"--harness", "claude", "--effort", "high"}, "claude model"},
		{[]string{"--harness", "codex", "--model", "gpt-6-luna", "--effort", "--max"}, "codex effort"},
		{[]string{"--harness", "agy", "--model", "gemini-3.8-flash-low", "--effort", "high"}, "agy names the effort"},
		{[]string{"--harness", "opencode", "--model", "acme/acme-coder-1#deep"}, "provider/id"},
		{[]string{"--harness", "opencode", "--model", "acme-coder-1"}, "provider/id"},
		{[]string{"--harness", "opencode", "--effort", "deep"}, "variant of a model"},
		{[]string{"--harness", "gemini", "--model", "x", "--effort", "low"}, `harness "gemini"`},
	} {
		h, rt := newSupervisorFixture(t)
		_, errOut, code := h.run(append([]string{"supervisor", "start"}, c.args...)...)
		if code != 2 || !strings.Contains(errOut, c.want) {
			t.Fatalf("%q: code=%d stderr=%q", c.args, code, errOut)
		}
		rt.mu.Lock()
		made := len(rt.creates)
		rt.mu.Unlock()
		if made != 0 {
			t.Fatalf("%q launched %d terminals", c.args, made)
		}
	}
}

func TestSupervisorLaunchFailureNamesTheModel(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	rt.srv.Handle("terminal.backend.create", func(json.RawMessage) (any, error) {
		return nil, fakeuhp.Fail{Code: "create_failed", Message: "terminal failed to start"}
	})
	_, errOut, code := h.run("supervisor", "start", "--harness", "claude", "--model", "acme-opus", "--effort", "high")
	if code == 0 || !strings.Contains(errOut, "terminal failed to start") || !strings.Contains(errOut, "launching claude acme-opus") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	has(t, "show", h.ok("supervisor", "show"), "harness: claude acme-opus high", "status: failed", "launching claude acme-opus")
}

func TestSupervisorResumeAndSwitchRepeatTheWarning(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	h.ok("supervisor", "start", "--harness", "claude", "--model", "acme-opus", "--effort", "high")
	h.ok("supervisor", "stop")
	has(t, "resume", h.ok("supervisor", "resume"), "warn:", "acme-opus", "supervisor: s2")
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	has(t, "switch", h.ok("supervisor", "switch", "--model", "my-gateway/claude-opus-5-5"), "switch: applied", `warn: "claude model my-gateway/claude-opus-5-5`)
	if _, errOut, code := h.run("supervisor", "switch", "--model", "a b"); code != 2 || !strings.Contains(errOut, `claude model "a b"`) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestAttemptStartWarnsAboutAnUnverifiedModel(t *testing.T) {
	fx := newAttemptFixture(t)
	out := fx.h.ok("attempt", "start", "--harness", "claude", "--model", "acme-opus", "--effort", "high", "--prompt-file", fx.brief, "t1")
	has(t, "start", out, "attempt: a1", `warn: "claude model acme-opus`)
	if !slices.Contains(fx.rt.lastCreate().Command, "acme-opus") {
		t.Fatalf("argv = %q", fx.rt.lastCreate().Command)
	}
}

func TestAttemptStartWarnsWithoutACodexCache(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.h.vars["HOME"] = t.TempDir()
	out := fx.h.ok("attempt", "start", "--harness", "codex", "--model", "acme-coder-1", "--effort", "high", "--prompt-file", fx.brief, "t1")
	has(t, "start", out, `warn: "codex model acme-coder-1 could not be checked`)
}
