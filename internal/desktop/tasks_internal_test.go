package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestATaskStateTakesTheBoardsToneAndIcon(t *testing.T) {
	cases := []struct {
		state, icon string
	}{
		{"waiting", "waiting"},
		{"failing", "failure"},
		{"running", "running"},
		{"idle", "idle"},
		{"passing", "passing"},
		{"", "idle"},
		{"unknown", "idle"},
	}
	for _, dark := range []bool{false, true} {
		p := paletteFor(dark)
		want := map[string]struct {
			icon  string
			color ui.Color
		}{
			"waiting": {"waiting", p.wait},
			"failing": {"failure", p.fail},
			"running": {"running", p.pass},
			"idle":    {"idle", p.neutral},
			"passing": {"passing", p.pass},
			"":        {"idle", p.neutral},
			"unknown": {"idle", p.neutral},
		}
		for _, tc := range cases {
			if got := stateIconName(tc.state); got != want[tc.state].icon {
				t.Errorf("dark=%v: state %q draws the %q icon, want %q", dark, tc.state, got, want[tc.state].icon)
			}
			if got := stateColor(tc.state, p); got != want[tc.state].color {
				t.Errorf("dark=%v: state %q is tinted %v, want the tone's %v", dark, tc.state, got, want[tc.state].color)
			}
			word := stateWord(tc.state)
			if tc.state != "" && word != tc.state {
				t.Errorf("dark=%v: state %q says %q, want the Board's word", dark, tc.state, word)
			}
			if tc.state == "" && word != "idle" {
				t.Errorf("dark=%v: no state says %q, want idle", dark, word)
			}
		}
	}
}

func TestTaskProjectKeepsTwentyFourCharacters(t *testing.T) {
	cases := []struct{ in, want string }{
		{"web", "web"},
		{strings.Repeat("a", 24), strings.Repeat("a", 24)},
		{strings.Repeat("a", 25), strings.Repeat("a", 23) + "…"},
		{strings.Repeat("é", 25), strings.Repeat("é", 23) + "…"},
	}
	for _, tc := range cases {
		if got := taskProject(tc.in); got != tc.want {
			t.Errorf("taskProject(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
