package kit

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

const darkMarker = "@media (prefers-color-scheme:dark){"

var lengths = map[string]float32{
	"r-xs": RadiusXS, "r-sm": RadiusSM, "r-md": RadiusMD, "r-lg": RadiusLG, "r-pill": RadiusPill,
	"control": Control, "badge": Badge, "mast": Mast,
	"space-2": Space2, "space-4": Space4, "space-6": Space6, "space-8": Space8, "space-10": Space10,
	"space-12": Space12, "space-14": Space14, "space-16": Space16, "space-20": Space20, "space-24": Space24,
	"row-sm": RowSM, "row": Row, "tap": Tap, "row-lg": RowLG, "sheet": Sheet,
}

var declaration = regexp.MustCompile(`--([a-z0-9-]+):([^;}]+)`)

func boardCSS(t *testing.T) (light, dark string) {
	t.Helper()
	b, err := os.ReadFile("../../board/static/board.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	start := strings.Index(css, darkMarker)
	if start < 0 {
		t.Fatal("board.css has no dark block")
	}
	depth, end := 1, start+len(darkMarker)
	for ; end < len(css) && depth > 0; end++ {
		switch css[end] {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return css[:start] + css[end:], css[start+len(darkMarker) : end-1]
}

func rootTokens(t *testing.T, block string) map[string]string {
	t.Helper()
	i := strings.Index(block, ":root{")
	if i < 0 {
		t.Fatal("block has no :root")
	}
	root := block[i:]
	root = root[:strings.IndexByte(root, '}')]
	tokens := map[string]string{}
	for _, m := range declaration.FindAllStringSubmatch(root, -1) {
		tokens[m[1]] = m[2]
	}
	return tokens
}

func TestLengthsMatchBoardCSS(t *testing.T) {
	lightCSS, _ := boardCSS(t)
	tokens := rootTokens(t, lightCSS)
	for name, want := range lengths {
		raw, ok := tokens[name]
		if !ok {
			t.Errorf("--%s is not in board.css", name)
			continue
		}
		got, err := strconv.ParseFloat(strings.TrimSuffix(raw, "px"), 32)
		if err != nil || !strings.HasSuffix(raw, "px") || float32(got) != want {
			t.Errorf("kit %s = %g, board.css --%s = %s", name, want, name, raw)
		}
	}
}

func TestTypeRolesMatchBoardCSS(t *testing.T) {
	lightCSS, _ := boardCSS(t)
	for name, c := range map[string]struct {
		role Role
		rule string
	}{
		"Display": {Display, ".thread-title{overflow-wrap:anywhere;%s"},
		"Heading": {Heading, "h2{%s}"},
		"Title":   {Title, "h1{%s}"},
		"Meta":    {Meta, ".meta,time{color:var(--muted);%s}"},
	} {
		size := fmt.Sprintf("font-size:%gpx;line-height:%gpx", c.role.Size, c.role.Line)
		if want := fmt.Sprintf(c.rule, size); !strings.Contains(lightCSS, want) {
			t.Errorf("kit %s is %gpx on %gpx, board.css has no %q", name, c.role.Size, c.role.Line, want)
		}
	}
	if want := fmt.Sprintf("font:%gpx/%gpx var(--sans)", Body.Size, Body.Line); !strings.Contains(lightCSS, want) {
		t.Errorf("kit Body is %gpx on %gpx, board.css has no %q", Body.Size, Body.Line, want)
	}
	if want := fmt.Sprintf(".card .copy{font-size:%gpx;line-height:%g}", Message.Size, Message.Line/Message.Size); !strings.Contains(lightCSS, want) {
		t.Errorf("kit Message is %gpx on %gpx, board.css has no %q", Message.Size, Message.Line, want)
	}
}

func TestThePassPaletteKeepsItsRoles(t *testing.T) {
	for _, dark := range []bool{false, true} {
		k := ColorsFor(dark)
		name := map[bool]string{false: "light", true: "dark"}[dark]
		light := func(c ui.Color) float64 { return float64(c.R) + float64(c.G) + float64(c.B) }
		if dark {
			if !(light(k.Shell) > light(k.BG) && light(k.BG) > light(k.Panel)) {
				t.Errorf("%s: want Shell > BG > Panel in lightness, got %+v %+v %+v", name, k.Shell, k.BG, k.Panel)
			}
		} else {
			if k.BG != ui.Hex("#ffffff") || !(light(k.BG) > light(k.Panel) && light(k.Panel) > light(k.Shell)) {
				t.Errorf("%s: want white BG, then Panel, then the steel Shell, got %+v %+v %+v", name, k.BG, k.Panel, k.Shell)
			}
		}
		for _, c := range []struct {
			what string
			col  ui.Color
			ok   bool
		}{
			{"Fail is red", k.Fail, k.Fail.R > k.Fail.G && k.Fail.R > k.Fail.B},
			{"Wait is amber", k.Wait, k.Wait.R > k.Wait.G && k.Wait.G > k.Wait.B},
			{"Pass is green", k.Pass, k.Pass.G > k.Pass.R && k.Pass.G > k.Pass.B},
			{"Accent is blue", k.Accent, k.Accent.B > k.Accent.R && k.Accent.B > k.Accent.G},
		} {
			if !c.ok {
				t.Errorf("%s: %s, got %+v", name, c.what, c.col)
			}
		}
	}
}
