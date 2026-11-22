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
	Tone, Status     string
	Link, LinkIcon   string
	LinkFG           ui.Color
}

func (n NavItem) Name() string {
	name := n.Label
	if n.Link != "" {
		name += ", " + n.Link
	}
	if n.Status != "" {
		name += ", " + n.Status
	}
	return name
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
	height := float32(Row)
	if n.Link != "" && n.Open {
		height = RowLG
	}
	el.Height(height).Radius(RadiusMD).Border(1, edge).Background(bg).AlignItems(ui.Center).Label(n.Name())
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
		switch {
		case n.Open && n.Link != "":
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, n.Label).FontSize(Body.Size).Bold().TextColor(fg).SingleLine().Ellipsis("…")
				ui.Row(c).Gap(Space4).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, Icon(n.LinkIcon)).Size(12, 12).TextColor(n.LinkFG)
					ui.Text(c, n.Link).FontSize(Meta.Size).TextColor(k.Muted).SingleLine().Ellipsis("…")
				})
			})
		case n.Open:
			ui.Text(c, n.Label).FontSize(Body.Size).Bold().TextColor(fg).SingleLine().Ellipsis("…").Grow(1)
		case n.Link != "":
			ui.Box(c).Absolute().Bottom(4).Right(4).Padding(1).Radius(RadiusPill).Background(k.Panel).Children(func() {
				ui.Icon(c, Icon(n.LinkIcon)).Size(12, 12).TextColor(n.LinkFG)
			})
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
		if n.Fleet != "" && n.Tone != "" {
			attentionDot(c, k, n.Tone, n.Open)
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
			tip.Children(func() { ui.Text(c, n.Name()).FontSize(Meta.Size) })
		})
	}
	return el.Clicked() && !n.Inert
}

func attentionDot(c *ui.Context, k Colors, tone string, open bool) {
	shape := func() {
		ui.Row(c).Size(10, 10).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			switch tone {
			case "fail":
				ui.Box(c).Size(10, 10).Radius(RadiusPill).Background(k.Fail)
			case "wait":
				ui.Box(c).Size(10, 10).Radius(RadiusPill).Border(2, k.Wait)
			case "neutral":
				ui.Box(c).Size(6, 6).Radius(RadiusPill).Background(k.Muted)
			case "unavailable":
				ui.Box(c).Size(8, 2).Radius(RadiusPill).Background(k.Muted)
			}
		})
	}
	if open {
		shape()
		return
	}
	ui.Box(c).Absolute().Top(5).Right(5).Padding(2).Radius(RadiusPill).Background(k.Panel).Children(shape)
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

func FieldLabel(c *ui.Context, k Colors, text string) {
	ui.Text(c, text).FontSize(Meta.Size).Bold().TextColor(k.Muted).Padding(Space4, 0, 0)
}

func Form(c *ui.Context, k Colors, body func()) {
	ui.Column(c).Gap(Space8).Padding(Space12, 0, 0).BorderWidth(1, 0, 0, 0).
		BorderColor(k.Line).Children(body)
}

func MenuPanel(c *ui.Context, k Colors, panel ui.Element, width, height float32) {
	panel.Width(width).MaxHeight(height).Margin(Space6, 0, 0, 0).Padding(Space12).
		Gap(Space8).Radius(RadiusLG).Background(k.BG).Border(1, k.Line).
		Shadow(0, 8, 24, 0, k.Shadow)
}

type Option struct{ Value, Label string }

func Select(c *ui.Context, k Colors, value *string, options []Option) {
	selectField(c, k, "", value, options)
}

func SelectLabel(c *ui.Context, k Colors, label string, value *string, options []Option) {
	selectField(c, k, label, value, options)
}

func selectField(c *ui.Context, k Colors, name string, value *string, options []Option) {
	sel := ui.SelectBase(c, value)
	label := ""
	for _, o := range options {
		if o.Value == *value {
			label = o.Label
		}
	}
	trigger := sel.Trigger.Height(Control).Padding(0, Space10).Gap(Space8).Radius(RadiusMD).
		Border(1, k.LineStrong).Background(k.BG).Justify(ui.SpaceBetween).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(label)
	trigger.Children(func() {
		if name != "" {
			ui.Text(c, name).FontSize(Meta.Size).Bold().TextColor(k.Muted).Shrink(0)
		}
		ui.Text(c, label).FontSize(Body.Size).SingleLine().Ellipsis("…").Grow(1)
		ui.Icon(c, Icon("chevron")).TextColor(k.Muted)
	})
	sel.Popup(func(panel ui.Element) {
		panel.Padding(Space4).Radius(RadiusMD).Background(k.BG).Border(1, k.Line).
			Shadow(0, 6, 20, 0, k.Shadow)
		for _, o := range options {
			option := o
			item := sel.Item(option.Value).Height(Control).Padding(0, Space10).Radius(RadiusSM).
				Cursor(ui.CursorPointer).Label(option.Label)
			if item.Highlighted() {
				item.Background(k.Hover)
			}
			item.Children(func() {
				ui.Text(c, option.Label).FontSize(Body.Size).SingleLine().Ellipsis("…")
			})
		}
	})
}

func Button(c *ui.Context, k Colors, key, label string, width float32, disabled bool) bool {
	el := ui.ButtonBase(c.Key(key)).Height(Control).Padding(0, Space10).Radius(RadiusMD).
		Border(1, k.LineStrong).Background(k.BG).Justify(ui.Center).AlignItems(ui.Center).
		Disabled(disabled).Cursor(ui.CursorPointer).Label(label)
	if width > 0 {
		el.Width(width)
	}
	el.Children(func() {
		ui.Text(c, label).FontSize(Body.Size).TextColor(k.FG).SingleLine()
	})
	return el.Clicked()
}

func PrimaryButton(c *ui.Context, k Colors, key, label string, width float32, disabled bool) bool {
	el := ui.ButtonBase(c.Key(key)).Height(Control).Padding(0, Space10).Radius(RadiusMD).
		Background(k.Primary).Justify(ui.Center).AlignItems(ui.Center).
		Disabled(disabled).Cursor(ui.CursorPointer).Label(label)
	if width > 0 {
		el.Width(width)
	}
	el.Children(func() {
		ui.Text(c, label).FontSize(Body.Size).Bold().TextColor(k.OnPrimary).SingleLine()
	})
	return el.Clicked()
}

func TextButton(c *ui.Context, k Colors, key, label string) bool {
	el := ui.ButtonBase(c.Key(key)).Height(Control).Padding(0, Space8).Radius(RadiusSM).
		Background(ui.Transparent).Cursor(ui.CursorPointer).AlignItems(ui.Center).Label(label)
	el.Children(func() {
		ui.Text(c, label).FontSize(Body.Size).TextColor(k.Accent).SingleLine()
	})
	return el.Clicked()
}

func Field(c *ui.Context, k Colors, label, placeholder string, value *string) {
	ui.Row(c).Height(Control).Grow(1).MinWidth(110).Padding(0, Space10).Gap(Space8).
		Radius(RadiusMD).Border(1, k.LineStrong).Background(k.BG).AlignItems(ui.Center).
		Label(label).Children(func() {
		ui.Text(c, label).FontSize(Meta.Size).Bold().TextColor(k.Muted).Shrink(0)
		ui.TextInput(c, value).Grow(1).MinWidth(0).Placeholder(placeholder).FontSize(Body.Size).
			BorderWidth(0, 0, 0, 0).Background(ui.Transparent).Padding(0)
	})
}
