package desktop_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

const (
	htmlMissing    = "An HTML page cannot be shown in this window: the system web engine is not available (WebKitGTK on Linux, WebView2 on Windows). Download the file to open it elsewhere."
	htmlUnconfined = "An HTML page cannot be shown in this window: the web view could not be limited to the Board's address, so nothing was loaded. Download the file to open it elsewhere."
	htmlGone       = "An HTML page stopped drawing: the web view's process ended. The source is shown instead. Choose Rendered to try again."
	htmlFrame      = "http://127.0.0.1:4100/" + fleetID + "/api/v1/doc/" + pagePath + "?raw&frame"
)

func htmlApp(t *testing.T, w *fakeWeb, source bool) (*desktop.App, *ui.Tester, *framedModel) {
	t.Helper()
	m := &framedModel{fixture: kindsFixture(t)}
	app := webApp(t, m, w)
	app.DocSource = source
	app.OpenDoc(pagePath)
	return app, sized(t, app, windowWidth, windowHeight, false), m
}

func TestAnHTMLPageOpensInTheWebViewWithTheFramedAddress(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		w := &fakeWeb{}
		app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
		app.OpenDoc(pagePath)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "preview.html", "177 B", pdfPlaceholder, "Rendered", "Source", "Download", "Copy path")
		mustNotText(t, tt, "An HTML page cannot be shown in this window.")
		if w.opens() != 1 {
			t.Fatalf("web views opened = %d, want 1", w.opens())
		}
		f := w.frames[0]
		if f.URL != htmlFrame || f.Cookie.Domain != "127.0.0.1" || f.Cookie.Name != "hand_board_"+fleetID {
			t.Fatalf("frame = %+v", f)
		}
		if got := tt.OpenedURLs(); len(got) != 0 {
			t.Fatalf("opened %v", got)
		}
	})
}

func TestAPDFFrameKeepsItsRawAddress(t *testing.T) {
	w := &fakeWeb{}
	app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
	app.OpenDoc(planPath)
	sized(t, app, windowWidth, windowHeight, false)
	if got := w.frames[0].URL; got != "http://127.0.0.1:4100/"+fleetID+"/api/v1/doc/"+planPath+"?raw" {
		t.Fatalf("pdf frame = %q", got)
	}
}

func TestTheSourceToggleReleasesTheWebViewAndRenderedOpensItAgain(t *testing.T) {
	w := &fakeWeb{}
	app, tt, _ := htmlApp(t, w, false)
	mustText(t, tt, pdfPlaceholder)
	click(t, tt, "Source")
	tt.Frame()
	if !app.DocSource || w.closes() != 1 {
		t.Fatalf("source = %v, closes = %d", app.DocSource, w.closes())
	}
	mustContain(t, tt, "<h1>Nightly export</h1>")
	mustNotText(t, tt, pdfPlaceholder)
	click(t, tt, "Rendered")
	tt.Frame()
	if app.DocSource || w.opens() != 2 || w.closes() != 1 {
		t.Fatalf("source = %v, opens = %d, closes = %d", app.DocSource, w.opens(), w.closes())
	}
	mustText(t, tt, pdfPlaceholder)
	mustNotText(t, tt, "Go to line")
}

func TestAPageOpenedInSourceNeverOpensAWebView(t *testing.T) {
	w := &fakeWeb{}
	_, tt, m := htmlApp(t, w, true)
	mustContain(t, tt, "<h1>Nightly export</h1>")
	mustNotText(t, tt, pdfPlaceholder)
	if w.opens() != 0 || m.frames != 0 {
		t.Fatalf("opens = %d, frames = %d", w.opens(), m.frames)
	}
}

func TestReloadingAnHTMLPageKeepsItsWebView(t *testing.T) {
	w := &fakeWeb{}
	app, _, m := htmlApp(t, w, false)
	app.Reload()
	if w.opens() != 1 || w.closes() != 0 || m.frames != 1 {
		t.Fatalf("opens = %d, closes = %d, frames = %d", w.opens(), w.closes(), m.frames)
	}
}

func TestTheWebViewIsReleasedWhenTheWindowLeavesTheHTMLPage(t *testing.T) {
	for name, leave := range map[string]func(*desktop.App){
		"back":         func(a *desktop.App) { a.Back() },
		"close viewer": func(a *desktop.App) { a.CloseViewer() },
		"other file":   func(a *desktop.App) { a.OpenDoc("reports/t1/design.md") },
		"other fleet":  func(a *desktop.App) { a.SelectFleet(secondID) },
		"window close": func(a *desktop.App) { a.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			w := &fakeWeb{}
			app, tt, _ := htmlApp(t, w, false)
			leave(app)
			tt.Frame()
			if w.closes() != 1 {
				t.Fatalf("closes = %d after leaving, want 1", w.closes())
			}
		})
	}
}

func TestAMissingWebEngineShowsTheNoticeAndTheSourceOfAnHTMLPage(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		w := &fakeWeb{err: errors.New("libwebkit2gtk-4.1.so.0: cannot open shared object file")}
		app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
		app.OpenDoc(pagePath)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "preview.html", htmlMissing, "Rendered", "Source", "Download", "Copy path", "Find")
		mustContain(t, tt, "<h1>Nightly export</h1>")
		mustNotText(t, tt, pdfPlaceholder)
		if got := tt.OpenedURLs(); len(got) != 0 {
			t.Fatalf("opened %v", got)
		}
	})
}

