package board

import "testing"

func TestBase(t *testing.T) {
	for in, want := range map[string]string{
		"/home/me/out/notes.md":       "notes.md",
		`C:\Users\me\out\notes.pdf`:   "notes.pdf",
		`C:\Users\me/mixed\notes.pdf`: "notes.pdf",
		"/home/me/out/":               "out",
		`\\server\share\notes.md`:     "notes.md",
		"/notes.md":                   "notes.md",
	} {
		if got := base(in); got != want {
			t.Errorf("base(%q) = %q, want %q", in, got, want)
		}
	}
}
