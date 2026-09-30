package board

import (
	"reflect"
	"testing"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

func TestGaugeFormat(t *testing.T) {
	for _, c := range []struct {
		in          transcript.Status
		text, level string
		pct         int
	}{
		{transcript.Status{}, "", "", 0},
		{transcript.Status{Context: 508311}, "508K", "", 0},
		{transcript.Status{Context: 1234567}, "1.23M", "", 0},
		{transcript.Status{Context: 72479, Window: 258400}, "72.5K / 258K", "", 28},
		{transcript.Status{Context: 210000, Window: 258400}, "210K / 258K", "warn", 81},
		{transcript.Status{Context: 240000, Window: 258400}, "240K / 258K · compact soon", "fail", 93},
		{transcript.Status{Context: 508139, Window: 1_000_000}, "508K / 1M", "", 51},
		{transcript.Status{Context: 950}, "950", "", 0},
	} {
		if text, level, pct := gauge(c.in); text != c.text || level != c.level || pct != c.pct {
			t.Errorf("gauge(%+v) = %q %q %d, want %q %q %d", c.in, text, level, pct, c.text, c.level, c.pct)
		}
	}
}

func TestChatItemsNameTheSupervisorOfTheirTime(t *testing.T) {
	sups := []state.Supervisor{{ID: 1, CreatedAt: "2026-09-29T10:00:00Z"}, {ID: 2, CreatedAt: "2026-09-29T11:00:00.5Z"}}
	for at, want := range map[string]string{
		"2026-09-29T09:59:00Z":     "s1",
		"2026-09-29T10:30:00Z":     "s1",
		"2026-09-29T11:00:00.5Z":   "s2",
		"2026-09-29T12:00:00.000Z": "s2",
		"not a time":               "s1",
	} {
		if got := refAt(sups, at); got != want {
			t.Errorf("refAt(%q) = %q, want %q", at, got, want)
		}
	}
	if got := refAt(nil, "2026-09-29T10:00:00Z"); got != "" {
		t.Errorf("refAt with no supervisors = %q", got)
	}
}

func TestBlockedPromptsReadTheScreen(t *testing.T) {
	screen := "────────────────────────\n Bash command\n  rm -f $r/$f\n Claude Code will automatically deny this request in 1:59\n Do you want to proceed?\n❯ 1. Yes\n  2. No\n Esc to cancel · Tab to amend"
	want := prompt{Headline: "Bash: rm -f $r/$f · Do you want to proceed?", Options: []key{{"2", "2 No"}, {"esc", "Esc"}, {"1", "1 Yes"}}, Deadline: "1:59"}
	if got := blockedPrompt(screen); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompt = %+v, want %+v", got, want)
	}
	if got := blockedPrompt("Press Enter to continue"); got.Headline != "" || got.Options != nil || got.Deadline != "" {
		t.Fatalf("a screen without a question invented %+v", got)
	}
}
