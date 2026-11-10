package board_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheStartFormMarksAHarnessFoundOnlyAsABatchFile(t *testing.T) {
	start := func(t *testing.T, bins []string, shims ...string) string {
		fx := newFixture(t)
		fx.options.Harness.Path = pathWith(t, bins...)
		for _, shim := range shims {
			if err := os.WriteFile(filepath.Join(fx.options.Harness.Path, shim+".cmd"), []byte("@exit 0\r\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return region(get(t, fx.handler(), "/"), "console")
	}
	shim := start(t, []string{"claude"}, "codex")
	contains(t, "codex shim", shim, `<button disabled>Start codex</button><p class="gone">codex found only as a batch file</p>`, `<button>Start claude</button>`)

	both := start(t, []string{"claude", "codex"}, "codex")
	lacks(t, "exe and shim", both, "batch file")
}
