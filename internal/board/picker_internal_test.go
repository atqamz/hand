package board

import "testing"

func TestTheStartPickerLineNamesEachKindOfMissingHarness(t *testing.T) {
	const tail = ", then restart the board: `hand board stop`, then `hand board`, or restart its unit."
	for _, tc := range []struct {
		name string
		ps   []profile
		want string
	}{
		{"one is startable", []profile{{Name: "a", Missing: "claude not found on the board's PATH"}, {Name: "b"}}, ""},
		{"no profiles", nil, ""},
		{"not found", []profile{{Name: "a", Missing: "claude not found on the board's PATH"}, {Name: "b", Missing: "codex not found on the board's PATH"}, {Name: "c", Missing: "claude not found on the board's PATH"}},
			"claude, codex not found on the board's PATH. Install the harness or add its folder to PATH" + tail},
		{"batch files", []profile{{Name: "a", Missing: "codex " + batchReason}, {Name: "b", Missing: "codex " + batchReason}},
			"codex found only as a batch file, which Hand does not start. Install the standalone harness or add the folder of its .exe to PATH" + tail},
		{"both", []profile{{Name: "a", Missing: "claude not found on the board's PATH"}, {Name: "b", Missing: "codex " + batchReason}},
			"claude not found on the board's PATH. Install the harness or add its folder to PATH. codex found only as a batch file, which Hand does not start. Install the standalone harness or add the folder of its .exe to PATH" + tail},
	} {
		if got := noneStartable(tc.ps); got != tc.want {
			t.Errorf("%s: noneStartable = %q, want %q", tc.name, got, tc.want)
		}
	}
}
