package cli

import "testing"

func TestFolderOf(t *testing.T) {
	dir := t.TempDir()
	for path, want := range map[string]string{
		`C:\Users\me\out\report.docx`: `C:\Users\me\out`,
		`C:\report.docx`:              `C:\`,
		`\\server\share\dir\f.md`:     `\\server\share\dir`,
		"/home/me/f.md":               "/home/me",
		"/f.md":                       "/",
		"/home/me/out/":               "/home/me/out",
		`C:\out\`:                     `C:\out`,
		dir:                           dir,
	} {
		if got := folderOf(path); got != want {
			t.Errorf("folderOf(%q) = %q, want %q", path, got, want)
		}
	}
}
