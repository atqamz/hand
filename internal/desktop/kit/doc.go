package kit

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

const (
	DocBadge  = 48
	docGutter = 22
	docDigits = 8
)

func DocRow(c *ui.Context, k Colors, key, badge, name, meta string, plain bool) bool {
	fg, tone := k.FG, k.Neutral
	var el ui.Element
	if plain {
		fg = k.Muted
		el = ui.Row(c)
	} else {
		el = ui.ButtonBase(c.Key(key)).Cursor(ui.CursorPointer)
		if el.Hovered() {
			el.Background(k.Hover)
		}
	}
	el.MinHeight(Tap).Padding(0, Space4).Gap(Space10).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Label(name)
	el.Children(func() {
		ui.Row(c).Width(DocBadge).Height(Badge).Shrink(0).Radius(RadiusPill).Background(k.NeutralBG).
			Justify(ui.Center).AlignItems(ui.Center).Label("kind " + badge).Children(func() {
			ui.Text(c, badge).FontSize(Meta.Size).Bold().TextColor(tone).SingleLine()
		})
		ui.Text(c, name).FontSize(Body.Size).TextColor(fg).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if meta != "" {
			ui.Text(c, meta).FontSize(Meta.Size).TextColor(k.Muted).SingleLine().Shrink(0).FontFeatures("tnum")
		}
	})
	return !plain && el.Clicked()
}

func DocNotice(c *ui.Context, k Colors, text string, actions func()) {
	ui.Column(c).Gap(Space10).Padding(Space10, Space12).Radius(RadiusMD).
		Background(k.WaitBG).Border(1, k.Line).Children(func() {
		ui.Text(c, text).FontSize(Body.Size).FixedLineHeight(Body.Line)
		if actions != nil {
			ui.Row(c).Gap(Space8).Wrap().Children(actions)
		}
	})
}

func DocLines(c *ui.Context, k Colors, first int, lines []string) {
	last := first + len(lines) - 1
	width := max(len(strconv.Itoa(last)), 3)
	numbers := make([]string, len(lines))
	for i := range lines {
		numbers[i] = strings.Repeat(" ", width-len(strconv.Itoa(first+i))) + strconv.Itoa(first+i)
	}
	gw, _ := c.MeasureText(1e6, ui.Span{Text: strings.Repeat("0", docDigits), Font: "monospace", Size: Meta.Size})
	ui.Row(c).Padding(Space6, 0).Radius(RadiusMD).Background(k.Card).Border(1, k.Line).Clip().
		Label("Text").Children(func() {
		ui.Text(c, strings.Join(numbers, "\n")).Font("monospace").FontSize(Meta.Size).
			FixedLineHeight(Meta.Line).TextColor(k.Muted).NoWrap().Unselectable().
			Width(gw+docGutter).Padding(0, Space10, 0, Space12).Shrink(0).TextAlign(ui.End)
		ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, strings.Join(lines, "\n")).Font("monospace").FontSize(Meta.Size).
				FixedLineHeight(Meta.Line).NoWrap().Padding(0, Space12, 0, 0)
		})
	})
}

func DocPicture(c *ui.Context, k Colors, img ui.ImageSource, w, h float32, name string) {
	iw, ih := ui.ImageSize(img)
	if iw <= 0 || ih <= 0 {
		return
	}
	scale := min(1, (w-2)/iw, (h-2)/ih)
	ui.Box(c).AlignSelf(ui.Start).Radius(RadiusMD).Border(1, k.Line).Background(k.Card).Clip().
		Label(name).Children(func() {
		ui.Image(c, img).Size(iw*scale, ih*scale)
	})
}

func DocStep(c *ui.Context, k Colors, name string, next bool) bool {
	word, kind := "← Previous", "Previous"
	if next {
		word, kind = "Next →", "Next"
	}
	el := ui.ButtonBase(c.Key("doc:" + kind)).MinHeight(Control).Gap(Space8).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(kind + " " + name)
	el.Children(func() {
		ui.Text(c, word).FontSize(Body.Size).TextColor(k.Accent).SingleLine().Shrink(0)
		ui.Text(c, name).FontSize(Meta.Size).TextColor(k.Muted).SingleLine().Ellipsis("…").MinWidth(0)
	})
	return el.Clicked()
}
