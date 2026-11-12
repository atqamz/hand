package kit

import (
	"os"
	"regexp"
	"testing"
)

var iconDefinition = regexp.MustCompile(`\{\{define "icon-([a-z]+)"\}\}(<svg .*?</svg>)\{\{end\}\}`)
var iconDecoration = regexp.MustCompile(` (class|aria-hidden)="[^"]*"`)

func TestIconsMatchBoard(t *testing.T) {
	b, err := os.ReadFile("../../board/templates/icons.html")
	if err != nil {
		t.Fatal(err)
	}
	board := map[string]string{}
	for _, m := range iconDefinition.FindAllStringSubmatch(string(b), -1) {
		board[m[1]] = iconDecoration.ReplaceAllString(m[2], "")
	}
	for name, src := range sources {
		want, ok := board[name]
		if !ok {
			t.Errorf("icon %q is not in icons.html", name)
			continue
		}
		if src != want {
			t.Errorf("icon %q drifts from icons.html\n kit:   %s\n board: %s", name, src, want)
		}
	}
}
