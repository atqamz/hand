package desktop_test

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

func docPageApp(t *testing.T) (*desktop.App, *ui.Tester, fakeHand) {
	t.Helper()
	hand := installFakeHand(t)
	f := withDocs(t, viewFixture(t))
	f.fleets[0].Home = hand.home
	app := newApp(t, f)
	tt := newTester(app)
	return app, tt, hand
}

func noBrowser(t *testing.T, hand fakeHand) {
	t.Helper()
	if got := hand.calls(t); len(got) != 0 {
		t.Fatalf("hand ran %q, a document must not reach the browser", got)
	}
}

func TestADocumentIsAPageWithNoBackdropAndNoOpenAsAPage(t *testing.T) {
	app, tt, hand := docPageApp(t)
	click(t, tt, "Fix login")
	click(t, tt, "open /"+fleetID+"/doc/reports/t1/design.md")
	if app.Screen != desktop.DocScreen {
		t.Fatalf("screen %v", app.Screen)
	}
	mustNotText(t, tt, "Open as a page")
	mustText(t, tt, "Back", "Close the viewer", "Masthead")
	tt.ClickAt(4, 4)
	tt.Frame()
	if app.Screen != desktop.DocScreen {
		t.Fatalf("a click at the edge left the page on %v: a page has no backdrop", app.Screen)
	}
	noBrowser(t, hand)
}

func TestBackOnADocumentPageGoesBackThenCloses(t *testing.T) {
	app, tt, hand := docPageApp(t)
	click(t, tt, "Fix login")
	click(t, tt, "open /"+fleetID+"/doc/reports/t1/design.md")
	click(t, tt, "Back")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("Back left %v %q, want the task", app.Screen, app.Ref)
	}
	app.OpenDoc("reports/t1/design.md")
	tt.Frame()
	app.OpenDoc("reports/t1/notes.txt")
	tt.Frame()
	click(t, tt, "Back")
	if app.Ref != "reports/t1/design.md" || app.Screen != desktop.DocScreen {
		t.Fatalf("Back left %v %q", app.Screen, app.Ref)
	}
	if !tt.Focused("The page") {
		t.Fatal("Back between documents left the focus off the page")
	}
	click(t, tt, "Back")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("the last Back left %v %q, want the task", app.Screen, app.Ref)
	}
	noBrowser(t, hand)
}

func TestBackOnAFirstDocumentPageReturnsToTheBoard(t *testing.T) {
	app, tt, _ := docPageApp(t)
	app.OpenDoc("reports/t1/design.md")
	tt.Frame()
	mustText(t, tt, "Back")
	click(t, tt, "Back")
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("Back left %v", app.Screen)
	}
	if !tt.Focused("The board") {
		t.Fatal("the focus did not come back to the board")
	}
	if desktop.DocShown(app) != "" {
		t.Fatal("the closed page still holds the document")
	}
}

func TestEscapeClosesTheWholeDocumentStack(t *testing.T) {
	app, tt, hand := docPageApp(t)
	click(t, tt, "Fix login")
	click(t, tt, "open /"+fleetID+"/doc/reports/t1/design.md")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("Escape left %v", app.Screen)
	}
	noBrowser(t, hand)
}

func TestADocumentPageHasAKeyboardPath(t *testing.T) {
	app, tt, _ := docPageApp(t)
	app.OpenDoc("reports/t1/design.md")
	tt.Frame()
	if !tt.Focused("The page") {
		t.Fatal("the focus is not in the document page")
	}
	tt.Key(ui.Shift, ui.KeyTab)
	if !tt.Focused("Close the viewer") {
		t.Fatal("Shift+Tab from the body does not reach Close")
	}
	tt.Key(ui.Shift, ui.KeyTab)
	if !tt.Focused("Back") {
		t.Fatal("Shift+Tab from Close does not reach Back")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("Enter on Back left %v", app.Screen)
	}
}

