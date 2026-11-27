package desktop

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop/kit"
)

func TestTheWindowsThemeFontIsNotoSans(t *testing.T) {
	for _, dark := range []bool{false, true} {
		if got := boardTheme(dark).Font; got != kit.Sans {
			t.Errorf("dark=%v: the theme font is %q, want %q", dark, got, kit.Sans)
		}
	}
}

func TestEveryDrawnStampIsSetInMono(t *testing.T) {
	drawn := regexp.MustCompile(`ui\.Text\(c, (stamp|taskClock|clock)\(`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if !drawn.MatchString(line) {
				continue
			}
			seen++
			if !strings.Contains(line, ".Font(kit.Mono)") {
				t.Errorf("%s:%d draws a time without kit.Mono", f, i+1)
			}
		}
	}
	if seen < 10 {
		t.Errorf("found %d drawn stamps, want the window's 10 or more", seen)
	}
}
