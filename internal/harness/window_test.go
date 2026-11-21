package harness

import "testing"

func TestClaudeWindows(t *testing.T) {
	for model, want := range map[string]int64{"claude-opus-5-5": 1_000_000, "claude-sonnet-5-5": 1_000_000, "claude-fable-5-1": 1_000_000, "claude-haiku-4-5-20251001": 200_000, "gpt-6": 0, "": 0} {
		if got := ClaudeWindow(model); got != want {
			t.Errorf("ClaudeWindow(%q) = %d, want %d", model, got, want)
		}
	}
}

func TestCompactPercentFromTokens(t *testing.T) {
	for _, tc := range []struct {
		model  string
		tokens int64
		want   int
	}{
		{"claude-sonnet-5-5", 200_000, 20},
		{"sonnet", 200_000, 20},
		{"sonnet[1m]", 200_000, 20},
		{"opus", 150_000, 15},
		{"claude-opus-5-5", 120_000, 12},
		{"claude-fable-5-1", 199_999, 19},
		{"claude-sonnet-5-5", 986_999, 98},
		{"claude-sonnet-5-5", 987_000, 0},
		{"claude-haiku-4-5-20251001", 200_000, 0},
		{"claude-haiku-4-5-20251001", 187_000, 0},
		{"claude-haiku-4-5-20251001", 186_999, 93},
		{"haiku", 120_000, 60},
		{"haiku", 150_000, 75},
		{"claude-haiku-5-5", 150_000, 0},
		{"gpt-6", 200_000, 0},
		{"m", 200_000, 0},
		{"", 200_000, 0},
		{"us.anthropic.claude-sonnet-5-5-v1:0", 200_000, 0},
	} {
		got, err := CompactPercent(tc.model, tc.tokens)
		if got != tc.want || (err != nil) != (tc.want == 0) {
			t.Errorf("CompactPercent(%q, %d) = %d, %v; want %d", tc.model, tc.tokens, got, err, tc.want)
		}
	}
}

func TestHaikuPercentNeverSitsBelowTheBaseline(t *testing.T) {
	const baseline = 52_000
	for tokens := int64(MinCompactTokens); tokens < 200_000; tokens += 1_000 {
		pct, err := CompactPercent("haiku", tokens)
		if err == nil && int64(pct)*200_000/100 < baseline {
			t.Fatalf("haiku at %d tokens gets %d%%, below the %d baseline", tokens, pct, baseline)
		}
	}
}

func TestCompactPercentNeverCompactsLaterThanAsked(t *testing.T) {
	for _, model := range []string{"sonnet", "haiku"} {
		window := ClaudeWindow(claudeAliasIDs[model])
		for tokens := int64(MinCompactTokens); tokens < window-compactHeadroom; tokens += 997 {
			pct, err := CompactPercent(model, tokens)
			if err != nil || pct < 1 || pct > 99 || int64(pct)*window/100 > tokens {
				t.Fatalf("%s at %d tokens = %d, %v", model, tokens, pct, err)
			}
		}
	}
}
