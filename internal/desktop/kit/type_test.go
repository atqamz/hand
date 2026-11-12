package kit

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestMeasureHoldsTheBoardsCharacters(t *testing.T) {
	var measure, held, over float32
	ui.NewTester(func(c *ui.Context) {
		measure = MeasureOf(c)
		held, _ = c.MeasureText(1e6, ui.Span{Text: strings.Repeat("0", MeasureCharacters), Size: Message.Size})
		over, _ = c.MeasureText(1e6, ui.Span{Text: strings.Repeat("0", MeasureCharacters+1), Size: Message.Size})
	}, 200, 200)
	if held > measure || measure >= over {
		t.Errorf("the measure is %.1f; %d characters take %.1f and %d take %.1f, want the Board's %d held",
			measure, MeasureCharacters, held, MeasureCharacters+1, over, MeasureCharacters)
	}
}
