package desktop_test

import (
	"fmt"
	"image"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
)

type layoutSize struct{ w, h int }

func (s layoutSize) String() string { return fmt.Sprintf("%dx%d", s.w, s.h) }

var layoutSizes = []layoutSize{
	{1440, 900},
	{1024, 768},
	{desktop.MinWindowWidth, 720},
}

func eachLayout(t *testing.T, scenes []layoutScene, run func(t *testing.T, sc layoutScene, sz layoutSize, dark bool, tt *ui.Tester)) {
	t.Helper()
	for _, sc := range scenes {
		for _, sz := range layoutSizes {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%s", sc.name, sz, theme(dark)), func(t *testing.T) {
					run(t, sc, sz, dark, sc.open(t, sz.w, sz.h, dark))
				})
			}
		}
	}
}

func underlay(t *testing.T, sc layoutScene, sz layoutSize, dark bool) []textBox {
	if sc.under == nil {
		return nil
	}
	texts, _ := readBoxes(sc.under(t, sz.w, sz.h, dark))
	return texts
}

func layerFor(t *testing.T, sc layoutScene, sz layoutSize, dark bool, tt *ui.Tester) layer {
	t.Helper()
	v, bad := layerOf(tt, underlay(t, sc, sz, dark))
	if bad != "" {
		t.Fatal(bad)
	}
	return v
}

func TestLayoutNoTextIsSilentlyLeftUnchecked(t *testing.T) {
	eachLayout(t, layoutScenes(), func(t *testing.T, sc layoutScene, sz layoutSize, dark bool, tt *ui.Tester) {
		v := layerFor(t, sc, sz, dark, tt)
		modal := sc.modal != nil && sc.modal(sz.w)
		if (v.dropped > 0) != modal {
			t.Fatalf("modal %v but %d of %d texts are under a modal dialog", modal, v.dropped, v.total)
		}
		if named := sc.under != nil && !modal; (v.known > 0) != named {
			t.Fatalf("%d texts taken out as the underlay, scene names one: %v", v.known, named)
		}
		if v.checked() < 5 {
			t.Fatalf("only %d of %d texts are checked", v.checked(), v.total)
		}
		if modal && v.checked() != sc.dialog {
			t.Fatalf("the dialog has %d checked texts, the scene expects %d (of %d, %d under it)", v.checked(), sc.dialog, v.total, v.dropped)
		}
	})
}

func TestLayoutEverySceneLoadsAndDrawsText(t *testing.T) {
	eachLayout(t, layoutScenes(), func(t *testing.T, sc layoutScene, sz layoutSize, dark bool, tt *ui.Tester) {
		if len(tt.Texts()) < 5 {
			t.Fatalf("the scene draws %d texts: %q", len(tt.Texts()), tt.Texts())
		}
		for _, s := range tt.Texts() {
			if s == "Could not reach the board" || s == "The board refused the request" {
				t.Fatalf("the scene shows the error screen %q", s)
			}
			if bad := errorText(s); bad && !contains(sc.errors, s) {
				t.Fatalf("the scene shows an error state %q the fixture does not name", s)
			}
		}
	})
}

func TestLayoutNothingOverflowsTheWindowOrItsPane(t *testing.T) {
	eachLayout(t, layoutScenes(), func(t *testing.T, sc layoutScene, sz layoutSize, dark bool, tt *ui.Tester) {
		report(t, "overflow", noOverflow(tt, sz.w, sz.h, false, nil))
	})
}

func TestLayoutControlsKeepTheirMinimumHitSize(t *testing.T) {
	eachLayout(t, layoutScenes(), func(t *testing.T, sc layoutScene, sz layoutSize, dark bool, tt *ui.Tester) {
		report(t, "hit size", smallTargets(hitsOf(tt), tolerances()))
	})
}

func TestLayoutNoTextIsOverprinted(t *testing.T) {
	eachLayout(t, layoutScenes(), func(t *testing.T, sc layoutScene, sz layoutSize, dark bool, tt *ui.Tester) {
		v := layerFor(t, sc, sz, dark, tt)
		report(t, "overprint", overprinted(v))
		report(t, "text outside the label that names it", strayed(v))
		report(t, "collapsed text", collapsed(v, sz.w, sz.h))
	})
}

