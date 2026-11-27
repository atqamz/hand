package kit

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func linear(c ui.Color, exp float64) float64 {
	var out [3]float64
	for i, v := range [3]uint8{c.R, c.G, c.B} {
		out[i] = math.Pow(float64(v)/255, exp)
	}
	return 0.2126729*out[0] + 0.7151522*out[1] + 0.072175*out[2]
}

func wcagRatio(fg, bg ui.Color) float64 {
	lum := func(c ui.Color) float64 {
		var out [3]float64
		for i, v := range [3]uint8{c.R, c.G, c.B} {
			s := float64(v) / 255
			if s <= 0.04045 {
				out[i] = s / 12.92
			} else {
				out[i] = math.Pow((s+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*out[0] + 0.7152*out[1] + 0.0722*out[2]
	}
	a, b := lum(fg), lum(bg)
	return (math.Max(a, b) + 0.05) / (math.Min(a, b) + 0.05)
}

func apcaLc(fg, bg ui.Color) float64 {
	soft := func(y float64) float64 {
		if y > 0.022 {
			return y
		}
		return y + math.Pow(0.022-y, 1.414)
	}
	yt, yb := soft(linear(fg, 2.4)), soft(linear(bg, 2.4))
	if math.Abs(yb-yt) < 0.0005 {
		return 0
	}
	if yb > yt {
		if s := (math.Pow(yb, 0.56) - math.Pow(yt, 0.57)) * 1.14; s >= 0.1 {
			return (s - 0.027) * 100
		}
		return 0
	}
	if s := (math.Pow(yb, 0.65) - math.Pow(yt, 0.62)) * 1.14; s <= -0.1 {
		return (s + 0.027) * 100
	}
	return 0
}

func TestColorsKeepWCAGContrast(t *testing.T) {
	type tone struct {
		name string
		get  func(Colors) ui.Color
	}
	text := []tone{
		{"FG", func(c Colors) ui.Color { return c.FG }}, {"Muted", func(c Colors) ui.Color { return c.Muted }},
		{"Neutral", func(c Colors) ui.Color { return c.Neutral }}, {"Accent", func(c Colors) ui.Color { return c.Accent }},
		{"Fail", func(c Colors) ui.Color { return c.Fail }}, {"Wait", func(c Colors) ui.Color { return c.Wait }},
		{"Pass", func(c Colors) ui.Color { return c.Pass }},
	}
	grounds := []tone{
		{"BG", func(c Colors) ui.Color { return c.BG }}, {"Panel", func(c Colors) ui.Color { return c.Panel }},
		{"Card", func(c Colors) ui.Color { return c.Card }}, {"Ticket", func(c Colors) ui.Color { return c.Ticket }},
		{"Hover", func(c Colors) ui.Color { return c.Hover }},
	}
	shellText := []tone{text[0], text[1], text[3]}
	type pair struct {
		name   string
		fg, bg func(Colors) ui.Color
		min    float64
	}
	var pairs []pair
	for _, bg := range grounds {
		for _, fg := range text {
			pairs = append(pairs, pair{fg.name + "/" + bg.name, fg.get, bg.get, 4.5})
		}
	}
	shell := tone{"Shell", func(c Colors) ui.Color { return c.Shell }}
	for _, fg := range shellText {
		pairs = append(pairs, pair{fg.name + "/Shell", fg.get, shell.get, 4.5})
	}
	for _, fg := range text[4:] {
		pairs = append(pairs, pair{fg.name + "/Shell shape", fg.get, shell.get, 3})
	}
	chips := []struct {
		fg, bg tone
	}{
		{text[3], tone{"AccentBG", func(c Colors) ui.Color { return c.AccentBG }}},
		{text[4], tone{"FailBG", func(c Colors) ui.Color { return c.FailBG }}},
		{text[5], tone{"WaitBG", func(c Colors) ui.Color { return c.WaitBG }}},
		{text[6], tone{"PassBG", func(c Colors) ui.Color { return c.PassBG }}},
		{text[2], tone{"NeutralBG", func(c Colors) ui.Color { return c.NeutralBG }}},
		{tone{"ScreenFG", func(c Colors) ui.Color { return c.ScreenFG }}, tone{"Screen", func(c Colors) ui.Color { return c.Screen }}},
		{tone{"OnPrimary", func(c Colors) ui.Color { return c.OnPrimary }}, tone{"Primary", func(c Colors) ui.Color { return c.Primary }}},
	}
	for _, p := range chips {
		pairs = append(pairs, pair{p.fg.name + "/" + p.bg.name, p.fg.get, p.bg.get, 4.5})
	}
	line := tone{"LineStrong", func(c Colors) ui.Color { return c.LineStrong }}
	for _, bg := range append(grounds[:4:4], shell) {
		pairs = append(pairs, pair{"LineStrong/" + bg.name, line.get, bg.get, 3})
	}
	for _, theme := range []struct {
		name string
		dark bool
	}{{"light", false}, {"dark", true}} {
		c := ColorsFor(theme.dark)
		for _, p := range pairs {
			got := wcagRatio(p.fg(c), p.bg(c))
			t.Logf("%s %s: %.2f", theme.name, p.name, got)
			if got < p.min {
				t.Errorf("%s %s: WCAG 2 contrast %.2f, want at least %.1f", theme.name, p.name, got, p.min)
			}
		}
	}
}

func TestAPCAKnownAnswers(t *testing.T) {
	black, white := ui.Hex("#000000"), ui.Hex("#ffffff")
	if got := apcaLc(black, white); math.Abs(got-106.04) > 0.01 {
		t.Errorf("black on white: Lc %.2f, want 106.04", got)
	}
	if got := apcaLc(white, black); math.Abs(got+107.88) > 0.01 {
		t.Errorf("white on black: Lc %.2f, want -107.88", got)
	}
}

func TestDarkSecondaryColorsReachAPCA60(t *testing.T) {
	c := ColorsFor(true)
	for _, fg := range []struct {
		name string
		c    ui.Color
		chip ui.Color
	}{{"Muted", c.Muted, c.NeutralBG}, {"Neutral", c.Neutral, c.NeutralBG}, {"Accent", c.Accent, c.AccentBG}, {"Fail", c.Fail, c.FailBG}} {
		for bgName, bg := range map[string]ui.Color{"BG": c.BG, "Panel": c.Panel, "Card": c.Card, "Hover": c.Hover, "chip": fg.chip} {
			if got := math.Abs(apcaLc(fg.c, bg)); got < 60 {
				t.Errorf("dark %s on %s: APCA Lc %.1f, want at least 60", fg.name, bgName, got)
			}
		}
	}
}
