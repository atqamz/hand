package kit

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

type Role struct{ Size, Line float32 }

var (
	Display = Role{22, 30}
	Heading = Role{18, 24}
	Title   = Role{15, 22}
	Body    = Role{14, 20}
	Meta    = Role{12, 18}
)

var Message = Role{15, 24}

const MeasureCharacters = 72

// MeasureOf is board.css's 72ch in the face the window draws with: a ch is
// the advance of "0", which the system font and its hinting decide.
func MeasureOf(c *ui.Context) float32 {
	w, _ := c.MeasureText(1e6, ui.Span{Text: strings.Repeat("0", MeasureCharacters), Size: Message.Size})
	return w
}
