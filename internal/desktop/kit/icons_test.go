package kit

import (
	"maps"
	"os"
	"regexp"
	"slices"
	"testing"
)

var iconDefinition = regexp.MustCompile(`\{\{define "icon-([a-z]+)"\}\}(<svg .*?</svg>)\{\{end\}\}`)
var iconDecoration = regexp.MustCompile(` (class|aria-hidden)="[^"]*"`)

var iconNames = []string{
	"attach", "board", "chat", "chevron", "close", "failure", "fleets", "idle",
	"mark", "passing", "phone", "running", "screen", "search", "tasks", "waiting",
}

func TestIconNames(t *testing.T) {
	if got := slices.Sorted(maps.Keys(sources)); !slices.Equal(got, iconNames) {
		t.Errorf("icons = %v, want %v", got, iconNames)
	}
}

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
