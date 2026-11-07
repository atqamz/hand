package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captured(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWaitingTakesRealPrompts(t *testing.T) {
	for name, tc := range map[string]struct {
		harness string
		screen  string
	}{
		"claude permission": {harness: "claude", screen: screen(t, "commit")},
		"claude dialog":     {harness: "claude", screen: screen(t, "askq")},
		"claude trust":      {harness: "claude", screen: screen(t, "trust")},
		"codex question":    {harness: "codex", screen: captured(t, "codex-question.txt")},
		"codex trust":       {harness: "codex", screen: "Folder access\n\nTrust this folder?\n\n› 1. Trust and continue\n  2. Quit\n\n  enter continue · esc quit"},
		"agy trust":         {harness: "agy", screen: "Accessing workspace:\n/tmp/w\nDo you trust the contents of this project?\n> Yes, I trust this folder\n  No, exit\n  ↑/↓ Navigate · enter Confirm"},
		"copilot trust":     {harness: "copilot", screen: "╭────────────────────╮\n│ Confirm folder trust │\n│ Do you trust the files in this folder? │\n│ ❯ 1. Yes            │\n│   3. No (Esc)       │\n╰────────────────────╯"},
		"unseen dialog":     {harness: "claude", screen: "────────────────────────────────────────────────────────────────────────────────\n  A dialog Hand has never seen\n\n  ❯ 1. Go ahead\n    2. Stop\n\n  Enter to confirm · Esc to cancel"},
		"plain prompt":      {harness: "opencode", screen: "Do you want to proceed?\n❯ 1. Yes"},
	} {
		if !Waiting(tc.harness, tc.screen) {
			t.Errorf("%s: no prompt", name)
		}
	}
}

func TestWaitingIgnoresAnOmpStatusBox(t *testing.T) {
	for name, text := range map[string]string{
		"working": captured(t, "omp-18.18.0-working.txt"),
		"idle":    captured(t, "omp-18.18.0-idle.txt"),
	} {
		if Waiting("omp", text) {
			t.Errorf("%s: a status box is a prompt", name)
		}
	}
}

func TestWaitingNeedsAnAnswerNotJustAnEdge(t *testing.T) {
	for name, text := range map[string]string{
		"box corner":          "╰─",
		"box top":             "╭────────────────────────────────╮\n│                                │\n╰────────────────────────────────╯",
		"rule":                strings.Repeat("─", 80),
		"empty":               "",
		"status bar":          "╰─                                                  ⇧⇥ to change thinking effort",
		"status fragment":     "⏸ manual mode on · ? for shortcuts",
		"claude idle":         screen(t, "idle"),
		"a rule and a corner": strings.Repeat("─", 80) + "\n╰─",
	} {
		if Waiting("claude", text) || Waiting("omp", text) {
			t.Errorf("%s: not a prompt", name)
		}
	}
}
