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
		h := newHarness(t)
		for _, sub := range []string{"board", "watch"} {
			out, errOut, code := h.run("unit", sub)
			if code != 2 || out != "" || !strings.Contains(errOut, "systemd-only") || !strings.Contains(errOut, "Autostart") {
				t.Fatalf("%s unit %s: code=%d stdout=%q stderr=%q", goos, sub, code, out, errOut)
			}
		}
		*cli.UnitOS = old
	}
}
