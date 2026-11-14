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
	BoardWidth    = 900
	Measure       = 880
	TabHeight     = 40
	SheetWidth    = 300
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
	if n.Inert {
		fg = k.Muted.Alpha(.55)
	}
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
	var el ui.Element
	if n.Inert {
		el = ui.Row(c).Cursor(ui.CursorDefault)
	} else {
		el = ui.ButtonBase(c.Key(n.Key)).Cursor(ui.CursorPointer)
	}
	el.Height(Row).Radius(RadiusMD).Border(1, edge).Background(bg).AlignItems(ui.Center).Label(n.Label)
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
	if !n.Open {
		ui.TooltipBase(c, el, func(tip ui.Element) {
			tip.AttachTo(el, ui.AnchorEnd, ui.AnchorStart).MarginStart(Space10)
			tip.Padding(3, Space8).Radius(RadiusSM).Background(k.FG).TextColor(k.BG)
			tip.Children(func() { ui.Text(c, n.Label).FontSize(Meta.Size) })
		})
	}
	return el.Clicked() && !n.Inert
}

func countPill(c *ui.Context, text string, bg, fg ui.Color) {
	ui.Row(c).Height(Badge).MinWidth(Badge).Padding(0, 5).Radius(RadiusPill).Background(bg).
		Justify(ui.Center).AlignItems(ui.Center).Children(func() {
		ui.Text(c, text).FontSize(Meta.Size).Bold().TextColor(fg).FontFeatures("tnum")
	})
}

func Keycap(c *ui.Context, k Colors, text string) {
	ui.Row(c).Height(Badge).Padding(0, Space6).Radius(RadiusSM).BorderWidth(1, 1, 2, 1).
		BorderColor(k.Line).AlignItems(ui.Center).Label(text).Children(func() {
		ui.Text(c, text).Font("monospace").FontSize(Meta.Size).TextColor(k.Muted).SingleLine()
	})
}

func Masthead(c *ui.Context, k Colors, id, name string, trailing func()) {
	ui.Column(c).Height(Mast).BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Label("Masthead").Children(func() {
		ui.Box(c).Height(3).Background(k.Tint[TintOf(id)])
		mastheadRow(c, k, id, name, Space24, nil, trailing)
	})
}

type Tab struct {
	Key, Label       string
	Current          bool
	Count            int
	CountBG, CountFG ui.Color
	Dot              bool
}

func MenuButton(c *ui.Context, k Colors) bool {
	b := ui.ButtonBase(c.Key("nav:menu")).Size(Row, Row).Radius(RadiusMD).
		Background(ui.Transparent).Justify(ui.Center).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label("Menu")
	b.Children(func() { ui.Icon(c, Icon("menu")).TextColor(k.Muted) })
	return b.Clicked()
}

func MastheadTabs(c *ui.Context, k Colors, id, name string, onMenu func(), trailing func(), tabs []Tab, pick func(string)) {
	h := float32(Mast)
	if len(tabs) > 0 {
		h += TabHeight
	}
	ui.Column(c).Height(h).BorderWidth(0, 0, 1, 0).BorderColor(k.Line).
		Label("Masthead").Children(func() {
		ui.Box(c).Height(3).Background(k.Tint[TintOf(id)])
		mastheadRow(c, k, id, name, Space12, onMenu, trailing)
		if len(tabs) > 0 {
			Tabs(c, k, tabs, pick)
		}
	})
}

func mastheadRow(c *ui.Context, k Colors, id, name string, pad float32, onMenu, trailing func()) {
	tint := k.Tint[TintOf(id)]
	ui.Row(c).Grow(1).Padding(0, pad).Gap(Space12).AlignItems(ui.Center).Children(func() {
		if onMenu != nil && MenuButton(c, k) {
			onMenu()
		}
		ui.Icon(c, Icon("mark")).Size(22, 22).TextColor(tint)
		ui.Text(c, name).FontSize(Title.Size).LineHeight(Title.Line).Bold().SingleLine().Ellipsis("…")
		if id != "" {
			ui.Text(c, id).Font("monospace").FontSize(Meta.Size).TextColor(k.Muted).SingleLine()
		}
		ui.Spacer(c)
		trailing()
	})
}

func Tabs(c *ui.Context, k Colors, tabs []Tab, pick func(string)) {
	ui.Row(c).Height(TabHeight).BorderWidth(1, 0, 0, 0).BorderColor(k.Line).Label("Tabs").Children(func() {
		for _, t := range tabs {
			tab := t
			fg := k.Muted
			if tab.Current {
				fg = k.FG
			}
			el := ui.ButtonBase(c.Key(tab.Key)).Grow(1).Height(TabHeight).
				Justify(ui.Center).AlignItems(ui.Center).Gap(Space6).
				Background(k.BG).Cursor(ui.CursorPointer).Label(tab.Label)
			el.Children(func() {
				ui.Text(c, tab.Label).FontSize(Body.Size).Bold().TextColor(fg).SingleLine()
				if tab.Count > 0 {
					countPill(c, fmt.Sprint(tab.Count), tab.CountBG, tab.CountFG)
				}
				if tab.Dot {
					ui.Box(c).Size(6, 6).Radius(RadiusPill).Background(k.Accent).Label("Working")
				}
				if tab.Current {
					ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(2).Background(k.FG)
				}
			})
			if el.Clicked() && pick != nil {
				pick(tab.Key)
			}
		}
	})
}

func NavSheet(c *ui.Context, k Colors, open *bool, body func()) bool {
	el := ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(k.Backdrop).AlignItems(ui.Start)
		w, _ := c.Size()
		panel.Absolute().Left(0).Top(0).Bottom(0).Width(min(SheetWidth, w*.86)).
			Background(k.Panel).BorderWidth(0, 1, 0, 0).BorderColor(k.Line).
			Label("Pages and fleets")
		panel.Children(func() {
			ui.Scroll(c).Grow(1).Padding(Space16, Space12, Space16, Space16).Children(func() {
				ui.Column(c).Gap(Space2).Children(body)
			})
		})
	})
	return el.Dismissed()
}
