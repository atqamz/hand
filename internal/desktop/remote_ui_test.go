package desktop_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/remote"
	"github.com/atqamz/hand/internal/sidecar"
)

type fakeDesk struct {
	mu     sync.Mutex
	reqs   []desktop.PairRequest
	err    error
	forgot []string
	store  *remote.Store
	status func(string) sidecar.ClientStatus
}

func (d *fakeDesk) Hosts() ([]remote.Host, error) { return d.store.Hosts() }

func (d *fakeDesk) Status(host string) sidecar.ClientStatus { return d.status(host) }

func (d *fakeDesk) Pair(_ context.Context, req desktop.PairRequest) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reqs = append(d.reqs, req)
	return d.err
}

func (d *fakeDesk) Forget(host string) error {
	d.mu.Lock()
	d.forgot = append(d.forgot, host)
	d.mu.Unlock()
	return d.store.Forget(host)
}

func (d *fakeDesk) calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.reqs)
}

type remoteUI struct {
	g     *rtRig
	local *rtHost
	host  *rtHost
	desk  *fakeDesk
	app   *desktop.App
	tt    *ui.Tester
}

func (r *remoteUI) status(st sidecar.ClientStatus) {
	r.g.clients.set(rtHostA, st)
}

func newRemoteUI(t *testing.T, selectRemote bool) *remoteUI {
	t.Helper()
	g := newRTRig(t)
	r := &remoteUI{g: g}
	r.local = newRTHost(t, "hand_board_", map[string]string{rtLocal: rtToken}, rtFleetsLocal)
	r.host = newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	g.localFleet(t, rtLocal, r.local)
	h := remote.Host{ID: rtHostA, ClientID: rtClient, PairedAt: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)}
	if err := g.store.Add(h, remote.Fleet{ID: rtA, Name: "lab"}, rtCookieA, nil); err != nil {
		t.Fatal(err)
	}
	r.status(sidecar.ClientStatus{State: sidecar.Ready, Addr: r.host.addr(), ID: rtClient, Link: sidecar.Connected})
	r.desk = &fakeDesk{store: g.store, status: g.clients.Status}
	r.app = startApp(t, desktop.Connect(g.getenv, g.store, g.clients, nil), nil)
	r.app.Remote = r.desk
	if selectRemote {
		r.app.SelectFleet(rtA)
	}
	r.tt = newTester(r.app)
	r.tt.Frame()
	return r
}

func (r *remoteUI) state(t *testing.T, st sidecar.ClientStatus) {
	t.Helper()
	r.status(st)
	r.app.Refresh()
	r.tt.Frame()
}

func storeDir(s *remote.Store) string {
	dir, _ := s.KeyDir(rtHostA)
	return filepath.Dir(dir)
}

func up(r *remoteUI, link string) sidecar.ClientStatus {
	return sidecar.ClientStatus{State: sidecar.Ready, Addr: r.host.addr(), ID: rtClient, Link: link}
}

func TestTheRailOffersConnectBesideNewFleetOnlyWhenRemoteIsAvailable(t *testing.T) {
	r := newRemoteUI(t, false)
	mustMark(t, r.tt, "Connect remote fleet", "New fleet")
	_, tt := railApp(t, &newFleetSpy{}, &folderPicker{})
	mustNotText(t, tt, "Connect remote fleet")
	if hasMark(tt, "Connect remote fleet") {
		t.Fatal("the rail offers remote with no remote desk")
	}
}

func TestTheZeroFleetCardOffersConnectAndOpensTheForm(t *testing.T) {
	app, tt := zeroFleetApp(t, noFleets, &newFleetSpy{})
	mustNotText(t, tt, "Connect to a remote fleet")
	app.Remote = &fakeDesk{store: remote.OpenStore(t.TempDir())}
	tt.Frame()
	mustText(t, tt, "Connect to a remote fleet")
	click(t, tt, "Connect to a remote fleet")
	tt.Frame()
	mustText(t, tt, "Pairing link", "Device name", "Continue", "Cancel")
	mustNotText(t, tt, "Create fleet")
}

