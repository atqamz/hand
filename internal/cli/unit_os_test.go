package cli_test

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
)

func TestUnitRefusedOutsideLinux(t *testing.T) {
	for _, goos := range []string{"darwin", "windows"} {
		old := *cli.UnitOS
		*cli.UnitOS = goos
		t.Cleanup(func() { *cli.UnitOS = old })
		h := newHarness(t)
		out, errOut, code := h.run("unit", "board")
		if code != 2 || out != "" || !strings.Contains(errOut, "systemd-only") || !strings.Contains(errOut, "Autostart") {
			t.Fatalf("%s: code=%d stdout=%q stderr=%q", goos, code, out, errOut)
		}
	}
}
