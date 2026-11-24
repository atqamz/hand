package desktop_test

import (
	"fmt"
	"image"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

func countNear(img image.Image, r image.Rectangle, c ui.Color, tol int) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			p := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			d := func(a, b uint8) int { return max(int(a)-int(b), int(b)-int(a)) }
			if d(p.R, c.R) <= tol && d(p.G, c.G) <= tol && d(p.B, c.B) <= tol {
				n++
			}
		}
	}
	return n
}

func bandRect(t *testing.T, tt *ui.Tester, label string) image.Rectangle {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok || r.W == 0 {
		t.Fatalf("no band %q in %q", label, brief(tt))
	}
	return image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H))
}

func TestTheMarkedLineKeepsItsNumberAndTextVisible(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			_, tt := openFind(t, findFixture(t), bigLog, dark)
			click(t, tt, "Go to line")
			tt.Type("300")
			click(t, tt, "Go")
			tt.Frame()
			r := bandRect(t, tt, "Line 300")
			img := tt.Image()
			bg := color.NRGBAModel.Convert(img.At(r.Max.X-30, r.Min.Y+9)).(color.NRGBA)
			base := ui.Color{R: bg.R, G: bg.G, B: bg.B}
			inkOf := func(x0, x1 int) int {
				rect := image.Rect(r.Min.X+x0, r.Min.Y, r.Min.X+x1, r.Max.Y)
				return rect.Dx()*rect.Dy() - countNear(img, rect, base, 90)
			}
			text, body := inkOf(10, 70), inkOf(80, 300)
			if text < 20 || body < 100 {
				t.Fatalf("the marked line is drawn blank: %d number pixels, %d text pixels", text, body)
			}
		})
	}
}

func TestTheCurrentHitIsPaintedInTheNowStyle(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			k := kit.ColorsFor(dark)
			_, tt := openFind(t, findFixture(t), bigLog, dark)
			search(t, tt, "error")
			tt.Frame()
			r := bandRect(t, tt, "Line 42")
			img := tt.Image()
			if n := countNear(img, r, k.Wait, 60); n < 100 {
				t.Fatalf("no Now-style background on the current hit: %d pixels", n)
			}
		})
	}
}