func TestWithRemoteOffTheFormSaysWhyAndHasNoFields(t *testing.T) {
	app, tt := zeroFleetApp(t, noFleets, &newFleetSpy{})
	app.RemoteOff = "Remote fleets are not supported on Windows yet."
	tt.Frame()
	click(t, tt, "Connect to a remote fleet")
	tt.Frame()
	mustText(t, tt, "Remote fleets are not supported on Windows yet.", "Cancel")
	mustNotText(t, tt, "Continue")
	click(t, tt, "Cancel")
	tt.Frame()
	mustText(t, tt, "Create fleet")
}

func openForm(t *testing.T, r *remoteUI) {
	t.Helper()
	click(t, r.tt, "Connect remote fleet")
	r.tt.Frame()
}

func TestAPastedLinkIsCheckedAskedAboutAndGoneBeforeAnyDial(t *testing.T) {
	r := newRemoteUI(t, false)
	openForm(t, r)
	typeInto(t, r.tt, "Pairing link", "handboard://c#v=1&e="+rtHostB+"&f="+rtB+"&c="+pairCode+"&n=Lab")
	click(t, r.tt, "Continue")
	r.tt.Frame()
	if r.desk.calls() != 0 {
		t.Fatal("the host was dialled before the user confirmed")
	}
	mustText(t, r.tt, "Add bbbb…bbbb?", "The link says it is “Lab”.", "Add", "Back")
	for _, s := range r.tt.Texts() {
		if strings.Contains(s, pairCode) || strings.Contains(s, "handboard://") {
			t.Fatalf("the screen shows the link or its code: %q", s)
		}
	}
	if got := desktop.RemoteLinkField(r.app); got != "" {
		t.Fatalf("the field holds %d bytes after Continue", len(got))
	}
	click(t, r.tt, "Back")
	r.tt.Frame()
	if got := desktop.RemoteLinkField(r.app); got != "" {
		t.Fatalf("the field holds %d bytes after Back", len(got))
	}
	for _, s := range r.tt.Texts() {
		if strings.Contains(s, pairCode) || strings.Contains(s, "handboard://") {
			t.Fatalf("the field still holds the link: %q", s)
		}
	}
}

func TestConfirmingPairsWithTheDeviceNameAndOpensTheNewFleet(t *testing.T) {
	r := newRemoteUI(t, false)
	openForm(t, r)
	typeInto(t, r.tt, "Pairing link", "handboard://c#v=1&e="+rtHostB+"&f="+rtB+"&c="+pairCode)
	click(t, r.tt, "Continue")
	r.tt.Frame()
	click(t, r.tt, "Add")
	r.tt.Frame()
	if r.desk.calls() != 1 {
		t.Fatalf("pair calls = %d", r.desk.calls())
	}
	req := r.desk.reqs[0]
	if req.Link.Host != rtHostB || req.Link.Fleet != rtB || req.Link.Code() != pairCode || req.Name != "Hand Desktop" || req.Again {
		t.Fatalf("request = %+v", req)
	}
	if !req.Held(rtA) || !req.Held(rtLocal) || req.Held(rtB) {
		t.Fatal("the held fleets are not the window's")
	}
	mustNotText(t, r.tt, "Add bbbb…bbbb?")
}

func TestLinkRefusalsUseTheDocumentedSentencesAndNeverDial(t *testing.T) {
	for link, want := range map[string]string{
		"https://example.com":   "This is not a Hand pairing code. Show a new one on the Board.",
		"handboard://c#v=2&e=x": "This code needs a newer Hand. Update Hand.",
		"handboard://c#v=1&e=" + rtHostB + "&f=" + rtA + "&c=" + pairCode: "This window already holds that fleet.",
	} {
		r := newRemoteUI(t, false)
		openForm(t, r)
		typeInto(t, r.tt, "Pairing link", link)
		click(t, r.tt, "Continue")
		r.tt.Frame()
		mustText(t, r.tt, want)
		if r.desk.calls() != 0 {
			t.Fatalf("%q dialled", link)
		}
		for _, s := range r.tt.Texts() {
			if strings.Contains(s, link) || strings.Contains(s, "version_unsupported") {
				t.Fatalf("the screen shows %q", s)
			}
		}
	}
}

