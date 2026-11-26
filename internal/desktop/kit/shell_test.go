package kit

import (
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestTintOfMatchesTheBoard(t *testing.T) {
	for id, want := range map[string]int{"": 0, "f00000000e0aa": 10, "f00000000e0ab": 11, "f00000000e00d": 4, "f6b63e98d4af2": 3, "fda07bc0c8b0f": 5} {
		if got := TintOf(id); got != want {
			t.Errorf("TintOf(%q) = %d, want %d", id, got, want)
		}
	}
}

func TestContextFill(t *testing.T) {
	for pct, want := range map[int]float32{-5: 0, 0: 0, 50: 24, 100: 48, 250: 48} {
		if got := ContextFill(pct); got != want {
			t.Errorf("ContextFill(%d) = %v, want %v", pct, got, want)
		}
	}
}

func width(t *testing.T, tt *ui.Tester, label string) float32 {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("no %q in %q", label, tt.Texts())
	}
	return r.W
}

func TestRailIsFiftySixWideAndOpensToTwoThirtyTwo(t *testing.T) {
	open := false
	tt := ui.NewTester(func(c *ui.Context) {
		Rail(c, ColorsFor(false), open, func() { open = !open }, func() {
			Nav(c, ColorsFor(false), NavItem{Key: "a", Label: "Board", Icon: "board", Open: open})
		})
	}, 400, 300)
	if got := width(t, tt, "Pages and fleets"); got != 56 {
		t.Errorf("closed rail is %v wide", got)
	}
	if err := tt.Click("Show labels"); err != nil {
		t.Fatal(err)
	}
	if got := width(t, tt, "Pages and fleets"); got != 232 {
		t.Errorf("open rail is %v wide", got)
	}
	if !tt.HasText("Board") {
		t.Errorf("open rail shows no label in %q", tt.Texts())
	}
}

func nav(t *testing.T, n NavItem) *ui.Tester {
	k := ColorsFor(false)
	return ui.NewTester(func(c *ui.Context) {
		Rail(c, k, false, func() {}, func() { Nav(c, k, n) })
	}, 400, 300)
}

func TestNavBadgeShowsOnlyWithACount(t *testing.T) {
	if tt := nav(t, NavItem{Key: "a", Label: "Board", Icon: "board", Count: 8}); !tt.HasText("8") {
		t.Errorf("no count in %q", tt.Texts())
	}
	if tt := nav(t, NavItem{Key: "a", Label: "Board", Icon: "board"}); tt.HasText("0") {
		t.Errorf("count shown in %q", tt.Texts())
	}
}

func TestNavWorkingDotFollowsDot(t *testing.T) {
	if _, ok := nav(t, NavItem{Key: "a", Label: "Conversation", Icon: "chat", Dot: true}).Find("Working"); !ok {
		t.Error("no working dot")
	}
	if _, ok := nav(t, NavItem{Key: "a", Label: "Conversation", Icon: "chat"}).Find("Working"); ok {
		t.Error("working dot without Dot")
	}
}

func TestOnlyTheCurrentFleetHasAnInkBar(t *testing.T) {
	if _, ok := nav(t, NavItem{Key: "a", Label: "busy", Fleet: "f1", Current: true}).Find("Current fleet"); !ok {
		t.Error("no ink bar on the current fleet")
	}
	if _, ok := nav(t, NavItem{Key: "a", Label: "busy", Fleet: "f1"}).Find("Current fleet"); ok {
		t.Error("ink bar on another fleet")
	}
}

func TestMastheadIsFortyEightHighWithItsFleetTint(t *testing.T) {
	for _, dark := range []bool{false, true} {
		k := ColorsFor(dark)
		for _, id := range []string{"f00000000e0aa", "f00000000e0ab", "f00000000e00d"} {
			tt := ui.NewTester(func(c *ui.Context) {
				Masthead(c, k, id, "busy", func() {})
			}, 400, 200)
			if h := height(t, tt, "Masthead"); h != 48 {
				t.Errorf("masthead is %v high", h)
			}
			want := k.Tint[TintOf(id)]
			r, g, b, _ := tt.Image().At(200, 1).RGBA()
			if got := [3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}; got != [3]uint8{want.R, want.G, want.B} {
				t.Errorf("dark=%v %s: rule is %v, want %v", dark, id, got, want)
			}
		}
	}
}

func height(t *testing.T, tt *ui.Tester, label string) float32 {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("no %q", label)
	}
	return r.H
}

