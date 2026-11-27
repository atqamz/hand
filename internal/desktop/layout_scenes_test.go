package desktop_test

import (
	"fmt"
	"image"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
)

const inkFloor = 3.0

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

func TestLayoutEverySceneLoadsAndDrawsText(t *testing.T) {
	eachLayout(t, layoutScenes(), func(t *testing.T, sc layoutScene, sz layoutSize, dark bool, tt *ui.Tester) {
		if len(tt.Texts()) < 5 {
			t.Fatalf("the scene draws %d texts: %q", len(tt.Texts()), tt.Texts())
		}
		for _, s := range tt.Texts() {
			if s == "Could not reach the board" || s == "The board refused the request" {
				t.Fatalf("the scene shows the error screen %q", s)
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
		report(t, "hit size", smallTargets(tt, minHitWidth, minHitHeight, nil))
		report(t, "chrome hit size", smallTargets(tt, kit.Control, kit.Control, inRail))
	})
}

func TestLayoutNoTextIsOverprinted(t *testing.T) {
	eachLayout(t, layoutScenes(), func(t *testing.T, sc layoutScene, sz layoutSize, dark bool, tt *ui.Tester) {
		report(t, "overprint", overprinted(tt, sz.w, sz.h, underlay(t, sc, sz, dark)))
	})
}

func TestLayoutTextKeepsItsContrast(t *testing.T) {
	eachLayout(t, layoutScenes(), func(t *testing.T, sc layoutScene, sz layoutSize, dark bool, tt *ui.Tester) {
		report(t, "contrast", lowContrast(tt, sz.w, sz.h, inkFloor, underlay(t, sc, sz, dark)))
	})
}

func TestLayoutNoRuleIsDoubled(t *testing.T) {
	eachLayout(t, layoutScenes(), func(t *testing.T, sc layoutScene, sz layoutSize, dark bool, tt *ui.Tester) {
		report(t, "double hairline", doubledRules(tt.Image(), dark, panelEdges(hitsOf(tt), sz.w, sz.h)))
	})
}

func ruleSpans(img *image.RGBA, y int, colors ...ui.Color) [][2]int {
	var spans [][2]int
	start := -1
	for x := img.Bounds().Min.X; x <= img.Bounds().Max.X; x++ {
		hit := false
		if x < img.Bounds().Max.X {
			for _, c := range colors {
				if rgbIs(img.RGBAAt(x, y), c) {
					hit = true
				}
			}
		}
		switch {
		case hit && start < 0:
			start = x
		case !hit && start >= 0:
			if x-start >= 120 {
				spans = append(spans, [2]int{start, x})
			}
			start = -1
		}
	}
	return spans
}

func panelEdges(hits []hitBox, width, height int) []int {
	var ys []int
	for _, h := range hits {
		if h.R.W > 300 && h.R.H > 300 && h.R.W < float32(width)-1 {
			ys = append(ys, int(h.R.Y), int(h.R.Y+h.R.H))
		}
	}
	return ys
}

func nearAny(y int, edges []int) bool {
	for _, e := range edges {
		if y >= e-3 && y <= e+3 {
			return true
		}
	}
	return false
}

func doubledRules(img *image.RGBA, dark bool, panels []int) []string {
	k := kit.ColorsFor(dark)
	rows := map[int][][2]int{}
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		if sp := ruleSpans(img, y, k.Line, k.LineStrong); len(sp) > 0 {
			rows[y] = sp
		}
	}
	var out []string
	for y, a := range rows {
		for gap := 2; gap <= 3; gap++ {
			for _, sa := range a {
				for _, sb := range rows[y+gap] {
					lo, hi := max(sa[0], sb[0]), min(sa[1], sb[1])
					if nearAny(y, panels) || nearAny(y+gap, panels) {
						continue
					}
					if hi-lo >= 120 && hi-lo >= (min(sa[1]-sa[0], sb[1]-sb[0]))*9/10 {
						out = append(out, fmt.Sprintf("rules at y=%d and y=%d, x %d to %d", y, y+gap, lo, hi))
					}
				}
			}
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
