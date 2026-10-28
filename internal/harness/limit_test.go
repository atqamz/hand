package harness

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestLimitMatchesTheLinesEachHarnessShows(t *testing.T) {
	for _, tc := range []struct{ name, screen, want string }{
		{"claude", "● Fixing the redirect\n  ⎿  You've hit your session limit · resets 6:10am (Asia/Jakarta)\n\n───────────\n> \n───────────", "You've hit your session limit · resets 6:10am (Asia/Jakarta)"},
		{"claude", "  ⎿  You've hit your weekly limit · resets Sep 27, 5pm (Asia/Jakarta)\n> ", "You've hit your weekly limit · resets Sep 27, 5pm (Asia/Jakarta)"},
		{"codex", "■ You've hit your usage limit. Upgrade to Pro (...) or try again at 6:58 PM.\n\n› ", "You've hit your usage limit. Upgrade to Pro (...) or try again at 6:58 PM."},
		{"codex", "■ You've hit your usage limit. Upgrade to Pro (...) or try again at Sep 22nd, 2026 7:16 PM.", "You've hit your usage limit. Upgrade to Pro (...) or try again at Sep 22nd, 2026 7:16 PM."},
	} {
		got, ok := Limit(tc.name, tc.screen)
		if !ok || got != tc.want {
			t.Errorf("Limit(%s, %q) = %q, %v; want %q", tc.name, tc.screen, got, ok, tc.want)
		}
	}
}

func TestLimitTakesTheNewestLine(t *testing.T) {
	screen := "  ⎿  You've hit your session limit · resets 6:10am (Asia/Jakarta)\n● Continuing\n  ⎿  You've hit your weekly limit · resets Sep 27, 5pm (Asia/Jakarta)\n> "
	if got, ok := Limit("claude", screen); !ok || got != "You've hit your weekly limit · resets Sep 27, 5pm (Asia/Jakarta)" {
		t.Fatalf("Limit = %q, %v", got, ok)
	}
}

func TestLimitJoinsAgyWrappedQuotaLineAndAddsTheResetTime(t *testing.T) {
	before := time.Now()
	for _, screen := range []string{
		"⚠ Individual quota reached. Please upgrade your subscription to increase your\n  limits. Resets in 118h19m26s.\n\n> ",
		"⚠ Individual quota reached. Please upgrade your subscription to\n  increase your limits. Resets\n  in 118h19m26s.\n\n> ",
	} {
		got, ok := Limit("agy", screen)
		m := regexp.MustCompile(`^Individual quota reached\. Please upgrade your subscription to increase your limits\. Resets in 118h19m26s\. \(~(\S+)\)$`).FindStringSubmatch(got)
		if !ok || m == nil {
			t.Fatalf("Limit(agy, %q) = %q, %v", screen, got, ok)
		}
		at, err := time.Parse(time.RFC3339, m[1])
		if err != nil || !strings.HasSuffix(m[1], "Z") {
			t.Fatalf("reset time %q: %v", m[1], err)
		}
		if want := before.Add(118*time.Hour + 19*time.Minute + 26*time.Second); at.Before(want.Add(-time.Second)) || at.After(want.Add(time.Minute)) {
			t.Fatalf("reset time %s, want about %s", at, want.UTC())
		}
	}
}

