package kit

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

const (
	AttachMax  = 280
	ChipRowMax = 160
	ShotWidth  = 240
	ShotHeight = 180
	dropChars  = 44
)

type ChipTone int

const (
	ChipReady ChipTone = iota
	ChipBusy
	ChipFail
)

type Chip struct {
	Key, Badge, Name, Meta, Remove string
	Tone                           ChipTone
	Retry                          bool
}

func kindPill(c *ui.Context, text string, fg, bg ui.Color) {
	ui.Row(c).Height(Badge).MinWidth(Badge).Padding(0, Space8).Radius(RadiusPill).Background(bg).
		Justify(ui.Center).AlignItems(ui.Center).Shrink(0).Label("kind " + text).Children(func() {
		ui.Text(c, text).FontSize(Meta.Size).Bold().TextColor(fg).SingleLine()
	})
}

func AttachChip(c *ui.Context, k Colors, ch Chip) (remove, retry bool) {
	edge, bg, nameFG, metaFG, metaLine, pillBG := k.Line, k.Panel, k.FG, k.Muted, float32(Body.Line), k.NeutralBG
	switch ch.Tone {
	case ChipBusy:
		nameFG = k.Muted
	case ChipFail:
		edge, bg, metaFG, metaLine, pillBG = k.Fail, k.FailBG, k.Fail, 16, k.BG
	}
	pill := k.Neutral
	if ch.Tone == ChipFail {
		pill = k.Fail
	}
	box := ui.Row(c).MaxWidth(AttachMax).MinWidth(0).Padding(Space2, Space2, Space2, Space8).Gap(Space8).
		AlignItems(ui.Center).Radius(RadiusMD).Border(1, edge).Background(bg).Clip().Label("Attachment " + ch.Name)
	box.Children(func() {
		kindPill(c, ch.Badge, pill, pillBG)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, ch.Name).FontSize(Body.Size).FixedLineHeight(Body.Line).TextColor(nameFG).SingleLine().Ellipsis("…")
			meta := ui.Text(c, ch.Meta).FontSize(Meta.Size).FixedLineHeight(metaLine).TextColor(metaFG)
			if ch.Tone == ChipFail {
				meta.Wrap()
			} else {
				meta.SingleLine().Ellipsis("…").FontFeatures("tnum")
			}
			if ch.Retry {
				again := ui.ButtonBase(c.Key(ch.Key + ":retry")).Height(Badge).AlignSelf(ui.Start).Cursor(ui.CursorPointer).
					AlignItems(ui.Center).Label("Retry " + ch.Name)
				again.Children(func() { ui.Text(c, "Retry").FontSize(Meta.Size).Bold().TextColor(k.Accent).SingleLine() })
				retry = again.Clicked()
			}
		})
		x := ui.ButtonBase(c.Key(ch.Key + ":remove")).Width(Control).Shrink(0).AlignSelf(ui.Stretch).Padding(0).
			Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(ch.Remove)
		if x.Hovered() {
			x.Background(k.Hover)
		}
		x.Children(func() { ui.Icon(c, Icon("close")).TextColor(k.Muted) })
		remove = x.Clicked()
	})
	return remove, retry
}

func IconButton(c *ui.Context, k Colors, key, label, icon string) bool {
	el := ui.ButtonBase(c.Key(key)).Size(Control, Control).Shrink(0).Radius(RadiusMD).Border(1, k.LineStrong).
		Background(k.BG).Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(label)
	fg := k.Muted
	if el.Hovered() {
		el.Background(k.Hover)
		fg = k.FG
	}
	el.Children(func() { ui.Icon(c, Icon(icon)).TextColor(fg) })
	return el.Clicked()
}

func ChipRow(c *ui.Context, chips func()) {
	ui.Scroll(c.Key("chips")).MaxHeight(ChipRowMax).Children(func() {
		ui.Row(c).Wrap().AlignItems(ui.Start).Gap(Space6).Padding(Space2, Space6, Space8).Label("Attachments").Children(chips)
	})
}

func SentFile(c *ui.Context, k Colors, key, badge, name, meta string, missing bool) bool {
	fg, metaFG := k.FG, k.Muted
	var el ui.Element
	if missing {
		fg = k.Muted
		el = ui.Row(c).Border(1, k.Line).BorderStyle(ui.BorderDashed)
	} else {
		el = ui.ButtonBase(c.Key(key)).Border(1, k.Line).Cursor(ui.CursorPointer)
		if el.Hovered() {
			el.BorderColor(k.LineStrong)
		}
	}
	el.MaxWidth(AttachMax).MinWidth(0).Padding(Space2, Space12, Space2, Space8).Gap(Space10).AlignItems(ui.Center).
		Radius(RadiusMD).Background(k.BG).Label(name)
	el.Children(func() {
		kindPill(c, badge, k.Neutral, k.NeutralBG)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, name).FontSize(Body.Size).FixedLineHeight(Body.Line).TextColor(fg).SingleLine().Ellipsis("…")
			ui.Text(c, meta).FontSize(Meta.Size).FixedLineHeight(Body.Line).TextColor(metaFG).SingleLine().Ellipsis("…").FontFeatures("tnum")
		})
	})
	return !missing && el.Clicked()
}

func SentPicture(c *ui.Context, k Colors, key string, img ui.ImageSource, name string) bool {
	iw, ih := ui.ImageSize(img)
	if iw <= 0 || ih <= 0 {
		return false
	}
	scale := min(1, (ShotWidth-2)/iw, (ShotHeight-2)/ih)
	el := ui.ButtonBase(c.Key(key)).Radius(RadiusMD).Border(1, k.Line).Background(k.Card).Clip().
		Cursor(ui.CursorPointer).Label("Attached image " + name)
	el.Children(func() { ui.Image(c, img).Size(iw*scale, ih*scale) })
	return el.Clicked()
}

func DropVeil(c *ui.Context, k Colors) {
	gw, _ := c.MeasureText(1e6, ui.Span{Text: strings.Repeat("0", dropChars), Size: Meta.Size})
	ui.Column(c).Absolute().Top(Space8).Left(Space8).Right(Space8).Bottom(Space8).Padding(Space24).Gap(Space6).
		Justify(ui.Center).AlignItems(ui.Center).Radius(RadiusLG).Border(2, k.Accent).BorderStyle(ui.BorderDashed).
		Background(k.BG.Alpha(.95)).IgnoreInput(true).Label("Drop to attach").Children(func() {
		ui.Row(c).Gap(Space8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, Icon("attach")).TextColor(k.Accent)
			ui.Text(c, "Drop to attach").FontSize(Message.Size).Bold().TextColor(k.Accent).SingleLine()
		})
		ui.Text(c, "Each file is copied to this fleet's inbox/, up to 10 MiB. Folders: paste their path.").
			FontSize(Meta.Size).TextColor(k.Muted).TextAlign(ui.Center).MaxWidth(gw)
	})
}
