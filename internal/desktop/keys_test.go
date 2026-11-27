package desktop_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

const sheetTitle = "Keyboard shortcuts"

func themes(t *testing.T, run func(t *testing.T, dark bool, name string)) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) { run(t, dark, name) })
	}
}

func pressHelp(tt *ui.Tester) {
	tt.TypeKey(ui.Shift, ui.KeySlash, "?")
	tt.Frame()
}

func TestQuestionMarkOpensAndEscapeClosesTheShortcutSheet(t *testing.T) {
	themes(t, func(t *testing.T, dark bool, _ string) {
		tt := sized(t, newApp(t, goldenFixture(t)), windowWidth, windowHeight, dark)
		mustNotText(t, tt, sheetTitle)
		pressHelp(tt)
		mustText(t, tt, sheetTitle)
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		mustNotText(t, tt, sheetTitle)
	})
}

func TestQuestionMarkAlsoClosesTheSheetAndASecondOneReopensIt(t *testing.T) {
	tt := sized(t, newApp(t, goldenFixture(t)), windowWidth, windowHeight, false)
	pressHelp(tt)
	mustText(t, tt, sheetTitle)
	pressHelp(tt)
	mustNotText(t, tt, sheetTitle)
	pressHelp(tt)
	mustText(t, tt, sheetTitle)
}

func TestQuestionMarkTypedIntoATextFieldDoesNotOpenTheSheet(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := sized(t, app, windowWidth, windowHeight, false)
	click(t, tt, "Message the supervisor")
	tt.Type("why")
	tt.TypeKey(ui.Shift, ui.KeySlash, "?")
	tt.Frame()
	mustNotText(t, tt, sheetTitle)
	if app.Draft != "why?" {
		t.Errorf("draft is %q, want %q", app.Draft, "why?")
	}
}

func TestQuestionMarkInTheFindBoxDoesNotOpenTheSheet(t *testing.T) {
	_, tt := openFind(t, findFixture(t), bigLog, false)
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	tt.TypeKey(ui.Shift, ui.KeySlash, "?")
	tt.Frame()
	mustNotText(t, tt, sheetTitle)
}

func TestTheSlashKeyStillFindsAndIsNotTheSheet(t *testing.T) {
	_, tt := openFind(t, findFixture(t), bigLog, false)
	tt.Key(0, ui.KeySlash)
	tt.Frame()
	mustNotText(t, tt, sheetTitle)
}

func TestTheSheetListsEveryRowOfTheTable(t *testing.T) {
	themes(t, func(t *testing.T, dark bool, _ string) {
		for _, apple := range []bool{false, true} {
			app := newApp(t, goldenFixture(t))
			app.Apple = apple
			tt := sized(t, app, windowWidth, windowHeight, dark)
			pressHelp(tt)
			groups := map[string]bool{}
			for _, row := range desktop.ShortcutRows() {
				groups[row.Group] = true
				mustText(t, tt, row.Does)
				caps := row.Caps
				if apple {
					caps = row.AppleCaps
				}
				if len(caps) == 0 {
					t.Errorf("%q has no key to show", row.Does)
				}
				for _, parts := range caps {
					mustText(t, tt, parts...)
				}
			}
			for g := range groups {
				mustText(t, tt, g)
			}
		}
	})
}

func TestEveryChordHasANameAndNoGroupBindsOneChordTwice(t *testing.T) {
	seen := map[string]string{}
	for _, row := range desktop.ShortcutRows() {
		if row.Does == "" || row.Group == "" {
			t.Errorf("row %+v has no group or no text", row)
		}
		for i, key := range row.Keys {
			id := fmt.Sprint(row.Group, "|", row.Mods[i], "|", key)
			if other, dup := seen[id]; dup {
				t.Errorf("%q and %q bind the same chord in %q", other, row.Does, row.Group)
			}
			seen[id] = row.Does
		}
		for _, parts := range append(row.Caps, row.AppleCaps...) {
			for _, p := range parts {
				if strings.TrimSpace(p) == "" {
					t.Errorf("%q has an unnamed key", row.Does)
				}
			}
		}
	}
}

func TestTheRowsOfAGroupSitTogether(t *testing.T) {
	done := map[string]bool{}
	last := ""
	for _, row := range desktop.ShortcutRows() {
		if row.Group != last {
			if done[row.Group] {
				t.Errorf("group %q is split", row.Group)
			}
			done[last], last = true, row.Group
		}
	}
}

type callSite struct{ file, fn, mods, key string }

