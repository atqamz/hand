package update

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fleet"
)

func TestPendingLineNamesSystemctlOnlyOnLinux(t *testing.T) {
	e := fleet.Entry{ID: "f1", Home: "/h"}
	for goos, want := range map[string]bool{"linux": true, "darwin": false, "windows": false} {
		if got := strings.Contains(pendingLineFor(goos, e), "systemctl"); got != want {
			t.Fatalf("%s: systemctl in pending line = %v, want %v", goos, got, want)
		}
	}
}
