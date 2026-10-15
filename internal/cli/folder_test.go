package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFolderOf(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "program")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		`C:\Users\me\out\report.docx`: `C:\Users\me\out`,
		`C:\report.docx`:              `C:\`,
		`\\server\share\dir\f.md`:     `\\server\share\dir`,
		"/home/me/f.md":               "/home/me",
		"/f.md":                       "/",
		"/home/me/out/":               "/home/me",
		`C:\out\`:                     `C:\`,
		dir:                           dir,
		dir + "/":                     dir,
		file:                          dir,
		file + "/":                    dir,
	} {
		if got := folderOf(path); got != want {
			t.Errorf("folderOf(%q) = %q, want %q", path, got, want)
		}
	}
}