func TestLayoutTextKeepsItsContrast(t *testing.T) {
	eachLayout(t, layoutScenes(), func(t *testing.T, sc layoutScene, sz layoutSize, dark bool, tt *ui.Tester) {
		report(t, "contrast", lowContrast(layerFor(t, sc, sz, dark, tt), tt.Image(), tolerances()))
	})
}

func TestLayoutNoRuleIsDoubled(t *testing.T) {
	eachLayout(t, layoutScenes(), func(t *testing.T, sc layoutScene, sz layoutSize, dark bool, tt *ui.Tester) {
		pairs := rulePairs(tt.Image(), dark)
		hits := hitsOf(tt)
		report(t, "double hairline", doubledRules(pairs, hits, tolerances()))
		report(t, "double hairline with the macOS and Windows exemption", doubledRules(pairs, hits, otherTolerance(darwinInk)))
	})
}

func ruleRows(img *image.RGBA, colors ...ui.Color) map[int][][2]int {
	b := img.Bounds()
	want := make([][3]uint8, len(colors))
	for i, c := range colors {
		want[i] = [3]uint8{c.R, c.G, c.B}
	}
	rows := map[int][][2]int{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[(y-b.Min.Y)*img.Stride:]
		start := -1
		for x := b.Min.X; x <= b.Max.X; x++ {
			hit := false
			if x < b.Max.X {
				o := (x - b.Min.X) * 4
				px := [3]uint8{row[o], row[o+1], row[o+2]}
				for _, w := range want {
					if px == w {
						hit = true
						break
					}
				}
			}
			switch {
			case hit && start < 0:
				start = x
			case !hit && start >= 0:
				if x-start >= 120 {
					rows[y] = append(rows[y], [2]int{start, x})
				}
				start = -1
			}
		}
	}
	return rows
}

const controlRadius = 6

type rulePair struct{ y1, y2, lo, hi int }

func (p rulePair) String() string {
	return fmt.Sprintf("rules at y=%d and y=%d, x %d to %d", p.y1, p.y2, p.lo, p.hi)
}

func ownBorder(p rulePair, hits []hitBox, tol tolerance) bool {
	for _, h := range hits {
		top, bot := int(h.R.Y), int(h.R.Y+h.R.H)
		left, right := int(h.R.X), int(h.R.X+h.R.W)
		switch {
		case h.Flags&hitClickable != 0 && h.R.H > 0 && h.R.H <= 60:
			if p.y2-p.y1 != 1 || !near(p.lo-left, controlRadius) || right-p.hi < controlRadius-1 {
				continue
			}
			if near(p.y1, top) || near(p.y2, bot-1) {
				return true
			}
		case tol.viewer && h.Flags&hitScrollY != 0 && h.R.H > 300:
			if p.y2 >= bot-1 && p.y2 <= bot+1 && p.y1 >= bot-3 && p.lo >= left-1 && p.hi <= right+1 {
				return true
			}
		}
	}
	return false
}

func near(a, b int) bool { return a-b >= -1 && a-b <= 1 }

func rulePairs(img *image.RGBA, dark bool) []rulePair {
	k := kit.ColorsFor(dark)
	rows := ruleRows(img, k.Line, k.LineStrong)
	var out []rulePair
	for y, a := range rows {
		for gap := 1; gap <= 3; gap++ {
			for _, sa := range a {
				for _, sb := range rows[y+gap] {
					lo, hi := max(sa[0], sb[0]), min(sa[1], sb[1])
					if hi-lo >= 120 && hi-lo >= min(sa[1]-sa[0], sb[1]-sb[0])*9/10 {
						out = append(out, rulePair{y, y + gap, lo, hi})
					}
				}
			}
		}
	}
	return out
}

func doubledRules(pairs []rulePair, hits []hitBox, tol tolerance) []string {
	var out []string
	for _, p := range pairs {
		if !ownBorder(p, hits, tol) {
			out = append(out, p.String())
		}
	}
	return out
}

func TestLayoutNestedTasksIndentPastTheirParent(t *testing.T) {
	for _, sz := range layoutSizes {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s", sz, theme(dark)), func(t *testing.T) {
				tt := boardOpen(layoutNested, nil)(t, sz.w, sz.h, dark)
				if sz.w < 900 {
					click(t, tt, "Tasks")
				}
				x := map[string]float32{}
				for _, ref := range []string{"t10", "t11", "t12"} {
					r, ok := tt.Find(ref)
					if !ok {
						t.Fatalf("no row for %s in %q", ref, tt.Texts())
					}
					x[ref] = r.X
				}
				if !(x["t11"] > x["t10"]) || !(x["t12"] > x["t11"]) {
					t.Fatalf("depth 1 and 2 rows are not indented past depth 0: t10 %v, t11 %v, t12 %v", x["t10"], x["t11"], x["t12"])
				}
			})
		}
	}
}

