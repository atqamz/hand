package desktop_test

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
)

const (
	videoMissing = "A video cannot be shown in this window: the system web engine is not available (WebKitGTK on Linux, WebView2 on Windows). Download the file to open it elsewhere."
	videoGone    = "A video stopped playing: the web view's process ended. Download the file to open it elsewhere."
)

func TestAVideoOpensInTheWebViewAsAVideoFrameWithDownloadBeside(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		w := &fakeWeb{}
		app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
		app.OpenDoc(clipPath)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "demo.mp4", "3.0 MiB", pdfPlaceholder, "Document view", "Download", "Copy path")
		mustNotText(t, tt, "A video cannot be shown in this window.")
		if w.opens() != 1 {
			t.Fatalf("web views opened = %d, want 1", w.opens())
		}
		if f := w.frames[0]; f.Kind != "video" || f.URL != "http://127.0.0.1:4100/"+fleetID+"/api/v1/doc/"+clipPath+"?raw" {
			t.Fatalf("frame = %+v", f)
		}
	})
}

func TestAPDFFrameIsNotAVideoFrame(t *testing.T) {
	w := &fakeWeb{}
	app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
	app.OpenDoc(planPath)
	sized(t, app, windowWidth, windowHeight, false)
	if f := w.frames[0]; f.Kind != "pdf" {
		t.Fatalf("kind = %q, want pdf", f.Kind)
	}
}

func TestDownloadBesideAVideoSavesTheFile(t *testing.T) {
	w := &fakeWeb{}
	app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
	var asked []string
	app.SaveAs = func(name string) string { asked = append(asked, name); return "" }
	app.OpenDoc(clipPath)
	tt := sized(t, app, windowWidth, windowHeight, false)
	if err := tt.Click("Download"); err != nil {
		t.Fatalf("Download is not visible beside the video: %v", err)
	}
	tt.Frame()
	if len(asked) != 1 || asked[0] != "demo.mp4" {
		t.Fatalf("save dialog asked for %v", asked)
	}
}

func TestAVideoWithNoWebEngineShowsTheNoticeAndDownload(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		w := &fakeWeb{err: errors.New("no engine")}
		app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
		app.OpenDoc(clipPath)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, videoMissing, "Download")
		mustNotText(t, tt, pdfPlaceholder)
		if err := tt.Click("Download"); err != nil {
			t.Fatalf("Download: %v", err)
		}
	})
}

func TestAVideoWhoseProcessEndsOrLoadFailsShowsTheNoticeAndDownload(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		w := &fakeWeb{}
		app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
		app.OpenDoc(clipPath)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		w.end(0)
		tt.Frame()
		mustText(t, tt, videoGone, "Download")
		mustNotText(t, tt, pdfPlaceholder)
		mustNotText(t, tt, "Choose Rendered to try again")
		if w.closes() != 1 {
			t.Fatalf("closes = %d, want the ended view released once", w.closes())
		}
		text, ok := tt.Find(videoGone)
		btn, ok2 := tt.Find("Download")
		if !ok || !ok2 {
			t.Fatalf("notice found %t, Download found %t", ok, ok2)
		}
		if gap := btn.Y - (text.Y + text.H); gap < 0 || gap > kit.Space10 {
			t.Fatalf("Download is %.1f below the notice text, want it inside the notice box (at most %d)", gap, kit.Space10)
		}
	})
}

func TestAVideoWithNoSignalHookStillShowsTheViewAndDownload(t *testing.T) {
	app := webApp(t, &framedModel{fixture: kindsFixture(t)}, nil)
	app.OpenWeb = func(desktop.DocFrame) (desktop.WebHandle, error) { return desktop.WebHandle{}, nil }
	app.OpenDoc(clipPath)
	tt := sized(t, app, windowWidth, windowHeight, false)
	mustText(t, tt, pdfPlaceholder, "Download")
}

func TestScenesAVideoHasAWebViewPlaceholderAndDownload(t *testing.T) {
	w := &fakeWeb{}
	mediaScene(t, "139-doc-video-placeholder", clipPath, func(app *desktop.App) { app.OpenWeb = w.open },
		func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
			mustText(t, tt, "demo.mp4", "3.0 MiB", pdfPlaceholder, "Document view", "Download", "Copy path")
			inked(t, tt, pdfPlaceholder)
		})
}

func TestScenesAVideoWithNoWebEngine(t *testing.T) {
	w := &fakeWeb{err: errors.New("libwebkit2gtk-4.1.so.0: cannot open shared object file")}
	mediaScene(t, "140-doc-video-no-engine", clipPath, func(app *desktop.App) { app.OpenWeb = w.open },
		func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
			notice(t, tt, dark, videoMissing)
			mustText(t, tt, "demo.mp4", "3.0 MiB")
		})
}

func TestScenesAVideoWhoseWebViewEnded(t *testing.T) {
	w := &fakeWeb{}
	mediaScene(t, "141-doc-video-view-ended", clipPath, func(app *desktop.App) { app.OpenWeb = w.open },
		func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
			w.end(w.opens() - 1)
			tt.Frame()
			notice(t, tt, dark, videoGone)
		})
}
