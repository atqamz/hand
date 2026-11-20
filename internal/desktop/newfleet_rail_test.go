package desktop_test

import (
	"image/color"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

type folderPicker struct {
	dir   string
	calls int
}

func (p *folderPicker) pick() string {
	p.calls++
	return p.dir
}

func railApp(t *testing.T, spy *newFleetSpy, p *folderPicker) (*desktop.App, *ui.Tester) {
	t.Helper()
	app, _, _ := markApp(t, markFixture(t))
	app.NewFleet = spy.run
	app.PickFolder = p.pick
	tt := newTester(app)
	tt.Frame()
	return app, tt
}

func typeInto(t *testing.T, tt *ui.Tester, label, text string) {
	t.Helper()
	click(t, tt, label)
	selectAll := ui.Ctrl
	if runtime.GOOS == "darwin" {
		selectAll = ui.Cmd
	}
	tt.Key(selectAll, ui.KeyA)
	tt.Type(text)
	tt.Frame()
	tt.Frame()
}

func TestTheRailOffersNewFleetBesideFleetsAndWithNone(t *testing.T) {
	app, tt := railApp(t, &newFleetSpy{}, &folderPicker{})
	mustMark(t, tt, "New fleet", "golden, blocked")
	mustNotText(t, tt, "Create fleet")
	if desktop.NewFleetOpen(app) {
		t.Fatal("form open before a click")
	}
	_, none := zeroFleetApp(t, noFleets, &newFleetSpy{})
	mustMark(t, none, "New fleet")
	mustText(t, none, "Create fleet")
}

func TestTheRailItemOpensTheFormAndMarksItselfSelected(t *testing.T) {
	spy, p := &newFleetSpy{}, &folderPicker{}
	app, tt := railApp(t, spy, p)
	if !hasMark(tt, "Current fleet") {
		t.Fatal("no current fleet before the form")
	}
	click(t, tt, "New fleet")
	tt.Frame()
	mustText(t, tt, "Make a fleet.", "Name", "Folder", "Create fleet", "Cancel", "Choose...")
	mustNotText(t, tt, "No fleet on this computer yet.")
	mustNotText(t, tt, "Fix login")
	if !desktop.NewFleetOpen(app) {
		t.Fatal("form not open")
	}
	if hasMark(tt, "Current fleet") {
		t.Fatal("a fleet is still marked current under the form")
	}
	mustMark(t, tt, "New fleet", "golden, blocked")
	if len(spy.reqs) != 0 || p.calls != 0 {
		t.Fatalf("View ran %d creates and %d pickers", len(spy.reqs), p.calls)
	}
}

func railPixel(t *testing.T, tt *ui.Tester) color.Color {
	t.Helper()
	r, ok := tt.Find("New fleet")
	if !ok {
		t.Fatal("no New fleet item")
	}
	return tt.Image().At(int(r.X+r.W)-3, int(r.Y+r.H/2))
}

func TestTheNewFleetItemDrawsSelectedOnlyWhileTheFormShows(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			app, _, _ := markApp(t, markFixture(t))
			tt := sized(t, app, windowWidth, windowHeight, dark)
			tt.Frame()
			idle := railPixel(t, tt)
			click(t, tt, "New fleet")
			tt.Frame()
			open := railPixel(t, tt)
			if idle == open {
				t.Fatalf("item looks the same open and idle: %v", idle)
			}
			click(t, tt, "Cancel")
			tt.Frame()
			if got := railPixel(t, tt); got != idle {
				t.Fatalf("item stays selected after cancel: %v, want %v", got, idle)
			}
		})
	}
}

func TestTheFormOpensFreshAfterACreate(t *testing.T) {
	spy := &newFleetSpy{res: []desktop.NewFleetResult{{ID: secondID, Folder: "/srv/atlas"}}, errs: []error{nil}}
	app, tt := railApp(t, spy, &folderPicker{})
	click(t, tt, "New fleet")
	tt.Frame()
	typeInto(t, tt, "Fleet name", "atlas")
	click(t, tt, "Create fleet")
	tt.Frame()
	if app.Fleet.ID != secondID || desktop.NewFleetOpen(app) {
		t.Fatalf("selected %q, open %v", app.Fleet.ID, desktop.NewFleetOpen(app))
	}
	click(t, tt, "New fleet")
	tt.Frame()
	if name, _ := desktop.NewFleetForm(app); name != "" {
		t.Fatalf("name = %q in a new form", name)
	}
	mustText(t, tt, "Create fleet")
}