func TestAViewThatCannotBeConfinedShowsItsOwnNoticeAndTheSource(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		w := &fakeWeb{err: fmt.Errorf("%w: content rules are not available", desktop.ErrWebUnconfined)}
		app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
		app.OpenDoc(pagePath)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "preview.html", htmlUnconfined, "Rendered", "Source", "Download")
		mustNotText(t, tt, htmlMissing)
		mustContain(t, tt, "<h1>Nightly export</h1>")
	})
}

func TestTheSourceToggleIsLitOnSourceWhileANoticeShowsIt(t *testing.T) {
	for name, err := range map[string]error{
		"no engine":    errors.New("libwebkit2gtk-4.1.so.0: cannot open shared object file"),
		"not confined": desktop.ErrWebUnconfined,
	} {
		t.Run(name, func(t *testing.T) {
			w := &fakeWeb{err: err}
			app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
			app.OpenDoc(pagePath)
			tt := sized(t, app, windowWidth, windowHeight, false)
			if !has(tt, "Source, on") || !has(tt, "Rendered, off") {
				t.Fatalf("toggle: Source on %v, Rendered off %v", has(tt, "Source, on"), has(tt, "Rendered, off"))
			}
		})
	}
	w := &fakeWeb{}
	_, tt, _ := htmlApp(t, w, false)
	if !has(tt, "Rendered, on") || !has(tt, "Source, off") {
		t.Fatal("toggle with a web view: Rendered is not the lit one")
	}
}

func TestARenderProcessThatEndsLeavesTheNoticeAndTheSource(t *testing.T) {
	w := &fakeWeb{}
	app, tt, _ := htmlApp(t, w, false)
	mustText(t, tt, pdfPlaceholder)
	w.end(0)
	tt.Frame()
	if w.closes() != 1 {
		t.Fatalf("closes = %d, want the dead view released once", w.closes())
	}
	mustText(t, tt, htmlGone, "Find")
	mustContain(t, tt, "<h1>Nightly export</h1>")
	mustNotText(t, tt, pdfPlaceholder)
	click(t, tt, "Rendered")
	tt.Frame()
	if w.opens() != 2 {
		t.Fatalf("opens = %d, want a retry", w.opens())
	}
	mustText(t, tt, pdfPlaceholder)
	mustNotText(t, tt, htmlGone)
	app.Close()
}

func TestAReportFromAReleasedWebViewIsIgnored(t *testing.T) {
	w := &fakeWeb{}
	app, tt, _ := htmlApp(t, w, false)
	click(t, tt, "Source")
	tt.Frame()
	w.end(0)
	tt.Frame()
	mustNotText(t, tt, htmlGone)
	if w.closes() != 1 {
		t.Fatalf("closes = %d, want 1", w.closes())
	}
	click(t, tt, "Rendered")
	tt.Frame()
	w.end(0)
	tt.Frame()
	mustText(t, tt, pdfPlaceholder)
	mustNotText(t, tt, htmlGone)
	app.Close()
}

func TestAPDFWhoseProcessEndsShowsTheNotice(t *testing.T) {
	w := &fakeWeb{}
	app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
	app.OpenDoc(planPath)
	tt := sized(t, app, windowWidth, windowHeight, false)
	w.end(0)
	tt.Frame()
	mustText(t, tt, "A PDF stopped drawing: the web view's process ended.", "Download")
	mustNotText(t, tt, pdfPlaceholder)
}

func TestAnHTMLPageFindWorksOnTheSourceBehindTheNotice(t *testing.T) {
	w := &fakeWeb{err: errors.New("no engine")}
	app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
	app.OpenDoc(pagePath)
	tt := sized(t, app, windowWidth, windowHeight, false)
	search(t, tt, "manifest")
	mustText(t, tt, "1 of 1")
}

func TestScenesAnHTMLPageHasAWebViewPlaceholder(t *testing.T) {
	w := &fakeWeb{}
	mediaScene(t, "135-doc-html-placeholder", pagePath, func(app *desktop.App) { app.OpenWeb = w.open },
		func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
			mustText(t, tt, "preview.html", "177 B", pdfPlaceholder, "Document view", "Rendered", "Source", "Download")
			inked(t, tt, pdfPlaceholder)
		})
}

func TestScenesAnHTMLPageWithNoWebEngine(t *testing.T) {
	w := &fakeWeb{err: errors.New("libwebkit2gtk-4.1.so.0: cannot open shared object file")}
	mediaScene(t, "137-doc-html-no-engine", pagePath, func(app *desktop.App) { app.OpenWeb = w.open },
		func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
			notice(t, tt, dark, htmlMissing)
			mustContain(t, tt, "<h1>Nightly export</h1>")
			inked(t, tt, pageSource)
		})
}

func TestScenesAnHTMLPageWhoseWebViewEnded(t *testing.T) {
	w := &fakeWeb{}
	mediaScene(t, "138-doc-html-view-ended", pagePath, func(app *desktop.App) { app.OpenWeb = w.open },
		func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
			w.end(w.opens() - 1)
			tt.Frame()
			notice(t, tt, dark, htmlGone)
			mustContain(t, tt, "<h1>Nightly export</h1>")
			inked(t, tt, pageSource)
		})
}