func TestLayoutMenusAndSheetsStayInsideTheWindow(t *testing.T) {
	open := map[string]func(t *testing.T, tt *ui.Tester, width int){
		"overflow menu": func(t *testing.T, tt *ui.Tester, width int) { click(t, tt, menuButton(width)) },
		"shortcut sheet": func(t *testing.T, tt *ui.Tester, width int) {
			tt.Key(ui.Ctrl, ui.KeySlash)
			tt.Frame()
		},
	}
	names := make([]string, 0, len(open))
	for n := range open {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, sz := range layoutSizes {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%s", name, sz, theme(dark)), func(t *testing.T) {
					tt := sized(t, newApp(t, goldenFixture(t)), sz.w, sz.h, dark)
					known := hitsOf(tt)
					open[name](t, tt, sz.w)
					if len(tt.Texts()) == 0 {
						t.Fatal("nothing drawn")
					}
					report(t, name, noOverflow(tt, sz.w, sz.h, true, known))
				})
			}
		}
	}
}

var dayText = regexp.MustCompile(`^[A-Z][a-z]{2}, [A-Z][a-z]{2} \d{1,2}$`)

func TestLayoutTheConversationShowsDayDividers(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			tt := sized(t, historyApp(t, historyModel(t)), windowWidth, windowHeight, dark)
			n := 0
			for _, s := range tt.Texts() {
				if dayText.MatchString(s) {
					n++
				}
			}
			if n == 0 {
				t.Fatalf("no day dividers in %q", tt.Texts())
			}
		})
	}
}

func TestLayoutDayDividersFollowTheLocalZone(t *testing.T) {
	zone := time.FixedZone("WIB", 7*3600)
	old := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = old })
	instants := []time.Time{
		time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 31, 17, 30, 0, 0, time.UTC),
		time.Date(2026, 10, 31, 16, 30, 0, 0, time.UTC),
		time.Date(2026, 10, 31, 2, 0, 0, 0, time.UTC),
	}
	f := goldenFixture(t)
	var entries []desktop.Entry
	for i, at := range instants {
		entries = append(entries, desktop.Entry{Role: "hand", At: at.Format(time.RFC3339), Text: fmt.Sprintf("line %d", i)})
	}
	f.timeline[fleetID] = desktop.Timeline{Live: true, Entries: entries}
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			tt := sized(t, newApp(t, f), windowWidth, windowHeight, dark)
			var got []string
			for _, s := range tt.Texts() {
				if dayText.MatchString(s) {
					got = append(got, s)
				}
			}
			if fmt.Sprint(got) != "[Sun, Nov 1]" {
				t.Fatalf("dividers %v, want one at the local day change [Sun, Nov 1]", got)
			}
			for _, clock := range []string{"Oct 31, 09:00", "Oct 31, 23:30"} {
				if !tt.HasText(clock) {
					t.Fatalf("no card time %q in the local zone", clock)
				}
			}
			before, _ := tt.Find("line 2")
			divider, _ := tt.Find("Sun, Nov 1")
			after, _ := tt.Find("line 1")
			if !(before.Y < divider.Y && divider.Y < after.Y) {
				t.Fatalf("divider not between the last Oct 31 line and the first Nov 1 line: %v %v %v", before.Y, divider.Y, after.Y)
			}
		})
	}
}

func TestLayoutTheNavigationTargetsCanBeClicked(t *testing.T) {
	for _, sz := range layoutSizes {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s", sz, theme(dark)), func(t *testing.T) {
				tt := sized(t, newApp(t, goldenFixture(t)), sz.w, sz.h, dark)
				if sz.w < 900 {
					click(t, tt, "Tasks")
				} else {
					tt = sized(t, newApp(t, goldenFixture(t)), sz.w, sz.h, dark)
				}
				click(t, tt, "Fix login")
			})
		}
	}
}

