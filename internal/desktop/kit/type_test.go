package kit

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestMeasureHoldsTheBoardsCharacters(t *testing.T) {
	var measure, w72, w73 float32
	ui.NewTester(func(c *ui.Context) {
		measure = MeasureOf(c)
		w72, _ = c.MeasureText(1e6, ui.Span{Text: strings.Repeat("0", 72), Size: Message.Size})
		w73, _ = c.MeasureText(1e6, ui.Span{Text: strings.Repeat("0", 73), Size: Message.Size})
	}, 200, 200)
	if w72 > measure || measure >= w73 {
		t.Errorf("the measure is %.1f; 72 characters take %.1f and 73 take %.1f, want the Board's 72 held", measure, w72, w73)
	}
}
