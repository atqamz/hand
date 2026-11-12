package kit

import (
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
		if !tipShown(t, NavItem{Key: "a", Label: "Phone", Icon: "phone", Inert: inert}) {
			t.Errorf("no tooltip, inert=%v", inert)
		}
	}
}
