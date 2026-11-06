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

func TestReportFooterNamesTheScratchFolderOnce(t *testing.T) {
	for goos, names := range map[string][]string{
		"linux":   {"`$TMPDIR`", "`$GOTMPDIR`", "not in `$HOME`"},
		"windows": {"`%TEMP%`", "`%TMP%`", "not in `%USERPROFILE%`"},
	} {
		footer := reportFooterFor(goos, "/bin/hand")
		if n := strings.Count(footer, "scratch folder, which is"); n != 1 {
			t.Errorf("%s footer has %d scratch sentences, want 1", goos, n)
		}
		for _, want := range names {
			if !strings.Contains(footer, want) {
				t.Errorf("%s footer lacks %q", goos, want)
			}
		}
	}
}

func TestReportFooterAsksForDeliveredLines(t *testing.T) {
	const ask = "Say what changed and list any commits. End with one line `Delivered: <link or absolute path>` for each thing you delivered: a PR, a merge request, a document, a file."
	const wait = "Never end your turn to wait and never wait in a background shell, because ending a turn wakes the supervisor. Apart from a milestone, write a report only when the work is done or you are stuck."
	for _, goos := range []string{"linux", "windows"} {
		footer := reportFooterFor(goos, "/bin/hand")
		for _, want := range []string{ask, wait} {
			if !strings.Contains(footer, want) {
				t.Errorf("%s footer lacks %q", goos, want)
			}
		}
		if strings.Contains(footer, "PR link") {
			t.Errorf("%s footer still asks for a PR link", goos)
		}
	}
}

func TestReadonlyFooterNamesOnlyTheReportCommand(t *testing.T) {
	got := readonlyReportFooter("/bin/hand")
	for _, want := range []string{reportMarker, "'/bin/hand' report add --status done --file - <<'HAND_REPORT_", "Delivered:"} {
		if !strings.Contains(got, want) {
			t.Errorf("read-only footer lacks %q", want)
		}
	}
	for _, bad := range []string{"gh pr checks", "until ...", "sleep"} {
		if strings.Contains(got, bad) {
			t.Errorf("read-only footer has %q", bad)
		}
	}
}

func TestReportRuleNamesTheReportCommandTheFooterPrints(t *testing.T) {
	if got := reportRule("/opt/it's/hand"); got != "Bash('/opt/it'\\''s/hand' report add *)" {
		t.Fatalf("rule = %q", got)
	}
}
