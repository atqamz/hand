package cli

import (
	"strings"
	"testing"
)

func TestReportFooterByOS(t *testing.T) {
	unix := reportFooterFor("linux", "/opt/it's/hand")
	for _, want := range []string{"'/opt/it'\\''s/hand' report add --status done --file - <<'HAND_REPORT_", "until ...; do sleep 30; done"} {
		if !strings.Contains(unix, want) {
			t.Errorf("unix footer lacks %q", want)
		}
	}
	win := reportFooterFor("windows", "C:/Program Files/hand/hand.exe")
	for _, want := range []string{reportMarker, "hand report add --status done --file PATH", "PowerShell or Git Bash", "`\"C:/Program Files/hand/hand.exe\" report add ...`"} {
		if !strings.Contains(win, want) {
			t.Errorf("windows footer lacks %q", want)
		}
	}
	for _, bad := range []string{"<<", "until ..."} {
		if strings.Contains(win, bad) {
			t.Errorf("windows footer has %q", bad)
		}
	}
}
