package desktop

import (
	"errors"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const (
	webEngineMissing = " cannot be shown in this window: the system web engine is not available (WebKitGTK on Linux, WebView2 on Windows). Download the file to open it elsewhere."
	webUnconfined    = " cannot be shown in this window: the web view could not be limited to the Board's address, so nothing was loaded. Download the file to open it elsewhere."
	webPlaceholder   = "The system web view draws the file here. A scene cannot show a real web view."
	webGone          = " stopped drawing: the web view's process ended. The source is shown instead. Choose Rendered to try again."
	webMinHeight     = 360
)

var ErrWebUnconfined = errors.New("the web view could not be limited to the Board's origin")

type WebHandle struct {
	View   ui.NativeWebView
	Close  func()
	OnGone func(func())
}

type webState struct {
	path   string
	handle WebHandle
	notice string
}

func (a *App) webKind(kind string) bool {
	return kind == "pdf" || kind == "html"
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
		notice := webEngineMissing
		if errors.Is(err, ErrWebUnconfined) {
			notice = webUnconfined
		}
		a.web = webState{path: rel, notice: noun + notice}
		return
	}
	a.web = webState{path: rel, handle: h}
	if h.OnGone != nil {
		gen := a.webGen
		h.OnGone(func() { a.later(func() { a.webEnded(rel, gen, noun) }) })
	}
}

func (a *App) later(f func()) {
	if a.Post != nil {
		a.Post(f)
		return
	}
	f()
}

func (a *App) webEnded(rel string, gen int, noun string) {
	if a.webGen != gen || a.web.path != rel || a.web.handle.Close == nil {
		return
	}
	a.releaseMedia()
	a.web = webState{path: rel, notice: noun + webGone}
}

func (a *App) htmlNotice() string {
	switch {
	case !a.webReady():
		return a.doc.row
	case a.web.path == a.Ref:
		return a.web.notice
	}
	return ""
}

func (a *App) htmlAsSource() bool {
	return a.DocSource || a.htmlNotice() != ""
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
