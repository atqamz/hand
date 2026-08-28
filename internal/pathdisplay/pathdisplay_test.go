package pathdisplay

import "testing"

func TestContextDelimitsWithoutEscapingSeparators(t *testing.T) {
	for _, path := range []string{
		`C:\Users\me\fleet home`,
		"/home/me/fleet home",
		"/home/me/plain",
		"",
	} {
		got := Context(path)
		want := "`" + path + "`"
		if got != want {
			t.Fatalf("Context(%q) = %q, want %q", path, got, want)
		}
	}
}