func TestContextBarIsFortyEightWide(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ContextBar(c, ColorsFor(false), 61, ColorsFor(false).Accent)
	}, 200, 100)
	if got := width(t, tt, "Context 61%"); got != 48 {
		t.Errorf("bar is %v wide", got)
	}
}

func tipShown(t *testing.T, n NavItem) bool {
	t.Helper()
	tt := nav(t, n)
	r, ok := tt.Find(n.Label)
	if !ok {
		t.Fatalf("no %q", n.Label)
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(2 * time.Second)
	seen := 0
	for _, s := range tt.Texts() {
		if s == n.Label {
			seen++
		}
	}
	return seen > 1
}

func TestAClosedRailNamesEveryItemOnHoverEvenAnInertOne(t *testing.T) {
	for _, inert := range []bool{false, true} {
		if !tipShown(t, NavItem{Key: "a", Label: "Companion", Icon: "phone", Inert: inert}) {
			t.Errorf("no tooltip, inert=%v", inert)
		}
	}
}

func inertRail(clicked *[]string) *ui.Tester {
	k := ColorsFor(false)
	return ui.NewTester(func(c *ui.Context) {
		Rail(c, k, false, func() {}, func() {
			for _, n := range []NavItem{
				{Key: "board", Label: "Board", Icon: "board"},
				{Key: "phone", Label: "Companion", Icon: "phone", Inert: true},
				{Key: "tasks", Label: "Tasks", Icon: "tasks"},
			} {
				if Nav(c, k, n) {
					*clicked = append(*clicked, n.Label)
				}
			}
		})
	}, 400, 300)
}

func TestAnInertItemIsNeverClicked(t *testing.T) {
	var clicked []string
	tt := inertRail(&clicked)
	if err := tt.Click("Companion"); err != nil {
		t.Fatal(err)
	}
	if len(clicked) != 0 {
		t.Errorf("clicked %v", clicked)
	}
	if err := tt.Click("Tasks"); err != nil || len(clicked) != 1 || clicked[0] != "Tasks" {
		t.Errorf("a live item does not click: %v %v", clicked, err)
	}
}

func TestTabSkipsAnInertItem(t *testing.T) {
	var clicked []string
	tt := inertRail(&clicked)
	seen := map[string]bool{}
	for range 6 {
		tt.Key(0, ui.KeyTab)
		for _, l := range []string{"Board", "Companion", "Tasks"} {
			if tt.Focused(l) {
				seen[l] = true
			}
		}
	}
	if seen["Companion"] {
		t.Error("Tab focused the inert Companion item")
	}
	if !seen["Board"] || !seen["Tasks"] {
		t.Errorf("Tab reached only %v", seen)
	}
}

func TestANavItemNameCarriesTheConnectionWordBeforeTheStatus(t *testing.T) {
	for n, want := range map[NavItem]string{
		{Label: "lab"}:                                       "lab",
		{Label: "lab", Status: "quiet"}:                      "lab, quiet",
		{Label: "lab", Link: "Host busy"}:                    "lab, Host busy",
		{Label: "lab", Link: "Connected", Status: "waiting"}: "lab, Connected, waiting",
	} {
		if got := n.Name(); got != want {
			t.Errorf("Name() = %q, want %q", got, want)
		}
	}
}

func TestAClosedRailDrawsTheLinkShapeAtTheLowerRightOfTheFleetMark(t *testing.T) {
	probe := ui.RGB(255, 0, 0)
	red := func(tt *ui.Tester, name string) (inLowerRight, elsewhere int) {
		t.Helper()
		r, ok := tt.Find(name)
		if !ok {
			t.Fatalf("no %q in %q", name, tt.Texts())
		}
		img := tt.Image()
		for y := int(r.Y); y < int(r.Y+r.H); y++ {
			for x := int(r.X); x < int(r.X+r.W); x++ {
				if p := img.RGBAAt(x, y); p.R > 240 && p.G < 20 && p.B < 20 {
					if x >= int(r.X+r.W/2) && y >= int(r.Y+r.H/2) {
						inLowerRight++
					} else {
						elsewhere++
					}
				}
			}
		}
		return
	}
	for _, dark := range []bool{false, true} {
		k := ColorsFor(dark)
		mk := func(link string) *ui.Tester {
			tt := ui.NewTester(func(c *ui.Context) {
				Rail(c, k, false, func() {}, func() {
					Nav(c, k, NavItem{Key: "f", Label: "lab", Fleet: "f00000000e0aa", Link: link, LinkIcon: "failure", LinkFG: probe})
				})
			}, 400, 300)
			tt.SetDark(dark)
			tt.Frame()
			return tt
		}
		with := mk("Can't reach host")
		if lr, other := red(with, "lab, Can't reach host"); lr == 0 || other != 0 {
			t.Errorf("dark=%v: closed rail draws %d link pixels at the lower right and %d elsewhere", dark, lr, other)
		}
		without := mk("")
		if lr, other := red(without, "lab"); lr+other != 0 {
			t.Errorf("dark=%v: a fleet with no link draws %d link pixels", dark, lr+other)
		}
	}
}

func TestMonograms(t *testing.T) {
	got := Monograms([]Named{{"b2", "Hand Dev"}, {"a1", "hand"}, {"c3", "Harbor"}, {"e5", "x"}, {"f6", ""}, {"g7", "日本語"}})
	want := map[string]string{"a1": "HA", "b2": "HD", "c3": "HA", "e5": "XE", "f6": "F6", "g7": "日"}
	if got["b2"] != "HD" || got["e5"] != "XE" || got["f6"] != "F6" || got["g7"] != "日" {
		t.Errorf("monograms = %v, want like %v", got, want)
	}
	seen := map[string]string{}
	for id, m := range got {
		if n := len([]rune(m)); n < 1 || n > 2 {
			t.Errorf("%s: %q is not one or two glyphs", id, m)
		}
		if other, dup := seen[m]; dup {
			t.Errorf("%q used by %s and %s", m, other, id)
		}
		seen[m] = id
	}
	rev := Monograms([]Named{{"g7", "日本語"}, {"f6", ""}, {"e5", "x"}, {"c3", "Harbor"}, {"b2", "Hand Dev"}, {"a1", "hand"}})
	if !maps.Equal(got, rev) {
		t.Errorf("order changes marks: %v vs %v", got, rev)
	}
}

func TestAMarkDoesNotChangeWhenAnotherFleetComesOrGoes(t *testing.T) {
	base := []Named{{"b2", "Hand Dev"}, {"a1", "api"}, {"c3", "Harbor"}}
	before := Monograms(base)
	for _, extra := range [][]Named{{{"d4", "docs"}}, {{"0z", "research"}, {"d4", "docs"}}, {{"d4", "Hand Dev"}}} {
		with := Monograms(append(append([]Named(nil), base...), extra...))
		for id, m := range before {
			if id == "b2" && extra[0].Name == "Hand Dev" {
				continue
			}
			if with[id] != m {
				t.Errorf("adding %v changed %s from %q to %q", extra, id, m, with[id])
			}
		}
		if len(with) != len(base)+len(extra) {
			t.Errorf("with %v: %v", extra, with)
		}
		back := Monograms(base)
		if !maps.Equal(back, before) {
			t.Errorf("removing %v changed marks: %v vs %v", extra, back, before)
		}
	}
}

func TestSameNamesGetDistinctMarksFromTheirOwnIDs(t *testing.T) {
	fleets := []Named{{"a1", "hand"}, {"b2", "ha"}, {"c3", "hand"}}
	got := Monograms(fleets)
	if len(map[string]bool{got["a1"]: true, got["b2"]: true, got["c3"]: true}) != 3 {
		t.Fatalf("marks collide: %v", got)
	}
	for id, m := range got {
		if []rune(m)[0] != 'H' {
			t.Errorf("%s: %q does not start with the name's letter", id, m)
		}
	}
	if again := Monograms([]Named{{"c3", "hand"}, {"a1", "hand"}, {"b2", "ha"}}); !maps.Equal(got, again) {
		t.Errorf("not deterministic: %v vs %v", got, again)
	}
}

func TestAWideMonogramFitsTheTile(t *testing.T) {
	k := ColorsFor(false)
	for _, mono := range []string{"WW", "MW", "日", "AB"} {
		tt := ui.NewTester(func(c *ui.Context) {
			Rail(c, k, true, func() {}, func() { Nav(c, k, NavItem{Key: "f", Label: "fleet", Fleet: "f1", Mono: mono, Open: true}) })
		}, 400, 300)
		tt.Frame()
		el, ok := tt.Find(mono)
		if !ok {
			t.Fatalf("%q not drawn in %q", mono, tt.Texts())
		}
		if el.W+4 > monoTile {
			t.Errorf("%q is %.0f px wide, the %d px tile needs 2 px each side", mono, el.W, monoTile)
		}
	}
}

func TestANavFleetDrawsItsMonogram(t *testing.T) {
	k := ColorsFor(false)
	tt := ui.NewTester(func(c *ui.Context) {
		Rail(c, k, false, func() {}, func() { Nav(c, k, NavItem{Key: "f", Label: "golden", Fleet: "f1", Mono: "GO"}) })
	}, 400, 300)
	tt.Frame()
	if !tt.HasText("GO") {
		t.Errorf("no monogram in %q", tt.Texts())
	}
}

func TestTheGearHasEightTeeth(t *testing.T) {
	if n := strings.Count(sources["gear"], "L"); n != 31 {
		t.Errorf("the gear outline has %d line segments, want 31 for 8 teeth", n)
	}
}

func TestFleetTip(t *testing.T) {
	if got := FleetTip("f00000000e0b1"); got != "Fleet ID f00000000e0b1" {
		t.Errorf("tip = %q", got)
	}
}

func hoverShows(tt *ui.Tester, target, want string) bool {
	el, ok := tt.Find(target)
	if !ok {
		return false
	}
	tt.Move(el.X+el.W/2, el.Y+el.H/2)
	tt.Advance(time.Second)
	tt.Frame()
	_, shown := tt.Find(want)
	return shown
}

func TestTheMastheadShowsTheFleetIDOnlyInATooltipOnTheName(t *testing.T) {
	k := ColorsFor(false)
	const id = "f00000000e0b1"
	tt := ui.NewTester(func(c *ui.Context) { Masthead(c, k, id, "golden", func() {}) }, 600, 200)
	tt.Frame()
	if tt.HasText(FleetTip(id)) || tt.HasText(id) {
		t.Errorf("the fleet ID is in the masthead: %q", tt.Texts())
	}
	if !hoverShows(tt, "golden", FleetTip(id)) {
		t.Errorf("no tooltip on the name, in %q", tt.Texts())
	}
}

func TestTheRailToggleNamesItselfOnHover(t *testing.T) {
	k := ColorsFor(false)
	for _, open := range []bool{false, true} {
		label := "Show labels"
		if open {
			label = "Hide labels"
		}
		tt := ui.NewTester(func(c *ui.Context) { Rail(c, k, open, func() {}, func() {}) }, 400, 300)
		tt.Frame()
		el, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no %q in %q", label, tt.Texts())
		}
		tt.Move(el.X+el.W/2, el.Y+el.H/2)
		tt.Advance(time.Second)
		tt.Frame()
		n := 0
		for _, s := range tt.Texts() {
			if s == label {
				n++
			}
		}
		if n < 2 {
			t.Errorf("open=%v: no tooltip, in %q", open, tt.Texts())
		}
	}
}

