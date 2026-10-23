package board_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheStartPickerMarksAHarnessFoundOnlyAsABatchFile(t *testing.T) {
	policy := `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"},"gpt":{"harness":"codex","model":"gpt-5.5","effort":"high"}}}`
	start := func(t *testing.T, bins []string, shims ...string) string {
		fx := newFixture(t)
		fx.options.Harness.Path = pathWith(t, bins...)
		for _, shim := range shims {
			if err := os.WriteFile(filepath.Join(fx.options.Harness.Path, shim+".cmd"), []byte("@exit 0\r\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(fx.options.Home, "routing.json"), []byte(policy), 0o644); err != nil {
			t.Fatal(err)
		}
		return region(get(t, fx.handler(), "/"), "console")
	}
	shim := start(t, []string{"claude"}, "codex")
	contains(t, "codex shim", shim, `<option value="gpt" disabled>gpt · codex gpt-5.5 high · codex found only as a batch file</option>`, `<option value="default" selected>default · claude sonnet medium</option>`)
	lacks(t, "codex shim", shim, `class="gone"`, `<button class="primary" disabled>`)

	both := start(t, []string{"claude", "codex"}, "codex")
	lacks(t, "exe and shim", both, " disabled", "batch file")

	none := start(t, nil, "claude", "codex")
	contains(t, "only shims", none, `<button class="primary" disabled>Start</button>`, `<p class="gone">claude, codex found only as a batch file, which Hand does not start. Install the standalone harness or add the folder of its .exe to PATH, then restart the board: `+"`hand board stop`"+`, then `+"`hand board`"+`, or restart its unit.</p>`)
}
