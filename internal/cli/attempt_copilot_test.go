package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

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