func copilotScreen(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLimitMatchesACopilotSessionLimitOnItsCapturedAndWrappedScreens(t *testing.T) {
	const want = "Session limit reached. Increase or unset the session limit to continue."
	for name, screen := range map[string]string{
		"captured":  copilotScreen(t, "copilot-limit-idle.txt"),
		"dialog":    copilotScreen(t, "copilot-limit-dialog.txt"),
		"width 56":  "● Done\n● Session limit reached. Increase or unset the\n   session limit to continue.\n❯\n",
		"width 44":  "● Session limit reached. Increase or\n   unset the session limit to\n   continue.\n❯\n",
		"no bullet": "Session limit reached. Increase or unset the session limit to continue.\n❯\n",
	} {
		if got, ok := Limit("copilot", screen); !ok || got != want {
			t.Errorf("%s: Limit(copilot) = %q, %v; want %q", name, got, ok, want)
		}
	}
}

func TestLimitIgnoresCopilotWarningsTheBareDialogAndQuotedLines(t *testing.T) {
	dialog := copilotScreen(t, "copilot-limit-dialog.txt")
	for name, screen := range map[string]string{
		"50%":         "● Session limits: 16.31/30 AI credits used.\n❯\n",
		"final call":  "● Session limits: 27.23/30 AI credits used. If this session continues, the next model call is final; tools will be disabled.\n❯\n",
		"bare dialog": dialog[strings.Index(dialog, "╭"):],
		"quoted":      "● The banner reads \"Session limit reached. Increase or unset the session limit to continue.\"\n❯\n",
		"mid line":    "● Hit it: Session limit reached. Increase or unset the session limit to continue.\n❯\n",
		"prompt echo": "❯ Session limit reached. Increase or unset the session limit to continue.\n❯\n",
		"quote":       "\"Session limit reached. Increase or unset the session limit to continue.\"\n❯\n",
		"boxed":       "│ Session limit reached. Increase or unset the session limit to continue. │\n❯\n",
		"claude":      "  ⎿  You've hit your session limit · resets 6:10am (Asia/Jakarta)\n❯\n",
	} {
		if got, ok := Limit("copilot", screen); ok {
			t.Errorf("%s: Limit(copilot) = %q, want no match", name, got)
		}
	}
	line := "● Session limit reached. Increase or unset the session limit to continue.\n❯\n"
	for _, name := range []string{"claude", "codex", "agy", "opencode"} {
		if got, ok := Limit(name, line); ok {
			t.Errorf("Limit(%s) = %q, want no match", name, got)
		}
	}
}

func TestLimitIgnoresWarningsOtherHarnessesAndOpencode(t *testing.T) {
	claude := "  ⎿  You've hit your session limit · resets 6:10am (Asia/Jakarta)"
	for _, tc := range []struct{ name, screen string }{
		{"codex", "⚠ Heads up, you have less than 25% of your 5h limit left. Run /status for a breakdown."},
		{"claude", "⚠ Heads up, you have less than 25% of your 5h limit left. Run /status for a breakdown."},
		{"claude", "● I raised the rate limit in config.go.\n> "},
		{"claude", `● The test expects "You've hit your session limit" on the screen.`},
		{"agy", `● The banner reads "Individual quota reached. Please upgrade your subscription to increase your limits. Resets in 1h2m3s."`},
		{"agy", "⚠ Individual quota reached. Please upgrade your subscription to increase your limits.\n> retry\n● Retried the build.\n● It printed: Resets in 5m."},
		{"agy", claude},
		{"codex", claude},
		{"opencode", claude},
		{"opencode", "■ You've hit your usage limit. Upgrade to Pro (...) or try again at 6:58 PM."},
		{"opencode", "⚠ Individual quota reached. Please upgrade your subscription to increase your limits. Resets in 118h19m26s."},
	} {
		if got, ok := Limit(tc.name, tc.screen); ok {
			t.Errorf("Limit(%s, %q) = %q, want no match", tc.name, tc.screen, got)
		}
	}
}

func TestAPIErrorMatchesOnlyALineStart(t *testing.T) {
	for screen, want := range map[string]string{
		"● API Error: Connection lost mid-response. The response above may be\n  incomplete.\n\n> ": "API Error: Connection lost mid-response. The response above may be",
		"● Retrying after an API Error: 500\n> ":                                                    "",
		"● Done\n> ": "",
		"● API Error: Connection lost\n● Continuing\n  ⎿  Edited a.go\n> ": "",
	} {
		got, ok := APIError("claude", screen)
		if got != want || ok != (want != "") {
			t.Errorf("APIError(%q) = %q, %v; want %q", screen, got, ok, want)
		}
	}
	if _, ok := APIError("codex", "● API Error: boom"); ok {
		t.Error("codex matched")
	}
}
