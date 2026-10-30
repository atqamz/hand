package cli_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
)

const (
	opencodeUnavailableScreen = `   PONG                            New session                   +

                                                                                                    New session - 2026-10-30T02:08:34.
                                                                                                    403Z






















                                                                                                      ⬖ Getting started                ✕

                                                                                                        OpenCode includes free models so
                                                                                                        you can start immediately.

                                                                                                        Connect from 75+ providers to
                                                                                                        use other models, including
  ┃                                                                                                     Claude, GPT, Gemini etc
  ┃  You are supervisor s1 of the Hand fleet
  ┃                                                                                                     Connect provider        /connect
  ┃  Build auto · x (unavailable) nope · zzz
  ╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
  /tmp/opencode/t279c/proj                                     shift+tab agents  ctrl+p commands    /tmp/opencode/t279c/proj`
	opencodeUnavailableToastScreen = `   PONG                            New session                   +
                                                                                  ┃                                                      ┃
                                                                                  ┃  Model unavailable                                x  ┃
                                                                                  ┃                                                      ┃
                                                                                  ┃  nope/x is not available in this session's location  ┃
                                                                                  ┃                                                      ┃
                                                                                                      ⬖ Getting started                ✕
                                                                                                        OpenCode includes free models so
                                                                                                        you can start immediately.
                                                                                                        Connect from 75+ providers to
                                                                                                        use other models, including
  ┃                                                                                                     Claude, GPT, Gemini etc
  ┃  You are supervisor s1 of the Hand fleet
  ┃                                                                                                     Connect provider        /connect
  ┃  Build auto · x (unavailable) nope · zzz
  ╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
  /tmp/opencode/t279c/proj                                     shift+tab agents  ctrl+p commands    /tmp/opencode/t279c/proj`
	opencodeUnavailableDisplayNameScreen = `   New session                     New session                   +

                                                                                                    New session - 2026-10-30T02:42:46.
                                                                                                    094Z






















                                                                                                      ⬖ Getting started                ✕

                                                                                                        OpenCode includes free models so
                                                                                                        you can start immediately.

                                                                                                        Connect from 75+ providers to
                                                                                                        use other models, including
  ┃                                                                                                     Claude, GPT, Gemini etc
  ┃  Fix the login bug
  ┃                                                                                                     Connect provider        /connect
  ┃  Build auto · zz (unavailable) Local Fake
  ╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
  /tmp/opencode/t279d/proj                                     shift+tab agents  ctrl+p commands    /tmp/opencode/t279d/proj`
	opencodeHealthyBriefScreen = `   New session                     New session                   +

                                                                                                    New session - 2026-10-30T02:42:47.
                                                                                                    237Z






















                                                                                                      ⬖ Getting started                ✕

                                                                                                        OpenCode includes free models so
                                                                                                        you can start immediately.

                                                                                                        Connect from 75+ providers to
  ┃                                                                                                     use other models, including
  ┃  Say that local/zz is not available in this session's location and print a line like:               Claude, GPT, Gemini etc
  ┃  Build auto · x (unavailable) nope
  ┃                                                                                                     Connect provider        /connect
  ┃  Build auto · Model Two Local Fake · deep
  ╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
  /tmp/opencode/t279d/proj                                     shift+tab agents  ctrl+p commands    /tmp/opencode/t279d/proj`
	opencodeHealthyScreen = ` • PONG                            New session                   +

     Switched agent to Build                                                                        PONG

  ┃                                                                                                 Context
  ┃  You are supervisor s1 of the Hand fleet                                                        2 tokens
  ┃                                                                                                 0% used

     PONG

     Build · Model Two · 117ms · 20.8 tok/s















                                                                                                      ⬖ Getting started                ✕

                                                                                                        OpenCode includes free models so
                                                                                                        you can start immediately.

                                                                                                        Connect from 75+ providers to
                                                                                                        use other models, including
  ┃                                                                                                     Claude, GPT, Gemini etc
  ┃
  ┃                                                                                                     Connect provider        /connect
  ┃  Build auto · Model Two Local Fake · deep
  ╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
  /tmp/opencode/t279c/proj                                               2 (0%)  ctrl+p commands    /tmp/opencode/t279c/proj`
)

