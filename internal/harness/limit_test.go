package harness

import (
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
