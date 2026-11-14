package kit

import "github.com/egoist/mygo/ui"

const (
	ViewerMax      = 1200
	ViewerEdge     = 24
	ViewerColumns  = 1000
	ViewerAside    = 300
	ViewerAsideGap = 40
	ViewerTop      = Control + 12
)

// ViewerBox is the box's size in a window of w by h: no wider than 1200,
// and ViewerEdge from each edge.
func ViewerBox(w, h float32) (float32, float32) {
	return min(float32(ViewerMax), w-2*ViewerEdge), h - 2*ViewerEdge
}

type ViewerOptions struct {
	Name    string
	Back    bool
	OnBack  func()
	OnClose func()
	OnOpen  func()
	Notice  func(*ui.Context)
	Body    func(*ui.Context)
}

// Viewer draws the Board's viewer: the backdrop, the box and its strip.
// It reports whether a click outside the box or Escape dismissed it.
func Viewer(c *ui.Context, k Colors, v ViewerOptions) bool {
	open := true
	el := ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
		backdrop.Background(k.Backdrop)
		w, h := c.Size()
		bw, bh := ViewerBox(w, h)
		panel.Width(bw).Height(bh).Background(k.BG).Border(1, k.Line).
			Radius(RadiusLG).Clip().Label(v.Name).
			Shadow(0, 8, 24, 0, k.Shadow).Children(func() {
			ui.Column(c).FillHeight().Children(func() {
				viewerTop(c, k, v)
				if v.Notice != nil {
					v.Notice(c)
				}
				v.Body(c)
			})
		})
	})
	return el.Dismissed()
}

func viewerTop(c *ui.Context, k Colors, v ViewerOptions) {
	ui.Row(c).MinHeight(ViewerTop).Padding(Space6, Space8).Gap(Space8).
		AlignItems(ui.Center).Background(k.Panel).
		BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Children(func() {
		back := ui.ButtonBase(c.Key("viewer:back")).Size(Control, Control).Radius(RadiusMD).
			Justify(ui.Center).AlignItems(ui.Center).Label("Back").Disabled(!v.Back)
		fg := k.FG
		if v.Back {
			back.Cursor(ui.CursorPointer)
		} else {
			fg = k.Muted.Alpha(.55)
		}
		back.Children(func() { ui.Icon(c, Icon("back")).TextColor(fg) })
		if v.Back && back.Clicked() && v.OnBack != nil {
			v.OnBack()
		}
		ui.Text(c, v.Name).Font("monospace").FontSize(Meta.Size).TextColor(k.FG).
			SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		tab := ui.ButtonBase(c.Key("viewer:open")).Height(Control).Padding(0, Space12).
			Radius(RadiusMD).Border(1, k.LineStrong).Cursor(ui.CursorPointer).
			Label("Open as a page").Shrink(0)
		tab.Children(func() {
			ui.Text(c, "Open as a page").FontSize(Body.Size).Bold().TextColor(k.FG).SingleLine()
		})
		if tab.Clicked() && v.OnOpen != nil {
			v.OnOpen()
		}
		close := ui.ButtonBase(c.Key("viewer:close")).Size(Control, Control).Radius(RadiusMD).
			Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Label("Close the viewer")
		close.Children(func() { ui.Icon(c, Icon("close")).TextColor(k.FG) })
		if close.Clicked() && v.OnClose != nil {
			v.OnClose()
		}
	})
}
