package desktop_test

import (
	"fmt"
	"image"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unsafe"

	"github.com/egoist/mygo/ui"
)

const (
	hitClickable   = 1 << 0
	hitFocusable   = 1 << 1
	hitEditable    = 1 << 2
	hitDisabled    = 1 << 9
	hitInteractive = hitClickable | hitFocusable | hitEditable
	layoutSlack    = 0.5
)

type textBox struct {
	Text string
	R    ui.Rect
}

type hitBox struct {
	R     ui.Rect
	Flags uint32
}

func unexported(v reflect.Value, name string) reflect.Value {
	f := v.FieldByName(name)
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}

func rectOf(v reflect.Value) ui.Rect {
	return ui.Rect{
		X: float32(unexported(v, "X").Float()),
		Y: float32(unexported(v, "Y").Float()),
		W: float32(unexported(v, "W").Float()),
		H: float32(unexported(v, "H").Float()),
	}
}

func readBoxes(tt *ui.Tester) ([]textBox, []hitBox) {
	rt := unexported(reflect.ValueOf(tt).Elem(), "rt").Elem()
	var texts []textBox
	labels := unexported(rt, "labels")
	for i := 0; i < labels.Len(); i++ {
		l := labels.Index(i)
		texts = append(texts, textBox{unexported(l, "text").String(), rectOf(unexported(l, "r"))})
	}
	var hits []hitBox
	hv := unexported(rt, "hits")
	for i := 0; i < hv.Len(); i++ {
		h := hv.Index(i)
		hits = append(hits, hitBox{rectOf(unexported(h, "r")), uint32(unexported(h, "flags").Uint())})
	}
	return texts, hits
}

func inside(r, outer ui.Rect) bool {
	return r.X >= outer.X-layoutSlack && r.Y >= outer.Y-layoutSlack &&
		r.X+r.W <= outer.X+outer.W+layoutSlack && r.Y+r.H <= outer.Y+outer.H+layoutSlack
}

func within(r ui.Rect, left, right float32) bool {
	return r.X >= left-layoutSlack && r.X+r.W <= right+layoutSlack
}

func overlap(a, b ui.Rect) (w, h float32) {
	w = min(a.X+a.W, b.X+b.W) - max(a.X, b.X)
	h = min(a.Y+a.H, b.Y+b.H) - max(a.Y, b.Y)
	return w, h
}

func describe(r ui.Rect) string {
	return fmt.Sprintf("(%.0f,%.0f %.0fx%.0f)", r.X, r.Y, r.W, r.H)
}

func shorten(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 32 {
		return string(r[:32]) + "…"
	}
	return s
}

func paneEdges(tt *ui.Tester, width int) [][2]float32 {
	w := float32(width)
	div, ok := tt.Find("Divider")
	if !ok {
		return [][2]float32{{0, w}}
	}
	rail := float32(0)
	if r, ok := tt.Find("Pages and fleets"); ok {
		rail = r.X + r.W
	}
	return [][2]float32{{0, rail}, {rail, div.X + div.W/2}, {div.X + div.W/2, w}}
}

func isDivider(r ui.Rect, div ui.Rect) bool {
	return math.Abs(float64(r.X-div.X)) < 2 && math.Abs(float64(r.W-div.W)) < 2
}

func hitsOf(tt *ui.Tester) []hitBox {
	_, hits := readBoxes(tt)
	return hits
}

func without(hits, known []hitBox) []hitBox {
	left := map[hitBox]int{}
	for _, k := range known {
		left[k]++
	}
	var out []hitBox
	for _, h := range hits {
		if left[h] > 0 {
			left[h]--
			continue
		}
		out = append(out, h)
	}
	return out
}

func covered(hits []hitBox, width, height int) bool {
	for _, h := range hits {
		if h.R.W >= float32(width)-1 && h.R.H >= float32(height)-1 {
			return true
		}
	}
	return false
}

func noOverflow(tt *ui.Tester, width, height int, vertical bool, known []hitBox) []string {
	hits := without(hitsOf(tt), known)
	var out []string
	win := ui.Rect{W: float32(width), H: float32(height)}
	div, hasDiv := tt.Find("Divider")
	edges := paneEdges(tt, width)
	if covered(hits, width, height) {
		edges = [][2]float32{{0, win.W}}
	}
	for _, h := range hits {
		if h.Flags&hitInteractive == 0 || h.R.W <= 0 || h.R.H <= 0 {
			continue
		}
		if hasDiv && isDivider(h.R, div) {
			continue
		}
		if !within(h.R, 0, win.W) {
			out = append(out, fmt.Sprintf("control %s leaves the %d px window", describe(h.R), width))
			continue
		}
		if vertical && (h.R.Y < -layoutSlack || h.R.Y+h.R.H > win.H+layoutSlack) {
			out = append(out, fmt.Sprintf("control %s leaves the %d px window", describe(h.R), height))
			continue
		}
		if h.R.W > 300 && h.R.H > 300 {
			continue
		}
		cx := h.R.X + h.R.W/2
		for _, e := range edges {
			if cx >= e[0] && cx < e[1] && !within(h.R, e[0], e[1]) {
				out = append(out, fmt.Sprintf("control %s crosses its pane %.0f to %.0f", describe(h.R), e[0], e[1]))
			}
		}
	}
	return out
}

