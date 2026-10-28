package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/state"
)

var sessionUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (fx *attemptFixture) startCopilot(extra ...string) string {
	fx.h.t.Helper()
	return fx.h.ok(append([]string{"attempt", "start", "--harness", "copilot", "--model", "auto", "--prompt-file", fx.brief}, append(extra, "t1")...)...)
}

func copilotTrustScreen(path string, width, cursor int) string {
	var b strings.Builder
	rule := func(left, right string) { fmt.Fprintf(&b, "%s%s%s\n", left, strings.Repeat("─", width-2), right) }
	row := func(s string) { fmt.Fprintf(&b, "│ %-*s │\n", width-4, s) }
	choice := func(n int, text string) {
		mark := "  "
		if n == cursor {
			mark = "❯ "
		}
		row(fmt.Sprintf("%s%d. %s", mark, n, text))
	}
	room := width - 8
	rule("╭", "╮")
	row("Confirm folder trust")
	row(strings.Repeat("─", width-4))
	row("╭" + strings.Repeat("─", width-6) + "╮")
	for rest := path; rest != ""; {
		n := min(room, len(rest))
		row(fmt.Sprintf("│ %-*s │", room, rest[:n]))
		rest = rest[n:]
	}
	row("╰" + strings.Repeat("─", width-6) + "╯")
	row("")
	row("Copilot can read files in this folder and, with your permission, edit them")
	row("or run code and shell commands. It will remember your permissions for the")
	row("rest of this session.")
	row("")
	row("Do you trust the files in this folder?")
	row("")
	choice(1, "Yes")
	choice(2, "Yes, and remember this folder for future sessions")
	choice(3, "No (Esc)")
	row("")
	row("↑/↓ to navigate · enter to select · esc to cancel")
	rule("╰", "╯")
	return b.String()
}

func TestCopilotWorkersStartWithTheirBrief(t *testing.T) {
	for effort, flags := range map[string][]string{"": nil, "high": {"--reasoning-effort", "high"}} {
		fx := agyFixture(t)
		var out string
		if effort == "" {
			out = fx.startCopilot()
		} else {
			out = fx.startCopilot("--effort", effort)
		}
		call := fx.rt.lastCreate().Command
		want := slices.Concat([]string{exe(fx.h.vars["PATH"], "copilot"), "--no-auto-update", "--model", "auto"}, flags, []string{"--allow-all-tools", "--allow-all-paths", "--session-id"})
		n := len(want)
		if len(call) != n+3 || !slices.Equal(call[:n], want) || !sessionUUID.MatchString(call[n]) || call[n+1] != "-i" || !strings.HasPrefix(call[n+2], "Fix the login bug, commit, then stop.") {
			t.Fatalf("effort %q: argv = %q", effort, call)
		}
		if !strings.Contains(out, "trust: not asked") || len(fx.rt.keysSent()) != 0 {
			t.Fatalf("effort %q: start = %q, keys = %q", effort, out, fx.rt.keysSent())
		}
		if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, call[n]) {
			t.Fatalf("show = %q, want the stored launch line to carry the session id %s", show, call[n])
		}
	}
}

func TestCopilotTrustScreenIsAcceptedInItsOwnWorktreeAtEveryWidth(t *testing.T) {
	for _, width := range []int{120, 80, 56} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			fx := agyFixture(t)
			wt := fx.h.worktree("t1-a1")
			fx.rt.set(func(rt *fakeRuntime) {
				rt.screen, rt.revision, rt.afterScreen = copilotTrustScreen(wt, width, 1), 4, "❯\n"
			})
			out := fx.startCopilot()
			if !strings.Contains(out, "trust: accepted") || !slices.Equal(fx.rt.keysSent(), []string{"enter"}) {
				t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
			}
			if strings.Contains(out, "trusted:") {
				t.Fatalf("start = %q: copilot trusts for the session only", out)
			}
		})
	}
}