func TestADeviceNameIsKeptToFortyCharacters(t *testing.T) {
	r := newRemoteUI(t, false)
	openForm(t, r)
	typeInto(t, r.tt, "Device name", strings.Repeat("n", 60))
	typeInto(t, r.tt, "Pairing link", "handboard://c#v=1&e="+rtHostB+"&f="+rtB+"&c="+pairCode)
	click(t, r.tt, "Continue")
	r.tt.Frame()
	click(t, r.tt, "Add")
	r.tt.Frame()
	if got := r.desk.reqs[0].Name; got != strings.Repeat("n", 40) {
		t.Fatalf("name = %q", got)
	}
}

func TestACodeRefusalReturnsToTheLinkFieldAndATransientOneOffersTryAgain(t *testing.T) {
	r := newRemoteUI(t, false)
	openForm(t, r)
	paste := func() {
		typeInto(t, r.tt, "Pairing link", "handboard://c#v=1&e="+rtHostB+"&f="+rtB+"&c="+pairCode)
		click(t, r.tt, "Continue")
		r.tt.Frame()
		click(t, r.tt, "Add")
		r.tt.Frame()
	}
	r.desk.err = &desktop.PairError{Code: "code_used", Message: "That code was already used. A code works once. Show a new code on the Board."}
	paste()
	mustText(t, r.tt, "That code was already used. A code works once. Show a new code on the Board.", "Continue")
	r.desk.err = &desktop.PairError{Code: "unreachable", Message: "Can't reach host.", Transient: true}
	paste()
	mustText(t, r.tt, "Can't reach host.", "Try again")
	r.desk.err = nil
	click(t, r.tt, "Try again")
	r.tt.Frame()
	if r.desk.calls() != 3 {
		t.Fatalf("pair calls = %d", r.desk.calls())
	}
	r.desk.err = errors.New("x")
}

func occurrences(tt *ui.Tester, word string) int {
	n := 0
	for _, s := range tt.Texts() {
		if s == word {
			n++
		}
	}
	return n
}

func TestEachStateHasItsWordInTheRailAndTheHead(t *testing.T) {
	cases := []struct {
		name string
		st   func(*remoteUI) sidecar.ClientStatus
		word string
	}{
		{"connected", func(r *remoteUI) sidecar.ClientStatus { return up(r, sidecar.Connected) }, "Connected"},
		{"connecting", func(r *remoteUI) sidecar.ClientStatus { return up(r, "") }, "Connecting"},
		{"starting", func(*remoteUI) sidecar.ClientStatus { return sidecar.ClientStatus{State: sidecar.Starting} }, "Connecting"},
		{"busy", func(r *remoteUI) sidecar.ClientStatus { return up(r, sidecar.Busy) }, "Host busy"},
		{"unreachable", func(r *remoteUI) sidecar.ClientStatus { return up(r, sidecar.Unreachable) }, "Can't reach host"},
		{"failed", func(*remoteUI) sidecar.ClientStatus { return sidecar.ClientStatus{State: sidecar.Failed} }, "Can't reach host"},
		{"board down", func(r *remoteUI) sidecar.ClientStatus { return up(r, sidecar.BoardDown) }, "Board not running"},
	}
	for _, c := range cases {
		for _, dark := range []bool{false, true} {
			t.Run(c.name, func(t *testing.T) {
				r := newRemoteUI(t, false)
				r.tt.SetDark(dark)
				r.app.RailOpen = true
				r.status(c.st(r))
				r.tt.Frame()
				if n := occurrences(r.tt, c.word); n != 1 {
					t.Fatalf("the rail shows %q %d times in %q", c.word, n, r.tt.Texts())
				}
				if !hasMark(r.tt, "n, "+c.word) {
					t.Fatalf("no rail mark with %q in %q", c.word, r.tt.Texts())
				}
				r.app.SelectFleet(rtA)
				r.tt.Frame()
				if n := occurrences(r.tt, c.word); n != 3 {
					t.Fatalf("rail and head show %q %d times in %q", c.word, n, r.tt.Texts())
				}
			})
		}
	}
}

func TestALocalFleetCarriesNoConnectionWord(t *testing.T) {
	r := newRemoteUI(t, false)
	r.app.RailOpen = true
	r.tt.Frame()
	if n := occurrences(r.tt, "Connected"); n != 1 {
		t.Fatalf("Connected shown %d times for one remote fleet: %q", n, r.tt.Texts())
	}
	if !hasMark(r.tt, "n, quiet") || !hasMark(r.tt, "n, Connected") {
		t.Fatalf("the rail marks are not a local \"n, quiet\" and a remote \"n, Connected\": %q", r.tt.Texts())
	}
}

