package desktop_test

import (
	"context"
	"errors"
	"image/color"
	"path/filepath"
	"runtime"
	"sync/atomic"
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

func itemPixel(t *testing.T, tt *ui.Tester, name string) color.Color {
	t.Helper()
	r, ok := tt.Find(name)
	if !ok {
		t.Fatalf("no %s item", name)
	}
	return tt.Image().At(int(r.X+r.W)-3, int(r.Y+r.H/2))
}

func railPixel(t *testing.T, tt *ui.Tester) color.Color {
	t.Helper()
	return itemPixel(t, tt, "New fleet")
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

func TestOnlyNewFleetIsSelectedWhileTheFormShows(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			for _, screen := range []desktop.Screen{desktop.BoardScreen, desktop.TasksScreen} {
				app, _, _ := markApp(t, markFixture(t))
				tt := sized(t, app, windowWidth, windowHeight, dark)
				app.Show(screen)
				click(t, tt, "New fleet")
				tt.Frame()
				board, tasks, add := itemPixel(t, tt, "Board"), itemPixel(t, tt, "Tasks"), railPixel(t, tt)
				if board != tasks {
					t.Fatalf("screen %v: Board %v and Tasks %v differ under the form", screen, board, tasks)
				}
				if add == board {
					t.Fatalf("screen %v: New fleet looks like an unselected item", screen)
				}
			}
			for _, screen := range []desktop.Screen{desktop.BoardScreen, desktop.TasksScreen} {
				app, tt := zeroFleetApp(t, noFleets, &newFleetSpy{})
				tt.SetDark(dark)
				app.Show(screen)
				tt.Frame()
				board, tasks, add := itemPixel(t, tt, "Board"), itemPixel(t, tt, "Tasks"), railPixel(t, tt)
				if board != tasks {
					t.Fatalf("with none, screen %v: Board %v and Tasks %v differ", screen, board, tasks)
				}
				if add == board {
					t.Fatalf("with none, screen %v: New fleet looks unselected, or an item looks selected with it", screen)
				}
			}
		})
	}
}

func TestNewFleetWithNoneKeepsWhatWasTyped(t *testing.T) {
	app, tt := zeroFleetApp(t, noFleets, &newFleetSpy{})
	typeInto(t, tt, "Fleet name", "atlas")
	click(t, tt, "New fleet")
	tt.Frame()
	if name, _ := desktop.NewFleetForm(app); name != "atlas" {
		t.Fatalf("name = %q after a click on the selected item", name)
	}
	mustText(t, tt, "Create fleet")
}

func TestTheNewFleetIconIsThePlus(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			app, _, _ := markApp(t, markFixture(t))
			tt := sized(t, app, windowWidth, windowHeight, dark)
			tt.Frame()
			r, ok := tt.Find("New fleet")
			if !ok {
				t.Fatal("no New fleet item")
			}
			img := tt.Image()
			x0, y0 := int(r.X)+1, int(r.Y)
			bg := img.At(x0, y0+1)
			isInk := func(x, y int) bool {
				r1, g1, b1, _ := img.At(x0+x, y0+y).RGBA()
				r2, g2, b2, _ := bg.RGBA()
				d := func(a, b uint32) uint32 {
					if a > b {
						return a - b
					}
					return b - a
				}
				return d(r1, r2)+d(g1, g2)+d(b1, b2) > 0x10000
			}
			near := func(x, y int) bool {
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						if isInk(x+dx, y+dy) {
							return true
						}
					}
				}
				return false
			}
			minX, minY, maxX, maxY := 1<<30, 1<<30, -1, -1
			for y := 0; y < int(r.H); y++ {
				for x := 0; x < 30; x++ {
					if isInk(x, y) {
						minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
					}
				}
			}
			w, h := maxX-minX+1, maxY-minY+1
			if maxX < 0 || w < 8 || h < 8 || w-h > 1 || h-w > 1 {
				t.Fatalf("no plus-sized icon: box %dx%d", w, h)
			}
			cx, cy, q := (minX+maxX)/2, (minY+maxY)/2, w/4
			for _, c := range [][2]int{{cx, cy}, {cx, minY}, {cx, maxY}, {minX, cy}, {maxX, cy}} {
				if !near(c[0], c[1]) {
					t.Fatalf("no ink at %v of the icon: not a plus", c)
				}
			}
			for _, c := range [][2]int{{minX, minY}, {maxX - q, minY}, {minX, maxY - q}, {maxX - q, maxY - q}} {
				for y := c[1]; y <= c[1]+q; y++ {
					for x := c[0]; x <= c[0]+q; x++ {
						if isInk(x, y) {
							t.Fatalf("ink at %d,%d, in a corner of the icon: not a plus", x, y)
						}
					}
				}
			}
		})
	}
}

