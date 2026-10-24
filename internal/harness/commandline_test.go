package harness

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestCommandLineLenCountsWhatWindowsReceives(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want int
	}{
		{"a program alone", []string{`C:\x.exe`}, 8},
		{"a bare argument", []string{`C:\x.exe`, "go"}, 11},
		{"an argument with a space", []string{`C:\x.exe`, "two words"}, 8 + 1 + 11},
		{"an empty argument", []string{`C:\x.exe`, ""}, 8 + 1 + 2},
		{"quotes", []string{`C:\x.exe`, `say "hi"`}, 8 + 1 + 12},
		{"a bare quote", []string{`C:\x.exe`, `"`}, 8 + 1 + 4},
		{"a backslash before a quote", []string{`C:\x.exe`, `a\"b`}, 8 + 1 + 8},
		{"a trailing backslash in a quoted argument", []string{`C:\x.exe`, `a b\`}, 8 + 1 + 7},
		{"a newline", []string{`C:\x.exe`, "a\nb"}, 8 + 1 + 5},
		{"a letter outside the basic plane counts twice", []string{`C:\x.exe`, "🌊 x"}, 8 + 1 + 6},
		{"a quoted program", []string{`C:\Program Files\x.exe`, "go"}, 24 + 1 + 2},
	} {
		if got := CommandLineLen(tc.argv); got != tc.want {
			t.Errorf("%s: CommandLineLen(%q) = %d, want %d", tc.name, tc.argv, got, tc.want)
		}
	}
}

func TestCheckCommandLineRefusesWhatWindowsCannotStart(t *testing.T) {
	line := func(letters, quotes int) []string {
		return []string{`C:\x.exe`, strings.Repeat("a", letters) + strings.Repeat(`"`, quotes)}
	}
	if got := CommandLineLen(line(1, 16377)); got != MaxWindowsCommandLine {
		t.Fatalf("the longest line is %d characters, want %d", got, MaxWindowsCommandLine)
	}
	if err := checkCommandLine("windows", line(1, 16377)); err != nil {
		t.Fatalf("the longest line Windows starts: %v", err)
	}
	for _, argv := range [][]string{line(0, 16378), line(1, 16378), line(0, MaxPromptBytes)} {
		err := checkCommandLine("windows", argv)
		if !errors.Is(err, state.ErrInvalid) {
			t.Fatalf("a %d character line: err = %v", CommandLineLen(argv), err)
		}
		for _, want := range []string{"x.exe", strconv.Itoa(CommandLineLen(argv)), strconv.Itoa(MaxWindowsCommandLine), "write the long text to a file"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %q, want it to name %q", err, want)
			}
		}
	}
	for _, goos := range []string{"linux", "darwin"} {
		if err := checkCommandLine(goos, line(0, MaxPromptBytes)); err != nil {
			t.Errorf("%s: %v", goos, err)
		}
	}
}

func TestCheckPromptNamesTheLimitAndTheFix(t *testing.T) {
	err := CheckPrompt(strings.Repeat("x", MaxPromptBytes+1))
	if !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{strconv.Itoa(MaxPromptBytes + 1), strconv.Itoa(MaxPromptBytes), "write the long text to a file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to name %q", err, want)
		}
	}
}
