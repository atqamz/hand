package desktop

import (
	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const (
	webEngineMissing = " cannot be shown in this window: the system web engine is not available (WebKitGTK on Linux, WebView2 on Windows). Download the file to open it elsewhere."
	webPlaceholder   = "The system web view draws the file here. A scene cannot show a real web view."
	webMinHeight     = 360
)

type WebHandle struct {
	View  ui.NativeWebView
	Close func()
}

type webState struct {
	path   string
	handle WebHandle
	notice string
}

func (a *App) webKind(kind string) bool {
	return kind == "pdf"
}

func (a *App) webReady() bool {
	if a.OpenWeb == nil {
		return false
	}
	_, ok := a.Model.(DocFramer)
	return ok
}

func (a *App) openWeb(rel, noun string, f DocFrame) {
	h, err := a.OpenWeb(f)
	if err != nil {
		a.web = webState{path: rel, notice: noun + webEngineMissing}
		return
	}
	a.web = webState{path: rel, handle: h}
}

func (a *App) docWeb(c *ui.Context, k kit.Colors, p palette) {
	st := &a.doc
	switch {
	case st.row != "":
		a.docRow(c, k, st.row, false)
		return
	case a.web.path != a.Ref:
		a.quiet(c, p, "Reading…")
		return
	case a.web.notice != "":
		a.docRow(c, k, a.web.notice, false)
		return
	}
	_, h := c.Size()
	box := ui.Column(c).Height(max(h-360, webMinHeight)).Radius(kit.RadiusMD).Border(1, k.Line).
		Background(k.Card).Clip().Label("Document view")
	box.Children(func() {
		if a.web.handle.View == nil {
			ui.Column(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				ui.Text(c, webPlaceholder).FontSize(kit.Body.Size).TextColor(p.neutral)
			})
			return
		}
		ui.WebView(c, a.web.handle.View).Grow(1)
	})
}