func TestTheMastheadOffersNoTipWithoutAFleetID(t *testing.T) {
	k := ColorsFor(false)
	tt := ui.NewTester(func(c *ui.Context) { Masthead(c, k, "", "golden", func() {}) }, 600, 200)
	tt.Frame()
	if hoverShows(tt, "golden", FleetTip("")) {
		t.Errorf("a tooltip with no fleet ID, in %q", tt.Texts())
	}
}

func TestEveryFleetGetsAMarkEvenPastTheFreeSlots(t *testing.T) {
	var fleets []Named
	for i := 0; i < 40; i++ {
		fleets = append(fleets, Named{fmt.Sprintf("f%02d", i), "hand"})
	}
	got := Monograms(fleets)
	seen := map[string]bool{}
	for _, f := range fleets {
		m := got[f.ID]
		if m == "" || []rune(m)[0] != 'H' {
			t.Errorf("%s: mark %q", f.ID, m)
		}
		seen[m] = true
	}
	if len(seen) != len(monoAlphabet) {
		t.Errorf("%d distinct marks, want %d", len(seen), len(monoAlphabet))
	}
	if !maps.Equal(got, Monograms(fleets)) {
		t.Error("not deterministic")
	}
}

func TestAUniqueMarkIsNeverTakenByAClashingFleet(t *testing.T) {
	fleets := []Named{{"u", "ha"}}
	for i := 0; i < len(monoAlphabet)-1; i++ {
		fleets = append(fleets, Named{fmt.Sprintf("f%02d", i), "hx"})
	}
	got := Monograms(fleets)
	if got["u"] != "HA" {
		t.Fatalf("unique mark = %q", got["u"])
	}
	seen := map[string]string{}
	for id, m := range got {
		if other, dup := seen[m]; dup {
			t.Errorf("%q used by %s and %s", m, other, id)
		}
		seen[m] = id
	}
}
