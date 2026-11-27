package desktop_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
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

var railPages = map[string]desktop.Screen{
	"Open the Board":          desktop.BoardScreen,
	"Open Tasks":              desktop.TasksScreen,
	"Open Skills":             desktop.SkillsScreen,
	"Open Alerts":             desktop.AlertsScreen,
	"Open the Conversation":   desktop.HistoryScreen,
	"Open the Companion page": desktop.PhoneScreen,
}

func railRows() []desktop.ShortcutRow {
	var out []desktop.ShortcutRow
	for _, row := range desktop.ShortcutRows() {
		if row.Group == "Pages and fleets" {
			out = append(out, row)
		}
	}
	return out
}

func TestEveryKeyOfTheRailGroupDoesItsThing(t *testing.T) {
	rows := railRows()
	if len(rows) != len(railPages)+1 {
		t.Fatalf("%d rows in the group, want %d", len(rows), len(railPages)+1)
	}
	for _, row := range rows {
		for i, key := range row.Keys {
			mods := row.Mods[i]
			app := newApp(t, goldenFixture(t))
			tt := sized(t, app, windowWidth, windowHeight, false)
			if row.Does == "Show or hide the rail labels" {
				tt.Key(mods, key)
				tt.Frame()
				if !app.RailOpen {
					t.Errorf("%s (%v %v) did not open the labels", row.Does, mods, key)
				}
				tt.Key(mods, key)
				tt.Frame()
				if app.RailOpen {
					t.Errorf("%s (%v %v) did not close the labels", row.Does, mods, key)
				}
				continue
			}
			want, ok := railPages[row.Does]
			if !ok {
				t.Fatalf("no expected page for %q", row.Does)
			}
			from := desktop.AlertsScreen
			if want == from {
				from = desktop.BoardScreen
			}
			app.Show(from)
			tt.Frame()
			tt.Key(mods, key)
			tt.Frame()
			if app.Screen != want {
				t.Errorf("%s (%v %v) left %v, want %v", row.Does, mods, key, app.Screen, want)
			}
		}
	}
}

func TestTheCompanionKeyDoesNothingOnARemoteFleet(t *testing.T) {
	r := newRemoteUI(t, true)
	r.tt.Frame()
	before := r.app.Screen
	r.tt.Key(ui.Ctrl, ui.Key6)
	r.tt.Frame()
	if r.app.Screen != before || r.app.Screen == desktop.PhoneScreen {
		t.Errorf("Ctrl+6 on a remote fleet left %v, was %v", r.app.Screen, before)
	}
	r.tt.Key(ui.Ctrl, ui.Key5)
	r.tt.Frame()
	if r.app.Screen != desktop.HistoryScreen {
		t.Errorf("Ctrl+5 on a remote fleet left %v", r.app.Screen)
	}
}

func TestTheNarrowNavSheetShowsNoKeyItCannotHonour(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := sized(t, app, 880, windowHeight, false)
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !desktop.SheetOpen(app) {
		t.Fatal("sheet did not open")
	}
	for _, row := range railRows() {
		if tt.HasText(tableKeys(t, row.Does)) {
			t.Errorf("the sheet shows %q for %q", tableKeys(t, row.Does), row.Does)
		}
	}
}

func TestNoNavItemKeyIsALiteral(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if kv, ok := n.(*ast.KeyValueExpr); ok {
				if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Keys" {
					if _, lit := kv.Value.(*ast.BasicLit); lit {
						t.Errorf("%s: a literal in Keys", fset.Position(kv.Pos()))
					}
				}
			}
			return true
		})
	}
}

func TestScenesRailTooltip(t *testing.T) {
	themes(t, func(t *testing.T, dark bool, name string) {
		app := newApp(t, goldenFixture(t))
		app.Apple = false
		tt := sized(t, app, windowWidth, windowHeight, dark)
		hoverTip(t, tt, "Tasks")
		mustText(t, tt, "Tasks · Ctrl 2")
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
