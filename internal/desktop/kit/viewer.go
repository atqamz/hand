package kit

import "github.com/egoist/mygo/ui"

const (
	ViewerMax      = 1200
	ViewerColumns  = 1000
	ViewerAside    = 300
	ViewerAsideGap = 40
	ViewerTop      = Control + 12
	SupStrip       = 40
)

type ViewerOptions struct {
	Name    string
	Strip   string
	Wide    bool
	OnBack  func()
	OnClose func()
	Notice  func(*ui.Context)
	Body    func(*ui.Context)
	Keys    func(ui.Element) ui.Element
}

func viewerContent(c *ui.Context, k Colors, v ViewerOptions) {
	viewerTop(c, k, v)
	if v.Notice != nil {
		v.Notice(c)
	}
	v.Body(c)
}

func ViewerPage(c *ui.Context, k Colors, v ViewerOptions) {
	outer := ui.Column(c).Grow(1).MinHeight(0).MinWidth(0).Background(k.BG).AlignItems(ui.Center)
	if v.Keys != nil {
		outer = v.Keys(outer)
	}
	outer.Children(func() {
		col := ui.Column(c).Grow(1).MinHeight(0).FillWidth().Label(v.Name)
		if !v.Wide {
			col.MaxWidth(ViewerMax)
		}
		col.Children(func() { viewerContent(c, k, v) })
	})
}

func viewerTop(c *ui.Context, k Colors, v ViewerOptions) {
	ui.Row(c).MinHeight(ViewerTop).Padding(Space6, Space8).Gap(Space8).
		AlignItems(ui.Center).Background(k.Panel).
		BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Children(func() {
		back := ui.ButtonBase(c.Key("viewer:back")).Size(Control, Control).Radius(RadiusMD).
			Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label("Back")
		back.Children(func() { ui.Icon(c, Icon("back")).TextColor(k.FG) })
		if back.Clicked() && v.OnBack != nil {
			v.OnBack()
		}
		strip := v.Strip
		if strip == "" {
			strip = v.Name
		}
		ui.Text(c, strip).Font(Mono).FontSize(Meta.Size).TextColor(k.FG).
			SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		close := ui.ButtonBase(c.Key("viewer:close")).Size(Control, Control).Radius(RadiusMD).
			Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Label("Close the viewer")
		close.Children(func() { ui.Icon(c, Icon("close")).TextColor(k.FG) })
		if close.Clicked() && v.OnClose != nil {
			v.OnClose()
		}
	})
}
