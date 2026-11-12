package kit

import (
	"fmt"
	"hash/fnv"

	"github.com/egoist/mygo/ui"
)

const (
	RailWidth     = 56
	RailWidthOpen = 232
	ContextWidth  = 48
	railPad       = 7
)

func TintOf(id string) int {
	if id == "" {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return int(h.Sum32() % uint32(len(Colors{}.Tint)))
}

func ContextFill(pct int) float32 {
	return float32(min(max(pct, 0), 100)) * ContextWidth / 100
}

func ContextBar(c *ui.Context, k Colors, pct int, fill ui.Color) {
	ui.Box(c).Width(ContextWidth).Height(6).Radius(RadiusPill).Background(k.Line).
		Label(fmt.Sprintf("Context %d%%", pct)).Children(func() {
		if w := ContextFill(pct); w > 0 {
			ui.Box(c).Width(w).Height(6).Radius(RadiusPill).Background(fill)
		}
	})
}

func Rail(c *ui.Context, k Colors, open bool, toggle func(), body func()) {
	w, label := float32(RailWidth), "Show labels"
	if open {
		w, label = RailWidthOpen, "Hide labels"
	}
	ui.Column(c).Width(w).FillHeight().Background(k.Panel).
		BorderWidth(0, 1, 0, 0).BorderColor(k.Line).Label("Pages and fleets").Children(func() {
		ui.Row(c).Height(RowLG).Padding(0, railPad).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Children(func() {
			b := ui.ButtonBase(c.Key("rail-toggle")).Size(Row, Row).Radius(RadiusMD).
				Background(ui.Transparent).Justify(ui.Center).AlignItems(ui.Center).
				Cursor(ui.CursorPointer).Label(label)
			b.Children(func() { ui.Icon(c, Icon("sidebar")).TextColor(k.Muted) })
			if b.Clicked() {
				toggle()
			}
		})
		ui.Scroll(c).Grow(1).Padding(Space8, railPad, Space12).Children(func() {
			ui.Column(c).Gap(Space2).Children(body)
		})
	})
}

func RailHead(c *ui.Context, k Colors, open bool, text string) {
	if open {
		ui.Text(c, text).FontSize(Meta.Size).Bold().TextColor(k.Muted).Padding(Space16, Space12, Space6)
		return
	}
	ui.Box(c).Height(1).Background(k.Line).Margin(Space10, Space4)
}

type NavItem struct {
	Key, Label, Icon string
	Open, Current    bool
	Inert            bool
	Count            int
	CountBG, CountFG ui.Color
	Dot              bool
	Fleet            string
}

func Nav(c *ui.Context, k Colors, n NavItem) bool {
	fg := k.Muted
	edge := ui.Transparent
	bg := ui.Transparent
	if n.Current {
		fg, edge, bg = k.FG, k.Line, k.BG
	}
	if n.Fleet != "" {
		fg, bg, edge = k.Muted, ui.Transparent, ui.Transparent
		if n.Current {
			fg = k.FG
		}
	}
	el := ui.ButtonBase(c.Key(n.Key)).Height(Row).Radius(RadiusMD).Border(1, edge).Background(bg).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(n.Label).Disabled(n.Inert)
	if n.Open {
		el.Gap(Space12).Padding(0, 11)
	} else {
		el.Width(Row).Justify(ui.Center)
	}
	el.Children(func() {
		if n.Fleet != "" {
			ui.Icon(c, Icon("mark")).Size(18, 18).TextColor(k.Tint[TintOf(n.Fleet)])
		} else {
			ui.Icon(c, Icon(n.Icon)).TextColor(fg)
		}
		if n.Open {
			ui.Text(c, n.Label).FontSize(Body.Size).Bold().TextColor(fg).SingleLine().Ellipsis("…").Grow(1)
		}
		switch {
		case n.Count > 0 && n.Open:
			countPill(c, fmt.Sprint(n.Count), n.CountBG, n.CountFG)
		case n.Count > 0:
			ui.Box(c).Absolute().Top(-8).Right(-8).Padding(2).Radius(RadiusPill).Background(k.Panel).Children(func() {
				countPill(c, fmt.Sprint(n.Count), n.CountBG, n.CountFG)
			})
		case n.Dot && n.Open:
			ui.Box(c).Size(6, 6).Radius(RadiusPill).Background(k.Accent).Label("Working")
		case n.Dot:
			ui.Box(c).Absolute().Top(7).Right(7).Size(10, 10).Radius(RadiusPill).Background(k.Panel).Label("Working").Children(func() {
				ui.Box(c).Absolute().Top(2).Left(2).Size(6, 6).Radius(RadiusPill).Background(k.Accent)
			})
		}
		if n.Fleet != "" && n.Current {
			ui.Box(c).Absolute().Left(-railPad + 1).Top(10).Bottom(10).Width(3).
				Background(k.FG).Label("Current fleet")
		}
	})
	if !n.Open && !n.Inert {
		ui.TooltipBase(c, el, func(tip ui.Element) {
			tip.AttachTo(el, ui.AnchorEnd, ui.AnchorStart).MarginStart(Space10)
			tip.Padding(3, Space8).Radius(RadiusSM).Background(k.FG).TextColor(k.BG)
			tip.Children(func() { ui.Text(c, n.Label).FontSize(Meta.Size) })
		})
	}
	return el.Clicked()
}

func countPill(c *ui.Context, text string, bg, fg ui.Color) {
	ui.Row(c).Height(Badge).MinWidth(Badge).Padding(0, 5).Radius(RadiusPill).Background(bg).
		Justify(ui.Center).AlignItems(ui.Center).Children(func() {
		ui.Text(c, text).FontSize(Meta.Size).Bold().TextColor(fg).FontFeatures("tnum")
	})
}

func Masthead(c *ui.Context, k Colors, id, name string, trailing func()) {
	tint := k.Tint[TintOf(id)]
	ui.Column(c).Height(Mast).BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Label("Masthead").Children(func() {
		ui.Box(c).Height(3).Background(tint)
		ui.Row(c).Grow(1).Padding(0, Space24).Gap(Space12).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, Icon("mark")).Size(22, 22).TextColor(tint)
			ui.Text(c, name).FontSize(Title.Size).LineHeight(Title.Line).Bold().SingleLine().Ellipsis("…")
			if id != "" {
				ui.Text(c, id).Font("monospace").FontSize(Meta.Size).TextColor(k.Muted).SingleLine()
			}
			ui.Spacer(c)
			trailing()
		})
	})
}