func TestCopilotTrustScreenForAnotherPathIsLeftAlone(t *testing.T) {
	for name, path := range map[string]func(wt string) string{
		"elsewhere": func(string) string { return "/tmp/elsewhere" },
		"sibling":   func(wt string) string { return wt + "2" },
		"parent":    filepath.Dir,
		"spaced":    func(wt string) string { return wt[:len(wt)/2] + " " + wt[len(wt)/2:] },
		"none":      func(string) string { return "" },
	} {
		for _, width := range []int{80, 56} {
			t.Run(fmt.Sprintf("%s %d", name, width), func(t *testing.T) {
				fx := agyFixture(t)
				screen := copilotTrustScreen(path(fx.h.worktree("t1-a1")), width, 1)
				fx.rt.set(func(rt *fakeRuntime) { rt.screen = screen })
				out := fx.startCopilot()
				if !strings.Contains(out, "trust: not pressed") || !strings.Contains(out, "hand attempt read a1") || len(fx.rt.keysSent()) != 0 {
					t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
				}
				if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: copilot asks to trust a folder that is not its worktree; check the screen"`) {
					t.Fatalf("wait = %q", woke)
				}
			})
		}
	}
}

func TestCopilotTrustScreenNeverTakesTheRememberRow(t *testing.T) {
	for _, cursor := range []int{2, 3} {
		fx := agyFixture(t)
		screen := copilotTrustScreen(fx.h.worktree("t1-a1"), 80, cursor)
		fx.rt.set(func(rt *fakeRuntime) { rt.screen, rt.afterScreen = screen, screen })
		out := fx.startCopilot()
		if !strings.Contains(out, "trust: not pressed") || !slices.Equal(fx.rt.keysSent(), []string{"up"}) {
			t.Fatalf("cursor %d: start = %q, keys = %q", cursor, out, fx.rt.keysSent())
		}
	}
}

func TestCopilotMissingNamesItsInstallAndLogin(t *testing.T) {
	fx := agyFixture(t)
	if err := os.Remove(exe(fx.h.vars["PATH"], "copilot")); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := fx.h.run("attempt", "start", "--harness", "copilot", "--model", "auto", "--prompt-file", fx.brief, "t1")
	if code == 0 || !strings.Contains(errOut, "copilot not found on PATH; install copilot (`npm install -g @github/copilot`, then `copilot login`), or add its folder to PATH") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestCopilotAttemptsAreStoredAsWorkers(t *testing.T) {
	fx := agyFixture(t)
	fx.startCopilot()
	a, err := openStore(t, fx.h).Attempt(t.Context(), 1)
	if err != nil || a.Harness != "copilot" || a.Model != "auto" || a.Effort != "" || !slices.Contains(state.Harnesses, "copilot") {
		t.Fatalf("attempt = %+v, %v", a, err)
	}
}

func wrapAt(text string, width int) string {
	var b strings.Builder
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) > width:
			b.WriteString(line + "\n")
			line = "   " + word
		default:
			line += " " + word
		}
	}
	return b.String() + line + "\n"
}

func copilotLaunchScreen(problem string, width int, echoed bool, later string) string {
	screen := " ╭─╮╭─╮\n ╰─╯╰─╯  Copilot v1.0.105 uses AI.\n ● Tip: /init\n   └ Initialize Copilot instructions for this repository\n"
	if problem != "" {
		screen += wrapAt(problem, width)
	}
	if echoed {
		screen += "❯ Reply with the single word ok\n"
	}
	return screen + later + strings.Repeat("─", width) + "\n❯\n"
}

func startCopilotLaunch(t *testing.T, launch string) (*attemptFixture, string) {
	t.Helper()
	fx := agyFixture(t)
	trust := copilotTrustScreen(fx.h.worktree("t1-a1"), 80, 1)
	fx.rt.set(func(rt *fakeRuntime) { rt.screen, rt.revision, rt.afterScreen = trust, 4, launch })
	return fx, fx.startCopilot()
}

