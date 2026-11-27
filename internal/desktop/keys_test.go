package desktop_test

import (
	"fmt"
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

var keyIdents = map[ui.Key]string{
	ui.KeyEscape: "KeyEscape", ui.KeyEnter: "KeyEnter", ui.KeySlash: "KeySlash", ui.KeyEqual: "KeyEqual",
	ui.KeyMinus: "KeyMinus", ui.Key0: "Key0", ui.KeyF: "KeyF", ui.KeyBracketLeft: "KeyBracketLeft",
	ui.KeyBracketRight: "KeyBracketRight",
}

func TestEveryKeyTheWindowHandlesIsInTheTable(t *testing.T) {
	inTable := map[string]bool{}
	for _, row := range desktop.ShortcutRows() {
		for _, key := range row.Keys {
			inTable[keyIdents[key]] = true
		}
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// choicePress builds Key0 to Key9 and KeyA to KeyZ from an option's key; the table row says "1–9, A–Z".
	rangeStart := map[string]bool{"KeyA": true}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "keys.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "ui" && strings.HasPrefix(sel.Sel.Name, "Key") && len(sel.Sel.Name) > 3 {
				if id := sel.Sel.Name; !inTable[id] && !rangeStart[id] {
					t.Errorf("%s uses ui.%s with no row in the table", fset.Position(sel.Pos()), id)
				}
			}
			return true
		})
	}
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