func TestTheZeroFleetFormHasNoCancel(t *testing.T) {
	_, tt := zeroFleetApp(t, noFleets, &newFleetSpy{})
	mustNotText(t, tt, "Cancel")
}

func TestCancelLeavesTheFormAndTheNextOpenIsFresh(t *testing.T) {
	app, tt := railApp(t, &newFleetSpy{}, &folderPicker{})
	click(t, tt, "New fleet")
	tt.Frame()
	typeInto(t, tt, "Fleet name", "atlas")
	click(t, tt, "Cancel")
	tt.Frame()
	mustNotText(t, tt, "Create fleet")
	mustText(t, tt, "Fix login")
	if desktop.NewFleetOpen(app) || !hasMark(tt, "Current fleet") {
		t.Fatal("cancel did not return to the fleet")
	}
	click(t, tt, "New fleet")
	tt.Frame()
	if name, folder := desktop.NewFleetForm(app); name != "" || folder != "~/fleets/" {
		t.Fatalf("form = %q %q", name, folder)
	}
}

func TestPickingAFleetOrAPageClosesTheForm(t *testing.T) {
	for _, target := range []string{"second", "Tasks", "Board"} {
		t.Run(target, func(t *testing.T) {
			app, tt := railApp(t, &newFleetSpy{}, &folderPicker{})
			click(t, tt, "New fleet")
			tt.Frame()
			mustMark(t, tt, "New fleet")
			if err := tt.Click(target); err != nil {
				click(t, tt, target+", quiet")
			}
			tt.Frame()
			if desktop.NewFleetOpen(app) {
				t.Fatalf("form still open after %s", target)
			}
			mustNotText(t, tt, "Create fleet")
		})
	}
}

func TestARefreshKeepsWhatWasTypedInTheRailForm(t *testing.T) {
	app, tt := railApp(t, &newFleetSpy{}, &folderPicker{})
	click(t, tt, "New fleet")
	tt.Frame()
	typeInto(t, tt, "Fleet name", "atlas")
	app.Refresh()
	tt.Frame()
	if name, _ := desktop.NewFleetForm(app); name != "atlas" {
		t.Fatalf("name = %q after a refresh", name)
	}
	mustText(t, tt, "Create fleet")
}

func TestChooseFillsFolderFromThePickedParentAndTheName(t *testing.T) {
	parent := filepath.Join("srv", "work")
	spy, p := &newFleetSpy{}, &folderPicker{dir: parent}
	app, tt := railApp(t, spy, p)
	click(t, tt, "New fleet")
	tt.Frame()
	typeInto(t, tt, "Fleet name", "atlas")
	click(t, tt, "Choose...")
	tt.Frame()
	if _, folder := desktop.NewFleetForm(app); folder != filepath.Join(parent, "atlas") || p.calls != 1 {
		t.Fatalf("folder = %q, picker calls %d", folder, p.calls)
	}
	typeInto(t, tt, "Fleet name", "atlas2")
	if _, folder := desktop.NewFleetForm(app); folder != filepath.Join(parent, "atlas2") {
		t.Fatalf("folder after a name edit = %q", folder)
	}
	typeInto(t, tt, "Fleet name", "a/b")
	if _, folder := desktop.NewFleetForm(app); folder != filepath.Join(parent, "a-b") {
		t.Fatalf("folder with an unsafe name = %q", folder)
	}
	if len(spy.reqs) != 0 {
		t.Fatalf("requests = %+v", spy.reqs)
	}
}

func TestChooseBeforeTheNameStillFollowsTheName(t *testing.T) {
	parent := filepath.Join("srv", "work")
	app, tt := railApp(t, &newFleetSpy{}, &folderPicker{dir: parent})
	click(t, tt, "New fleet")
	tt.Frame()
	click(t, tt, "Choose...")
	tt.Frame()
	typeInto(t, tt, "Fleet name", "atlas")
	if _, folder := desktop.NewFleetForm(app); folder != filepath.Join(parent, "atlas") {
		t.Fatalf("folder = %q", folder)
	}
}

