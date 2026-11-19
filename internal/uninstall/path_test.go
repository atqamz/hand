package uninstall

import (
	"strings"
	"testing"
)

func TestDropPath(t *testing.T) {
	expand := func(s string) string { return strings.ReplaceAll(s, "%LOCALAPPDATA%", `C:\Users\op\AppData\Local`) }
	dir := `C:\Users\op\AppData\Local\hand\bin`
	cases := []struct {
		name, list, kept string
		found            bool
	}{
		{"exact", `C:\a;` + dir + `;C:\b`, `C:\a;C:\b`, true},
		{"case and a trailing backslash", `C:\a;c:\users\OP\appdata\local\hand\bin\`, `C:\a`, true},
		{"an unexpanded name", `%LOCALAPPDATA%\hand\bin;C:\a`, `C:\a`, true},
		{"the only entry", dir, ``, true},
		{"empty items go with it", `;C:\a;;` + dir + `;`, `C:\a`, true},
		{"another folder", `C:\a;` + dir + `x;C:\b`, `C:\a;` + dir + `x;C:\b`, false},
		{"a parent folder", `C:\Users\op\AppData\Local\hand`, `C:\Users\op\AppData\Local\hand`, false},
		{"empty", ``, ``, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kept, found := dropPath(c.list, dir, expand)
			if kept != c.kept || found != c.found {
				t.Fatalf("dropPath = %q, %v; want %q, %v", kept, found, c.kept, c.found)
			}
		})
	}
}
