package desktop_test

import (
	"fmt"
	"image"
	"math"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const (
	hitClickable   = 1 << 0
	hitFocusable   = 1 << 1
	hitEditable    = 1 << 2
	hitScrollY     = 1 << 5
	hitClip        = 1<<6 | 1<<7
	hitDisabled    = 1 << 9
	hitModal       = 1 << 22
	hitInteractive = hitClickable | hitFocusable | hitEditable
	layoutSlack    = 0.5
)

type tolerance struct {
	ink    float64
	linkW  float32
	linkH  float32
	textH  float32
	viewer bool
}

func tolerances() tolerance {
	if runtime.GOOS == "linux" {
		return tolerance{ink: linuxInk, linkW: 12, linkH: 16, textH: 12}
	}
	ink := darwinInk
	if runtime.GOOS == "windows" {
		ink = windowsInk
	}
	return tolerance{ink: ink, linkW: 12, linkH: 15, textH: 14, viewer: true}
}

const (
	linuxInk   = 4.5
	darwinInk  = 4.0
	windowsInk = 3.3
	chromeTop  = 100
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
	texts, all := readBoxes(tt)
	hits := without(all, known)
	var out []string
	win := ui.Rect{W: float32(width), H: float32(height)}
	div, hasDiv := tt.Find("Divider")
	edges := paneEdges(tt, width)
	if covered(hits, width, height) {
		edges = [][2]float32{{0, win.W}}
	}
	cross := func(what string, r ui.Rect) {
		if !within(r, 0, win.W) {
			out = append(out, fmt.Sprintf("%s %s leaves the %d px window", what, describe(r), width))
			return
		}
		if vertical && (r.Y < -layoutSlack || r.Y+r.H > win.H+layoutSlack) {
			out = append(out, fmt.Sprintf("%s %s leaves the %d px window", what, describe(r), height))
			return
		}
		cx := r.X + r.W/2
		for _, e := range edges {
			if cx >= e[0] && cx < e[1] && !within(r, e[0], e[1]) {
				out = append(out, fmt.Sprintf("%s %s crosses its pane %.0f to %.0f", what, describe(r), e[0], e[1]))
			}
		}
	}
	for _, h := range hits {
		if h.R.W <= 0 || h.R.H <= 0 || hasDiv && isDivider(h.R, div) {
			continue
		}
		if h.Flags&hitInteractive != 0 && !(h.R.W > 300 && h.R.H > 300) {
			cross("control", h.R)
		} else if h.Flags&hitClip != 0 && !(h.R.W >= win.W-1 && h.R.H >= win.H-1) {
			cross("panel", h.R)
		}
	}
	held := holders(texts)
	for i, x := range texts {
		if x.R.W > 0 && x.R.H > 0 && x.R.H <= 120 && held[i] == 0 {
			cross("text "+strconv.Quote(shorten(x.Text)), x.R)
		}
	}
	return out
}

func inChrome(h hitBox) bool { return h.R.Y+h.R.H/2 < chromeTop || inRail(h) }

func smallTargets(hits []hitBox, tol tolerance) []string {
	var out []string
	for _, h := range hits {
		if h.Flags&hitClickable == 0 || h.R.W <= 0 || h.R.H <= 0 {
			continue
		}
		minW, minH, what := tol.linkW, tol.linkH, "link"
		if inChrome(h) {
			minW, minH, what = kit.Control, kit.Control, "control"
		}
		if h.R.W < minW || h.R.H < minH {
			out = append(out, fmt.Sprintf("%s hit target %s under %.0fx%.0f px", what, describe(h.R), minW, minH))
		}
	}
	return out
}

const railWidth = 56

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

type layer struct {
	texts   []textBox
	hits    []hitBox
	total   int
	dropped int
	known   int
}

func sameRect(a, b ui.Rect) bool {
	return math.Abs(float64(a.X-b.X)) < 1 && math.Abs(float64(a.Y-b.Y)) < 1 && math.Abs(float64(a.W-b.W)) < 1 && math.Abs(float64(a.H-b.H)) < 1
}

func layerOf(tt *ui.Tester, known []textBox) (layer, string) {
	texts, hits := readBoxes(tt)
	v := layer{hits: hits, total: len(texts)}
	start := 0
	for i, h := range hits {
		if h.Flags&hitModal == 0 {
			continue
		}
		if i+1 >= len(hits) {
			return v, "a modal backdrop with no dialog after it"
		}
		found := -1
		for j, x := range texts {
			if sameRect(x.R, hits[i+1].R) {
				found = j
				break
			}
		}
		if found < 0 {
			return v, "a modal dialog with no label of its own"
		}
		start = found
	}
	v.dropped = start
	rest := withoutText(texts[start:], known)
	v.known = len(texts) - start - len(rest)
	v.texts = rest
	return v, ""
}

func (v layer) checked() int { return len(v.texts) }

func holders(texts []textBox) []int {
	n := make([]int, len(texts))
	for i, a := range texts {
		for j, b := range texts {
			if i != j && !sameRect(a.R, b.R) && a.R.W > 0 && b.R.W > 0 && inside(b.R, a.R) {
				n[i]++
			}
		}
	}
	return n
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

func clippedAway(x ui.Rect, hits []hitBox, width, height int) bool {
	if x.X >= float32(width) || x.Y >= float32(height) || x.X < 0 || x.Y < 0 {
		return true
	}
	for _, h := range hits {
		if h.Flags&(hitClip|hitScrollY) == 0 || h.R.W < 100 || h.R.H < 100 || x.X < h.R.X || x.X >= h.R.X+h.R.W {
			continue
		}
		if x.Y >= h.R.Y+h.R.H-layoutSlack || h.Flags&hitScrollY != 0 && x.Y <= h.R.Y+layoutSlack {
			return true
		}
	}
	return false
}

func atClipEdge(r ui.Rect, hits []hitBox) bool {
	for _, h := range hits {
		if h.Flags&(hitClip|hitScrollY) == 0 || h.R.W < 100 || h.R.H < 100 || r.X < h.R.X || r.X >= h.R.X+h.R.W {
			continue
		}
		if abs32(r.Y+r.H-(h.R.Y+h.R.H)) <= 1 || abs32(r.Y-h.R.Y) <= 1 {
			return true
		}
	}
	return false
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

func collapsed(v layer, width, height int) []string {
	var out []string
	for _, x := range v.texts {
		if (x.R.W <= 0 || x.R.H <= 0) && strings.TrimSpace(x.Text) != "" && !clippedAway(x.R, v.hits, width, height) {
			out = append(out, fmt.Sprintf("%q collapsed to %s in view", shorten(x.Text), describe(x.R)))
		}
	}
	return out
}

func wordsOf(s string) []string { return strings.Fields(strings.ToLower(s)) }

func inName(visible, name string) bool {
	v, n := wordsOf(visible), wordsOf(name)
	i := 0
	for _, w := range n {
		if i < len(v) && strings.HasPrefix(w, v[i]) || i < len(v) && strings.Contains(w, v[i]) {
			i++
		}
	}
	return len(v) > 0 && i == len(v)
}

func isControl(r ui.Rect, hits []hitBox) bool {
	for _, h := range hits {
		if h.Flags&hitInteractive != 0 && sameRect(h.R, r) {
			return true
		}
	}
	return false
}

func overprinted(v layer) []string {
	texts := v.texts
	held := holders(texts)
	exempt := func(inner, outer int) bool {
		o := texts[outer]
		return held[outer] >= 2 || o.R.H >= texts[inner].R.H+6 || inName(texts[inner].Text, o.Text) || isControl(o.R, v.hits) || atClipEdge(o.R, v.hits)
	}
	var out []string
	for i, a := range texts {
		if a.R.H > 60 || a.R.W <= 0 || a.R.H < 6 {
			continue
		}
		for j := i + 1; j < len(texts); j++ {
			b := texts[j]
			if b.R.H > 60 || b.R.W <= 0 || b.R.H < 6 || a.Text == b.Text {
				continue
			}
			w, h := overlap(a.R, b.R)
			if w <= 2 || h <= 2 || badge(a.Text) || badge(b.Text) || sameRect(a.R, b.R) {
				continue
			}
			if inside(a.R, b.R) && exempt(i, j) || inside(b.R, a.R) && exempt(j, i) {
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

func lowContrast(v layer, img *image.RGBA, tol tolerance) []string {
	held := holders(v.texts)
	var out []string
	for i, a := range v.texts {
		if disabledAt(v.hits, a.R) || held[i] > 0 {
			continue
		}
		if strings.TrimSpace(a.Text) == "" || a.R.H > 120 || a.R.W*a.R.H > 60000 || a.R.W <= 0 || a.R.H < tol.textH {
			continue
		}
		if r, ok := inkContrast(img, a.R); ok && r < tol.ink {
			out = append(out, fmt.Sprintf("%q %s contrast %.2f under %.1f", shorten(a.Text), describe(a.R), r, tol.ink))
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
