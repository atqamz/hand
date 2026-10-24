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
		{"model left", []profile{{Name: "a", Gone: "model gem-1 is no longer listed by agy"}}, "model gem-1 is no longer listed by agy."},
		{"model left twice", []profile{{Name: "a", Gone: "model gem-1 is no longer listed by agy"}, {Name: "b", Gone: "model gem-1 is no longer listed by agy"}, {Name: "c", Gone: "model x-2 is no longer listed by codex"}}, "model gem-1 is no longer listed by agy. model x-2 is no longer listed by codex."},
		{"harness and model", []profile{{Name: "a", Missing: "claude not found on the board's PATH"}, {Name: "b", Gone: "model gem-1 is no longer listed by agy"}},
			"claude not found on the board's PATH. Install the harness or add its folder to PATH" + tail + " model gem-1 is no longer listed by agy."},
		{"both", []profile{{Name: "a", Missing: "claude not found on the board's PATH"}, {Name: "b", Missing: "codex " + batchReason}},
			"claude not found on the board's PATH. Install the harness or add its folder to PATH. codex found only as a batch file, which Hand does not start. Install the standalone harness or add the folder of its .exe to PATH" + tail},
	} {
		if got := noneStartable(tc.ps); got != tc.want {
			t.Errorf("%s: noneStartable = %q, want %q", tc.name, got, tc.want)
		}
	}
}