func TestAnEditedFolderIsNotRecomputedButAPickReplacesIt(t *testing.T) {
	p := &folderPicker{dir: filepath.Join("srv", "work")}
	app, tt := railApp(t, &newFleetSpy{}, p)
	click(t, tt, "New fleet")
	tt.Frame()
	typeInto(t, tt, "Fleet name", "atlas")
	click(t, tt, "Choose...")
	tt.Frame()
	typeInto(t, tt, "Fleet folder", "/srv/mine")
	typeInto(t, tt, "Fleet name", "atlas2")
	if _, folder := desktop.NewFleetForm(app); folder != "/srv/mine" {
		t.Fatalf("an edited folder became %q", folder)
	}
	click(t, tt, "Choose...")
	tt.Frame()
	if _, folder := desktop.NewFleetForm(app); folder != filepath.Join("srv", "work", "atlas2") {
		t.Fatalf("folder after a second pick = %q", folder)
	}
}

func TestACancelledPickerLeavesTheFolder(t *testing.T) {
	p := &folderPicker{}
	app, tt := railApp(t, &newFleetSpy{}, p)
	click(t, tt, "New fleet")
	tt.Frame()
	typeInto(t, tt, "Fleet name", "atlas")
	click(t, tt, "Choose...")
	tt.Frame()
	if _, folder := desktop.NewFleetForm(app); folder != "~/fleets/atlas" || p.calls != 1 {
		t.Fatalf("folder = %q, picker calls %d", folder, p.calls)
	}
	typeInto(t, tt, "Fleet folder", "/srv/mine")
	click(t, tt, "Choose...")
	tt.Frame()
	if _, folder := desktop.NewFleetForm(app); folder != "/srv/mine" {
		t.Fatalf("folder = %q", folder)
	}
}

func TestChooseWorksInTheZeroFleetForm(t *testing.T) {
	app, tt := zeroFleetApp(t, noFleets, &newFleetSpy{})
	app.PickFolder = (&folderPicker{dir: filepath.Join("srv", "work")}).pick
	tt.Frame()
	typeInto(t, tt, "Fleet name", "atlas")
	click(t, tt, "Choose...")
	tt.Frame()
	if _, folder := desktop.NewFleetForm(app); folder != filepath.Join("srv", "work", "atlas") {
		t.Fatalf("folder = %q", folder)
	}
}

func TestAPickThatArrivesAfterCancelIsDropped(t *testing.T) {
	app, tt := railApp(t, &newFleetSpy{}, &folderPicker{})
	release := make(chan string, 1)
	app.PickFolder = func() string { return <-release }
	posted := make(chan func(), 1)
	app.Post = func(fn func()) { posted <- fn }
	click(t, tt, "New fleet")
	tt.Frame()
	click(t, tt, "Choose...")
	tt.Frame()
	click(t, tt, "Cancel")
	tt.Frame()
	release <- "/srv/late"
	select {
	case fn := <-posted:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("the pick never returned")
	}
	if _, folder := desktop.NewFleetForm(app); folder != "" {
		t.Fatalf("folder = %q after a late pick", folder)
	}
}

func TestCreateFromTheRailSendsThePickedFolder(t *testing.T) {
	spy := &newFleetSpy{res: []desktop.NewFleetResult{{ID: fleetID, Folder: "/srv/work/atlas"}}, errs: []error{nil}}
	parent := filepath.Join(t.TempDir(), "work")
	_, tt := railApp(t, spy, &folderPicker{dir: parent})
	click(t, tt, "New fleet")
	tt.Frame()
	typeInto(t, tt, "Fleet name", "atlas")
	click(t, tt, "Choose...")
	tt.Frame()
	click(t, tt, "Create fleet")
	if len(spy.reqs) != 1 || spy.reqs[0].Name != "atlas" || spy.reqs[0].Folder != filepath.Join(parent, "atlas") || len(spy.reqs[0].Fleets) != 2 {
		t.Fatalf("requests = %+v", spy.reqs)
	}
}

func TestScenesNewFleetFromTheRail(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			app, _, _ := markApp(t, markFixture(t))
			app.NewFleet = (&newFleetSpy{}).run
			app.PickFolder = (&folderPicker{}).pick
			tt := sized(t, app, windowWidth, windowHeight, dark)
			tt.Frame()
			click(t, tt, "New fleet")
			tt.Frame()
			typeInto(t, tt, "Fleet name", "atlas")
			mustText(t, tt, "Make a fleet.", "Choose...", "Cancel")
			shot(t, tt, "new-fleet-rail-"+theme(dark))
		})
	}
}
