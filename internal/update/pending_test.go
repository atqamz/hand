package update

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fleet"
)

func TestPendingLineNamesSystemctlOnlyOnLinux(t *testing.T) {
	e := fleet.Entry{ID: "f1", Home: "/h"}
	if got, want := pendingLineFor("linux", e), "At a quiet time: `systemctl --user stop "+fleet.LuvusUnit("f1")+".service`, then `hand supervisor resume` in /h"; got != want {
		t.Fatalf("linux: %q", got)
	}
	for _, goos := range []string{"darwin", "windows"} {
		if got := pendingLineFor(goos, e); strings.Contains(got, "systemctl") {
			t.Fatalf("%s: %q", goos, got)
		}
	}
}
