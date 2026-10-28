package board_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
)

func TestCopilotIsNotOfferedForTheSupervisor(t *testing.T) {
	policy := `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"},"cp":{"harness":"copilot","model":"auto"}}}`
	for _, running := range []bool{false, true} {
		fx := newFixture(t)
		fx.options.Harness = harness.Env{Path: pathWith(t, "claude", "codex", "opencode", "agy", "copilot")}
		if err := os.WriteFile(filepath.Join(fx.options.Home, "routing.json"), []byte(policy), 0o644); err != nil {
			t.Fatal(err)
		}
		if running {
			fx.status = "idle"
			fx.supervisor(t, state.AttemptRunning, "gen-1")
		}
		console := region(get(t, fx.handler(), "/"), "console")
		contains(t, "profiles", console, `<option value="default"`, `agy`)
		lacks(t, "copilot", console, "copilot", `value="cp"`)
	}
}
