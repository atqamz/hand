package kit

import (
	"fmt"
	"os"
	"reflect"
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

var unmirrored = []string{"doc-bg", "pane", "screen-fade", "screen-fade-fg", "screen-fade-fg-clear", "screen-thumb", "veil"}

var (
	declaration = regexp.MustCompile(`--([a-z0-9-]+):([^;}]+)`)
	tintRule    = regexp.MustCompile(`\.tint-(\d+)\{--tint:(#[0-9a-f]{6})\}`)
	rgbaValue   = regexp.MustCompile(`^rgba\((\d+),(\d+),(\d+),(\.?\d+)\)$`)
	slashValue  = regexp.MustCompile(`^rgb\((\d+) (\d+) (\d+) / (\.?\d+)\)$`)
)

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

func cssColor(t *testing.T, v string) ui.Color {
	t.Helper()
	n := func(s string) uint8 { x, _ := strconv.Atoi(s); return uint8(x) }
	if m := rgbaValue.FindStringSubmatch(v); m != nil {
		a, _ := strconv.ParseFloat(m[4], 32)
		return ui.RGBA(n(m[1]), n(m[2]), n(m[3]), float32(a))
	}
	if m := slashValue.FindStringSubmatch(v); m != nil {
		a, _ := strconv.ParseFloat(m[4], 32)
		return ui.RGBA(n(m[1]), n(m[2]), n(m[3]), float32(a))
	}
	return ui.Hex(v)
}

func tints(t *testing.T, block string) map[int]ui.Color {
	t.Helper()
	out := map[int]ui.Color{}
	for _, m := range tintRule.FindAllStringSubmatch(block, -1) {
		i, _ := strconv.Atoi(m[1])
		out[i] = ui.Hex(m[2])
	}
	return out
}

func TestColorsMatchBoardCSS(t *testing.T) {
	lightCSS, darkCSS := boardCSS(t)
	light := rootTokens(t, lightCSS)
	dark := map[string]string{}
	for k, v := range light {
		dark[k] = v
	}
	for k, v := range rootTokens(t, darkCSS) {
		dark[k] = v
	}
	lightTints := tints(t, lightCSS)
	darkTints := tints(t, darkCSS)
	mirrored := map[string]bool{}
	for _, name := range unmirrored {
		if _, ok := light[name]; !ok {
			t.Errorf("--%s is on the unmirrored list but not in board.css", name)
		}
		mirrored[name] = true
	}
	for _, th := range []struct {
		name   string
		dark   bool
		tokens map[string]string
		tints  map[int]ui.Color
	}{{"light", false, light, lightTints}, {"dark", true, dark, darkTints}} {
		v := reflect.ValueOf(ColorsFor(th.dark))
		for i := range v.NumField() {
			name := v.Type().Field(i).Tag.Get("css")
			if name == "" {
				if v.Type().Field(i).Name != "Tint" {
					t.Errorf("Colors.%s has no css tag", v.Type().Field(i).Name)
				}
				continue
			}
			mirrored[name] = true
			raw, ok := th.tokens[name]
			if !ok {
				t.Errorf("%s: --%s is not in board.css", th.name, name)
				continue
			}
			if got, want := v.Field(i).Interface().(ui.Color), cssColor(t, raw); got != want {
				t.Errorf("%s: kit %s = %+v, board.css --%s = %s", th.name, v.Type().Field(i).Name, got, name, raw)
			}
		}
		for name, raw := range th.tokens {
			if (strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "rgb")) && !mirrored[name] {
				t.Errorf("%s: board.css --%s = %s is neither in kit nor on the unmirrored list", th.name, name, raw)
			}
		}
		if len(th.tints) != 12 {
			t.Fatalf("%s: board.css has %d tints, want 12", th.name, len(th.tints))
		}
		for i, got := range ColorsFor(th.dark).Tint {
			if got != th.tints[i] {
				t.Errorf("%s: kit Tint[%d] = %+v, board.css .tint-%d = %+v", th.name, i, got, i, th.tints[i])
			}
		}
	}
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