func TestADeadClientIsStateNotAnErrorCard(t *testing.T) {
	r := newRemoteUI(t, true)
	r.state(t, sidecar.ClientStatus{State: sidecar.Starting})
	mustNotText(t, r.tt, "Starting the Board…")
	mustNotText(t, r.tt, "The Board refused this window.")
}

func TestOpenCallsOnARemoteFleetRunNothingAndSayNothing(t *testing.T) {
	r := newRemoteUI(t, true)
	for _, call := range []func(){r.app.OpenPhone, r.app.OpenScreen, r.app.OpenInBrowser, func() { r.app.OpenBoardPath("/" + rtA + "/task/t1") }, func() { r.app.OpenBoardPath("/" + rtA + "/doc/") }, func() { r.app.OpenBoardPath("/" + rtA + "/task/t1/x") }} {
		call()
		if r.app.OpenErr != "" || r.app.OpenBusy {
			t.Fatalf("a remote fleet ran or reported hand open: err %q busy %v", r.app.OpenErr, r.app.OpenBusy)
		}
	}
	r.app.SelectFleet(rtLocal)
	r.app.OpenPhone()
	if r.app.OpenErr == "" {
		t.Fatal("a local fleet with no folder no longer says so")
	}
}

func remoteGolden(t *testing.T) (*desktop.App, *ui.Tester) {
	t.Helper()
	f := goldenFixture(t)
	for i := range f.fleets {
		f.fleets[i].Host = rtHostA
	}
	app := newApp(t, f)
	app.Remote = &fakeDesk{store: remote.OpenStore(t.TempDir()), status: func(string) sidecar.ClientStatus {
		return sidecar.ClientStatus{State: sidecar.Ready, Addr: "127.0.0.1:1", ID: rtClient, Link: sidecar.Connected}
	}}
	app.RailOpen = true
	return app, newTester(app)
}

func TestTheOverflowMenuOfARemoteFleetHasNoBrowserOrModelRefresh(t *testing.T) {
	_, tt := remoteGolden(t)
	click(t, tt, "More")
	mustText(t, tt, "Refresh now")
	mustNotText(t, tt, "Open in browser")
	mustNotText(t, tt, "Refresh models")
}

func TestARemoteFleetsViewerHasNoOpenAsAPageAndALocalOneHas(t *testing.T) {
	app, tt := remoteGolden(t)
	app.OpenRef("t1")
	tt.Frame()
	mustText(t, tt, "Close the viewer")
	mustNotText(t, tt, "Open as a page")
	local := newApp(t, goldenFixture(t))
	lt := newTester(local)
	local.OpenRef("t1")
	lt.Frame()
	mustText(t, lt, "Open as a page")
}

func TestARemoteFleetOfTheRichFixtureShowsNoRailScreenCompanionNorHandOpenText(t *testing.T) {
	app, tt := remoteGolden(t)
	app.RailOpen = true
	tt.Frame()
	for _, name := range []string{"Screen", "Companion"} {
		if hasMark(tt, name) {
			t.Fatalf("a remote fleet shows %q", name)
		}
	}
	mustMark(t, tt, "Board", "Tasks", "New fleet")
	for _, s := range tt.Texts() {
		if strings.Contains(s, "hand open") || strings.Contains(s, "hand board start") {
			t.Fatalf("local action text %q", s)
		}
	}
	local := newApp(t, goldenFixture(t))
	lt := newTester(local)
	lt.Frame()
	mustMark(t, lt, "Screen", "Companion")
}

func TestAHostThatAnswers401IsNotRecognisedNeverTheLocalHandOpenCard(t *testing.T) {
	r := newRemoteUI(t, true)
	r.host.secret = map[string]string{rtA: "other"}
	r.app.Refresh()
	r.tt.Frame()
	mustText(t, r.tt, "The host does not recognise this device.", "Pair again", "Forget host")
	for _, s := range r.tt.Texts() {
		if strings.Contains(s, "hand open") || strings.Contains(s, "refused this window") {
			t.Fatalf("local card text %q", s)
		}
	}
}