func TestCopilotLaunchThatFellBackToAutoIsNotedBlockedAtEveryWidth(t *testing.T) {
	problem := `✗ Model "gpt-nosuch" from --model flag is not available. Using "auto" instead.`
	for _, width := range []int{120, 80, 56} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			fx, out := startCopilotLaunch(t, copilotLaunchScreen(problem, width, true, ""))
			if !strings.Contains(out, "trust: accepted") || !strings.Contains(out, "hand attempt read a1") {
				t.Fatalf("start = %q", out)
			}
			if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: copilot does not have model \"gpt-nosuch\" and runs on auto instead; check the screen"`) {
				t.Fatalf("wait = %q", woke)
			}
			if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "running") {
				t.Fatalf("show = %q: the attempt must keep running", show)
			}
		})
	}
}

func TestCopilotLaunchThatIsSignedOutIsNotedBlockedAtEveryWidth(t *testing.T) {
	for _, width := range []int{120, 80, 56} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			fx, out := startCopilotLaunch(t, copilotLaunchScreen("Please use /login to sign in to use Copilot", width, false, ""))
			if !strings.Contains(out, "hand attempt read a1") {
				t.Fatalf("start = %q", out)
			}
			if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, "a1: copilot is not signed in and ignored the brief; run `copilot login`, then start the attempt again") {
				t.Fatalf("wait = %q", woke)
			}
		})
	}
}

func TestCopilotLaunchWordsPrintedLaterByTheWorkerAreNotAProblem(t *testing.T) {
	later := wrapAt(`Model "gpt-nosuch" from --model flag is not available. Using "auto" instead.`, 80) + "Please use /login to sign in to use Copilot\n"
	fx, out := startCopilotLaunch(t, copilotLaunchScreen("", 80, true, later))
	if strings.Contains(out, "hand attempt read a1") {
		t.Fatalf("start = %q", out)
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); strings.Contains(woke, "attempt.blocked") {
		t.Fatalf("wait = %q", woke)
	}
}

func TestCopilotLaunchWordsAfterTheBannerScrolledAwayAreNotAProblem(t *testing.T) {
	screen := wrapAt(`Model "gpt-nosuch" from --model flag is not available. Using "auto" instead.`, 80) + "❯\n"
	fx, out := startCopilotLaunch(t, screen)
	if strings.Contains(out, "hand attempt read a1") {
		t.Fatalf("start = %q", out)
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); strings.Contains(woke, "attempt.blocked") {
		t.Fatalf("wait = %q", woke)
	}
}

func TestCopilotLaunchWithoutAnEchoEndsAtTheScreenWait(t *testing.T) {
	fx, out := startCopilotLaunch(t, copilotLaunchScreen("", 80, false, ""))
	if strings.Contains(out, "hand attempt read a1") {
		t.Fatalf("start = %q", out)
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); strings.Contains(woke, "attempt.blocked") {
		t.Fatalf("wait = %q", woke)
	}
}

func TestCopilotLaunchLineThatAppearsOnALaterReadIsNoted(t *testing.T) {
	fx := agyFixture(t)
	*cli.ScreenWait = 5 * time.Second
	t.Cleanup(func() { *cli.ScreenWait = 300 * time.Millisecond })
	trust := copilotTrustScreen(fx.h.worktree("t1-a1"), 80, 1)
	problem := `✗ Model "gpt-nosuch" from --model flag is not available. Using "auto" instead.`
	fx.rt.set(func(rt *fakeRuntime) {
		rt.screen, rt.revision, rt.afterScreen = trust, 4, copilotLaunchScreen("", 80, false, "")
	})
	go func() {
		for len(fx.rt.keysSent()) == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
		fx.rt.set(func(rt *fakeRuntime) { rt.screen = copilotLaunchScreen(problem, 80, true, "") })
	}()
	fx.startCopilot()
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `runs on auto instead`) {
		t.Fatalf("wait = %q", woke)
	}
}

func TestCopilotModelLineWithoutTheFallbackClauseIsNotAProblem(t *testing.T) {
	fx, out := startCopilotLaunch(t, copilotLaunchScreen(`✗ Model "gpt-nosuch" from --model flag is not available.`, 80, true, ""))
	if strings.Contains(out, "hand attempt read a1") {
		t.Fatalf("start = %q", out)
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); strings.Contains(woke, "attempt.blocked") {
		t.Fatalf("wait = %q", woke)
	}
}