func TestScenesLayout(t *testing.T) {
	n := 142
	for _, sc := range layoutScenes() {
		if !sc.fresh {
			continue
		}
		for _, dark := range []bool{false, true} {
			name := fmt.Sprintf("%d-layout-%s-%dx%d-%s", n, sc.name, windowWidth, windowHeight, theme(dark))
			t.Run(name, func(t *testing.T) {
				shotSized(t, sc.open(t, windowWidth, windowHeight, dark), name, windowWidth, windowHeight)
			})
		}
		n++
	}
}

func errorText(s string) bool {
	l := strings.ToLower(s)
	for _, w := range []string{"not found", "could not", "couldn't", "unavailable", "not_found", "refused", "unauthorized", "forbidden", "internal error"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestLayoutDetectorsSeeWhatTheyAreMeantTo(t *testing.T) {
	label := func(text string, x, y, w, h float32) textBox {
		return textBox{Text: text, R: ui.Rect{X: x, Y: y, W: w, H: h}}
	}
	for _, c := range []struct {
		name  string
		texts []textBox
		want  int
	}{
		{"an unrelated label inside a 44 px label", []textBox{label("Row title", 10, 10, 300, 44), label("Unrelated", 40, 20, 80, 17)}, 1},
		{"an unrelated label inside a 44 px label that holds two", []textBox{label("Row title", 10, 10, 300, 44), label("Other", 200, 40, 40, 12), label("Unrelated", 40, 20, 80, 17)}, 2},
		{"a landmark holding a label", []textBox{label("Masthead", 0, 0, 800, 48), label("working", 700, 16, 50, 17)}, 0},
		{"an accessible name holding its words", []textBox{label("golden, failed", 0, 0, 200, 40), label("failed", 100, 10, 40, 17)}, 0},
		{"two labels partly over each other", []textBox{label("One", 10, 10, 100, 20), label("Two", 60, 15, 100, 20)}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := overprinted(layer{texts: c.texts}); len(got) != c.want {
				t.Fatalf("%d overprints %q, want %d", len(got), got, c.want)
			}
		})
	}

	named := []textBox{label("group Active", 76, 589, 528, 35), label("Active", 716, 603, 37, 17), label("Active 3", 119, 603, 19, 17)}
	if got := strayed(layer{texts: named}); len(got) != 1 {
		t.Fatalf("a group name pushed outside its group: %d found, want 1: %q", len(got), got)
	}
	named[1] = label("Active", 90, 603, 37, 17)
	if got := strayed(layer{texts: named}); len(got) != 0 {
		t.Fatalf("a group name inside its group: %q", got)
	}

	box := func(x, y, w, h float32, flags uint32) hitBox {
		return hitBox{R: ui.Rect{X: x, Y: y, W: w, H: h}, Flags: flags}
	}
	control := box(100, 400, 300, 32, hitClickable|hitFocusable)
	viewer := box(100, 100, 800, 700, hitScrollY|hitClip)
	linux, other := tolerance{}, otherTolerance(darwinInk)
	for _, c := range []struct {
		name         string
		p            rulePair
		hits         []hitBox
		linux, other bool
	}{
		{"a control's own border, inset by its radius", rulePair{430, 431, 106, 394}, []hitBox{control}, false, false},
		{"a control's own top border", rulePair{400, 401, 106, 394}, []hitBox{control}, false, false},
		{"a second rule inset 4 px under a control's border", rulePair{430, 431, 104, 396}, []hitBox{control}, true, true},
		{"a second rule inset 2 px under a control's border", rulePair{430, 431, 102, 398}, []hitBox{control}, true, true},
		{"a rule 3 px under a control's border", rulePair{428, 431, 106, 394}, []hitBox{control}, true, true},
		{"the viewer's bottom border and the content's last rule", rulePair{797, 799, 120, 880}, []hitBox{viewer}, true, false},
		{"a second rule under the viewer's top edge", rulePair{99, 101, 120, 880}, []hitBox{viewer}, true, true},
		{"a second rule adjacent to the viewer's top edge", rulePair{99, 100, 120, 880}, []hitBox{viewer}, true, true},
		{"a rule pair inside the viewer", rulePair{300, 301, 120, 880}, []hitBox{viewer}, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := len(doubledRules([]rulePair{c.p}, c.hits, linux)) == 1; got != c.linux {
				t.Fatalf("Linux rules flag it: %v, want %v", got, c.linux)
			}
			if got := len(doubledRules([]rulePair{c.p}, c.hits, other)) == 1; got != c.other {
				t.Fatalf("macOS and Windows rules flag it: %v, want %v", got, c.other)
			}
		})
	}
}