type inflight struct {
	app     *desktop.App
	tt      *ui.Tester
	release chan struct{}
	posted  chan func()
	starts  atomic.Int32
}

func blockedCreate(t *testing.T) *inflight {
	t.Helper()
	f := &inflight{release: make(chan struct{}), posted: make(chan func(), 16)}
	f.app, f.tt = railApp(t, &newFleetSpy{}, &folderPicker{})
	f.app.Post = func(fn func()) { f.posted <- fn }
	f.app.NewFleet = func(context.Context, desktop.NewFleetRequest) (desktop.NewFleetResult, error) {
		f.starts.Add(1)
		<-f.release
		return desktop.NewFleetResult{}, errors.New("hand init failed: boom")
	}
	t.Cleanup(func() {
		select {
		case <-f.release:
		default:
			close(f.release)
		}
	})
	click(t, f.tt, "New fleet")
	f.tt.Frame()
	typeInto(t, f.tt, "Fleet name", "atlas")
	click(t, f.tt, "Create fleet")
	f.tt.Frame()
	if !desktop.NewFleetBusy(f.app) {
		t.Fatal("the create is not in flight")
	}
	return f
}

func (f *inflight) land(t *testing.T) {
	t.Helper()
	for desktop.NewFleetBusy(f.app) {
		select {
		case fn := <-f.posted:
			fn()
		case <-time.After(5 * time.Second):
			t.Fatal("the create never landed")
		}
	}
	f.tt.Frame()
}

func TestAnInFlightCreateSurvivesLeavingAndReopeningTheForm(t *testing.T) {
	f := blockedCreate(t)
	click(t, f.tt, "second")
	f.tt.Frame()
	if desktop.NewFleetOpen(f.app) {
		t.Fatal("form still open after picking a fleet")
	}
	click(t, f.tt, "New fleet")
	f.tt.Frame()
	if !desktop.NewFleetOpen(f.app) || !desktop.NewFleetBusy(f.app) {
		t.Fatalf("open %v busy %v after reopening", desktop.NewFleetOpen(f.app), desktop.NewFleetBusy(f.app))
	}
	if name, _ := desktop.NewFleetForm(f.app); name != "atlas" {
		t.Fatalf("name = %q on the reopened form", name)
	}
	mustText(t, f.tt, "Creating…")
	click(t, f.tt, "Creating…")
	f.tt.Frame()
	close(f.release)
	f.land(t)
	if desktop.NewFleetBusy(f.app) {
		t.Fatal("still busy after the result")
	}
	mustText(t, f.tt, "hand init failed: boom", "Create fleet")
	if n := f.starts.Load(); n != 1 {
		t.Fatalf("creates started = %d", n)
	}
}

func TestACreateInFlightKeepsItsFormThroughARefresh(t *testing.T) {
	f := blockedCreate(t)
	click(t, f.tt, "second")
	f.tt.Frame()
	f.app.Refresh()
	for idle := false; !idle; {
		select {
		case fn := <-f.posted:
			fn()
		case <-time.After(200 * time.Millisecond):
			idle = true
		}
	}
	f.tt.Frame()
	if !desktop.NewFleetBusy(f.app) {
		t.Fatal("a refresh forgot the create")
	}
}

func TestCancelIsRefusedWhileACreateRuns(t *testing.T) {
	f := blockedCreate(t)
	click(t, f.tt, "Cancel")
	f.tt.Frame()
	if !desktop.NewFleetOpen(f.app) || !desktop.NewFleetBusy(f.app) {
		t.Fatalf("open %v busy %v after Cancel", desktop.NewFleetOpen(f.app), desktop.NewFleetBusy(f.app))
	}
}

