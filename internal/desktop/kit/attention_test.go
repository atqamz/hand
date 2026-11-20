package kit

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

type shape struct {
	n, w, h  int
	centreOn bool
}

func fleetMark(t *testing.T, dark, open bool, n NavItem, want ui.Color) (shape, *ui.Tester) {
	t.Helper()
	k := ColorsFor(dark)
	n.Key, n.Fleet, n.Open = "fleet:f1", "f1", open
	tt := ui.NewTester(func(c *ui.Context) {
		Rail(c, k, open, func() {}, func() { Nav(c, k, n) })
	}, 400, 300)
	r, ok := tt.Find(n.Name())
	if !ok {
		t.Fatalf("no mark named %q in %q", n.Name(), tt.Texts())
	}
	img := tt.Image()
	region := image.Rect(int(r.X+r.W)-24, int(r.Y), int(r.X+r.W), int(r.Y+r.H))
	near := func(a, b ui.Color) int {
		d := func(x, y uint8) int { return max(int(x)-int(y), int(y)-int(x)) }
		return d(a.R, b.R) + d(a.G, b.G) + d(a.B, b.B)
	}
	minX, minY, maxX, maxY, count := 1<<30, 1<<30, -1, -1, 0
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			p := img.RGBAAt(x, y)
			got := ui.RGBA(p.R, p.G, p.B, 1)
			if near(got, want) < near(got, k.Panel) && near(got, want) < 40 {
				count++
				minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
			}
		}
	}
	if count == 0 {
		return shape{}, tt
	}
	cx, cy := (minX+maxX)/2, (minY+maxY)/2
	p := img.RGBAAt(cx, cy)
	return shape{count, maxX - minX + 1, maxY - minY + 1, near(ui.RGBA(p.R, p.G, p.B, 1), want) < 40}, tt
}

func TestAMarkDrawsOneShapePerToneInBothThemesAndBothRails(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, open := range []bool{false, true} {
			k := ColorsFor(dark)
			name := map[bool]string{false: "light", true: "dark"}[dark] + map[bool]string{false: "-closed", true: "-open"}[open]
			t.Run(name, func(t *testing.T) {
				fail, _ := fleetMark(t, dark, open, NavItem{Label: "a", Tone: "fail", Status: "blocked"}, k.Fail)
				if fail.w < 9 || fail.w > 11 || fail.h < 9 || fail.h > 11 || !fail.centreOn {
					t.Errorf("fail = %+v, want a filled 10 px disc", fail)
				}
				wait, _ := fleetMark(t, dark, open, NavItem{Label: "a", Tone: "wait", Status: "waiting"}, k.Wait)
				if wait.w < 9 || wait.w > 11 || wait.h < 9 || wait.h > 11 || wait.centreOn || wait.n*10 > fail.n*7 {
					t.Errorf("wait = %+v (fail %+v), want a 10 px ring with an empty centre", wait, fail)
				}
				neutral, _ := fleetMark(t, dark, open, NavItem{Label: "a", Tone: "neutral", Status: "waiting"}, k.Muted)
				if neutral.w < 4 || neutral.w > 7 || neutral.h < 4 || neutral.h > 7 || !neutral.centreOn {
					t.Errorf("neutral = %+v, want a filled 6 px disc", neutral)
				}
				dash, _ := fleetMark(t, dark, open, NavItem{Label: "a", Tone: "unavailable", Status: "unavailable"}, k.Muted)
				if dash.w < 6 || dash.w > 9 || dash.h < 1 || dash.h > 3 {
					t.Errorf("unavailable = %+v, want an 8 by 2 dash", dash)
				}
				for _, c := range []ui.Color{k.Fail, k.Wait, k.Muted} {
					if none, _ := fleetMark(t, dark, open, NavItem{Label: "a", Status: "quiet"}, c); none.n != 0 {
						t.Errorf("a mark with no tone drew %d pixels of %v", none.n, c)
					}
				}
			})
		}
	}
}

func TestAMarkShapesDifferWithoutColour(t *testing.T) {
	k := ColorsFor(false)
	fail, _ := fleetMark(t, false, false, NavItem{Label: "a", Tone: "fail", Status: "blocked"}, k.Fail)
	wait, _ := fleetMark(t, false, false, NavItem{Label: "a", Tone: "wait", Status: "waiting"}, k.Wait)
	if fail.n == wait.n || fail.centreOn == wait.centreOn {
		t.Fatalf("fail %+v and wait %+v differ only by colour", fail, wait)
	}
}

func TestAMarkNamesItsStatusAndShowsItInTheTooltip(t *testing.T) {
	n := NavItem{Key: "fleet:f1", Fleet: "f1", Label: "golden", Tone: "fail", Status: "blocked"}
	if got := n.Name(); got != "golden, blocked" {
		t.Fatalf("name = %q", got)
	}
	if got := (NavItem{Label: "golden"}).Name(); got != "golden" {
		t.Fatalf("a mark with no status is named %q", got)
	}
	tt := nav(t, n)
	r, ok := tt.Find("golden, blocked")
	if !ok {
		t.Fatalf("no mark named golden, blocked in %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(2 * time.Second)
	seen := 0
	for _, s := range tt.Texts() {
		if s == "golden, blocked" {
			seen++
		}
	}
	if seen < 2 {
		t.Fatalf("the tooltip did not say golden, blocked: %q", tt.Texts())
	}
}

func TestAMarkNeverShowsADigit(t *testing.T) {
	_, tt := fleetMark(t, false, true, NavItem{Label: "a", Tone: "fail", Status: "blocked"}, ColorsFor(false).Fail)
	for _, s := range tt.Texts() {
		for _, r := range s {
			if r >= '0' && r <= '9' {
				t.Fatalf("a mark shows %q", s)
			}
		}
	}
}
