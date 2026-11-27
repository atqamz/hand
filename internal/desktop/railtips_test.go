package desktop_test

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

var railKeyed = []struct{ label, does string }{
	{"Board", "Open the Board"},
	{"Tasks", "Open Tasks"},
	{"Skills", "Open Skills"},
	{"Alerts", "Open Alerts"},
	{"Conversation", "Open the Conversation"},
	{"Companion", "Open the Companion page"},
	{"Show labels", "Show or hide the rail labels"},
}

func tableKeys(t *testing.T, does string) string {
	t.Helper()
	for _, row := range desktop.ShortcutRows() {
		if row.Does == does {
			var words []string
			for _, caps := range row.Caps {
				words = append(words, strings.Join(caps, " "))
			}
			return strings.Join(words, " or ")
		}
	}
	t.Fatalf("no table row %q", does)
	return ""
}

func hoverTip(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("no %q in the rail", label)
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(time.Second)
	tt.Frame()
}

func TestEveryRailTooltipNamesTheKeyOfTheTable(t *testing.T) {
	themes(t, func(t *testing.T, dark bool, _ string) {
		for _, it := range railKeyed {
			app := newApp(t, goldenFixture(t))
			app.Apple = false
			tt := sized(t, app, windowWidth, windowHeight, dark)
			hoverTip(t, tt, it.label)
			want := it.label + " · " + tableKeys(t, it.does)
			if !tt.HasText(want) {
				t.Errorf("%s: no tooltip %q in %q", it.label, want, tt.Texts())
			}
		}
	})
}

func TestTheRailTooltipFollowsTheTableOnApple(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	app.Apple = true
	tt := sized(t, app, windowWidth, windowHeight, false)
	hoverTip(t, tt, "Tasks")
	var apple string
	for _, row := range desktop.ShortcutRows() {
		if row.Does == "Open Tasks" {
			apple = strings.Join(row.AppleCaps[0], " ")
		}
	}
	if !tt.HasText("Tasks · " + apple) {
		t.Errorf("no Apple tooltip in %q", tt.Texts())
	}
}

func TestARailIconWithNoShortcutShowsNoKey(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := sized(t, app, windowWidth, windowHeight, false)
	hoverTip(t, tt, "New fleet")
	for _, s := range tt.Texts() {
		if strings.Contains(s, "New fleet ·") {
			t.Errorf("a key on an icon with none: %q", s)
		}
	}
	if !tt.HasText("New fleet") {
		t.Error("no tooltip for New fleet")
	}
}

func TestTheRailKeysOpenTheirPages(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := sized(t, app, windowWidth, windowHeight, false)
	for key, want := range map[ui.Key]desktop.Screen{ui.Key2: desktop.TasksScreen, ui.Key3: desktop.SkillsScreen, ui.Key4: desktop.AlertsScreen, ui.Key1: desktop.BoardScreen} {
		tt.Key(ui.Alt, key)
		tt.Frame()
		if app.Screen != want {
			t.Errorf("Alt+%v left %v, want %v", key, app.Screen, want)
		}
	}
	tt.Key(ui.Alt, ui.KeyB)
	tt.Frame()
	if !app.RailOpen {
		t.Error("Alt+B did not open the rail labels")
	}
}

func TestScenesRailTooltip(t *testing.T) {
	themes(t, func(t *testing.T, dark bool, name string) {
		app := newApp(t, goldenFixture(t))
		app.Apple = false
		tt := sized(t, app, windowWidth, windowHeight, dark)
		hoverTip(t, tt, "Tasks")
		mustText(t, tt, "Tasks · Alt 2")
		shotSized(t, tt, "139-rail-tooltip-1100x760-"+name, windowWidth, windowHeight)
	})
}

func TestTheOpenRailShowsTheKeyFromTheTable(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	app.Apple = false
	app.RailOpen = true
	tt := sized(t, app, windowWidth, 900, false)
	mustText(t, tt, tableKeys(t, "Open Tasks"), tableKeys(t, "Open Skills"))
}
