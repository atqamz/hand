package board_test

import (
	"testing"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
)

func TestCopilotIsNotOfferedForTheSupervisor(t *testing.T) {
	for _, running := range []bool{false, true} {
		fx := newFixture(t)
		fx.options.Harness = harness.Env{Path: pathWith(t, "claude", "codex", "opencode", "agy", "copilot")}
		if running {
			fx.status = "idle"
			fx.supervisor(t, state.AttemptRunning, "gen-1")
		}
		console := region(get(t, fx.handler(), "/"), "console")
		contains(t, "harnesses", console, `agy`)
		lacks(t, "copilot", console, "copilot")
	}
}
