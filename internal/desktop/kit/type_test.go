package kit

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The Board holds the supervisor's card to 72 characters (board.css's
// 72ch); the measure is that width in pixels at the message size. The
// advance of the system font differs by platform (hinting rounds it on
// Linux, Core Text does not on macOS), so the count it holds is checked
// as the band those fonts fall in rather than as 72 on every one.
func TestMeasureHoldsTheBoardsCharacters(t *testing.T) {
	var w72 float32
	ui.NewTester(func(c *ui.Context) {
		w72, _ = c.MeasureText(1e6, ui.Span{Text: strings.Repeat("0", 72), Size: Message.Size})
	}, 200, 200)
	if n := Measure / (w72 / 72); n < 64 || n >= 76 {
		t.Errorf("the %d measure holds %.0f characters, want the Board's 72", Measure, n)
	}
}
