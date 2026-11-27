package desktop_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
)

const (
	engineMissing  = "A PDF cannot be shown in this window: the system web engine is not available (WebKitGTK on Linux, WebView2 on Windows). Download the file to open it elsewhere."
	pdfPlaceholder = "The system web view draws the file here. A scene cannot show a real web view."
)

type framedModel struct {
	*fixture
	fmu    sync.Mutex
	err    error
	frames int

	entered chan struct{}
	gate    chan struct{}
}

func (m *framedModel) DocFrame(ctx context.Context, fleet, path string) (desktop.DocFrame, error) {
	if m.entered != nil {
		m.entered <- struct{}{}
		<-m.gate
	}
	m.fmu.Lock()
	defer m.fmu.Unlock()
	m.frames++
	if m.err != nil {
		return desktop.DocFrame{}, m.err
	}
	return desktop.DocFrame{
		Host:   "local",
		URL:    "http://127.0.0.1:4100/" + fleet + "/api/v1/doc/" + path + "?raw",
		Cookie: desktop.WebCookie{Name: "hand_board_" + fleet, Value: "tok", Domain: "127.0.0.1"},
	}, nil
}

func (m *framedModel) setErr(err error) { m.fmu.Lock(); m.err = err; m.fmu.Unlock() }

type fakeWeb struct {
	mu     sync.Mutex
	frames []desktop.DocFrame
	closed int
	err    error
	gone   []func()
}

func (w *fakeWeb) open(f desktop.DocFrame) (desktop.WebHandle, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.frames = append(w.frames, f)
	if w.err != nil {
		return desktop.WebHandle{}, w.err
	}
	return desktop.WebHandle{
		Close:  func() { w.mu.Lock(); w.closed++; w.mu.Unlock() },
		OnGone: func(fn func()) { w.mu.Lock(); w.gone = append(w.gone, fn); w.mu.Unlock() },
	}, nil
}

func (w *fakeWeb) end(i int) { w.mu.Lock(); fn := w.gone[i]; w.mu.Unlock(); fn() }

func (w *fakeWeb) opens() int  { w.mu.Lock(); defer w.mu.Unlock(); return len(w.frames) }
func (w *fakeWeb) closes() int { w.mu.Lock(); defer w.mu.Unlock(); return w.closed }

func webApp(t *testing.T, m *framedModel, w *fakeWeb) *desktop.App {
	t.Helper()
	app := desktop.New(func() (desktop.ReadModel, error) { return m, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	if app.Err != "" {
		t.Fatalf("boot: %s", app.Err)
	}
	app.SaveAs = func(string) string { return "" }
	if w != nil {
		app.OpenWeb = w.open
	}
	return app
}

func TestAPDFOpensInTheWebViewWithItsFrame(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		w := &fakeWeb{}
		app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
		app.OpenDoc(planPath)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "plan.pdf", "51 KiB", pdfPlaceholder, "Download", "Copy path")
		mustNotText(t, tt, "A PDF cannot be shown in this window.")
		if w.opens() != 1 {
			t.Fatalf("web views opened = %d, want 1", w.opens())
		}
		f := w.frames[0]
		if f.URL != "http://127.0.0.1:4100/"+fleetID+"/api/v1/doc/"+planPath+"?raw" || f.Cookie.Domain != "127.0.0.1" || f.Cookie.Name != "hand_board_"+fleetID {
			t.Fatalf("frame = %+v", f)
		}
	})
}

func TestTheWebViewIsReleasedWhenTheWindowLeavesThePDF(t *testing.T) {
	for name, leave := range map[string]func(*desktop.App){
		"back":         func(a *desktop.App) { a.Back() },
		"close viewer": func(a *desktop.App) { a.CloseViewer() },
		"other file":   func(a *desktop.App) { a.OpenDoc("reports/t1/design.md") },
		"other fleet":  func(a *desktop.App) { a.SelectFleet(secondID) },
		"window close": func(a *desktop.App) { a.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			w := &fakeWeb{}
			app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
			app.OpenDoc(planPath)
			tt := sized(t, app, windowWidth, windowHeight, false)
			if w.closes() != 0 {
				t.Fatalf("closed %d times while the page is open", w.closes())
			}
			leave(app)
			tt.Frame()
			if w.closes() != 1 {
				t.Fatalf("closes = %d after leaving, want 1", w.closes())
			}
		})
	}
}