func TestARevokedFleetShowsItsStateItsPairingDateAndTheTwoActions(t *testing.T) {
	r := newRemoteUI(t, true)
	r.host.fail = func(w http.ResponseWriter) bool {
		w.Header().Set("X-Hand-Error", "no longer paired")
		w.WriteHeader(http.StatusForbidden)
		return true
	}
	r.app.Refresh()
	r.tt.Frame()
	mustText(t, r.tt, "Revoked on the host.", "Pair again", "Forget host", "Host aaaa…aaaa · paired 2026-11-18", "Revoked")
}

func TestAReplacedKeyReadsAsItsOwnStateWithTheSameActions(t *testing.T) {
	r := newRemoteUI(t, true)
	st := up(r, sidecar.Connected)
	st.ID = rtOther
	r.state(t, st)
	mustText(t, r.tt, "This device's key is not the one the host paired.", "Pair again", "Forget host", "Key replaced")
}

func TestForgetIsLocalAndLeavesTheLocalFleet(t *testing.T) {
	r := newRemoteUI(t, true)
	r.host.fail = func(w http.ResponseWriter) bool {
		w.Header().Set("X-Hand-Error", "no longer paired")
		w.WriteHeader(http.StatusForbidden)
		return true
	}
	r.app.Refresh()
	r.tt.Frame()
	click(t, r.tt, "Forget host")
	r.tt.Frame()
	if len(r.desk.forgot) != 1 || r.desk.forgot[0] != rtHostA {
		t.Fatalf("forgot = %v", r.desk.forgot)
	}
	if len(r.app.Fleets) != 1 || r.app.Fleets[0].ID != rtLocal || r.app.Fleet.ID != rtLocal {
		t.Fatalf("fleets = %+v, selected %+v", r.app.Fleets, r.app.Fleet)
	}
	if len(r.host.seen()) > 0 && strings.Contains(r.host.cookiesSeen(), "revoke") {
		t.Fatal("forgetting called the host")
	}
}

func TestPairAgainOpensTheFormForThatHostAndSendsAgain(t *testing.T) {
	r := newRemoteUI(t, true)
	r.host.fail = func(w http.ResponseWriter) bool {
		w.WriteHeader(http.StatusForbidden)
		return true
	}
	r.app.Refresh()
	r.tt.Frame()
	mustText(t, r.tt, "The host does not recognise this device.")
	click(t, r.tt, "Pair again")
	r.tt.Frame()
	mustText(t, r.tt, "Pair again.", "Continue")
	typeInto(t, r.tt, "Pairing link", "handboard://c#v=1&e="+rtHostB+"&f="+rtB+"&c="+pairCode)
	click(t, r.tt, "Continue")
	r.tt.Frame()
	mustText(t, r.tt, "This link is for another host than the one being paired again.")
	typeInto(t, r.tt, "Pairing link", "handboard://c#v=1&e="+rtHostA+"&f="+rtB+"&c="+pairCode)
	click(t, r.tt, "Continue")
	r.tt.Frame()
	mustText(t, r.tt, "This link is for another fleet than the one being paired again.")
	if r.desk.calls() != 0 {
		t.Fatalf("a link for another fleet was sent: %+v", r.desk.reqs)
	}
	typeInto(t, r.tt, "Pairing link", "handboard://c#v=1&e="+rtHostA+"&f="+rtA+"&c="+pairCode)
	click(t, r.tt, "Continue")
	r.tt.Frame()
	click(t, r.tt, "Add")
	r.tt.Frame()
	if r.desk.calls() != 1 || !r.desk.reqs[0].Again || r.desk.reqs[0].Link.Host != rtHostA {
		t.Fatalf("requests = %+v", r.desk.reqs)
	}
}

func TestABoardNotRunningOnTheHostSaysSoAndOffersNoLocalStart(t *testing.T) {
	r := newRemoteUI(t, true)
	r.status(up(r, sidecar.BoardDown))
	r.host.fail = func(w http.ResponseWriter) bool {
		panic(http.ErrAbortHandler)
	}
	r.app.Refresh()
	r.tt.Frame()
	mustText(t, r.tt, "Board not running on the host. Start it there.", "Board not running")
	for _, s := range r.tt.Texts() {
		if strings.Contains(s, "hand board start") || strings.Contains(s, "Try again") {
			t.Fatalf("a local start is offered: %q", s)
		}
	}
}