func startOpencodeOnScreen(t *testing.T, screen, status string, args ...string) (*attemptFixture, string) {
	t.Helper()
	prefill, screenWait, confirm := *cli.PrefillWait, *cli.ScreenWait, *cli.SubmitConfirm
	*cli.PrefillWait, *cli.ScreenWait, *cli.SubmitConfirm = 500*time.Millisecond, 300*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { *cli.PrefillWait, *cli.ScreenWait, *cli.SubmitConfirm = prefill, screenWait, confirm })
	fx := newAttemptFixture(t)
	installOpencode(t, fx.h, fx.rt, nil)
	fx.rt.set(func(rt *fakeRuntime) { rt.screen, rt.status, rt.afterKeys = screen, status, "" })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, errOut, code := fx.h.runCtx(ctx, append([]string{"attempt", "start", "--harness", "opencode", "--prompt-file", fx.brief}, append(args, "t1")...)...)
	if code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out, errOut)
	}
	return fx, out
}

func TestOpencodeUnavailableModelIsNamedNotMetWithAnEnterHint(t *testing.T) {
	for name, c := range map[string]struct {
		screen string
		args   []string
		want   string
	}{
		"footer, model from the screen":          {opencodeUnavailableScreen, nil, "x"},
		"toast alone, model from the screen":     {strings.Replace(opencodeUnavailableToastScreen, "x (unavailable) nope", "x nope", 1), nil, "nope/x"},
		"footer, the launched model":             {opencodeUnavailableScreen, []string{"--model", "nope/x"}, "nope/x"},
		"footer shows the provider display name": {opencodeUnavailableDisplayNameScreen, []string{"--model", "local/zz"}, "local/zz"},
	} {
		fx, out := startOpencodeOnScreen(t, c.screen, "working", c.args...)
		if !strings.Contains(out, "prompt: not submitted") || strings.Contains(out, "press Enter") || strings.Contains(out, "keys --revision") || !strings.Contains(out, "hand attempt read a1") {
			t.Fatalf("%s: start = %q", name, out)
		}
		woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms")
		if !strings.Contains(woke, `,attempt.blocked,t1,"a1: opencode model `+c.want+` is unavailable: connect its provider or pick another model"`) {
			t.Fatalf("%s: wait = %q", name, woke)
		}
	}
}

func TestOpencodeUnavailableModelAfterEnterWasSentIsNamedToo(t *testing.T) {
	screen := strings.Replace(opencodeUnavailableDisplayNameScreen, "Fix the login bug", "When you finish, report to Hand from inside this worktree:", 1)
	fx, out := startOpencodeOnScreen(t, screen, "idle", "--model", "local/zz")
	if !strings.Contains(out, "prompt: sent unconfirmed") || strings.Contains(out, "only if the briefing") || len(fx.rt.keysSent()) == 0 || slices.Contains(fx.rt.keysSent(), "x") {
		t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: opencode model local/zz is unavailable: connect its provider or pick another model"`) {
		t.Fatalf("wait = %q", woke)
	}
}

func TestOpencodeHealthyScreenKeepsTheEnterHintWhateverTheBriefQuotes(t *testing.T) {
	for name, screen := range map[string]string{"after the reply": opencodeHealthyScreen, "brief quoting both phrases": opencodeHealthyBriefScreen} {
		fx, out := startOpencodeOnScreen(t, screen, "working", "--model", "local/m2")
		if !strings.Contains(out, "prompt: not submitted") || !strings.Contains(out, "hand attempt keys --revision N a1 enter") {
			t.Fatalf("%s: start = %q", name, out)
		}
		if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: briefing not submitted; press Enter"`) || strings.Contains(woke, "unavailable") {
			t.Fatalf("%s: wait = %q", name, woke)
		}
	}
}
