package desktop

import (
	"math"

	"github.com/egoist/mygo/ui"
)

const zoomDefault = 1

// zoomSteps is the ladder a browser steps through, in percent.
var zoomSteps = []int{25, 33, 50, 67, 75, 80, 90, 100, 110, 125, 150, 175, 200, 250, 300, 400, 500}

func zoomFactor(percent int) float32 { return float32(percent) / 100 }

// zoomRung is the ladder rung nearest z, 100% for a value that is no zoom.
func zoomRung(z float32) int {
	if !(z > 0) {
		z = zoomDefault
	}
	rung := 0
	for i := range zoomSteps {
		if math.Abs(float64(zoomFactor(zoomSteps[i])-z)) < math.Abs(float64(zoomFactor(zoomSteps[rung])-z)) {
			rung = i
		}
	}
	return rung
}

// zoomStep is the rung by steps beyond z, held at the ladder's ends.
func zoomStep(z float32, steps int) float32 {
	rung := min(max(zoomRung(z)+steps, 0), len(zoomSteps)-1)
	return zoomFactor(zoomSteps[rung])
}

func (a *App) zoomKeys(c *ui.Context) {
	in := c.Shortcut(ui.Ctrl, ui.KeyEqual)
	plus := c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyEqual)
	out := c.Shortcut(ui.Ctrl, ui.KeyMinus)
	reset := c.Shortcut(ui.Ctrl, ui.Key0)
	switch {
	case in || plus:
		a.setZoom(zoomStep(a.Zoom, 1))
	case out:
		a.setZoom(zoomStep(a.Zoom, -1))
	case reset:
		a.setZoom(zoomDefault)
	}
}

func (a *App) setZoom(z float32) {
	if z == a.Zoom {
		return
	}
	a.Zoom = z
	a.savePrefs()
}
