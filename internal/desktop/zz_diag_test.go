package desktop_test

import (
	"fmt"
	"math"
	"runtime"
	"testing"
)

func TestZZDiag(t *testing.T) {
	minInk, minTH := math.MaxFloat64, float32(999)
	var inkAt string
	minLW, minLH := float32(999), float32(999)
	var lowAt []string
	rulesOn, rulesOff := 0, 0
	var ruleSample []string
	for _, sc := range layoutScenes() {
		for _, sz := range layoutSizes {
			for _, dark := range []bool{false, true} {
				tt := sc.open(t, sz.w, sz.h, dark)
				v, _ := layerOf(tt, underlay(t, sc, sz, dark))
				img := tt.Image()
				held := holders(v.texts)
				for i, a := range v.texts {
					if held[i] > 0 || a.R.W <= 0 || a.R.H > 120 || a.R.W*a.R.H > 60000 || disabledAt(v.hits, a.R) {
						continue
					}
					if a.R.H < 14 {
						lowAt = append(lowAt, fmt.Sprintf("%s %s h=%.0f %q", sc.name, sz, a.R.H, shorten(a.Text)))
						continue
					}
					if r, ok := inkContrast(img, a.R); ok && r < minInk {
						minInk, inkAt = r, fmt.Sprintf("%s %s dark=%v %q", sc.name, sz, dark, a.Text)
					}
					minTH = min(minTH, a.R.H)
				}
				for _, h := range v.hits {
					if h.Flags&hitClickable != 0 && h.R.W > 0 && h.R.H > 0 && !inChrome(h) {
						minLW, minLH = min(minLW, h.R.W), min(minLH, h.R.H)
					}
				}
				on := doubledRules(img, dark, v.hits, tolerance{viewer: true})
				off := doubledRules(img, dark, v.hits, tolerance{})
				rulesOn += len(on)
				rulesOff += len(off)
				if len(off) > len(on) && len(ruleSample) < 6 {
					ruleSample = append(ruleSample, fmt.Sprintf("%s %s %v: %s", sc.name, sz, dark, off[0]))
				}
			}
		}
	}
	if len(lowAt) > 6 {
		lowAt = lowAt[:6]
	}
	t.Errorf("DIAG %s: min ink %.2f at %s; min text H %.0f; min link %.0fx%.0f; rules viewer-exempt %d none %d; sample %v; short texts %v", runtime.GOOS, minInk, inkAt, minTH, minLW, minLH, rulesOn, rulesOff, ruleSample, lowAt)
}