func TestReloadingAPDFPageKeepsItsWebView(t *testing.T) {
	w := &fakeWeb{}
	m := &framedModel{fixture: kindsFixture(t)}
	app := webApp(t, m, w)
	app.OpenDoc(planPath)
	app.Reload()
	if w.opens() != 1 || w.closes() != 0 || m.frames != 1 {
		t.Fatalf("opens = %d, closes = %d, frames = %d after a reload", w.opens(), w.closes(), m.frames)
	}
}

func TestAMissingWebEngineShowsTheNoticeAndTheSavePath(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		w := &fakeWeb{err: errors.New("libwebkit2gtk-4.1.so.0: cannot open shared object file")}
		app := webApp(t, &framedModel{fixture: kindsFixture(t)}, w)
		app.OpenDoc(planPath)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "plan.pdf", engineMissing, "Download", "Copy path")
		mustNotText(t, tt, pdfPlaceholder)
		if w.closes() != 0 {
			t.Fatal("closed a view that never opened")
		}
		app.Back()
		tt.Frame()
	})
}

func TestAWindowWithNoWebHookShowsTheOldNotice(t *testing.T) {
	app := webApp(t, &framedModel{fixture: kindsFixture(t)}, nil)
	app.OpenDoc(planPath)
	tt := sized(t, app, windowWidth, windowHeight, false)
	mustText(t, tt, "A PDF cannot be shown in this window.", "Download")
}

func TestAModelThatCannotFrameShowsTheOldNotice(t *testing.T) {
	w := &fakeWeb{}
	app := desktop.New(func() (desktop.ReadModel, error) { return kindsFixture(t), nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	app.OpenWeb = w.open
	app.OpenDoc(planPath)
	tt := sized(t, app, windowWidth, windowHeight, false)
	mustText(t, tt, "A PDF cannot be shown in this window.")
	if w.opens() != 0 {
		t.Fatal("opened a web view without a frame")
	}
}

func TestAFrameErrorClearsWhenRetrySucceeds(t *testing.T) {
	w := &fakeWeb{}
	m := &framedModel{fixture: kindsFixture(t), err: errors.New("the board is busy")}
	app := webApp(t, m, w)
	app.OpenDoc(planPath)
	tt := sized(t, app, windowWidth, windowHeight, false)
	mustText(t, tt, "Retry")
	mustNotText(t, tt, pdfPlaceholder)
	m.setErr(nil)
	click(t, tt, "Retry")
	tt.Frame()
	mustText(t, tt, pdfPlaceholder)
	mustNotText(t, tt, "Retry")
	if w.opens() != 1 {
		t.Fatalf("opens = %d, want 1", w.opens())
	}
}

func gatedWeb(t *testing.T) (*framedModel, *fakeWeb, *desktop.App, chan func()) {
	t.Helper()
	m := &framedModel{fixture: kindsFixture(t), entered: make(chan struct{}, 1), gate: make(chan struct{})}
	w := &fakeWeb{}
	app := webApp(t, m, w)
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	return m, w, app, posted
}

func waitFrame(t *testing.T, m *framedModel) {
	t.Helper()
	select {
	case <-m.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the window never asked for the frame")
	}
}

func settleWebAnswer(t *testing.T, m *framedModel, posted chan func()) {
	t.Helper()
	close(m.gate)
	select {
	case fn := <-posted:
		fn()
	case <-time.After(10 * time.Second):
		t.Fatal("the window never posted the load")
	}
}

func TestAWebAnswerThatArrivesAfterLeavingOpensNoView(t *testing.T) {
	m, w, app, posted := gatedWeb(t)
	app.OpenDoc(planPath)
	waitFrame(t, m)
	app.CloseViewer()
	settleWebAnswer(t, m, posted)
	if w.opens() != 0 {
		t.Fatalf("a view opened for a page the window had left: %d", w.opens())
	}
}

func TestAWebAnswerThatArrivesAfterTheWindowClosedOpensNoView(t *testing.T) {
	m, w, app, posted := gatedWeb(t)
	app.OpenDoc(planPath)
	waitFrame(t, m)
	app.Close()
	settleWebAnswer(t, m, posted)
	if w.opens() != 0 {
		t.Fatalf("a view opened after the window closed: %d", w.opens())
	}
	if w.closes() != 0 {
		t.Fatalf("closes = %d with nothing opened", w.closes())
	}
}
