package desktop

import (
	"math"

	"github.com/egoist/mygo/ui"
)

const (
	zoomDefault    = 1
	MinWindowWidth = 860
)

var zoomSteps = []int{25, 33, 50, 67, 75, 80, 90, 100, 110, 125, 150, 175, 200, 250, 300, 400, 500}

func zoomFactor(percent int) float32 { return float32(percent) / 100 }

func zoomRung(z float32) int {
	if !(z > 0) {
		z = zoomDefault
	}
	if z >= zoomFactor(zoomSteps[len(zoomSteps)-1]) {
		return len(zoomSteps) - 1
	}
	if z <= zoomFactor(zoomSteps[0]) {
		return 0
	}
	rung := 0
	for i := range zoomSteps {
		if math.Abs(float64(zoomFactor(zoomSteps[i])-z)) < math.Abs(float64(zoomFactor(zoomSteps[rung])-z)) {
			rung = i
		}
	}
	return rung
}

func zoomStep(z float32, steps int) float32 {
	rung := min(max(zoomRung(z)+steps, 0), len(zoomSteps)-1)
	return zoomFactor(zoomSteps[rung])
}

func (a *App) drawnZoom(c *ui.Context) float32 {
	if w, _ := c.Size(); w > 0 {
		return min(a.Zoom, w*c.Zoom()/MinWindowWidth)
	}
	return a.Zoom
}

func (a *App) zoomKeys(c *ui.Context) {
	switch {
	case keyZoomIn.pressed(c):
		a.setZoom(zoomStep(a.Zoom, 1))
	case keyZoomOut.pressed(c):
		a.setZoom(zoomStep(a.Zoom, -1))
	case keyZoomReset.pressed(c):
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