func TestOneBadHostsFileSaysSoInsteadOfHidingEveryRemoteHostSilently(t *testing.T) {
	r := newRemoteUI(t, false)
	if err := os.WriteFile(filepath.Join(storeDir(r.g.store), "hosts.json"), []byte(`[{"id":"nope","fleets":[]}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	r.app.Refresh()
	r.tt.Frame()
	mustText(t, r.tt, "Remote hosts are hidden")
	if len(r.app.Fleets) != 1 || r.app.Fleets[0].ID != rtLocal {
		t.Fatalf("fleets = %+v", r.app.Fleets)
	}
}

func TestABadHostsFileWithNoLocalBoardStillSaysSo(t *testing.T) {
	g := newRTRig(t)
	if err := os.MkdirAll(storeDir(g.store), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir(g.store), "hosts.json"), []byte(`[{"id":"nope","fleets":[]}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := desktop.Connect(g.getenv, g.store, g.clients, nil)()
	if err == nil || !errors.Is(err, desktop.ErrNoBoard) || !strings.Contains(err.Error(), "remote hosts are hidden") {
		t.Fatalf("err = %v", err)
	}
}

func TestALocalBoardThatIsDownIsStartedWithoutDroppingTheRemoteFleet(t *testing.T) {
	g := newRTRig(t)
	g.localFleet(t, rtLocal, nil)
	host := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, sidecar.ClientStatus{State: sidecar.Ready, Addr: host.addr(), ID: rtClient, Link: sidecar.Connected})
	spy := &startSpy{block: make(chan struct{})}
	app := startApp(t, desktop.Connect(g.getenv, g.store, g.clients, nil), spy.run)
	posted := make(chan func(), 64)
	app.Post = func(fn func()) { posted <- fn }
	app.StartRefresh()
	spy.waitStarts(t, 1)
	if !strings.HasSuffix(spy.home, "fleet-"+rtLocal) {
		t.Fatalf("started for %q", spy.home)
	}
	if len(app.Fleets) != 2 || app.Fleet.ID == "" || strings.Contains(app.Err, noBoardText) {
		t.Fatalf("fleets %+v selected %+v err %q", app.Fleets, app.Fleet, app.Err)
	}
	close(spy.block)
	for done := false; !done; {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(500 * time.Millisecond):
			done = true
		}
	}
}

func TestTheParsedLinkIsHeldOnlyWhileTheConfirmationOrARetryNeedsIt(t *testing.T) {
	r := newRemoteUI(t, false)
	confirm := func() {
		t.Helper()
		openForm(t, r)
		typeInto(t, r.tt, "Pairing link", "handboard://c#v=1&e="+rtHostB+"&f="+rtB+"&c="+pairCode)
		click(t, r.tt, "Continue")
		r.tt.Frame()
		if !desktop.RemoteLinkPending(r.app) {
			t.Fatal("the confirmation holds nothing to send")
		}
	}
	gone := func(when string) {
		t.Helper()
		if desktop.RemoteLinkPending(r.app) {
			t.Fatalf("the parsed link is still held after %s", when)
		}
	}
	confirm()
	click(t, r.tt, "Back")
	r.tt.Frame()
	gone("Back")
	typeInto(t, r.tt, "Pairing link", "not a link")
	click(t, r.tt, "Continue")
	r.tt.Frame()
	gone("a link that does not parse")
	click(t, r.tt, "Cancel")
	r.tt.Frame()
	gone("Cancel")
	confirm()
	r.desk.err = &desktop.PairError{Code: "unreachable", Message: "Can't reach host.", Transient: true}
	click(t, r.tt, "Add")
	r.tt.Frame()
	if !desktop.RemoteLinkPending(r.app) {
		t.Fatal("a retry has nothing to send")
	}
	r.desk.err = &desktop.PairError{Code: "code_used", Message: "That code was already used. A code works once. Show a new code on the Board."}
	click(t, r.tt, "Try again")
	r.tt.Frame()
	gone("a refusal")
	confirm()
	r.desk.err = nil
	click(t, r.tt, "Add")
	r.tt.Frame()
	gone("a pairing that worked")
}