func TestEveryKeyTheWindowHandlesComesFromTheTable(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := map[callSite]bool{
		{"needs.go", "choiceKeys", "0", "key"}:         true,
		{"needs.go", "choiceKeys", "ui.Shift", "key"}:  true,
		{"needs.go", "choiceKeys", "0", "ui.KeyEnter"}: true,
	}
	wantRefs := map[string]bool{
		"needs.go choiceKeys ui.KeyEnter": true, "needs.go choicePress ui.Key0": true,
		"needs.go choicePress ui.KeyA": true, "needs.go choicePress ui.Key": true,
	}
	gotCalls, gotRefs := map[callSite]bool{}, map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "keys.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fd, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					sel, ok := n.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					switch sel.Sel.Name {
					case "Shortcut", "OnShortcut", "OverlayShortcut":
						if len(n.Args) < 2 {
							t.Errorf("%s: %s with %d arguments", fset.Position(n.Pos()), sel.Sel.Name, len(n.Args))
							return true
						}
						gotCalls[callSite{name, fd.Name.Name, types.ExprString(n.Args[0]), types.ExprString(n.Args[1])}] = true
					case "OnTextKey":
						if !callsIs(n) {
							t.Errorf("%s: OnTextKey does not read the table through .is", fset.Position(n.Pos()))
						}
					}
				case *ast.SelectorExpr:
					if x, ok := n.X.(*ast.Ident); ok && x.Name == "ui" && strings.HasPrefix(n.Sel.Name, "Key") && n.Sel.Name != "KeyboardEvent" {
						gotRefs[name+" "+fd.Name.Name+" ui."+n.Sel.Name] = true
					}
				}
				return true
			})
		}
	}
	for c := range gotCalls {
		if !wantCalls[c] {
			t.Errorf("%s %s binds a key directly (%s, %s) instead of reading the table", c.file, c.fn, c.mods, c.key)
		}
	}
	for c := range wantCalls {
		if !gotCalls[c] {
			t.Errorf("expected key binding %+v is gone; update the list", c)
		}
	}
	for r := range gotRefs {
		if !wantRefs[r] {
			t.Errorf("%s names a key outside the table", r)
		}
	}
	for r := range wantRefs {
		if !gotRefs[r] {
			t.Errorf("expected key reference %q is gone; update the list", r)
		}
	}
	var answer, choose bool
	for _, row := range desktop.ShortcutRows() {
		for i, key := range row.Keys {
			answer = answer || row.Group == "On an open decision" && key == ui.KeyEnter && row.Mods[i] == 0
		}
		choose = choose || row.Does == "Pick the option with that key"
	}
	if !answer || !choose {
		t.Errorf("the decision rows are missing: answer %v, choose %v", answer, choose)
	}
}

func callsIs(call *ast.CallExpr) bool {
	found := false
	ast.Inspect(call, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "is" {
				found = true
			}
		}
		return !found
	})
	return found
}

func TestTheComposerHintComesFromTheTable(t *testing.T) {
	for _, apple := range []bool{false, true} {
		app := newApp(t, goldenFixture(t))
		app.Apple = apple
		tt := sized(t, app, 1440, 900, false)
		click(t, tt, "Message the supervisor")
		tt.Type("hi")
		tt.Frame()
		if want := desktop.SendHint(apple); !tt.HasText(want) {
			t.Errorf("apple=%v: no %q in %q", apple, want, tt.Texts())
		}
	}
	if desktop.SendHint(false) == desktop.SendHint(true) {
		t.Error("the hint does not tell Apple hardware from the rest")
	}
}

