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
	type pair struct {
		name   string
		fg, bg func(Colors) ui.Color
		min    float64
	}
	grounds := map[string]func(Colors) ui.Color{
		"BG": func(c Colors) ui.Color { return c.BG }, "Panel": func(c Colors) ui.Color { return c.Panel },
		"Card": func(c Colors) ui.Color { return c.Card },
	}
	var pairs []pair
	for name, bg := range grounds {
		pairs = append(pairs,
			pair{"FG/" + name, func(c Colors) ui.Color { return c.FG }, bg, 4.5},
			pair{"Muted/" + name, func(c Colors) ui.Color { return c.Muted }, bg, 4.5},
			pair{"Accent/" + name, func(c Colors) ui.Color { return c.Accent }, bg, 4.5},
		)
	}
	pairs = append(pairs,
		pair{"Neutral/BG", func(c Colors) ui.Color { return c.Neutral }, grounds["BG"], 4.5},
		pair{"Fail/BG", func(c Colors) ui.Color { return c.Fail }, grounds["BG"], 3},
		pair{"Wait/BG", func(c Colors) ui.Color { return c.Wait }, grounds["BG"], 3},
		pair{"Pass/BG", func(c Colors) ui.Color { return c.Pass }, grounds["BG"], 3},
		pair{"Accent/AccentBG", func(c Colors) ui.Color { return c.Accent }, func(c Colors) ui.Color { return c.AccentBG }, 4.5},
		pair{"Fail/FailBG", func(c Colors) ui.Color { return c.Fail }, func(c Colors) ui.Color { return c.FailBG }, 4.5},
		pair{"Wait/WaitBG", func(c Colors) ui.Color { return c.Wait }, func(c Colors) ui.Color { return c.WaitBG }, 4.5},
		pair{"Pass/PassBG", func(c Colors) ui.Color { return c.Pass }, func(c Colors) ui.Color { return c.PassBG }, 4.5},
		pair{"Neutral/NeutralBG", func(c Colors) ui.Color { return c.Neutral }, func(c Colors) ui.Color { return c.NeutralBG }, 4.5},
		pair{"ScreenFG/Screen", func(c Colors) ui.Color { return c.ScreenFG }, func(c Colors) ui.Color { return c.Screen }, 4.5},
		pair{"OnPrimary/Primary", func(c Colors) ui.Color { return c.OnPrimary }, func(c Colors) ui.Color { return c.Primary }, 4.5},
		pair{"LineStrong/BG", func(c Colors) ui.Color { return c.LineStrong }, grounds["BG"], 3},
		pair{"LineStrong/Panel", func(c Colors) ui.Color { return c.LineStrong }, grounds["Panel"], 3},
	)
	for _, theme := range []struct {
		name string
		dark bool
	}{{"light", false}, {"dark", true}} {
		c := ColorsFor(theme.dark)
		for _, p := range pairs {
			if got := wcagRatio(p.fg(c), p.bg(c)); got < p.min {
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
