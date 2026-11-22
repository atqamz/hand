package desktop_test

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/sidecar"
)

const noBoardText = "no board is answering"

func remoteApp(t *testing.T, g *rtRig) (*desktop.App, *startSpy) {
	t.Helper()
	spy := &startSpy{}
	app := startApp(t, desktop.Connect(g.getenv, g.store, g.clients, nil), spy.run)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	app.StartRefresh()
	return app, spy
}

func TestARemoteOnlyWindowWhoseClientIsNotListeningYetSelectsItsFleetAndStartsNothing(t *testing.T) {
	g := newRTRig(t)
	a := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, sidecar.ClientStatus{State: sidecar.Starting})
	app, spy := remoteApp(t, g)
	tt := newTester(app)
	tt.Frame()
	if app.Fleet.ID != rtA || app.Fleet.Host != rtHostA {
		t.Fatalf("selected fleet = %+v", app.Fleet)
	}
	if strings.Contains(app.Err, noBoardText) || app.Starting || spy.count() != 0 {
		t.Fatalf("a remote host that is not up read as a local Board that is down: err %q, starting %v, starts %d", app.Err, app.Starting, spy.count())
	}
	if len(app.Fleets) != 1 || tt.HasText("Starting the Board…") {
		t.Fatalf("fleets = %+v, texts %q", app.Fleets, tt.Texts())
	}
	if len(a.seen()) != 0 {
		t.Fatalf("a request went out with no client: %v", a.seen())
	}
}

func TestALocalBoardThatIsUpKeepsItsFleetSelectedBesideARemoteHostThatIsNot(t *testing.T) {
	g := newRTRig(t)
	local := newRTHost(t, "hand_board_", map[string]string{rtLocal: rtToken}, rtFleetsLocal)
	g.localFleet(t, rtLocal, local)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, sidecar.ClientStatus{State: sidecar.Starting})
	app, spy := remoteApp(t, g)
	newTester(app).Frame()
	if app.Fleet.ID != rtLocal || len(app.Fleets) != 2 {
		t.Fatalf("selected %+v of %+v", app.Fleet, app.Fleets)
	}
	if strings.Contains(app.Err, noBoardText) || app.Starting || spy.count() != 0 {
		t.Fatalf("err %q, starting %v, starts %d", app.Err, app.Starting, spy.count())
	}
}

func TestALocalBoardThatIsDownStillGetsStartedForItsOwnFleetBesideARemoteHost(t *testing.T) {
	g := newRTRig(t)
	g.localFleet(t, rtLocal, nil)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, sidecar.ClientStatus{State: sidecar.Starting})
	_, spy := remoteApp(t, g)
	spy.waitStarts(t, 1)
	if !strings.HasSuffix(spy.home, "fleet-"+rtLocal) {
		t.Fatalf("the Board was started for %q, not the local fleet", spy.home)
	}
}