func TestNoDocumentStateOpensTheBrowser(t *testing.T) {
	hand := installFakeHand(t)
	f := docFixture(t)
	f.fleets[0].Home = hand.home
	for _, path := range []string{
		"reports/t1/design.md", "reports/t1", "reports/t1/notes.txt", "reports/t1/shot-1.png",
		"reports/t1/plan.pdf", "reports/t1/missing.md",
	} {
		app := newApp(t, f)
		app.OpenDoc(path)
		tt := newTester(app)
		mustNotText(t, tt, "Open in browser")
		mustNotText(t, tt, "Open as a page")
		app.OpenDoc("reports/t1/design.md")
		tt.Frame()
		app.Back()
		tt.Frame()
	}
	noBrowser(t, hand)
}

func TestScenesTheDocumentAsAPageWithBack(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "100-doc-page-back-1100x760-light"
		if dark {
			name = "100-doc-page-back-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			hand := installFakeHand(t)
			f := withDocs(t, viewFixture(t))
			f.fleets[0].Home = hand.home
			app := newApp(t, f)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			click(t, tt, "Fix login")
			click(t, tt, "open /"+fleetID+"/doc/reports/t1/design.md")
			mustText(t, tt, "design.md", "Back", "Close the viewer", "Copy contents", "Download")
			mustNotText(t, tt, "Open as a page")
			shotSized(t, tt, name, windowWidth, windowHeight)
			noBrowser(t, hand)
		})
	}
}

func TestScenesTheDocumentPageAfterBack(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "101-doc-page-folder-1100x760-light"
		if dark {
			name = "101-doc-page-folder-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			app := newApp(t, docFixture(t))
			app.OpenDoc("reports/t1")
			tt := sized(t, app, windowWidth, windowHeight, dark)
			click(t, tt, "design.md")
			click(t, tt, "Back")
			mustText(t, tt, "t1/", "design.md", "Back")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestScenesTheDocumentPageInANarrowWindow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "102-doc-page-880x760-light"
		if dark {
			name = "102-doc-page-880x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			app := newApp(t, docFixture(t))
			app.OpenDoc("reports/t1/design.md")
			tt := sized(t, app, 880, 760, dark)
			mustText(t, tt, "design.md", "Back", "Close the viewer")
			shotSized(t, tt, name, 880, 760)
		})
	}
}

func TestFocusFollowsADocumentOpenedFromATaskDialog(t *testing.T) {
	app, tt, _ := docPageApp(t)
	click(t, tt, "Fix login")
	click(t, tt, "open /"+fleetID+"/doc/reports/t1/design.md")
	if !tt.Focused("The page") {
		t.Fatal("the focus is not in the document page opened from a task")
	}
	click(t, tt, "Back")
	if app.Screen != desktop.TaskScreen || !tt.Focused("The page") {
		t.Fatalf("Back to the task left the focus off its page (screen %v)", app.Screen)
	}
}

func TestEscapeAndCloseReturnFocusToTheBoardFromADocumentPage(t *testing.T) {
	for name, closeIt := range map[string]func(*testing.T, *ui.Tester){
		"escape": func(t *testing.T, tt *ui.Tester) { tt.Key(0, ui.KeyEscape) },
		"close":  func(t *testing.T, tt *ui.Tester) { click(t, tt, "Close the viewer") },
	} {
		t.Run(name, func(t *testing.T) {
			app, tt, _ := docPageApp(t)
			click(t, tt, "Fix login")
			click(t, tt, "open /"+fleetID+"/doc/reports/t1/design.md")
			closeIt(t, tt)
			tt.Frame()
			if app.Screen != desktop.BoardScreen {
				t.Fatalf("left on %v", app.Screen)
			}
			if !tt.Focused("The board") {
				t.Fatal("the focus did not come back to the board")
			}
		})
	}
}

func TestEscapeOnARailItemLeavesThePageOpen(t *testing.T) {
	app, tt, _ := docPageApp(t)
	app.OpenDoc("reports/t1/design.md")
	tt.Frame()
	for i := 0; i < 14 && !tt.Focused("Board"); i++ {
		tt.Key(ui.Shift, ui.KeyTab)
	}
	if !tt.Focused("Board") {
		t.Fatal("could not reach the rail's Board item")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.Screen != desktop.DocScreen {
		t.Fatalf("Escape on a rail item left %v, want the page open", app.Screen)
	}
}