func smallTargets(tt *ui.Tester, minW, minH float32, keep func(hitBox) bool) []string {
	_, hits := readBoxes(tt)
	var out []string
	for _, h := range hits {
		if h.Flags&hitClickable == 0 || h.R.W <= 0 || h.R.H <= 0 {
			continue
		}
		if keep != nil && !keep(h) {
			continue
		}
		if h.R.W < minW || h.R.H < minH {
			out = append(out, fmt.Sprintf("hit target %s under %.0fx%.0f px", describe(h.R), minW, minH))
		}
	}
	return out
}

const (
	minHitWidth  = 8
	minHitHeight = 12
	railWidth    = 56
)

func inRail(h hitBox) bool { return h.R.X+h.R.W <= railWidth+layoutSlack }

func badge(s string) bool {
	if len(s) == 0 || len(s) > 3 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func topLayer(texts []textBox, width, height int) []textBox {
	for i, x := range texts {
		r := x.R
		if r.X >= 8 && r.Y >= 8 && r.X+r.W <= float32(width)-8 && r.Y+r.H <= float32(height)-8 && r.W*r.H >= float32(width*height)/2 {
			return texts[i:]
		}
	}
	return texts
}

func holdsAnother(texts []textBox, i int) bool {
	for j, b := range texts {
		if j != i && b.R != texts[i].R && inside(b.R, texts[i].R) {
			return true
		}
	}
	return false
}

func withoutText(texts, known []textBox) []textBox {
	left := map[textBox]int{}
	for _, k := range known {
		left[k]++
	}
	var out []textBox
	for _, x := range texts {
		if left[x] > 0 {
			left[x]--
			continue
		}
		out = append(out, x)
	}
	return out
}

func overprinted(tt *ui.Tester, width, height int, known []textBox) []string {
	texts, _ := readBoxes(tt)
	texts = withoutText(topLayer(texts, width, height), known)
	var out []string
	for i, a := range texts {
		if a.R.H > 60 || a.R.W <= 0 || a.R.H <= 0 {
			continue
		}
		for _, b := range texts[i+1:] {
			if b.R.H > 60 || b.R.W <= 0 || b.R.H <= 0 || a.Text == b.Text {
				continue
			}
			w, h := overlap(a.R, b.R)
			if w <= 2 || h <= 2 || badge(a.Text) || badge(b.Text) {
				continue
			}
			if inside(a.R, b.R) || inside(b.R, a.R) {
				continue
			}
			out = append(out, fmt.Sprintf("%q %s overprints %q %s", shorten(a.Text), describe(a.R), shorten(b.Text), describe(b.R)))
		}
	}
	return out
}

func luminance(r, g, b uint8) float64 {
	f := func(v uint8) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*f(r) + 0.7152*f(g) + 0.0722*f(b)
}

func ratio(a, b float64) float64 {
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

func inkContrast(img *image.RGBA, r ui.Rect) (float64, bool) {
	rect := image.Rect(int(r.X), int(r.Y), int(math.Ceil(float64(r.X+r.W))), int(math.Ceil(float64(r.Y+r.H)))).Intersect(img.Bounds())
	if rect.Empty() {
		return 0, false
	}
	counts := map[[3]uint8]int{}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			c := img.RGBAAt(x, y)
			counts[[3]uint8{c.R, c.G, c.B}]++
		}
	}
	var bg [3]uint8
	best := 0
	for c, n := range counts {
		if n > best || n == best && (c[0] < bg[0] || c[0] == bg[0] && c[1] < bg[1]) {
			bg, best = c, n
		}
	}
	if len(counts) < 2 {
		return 0, false
	}
	bl := luminance(bg[0], bg[1], bg[2])
	worst := 0.0
	for c := range counts {
		if r := ratio(luminance(c[0], c[1], c[2]), bl); r > worst {
			worst = r
		}
	}
	return worst, true
}

func disabledAt(hits []hitBox, r ui.Rect) bool {
	for _, h := range hits {
		if h.Flags&hitDisabled != 0 && inside(r, h.R) {
			return true
		}
	}
	return false
}

func lowContrast(tt *ui.Tester, width, height int, floor float64, known []textBox) []string {
	img := tt.Image()
	texts, hits := readBoxes(tt)
	texts = withoutText(topLayer(texts, width, height), known)
	var out []string
	for i, a := range texts {
		if disabledAt(hits, a.R) || holdsAnother(texts, i) {
			continue
		}
		if strings.TrimSpace(a.Text) == "" || a.R.H > 120 || a.R.W*a.R.H > 60000 || a.R.W < 3 || a.R.H < 14 {
			continue
		}
		if r, ok := inkContrast(img, a.R); ok && r < floor {
			out = append(out, fmt.Sprintf("%q %s contrast %.2f under %.1f", shorten(a.Text), describe(a.R), r, floor))
		}
	}
	return out
}

func report(t *testing.T, what string, found []string) {
	t.Helper()
	sort.Strings(found)
	for i, f := range found {
		if i == 5 {
			t.Errorf("%s: and %d more", what, len(found)-5)
			return
		}
		t.Errorf("%s: %s", what, f)
	}
}