func TestAPickThatArrivesAfterCancelAndReopenIsDropped(t *testing.T) {
	app, tt := railApp(t, &newFleetSpy{}, &folderPicker{})
	release := make(chan string, 1)
	started := make(chan struct{}, 4)
	app.PickFolder = func() string { started <- struct{}{}; return <-release }
	posted := make(chan func(), 4)
	app.Post = func(fn func()) { posted <- fn }
	click(t, tt, "New fleet")
	tt.Frame()
	click(t, tt, "Choose...")
	tt.Frame()
	<-started
	click(t, tt, "Cancel")
	tt.Frame()
	click(t, tt, "New fleet")
	tt.Frame()
	_, before := desktop.NewFleetForm(app)
	release <- "/srv/late"
	select {
	case fn := <-posted:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("the pick never returned")
	}
	if _, folder := desktop.NewFleetForm(app); folder != before {
		t.Fatalf("folder = %q on the reopened form, want %q", folder, before)
	}
	click(t, tt, "Choose...")
	tt.Frame()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the reopened form cannot start a pick")
	}
}

func TestRepeatedChooseClicksStartOneDialog(t *testing.T) {
	app, tt := railApp(t, &newFleetSpy{}, &folderPicker{})
	release := make(chan string, 1)
	started := make(chan struct{}, 4)
	app.PickFolder = func() string { started <- struct{}{}; return <-release }
	posted := make(chan func(), 4)
	app.Post = func(fn func()) { posted <- fn }
	click(t, tt, "New fleet")
	tt.Frame()
	click(t, tt, "Choose...")
	tt.Frame()
	<-started
	click(t, tt, "Choose...")
	tt.Frame()
	release <- "/srv/work"
	(<-posted)()
	select {
	case <-started:
		t.Fatal("a second dialog started")
	case <-time.After(100 * time.Millisecond):
	}
	if _, folder := desktop.NewFleetForm(app); folder != filepath.Join("/srv/work", "") {
		t.Fatalf("folder = %q", folder)
	}
}

func TestAPickThatArrivesDuringACreateLeavesTheFolder(t *testing.T) {
	f := &inflight{release: make(chan struct{}), posted: make(chan func(), 16)}
	f.app, f.tt = railApp(t, &newFleetSpy{}, &folderPicker{})
	pickRelease := make(chan string, 1)
	started := make(chan struct{}, 4)
	f.app.Post = func(fn func()) { f.posted <- fn }
	f.app.PickFolder = func() string { started <- struct{}{}; return <-pickRelease }
	f.app.NewFleet = func(context.Context, desktop.NewFleetRequest) (desktop.NewFleetResult, error) {
		<-f.release
		return desktop.NewFleetResult{}, errors.New("boom")
	}
	t.Cleanup(func() { close(f.release) })
	click(t, f.tt, "New fleet")
	f.tt.Frame()
	typeInto(t, f.tt, "Fleet name", "atlas")
	click(t, f.tt, "Choose...")
	f.tt.Frame()
	<-started
	click(t, f.tt, "Create fleet")
	f.tt.Frame()
	_, before := desktop.NewFleetForm(f.app)
	pickRelease <- "/srv/late"
	for idle := false; !idle; {
		select {
		case fn := <-f.posted:
			fn()
		case <-time.After(200 * time.Millisecond):
			idle = true
		}
	}
	if _, after := desktop.NewFleetForm(f.app); after != before {
		t.Fatalf("folder changed from %q to %q under a running create", before, after)
	}
}

func TestChooseIsNotOfferedWithoutAPickerAndLookAgainIsNotInTheRailForm(t *testing.T) {
	app, tt := railApp(t, &newFleetSpy{}, &folderPicker{})
	app.PickFolder = nil
	click(t, tt, "New fleet")
	tt.Frame()
	mustNotText(t, tt, "Choose...")
	mustNotText(t, tt, "Look again")
	mustText(t, tt, "Create fleet", "Cancel")
}
