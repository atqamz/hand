package harness

import (
	"fmt"
	"strings"
)

const (
	MinCompactTokens = 120_000
	compactHeadroom  = 13_000
)

var claudeWindows = []struct {
	prefix string
	size   int64
}{{"claude-opus-5", 1_000_000}, {"claude-sonnet-5", 1_000_000}, {"claude-fable-5", 1_000_000}, {"claude-haiku-4-5", 200_000}}

var claudeAliasIDs = map[string]string{"opus": "claude-opus-5", "sonnet": "claude-sonnet-5", "fable": "claude-fable-5", "haiku": "claude-haiku-4-5"}

func ClaudeWindow(model string) int64 {
	for _, w := range claudeWindows {
		if strings.HasPrefix(model, w.prefix) {
			return w.size
		}
	}
	return 0
}

// CompactPercent turns a token threshold into the percentage Claude Code reads
// from CLAUDE_AUTOCOMPACT_PCT_OVERRIDE for the model a worker was asked for.
// Claude Code compacts at min(window*pct/100, window-13000), so a threshold at
// or over window-13000 would lower nothing.
func CompactPercent(model string, tokens int64) (int, error) {
	requested := strings.TrimSuffix(model, "[1m]")
	if id, ok := claudeAliasIDs[requested]; ok {
		requested = id
	}
	window := ClaudeWindow(requested)
	if window == 0 {
		return 0, fmt.Errorf("Hand knows no context window for model %q", model)
	}
	if limit := window - compactHeadroom; tokens >= limit {
		return 0, fmt.Errorf("%d tokens is not under the %d at which Claude Code compacts a %d window on its own", tokens, limit, window)
	}
	return int(tokens * 100 / window), nil
}