func TestTheBracketTipComesFromTheTable(t *testing.T) {
	tip := desktop.DocStepTip()
	var prev, next string
	for _, row := range desktop.ShortcutRows() {
		switch row.Does {
		case "Open the previous document in this folder":
			prev = strings.Join(row.Caps[0], " ")
		case "Open the next document in this folder":
			next = strings.Join(row.Caps[0], " ")
		}
	}
	if prev == "" || next == "" || !strings.HasPrefix(tip, prev+" and "+next+" ") {
		t.Fatalf("tip %q does not start with the table's %q and %q", tip, prev, next)
	}
	app, _ := toolsApp(t, designPath)
	tt := newTester(app)
	r, ok := tt.Find("Source")
	if !ok {
		t.Fatal("no Source")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(time.Second)
	tt.Frame()
	if !tt.HasText("Rendered or Source. " + tip) {
		t.Errorf("the tooltip is not built from the table, in %q", tt.Texts())
	}
}

func TestTheSheetOpensAndClosesOnANarrowWindow(t *testing.T) {
	tt := sized(t, newApp(t, goldenFixture(t)), 480, 640, false)
	pressHelp(tt)
	mustText(t, tt, sheetTitle, "Send the message")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	mustNotText(t, tt, sheetTitle)
}

func TestScenesShortcutSheet(t *testing.T) {
	themes(t, func(t *testing.T, dark bool, name string) {
		app := newApp(t, goldenFixture(t))
		app.Apple = false
		tt := sized(t, app, windowWidth, windowHeight, dark)
		pressHelp(tt)
		mustText(t, tt, sheetTitle)
		shotSized(t, tt, "129-shortcut-sheet-1100x760-"+name, windowWidth, windowHeight)
	})
}

func TestEscapeOverADocumentPageClosesOnlyTheSheet(t *testing.T) {
	themes(t, func(t *testing.T, dark bool, _ string) {
		app, _ := toolsApp(t, designPath)
		tt := newTester(app)
		tt.SetDark(dark)
		pressHelp(tt)
		mustText(t, tt, sheetTitle)
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		mustNotText(t, tt, sheetTitle)
		if app.Screen != desktop.DocScreen {
			t.Fatalf("one Escape closed the document too, screen %v", app.Screen)
		}
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if app.Screen == desktop.DocScreen {
			t.Error("a second Escape did not close the document")
		}
	})
}

func TestTheFindRowKeepsSlashWhereCmdIsSuper(t *testing.T) {
	find := []desktop.Chord{{Mods: ui.Super, Key: ui.KeyF}, {Mods: 0, Key: ui.KeySlash}}
	for _, apple := range []bool{false, true} {
		if got := desktop.LabelOf(apple, find...); got != "⌘ F or /" {
			t.Errorf("apple=%v: find row reads %q, want %q", apple, got, "⌘ F or /")
		}
	}
}

func TestACtrlAndASuperTwinSplitByPlatformAndPlainChordsStay(t *testing.T) {
	send := []desktop.Chord{{Mods: ui.Ctrl, Key: ui.KeyEnter}, {Mods: ui.Super, Key: ui.KeyEnter}, {Mods: 0, Key: ui.KeyEscape}}
	if got := desktop.LabelOf(false, send...); got != "Ctrl Enter or Esc" {
		t.Errorf("not Apple: %q", got)
	}
	if got := desktop.LabelOf(true, send...); got != "⌘ Enter or Esc" {
		t.Errorf("Apple: %q", got)
	}
}

func TestTheHelpRowShowsALiteralQuestionMark(t *testing.T) {
	for _, row := range desktop.ShortcutRows() {
		if row.Does == "Show this list" {
			if len(row.Caps) != 1 || len(row.Caps[0]) != 1 || row.Caps[0][0] != "?" {
				t.Fatalf("the help row shows %q, want a single ? key", row.Caps)
			}
			return
		}
	}
	t.Fatal("no help row")
}

func TestTheSheetScrollsAndStaysInsideAShortWindow(t *testing.T) {
	const h = 300
	tt := sized(t, newApp(t, goldenFixture(t)), windowWidth, h, false)
	pressHelp(tt)
	title, ok := tt.Find(sheetTitle)
	if !ok || title.Y < 16 {
		t.Fatalf("title %v, found %v: want it inside the 16 px margin", title, ok)
	}
	last, ok := tt.Find("Close this list")
	if !ok || last.Y+last.H <= h-16 {
		t.Fatalf("the last row at %v is already in view: the sheet does not need to scroll", last)
	}
	tt.Scroll(windowWidth/2, h/2, 0, 2000)
	tt.Frame()
	last, ok = tt.Find("Close this list")
	if !ok || last.Y < 0 || last.Y+last.H > h-16 {
		t.Errorf("after scrolling the last row is at %v (found %v), want it inside the window and its margin", last, ok)
	}
}

func TestTheSheetNamesTheBackChordForEachPlatform(t *testing.T) {
	var row desktop.ShortcutRow
	for _, r := range desktop.ShortcutRows() {
		if r.Does == "Back one page; from the first page, close it" {
			row = r
		}
	}
	if row.Does == "" {
		t.Fatal("no Back row in the table")
	}
	if got, want := fmt.Sprint(row.Caps), fmt.Sprint([][]string{{"Alt", "←"}}); got != want {
		t.Errorf("the sheet shows %s for Back off Apple, want %s", got, want)
	}
	if got, want := fmt.Sprint(row.AppleCaps), fmt.Sprint([][]string{{"⌘", "["}}); got != want {
		t.Errorf("the sheet shows %s for Back on Apple, want %s", got, want)
	}
}
