package cli_test

import (
	"context"
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

func startOpencodeOnScreen(t *testing.T, screen string) (*attemptFixture, string) {
	t.Helper()
	prefill, screenWait := *cli.PrefillWait, *cli.ScreenWait
	*cli.PrefillWait, *cli.ScreenWait = 500*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { *cli.PrefillWait, *cli.ScreenWait = prefill, screenWait })
	fx := newAttemptFixture(t)
	fx.rt.set(func(rt *fakeRuntime) { rt.screen, rt.status = screen, "working" })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, errOut, code := fx.h.runCtx(ctx, "attempt", "start", "--harness", "opencode", "--prompt-file", fx.brief, "t1")
	if code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out, errOut)
	}
	return fx, out
}

func TestOpencodeUnavailableModelIsNamedNotMetWithAnEnterHint(t *testing.T) {
	for name, screen := range map[string]string{"footer": opencodeUnavailableScreen, "toast": opencodeUnavailableToastScreen} {
		fx, out := startOpencodeOnScreen(t, screen)
		if !strings.Contains(out, "prompt: not submitted") || strings.Contains(out, "press Enter") || strings.Contains(out, "keys --revision") || !strings.Contains(out, "hand attempt read a1") {
			t.Fatalf("%s: start = %q", name, out)
		}
		woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms")
		if !strings.Contains(woke, `,attempt.blocked,t1,"a1: opencode model nope/x is unavailable: connect its provider or pick another model"`) {
			t.Fatalf("%s: wait = %q", name, woke)
		}
	}
}

func TestOpencodeHealthyScreenKeepsTheEnterHint(t *testing.T) {
	fx, out := startOpencodeOnScreen(t, opencodeHealthyScreen)
	if !strings.Contains(out, "prompt: not submitted") || !strings.Contains(out, "hand attempt keys --revision N a1 enter") {
		t.Fatalf("start = %q", out)
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: briefing not submitted; press Enter"`) || strings.Contains(woke, "unavailable") {
		t.Fatalf("wait = %q", woke)
	}
}
