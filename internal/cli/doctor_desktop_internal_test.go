package cli

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktopasset"
)

func TestTheWindowNoteForWindowsHoldsOnEveryHostAndNamesNoChannel(t *testing.T) {
	note := windowTried("windows")
	for _, want := range []string{"untested on a real Windows PC", "opened on a CI runner", "`hand open`"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note = %q, want %q", note, want)
		}
	}
	for _, channel := range []string{"stable", "edge"} {
		if strings.Contains(note, channel) {
			t.Fatalf("note names the %s channel: %q", channel, note)
		}
		if !desktopasset.Supported("windows", "amd64", channel) {
			t.Fatalf("a %s build on windows/amd64 does not get the window row", channel)
		}
	}
	if !strings.Contains(windowTried("darwin"), "untested on a real Mac") || windowTried("linux") != "" {
		t.Fatalf("darwin = %q, linux = %q", windowTried("darwin"), windowTried("linux"))
	}
}
