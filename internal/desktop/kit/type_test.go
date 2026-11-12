package kit

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestMeasureHolds72Characters(t *testing.T) {
	var w72, w76 float32
	ui.NewTester(func(c *ui.Context) {
		w72, _ = c.MeasureText(1e6, ui.Span{Text: strings.Repeat("0", 72), Size: Message.Size})
		w76, _ = c.MeasureText(1e6, ui.Span{Text: strings.Repeat("0", 76), Size: Message.Size})
	}, 200, 200)
	if w72 > Measure {
		t.Errorf("72 characters take %.0f, more than the %d measure", w72, Measure)
	}
	if w76 <= Measure {
		t.Errorf("76 characters take %.0f, inside the %d measure", w76, Measure)
	}
}
