package desktop_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/remote"
	"github.com/atqamz/hand/internal/sidecar"
)

const (
	rtLocal   = "f0000000000aa"
	rtA       = "f0000000000bb"
	rtB       = "f0000000000cc"
	rtHostA   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rtHostB   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	rtClient  = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	rtOther   = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	rtCookieA = "1.0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	rtCookieB = "22.fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	rtToken   = "777777777777777777777777777777777777777777777777"
)

type rtCall struct {
	path    string
	cookies []string
	closed  bool
}

type rtHost struct {
	mu     sync.Mutex
	calls  []rtCall
	prefix string
	secret map[string]string
	fleets string
	fail   func(w http.ResponseWriter) bool
	srv    *httptest.Server
}

func newRTHost(t *testing.T, prefix string, secret map[string]string, fleets string) *rtHost {
	h := &rtHost{prefix: prefix, secret: secret, fleets: fleets}
	h.srv = httptest.NewServer(h)
	t.Cleanup(h.srv.Close)
	return h
}

func (h *rtHost) addr() string { return strings.TrimPrefix(h.srv.URL, "http://") }

func (h *rtHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	call := rtCall{path: r.URL.Path, closed: r.Close}
	for _, c := range r.Cookies() {
		call.cookies = append(call.cookies, c.Name+"="+c.Value)
	}
	h.mu.Lock()
	h.calls = append(h.calls, call)
	h.mu.Unlock()
	if h.fail != nil && h.fail(w) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if id, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/proof"); ok {
		mac := hmac.New(sha256.New, []byte(h.secret[id]))
		mac.Write([]byte(r.URL.Query().Get("nonce")))
		io.WriteString(w, hex.EncodeToString(mac.Sum(nil)))
		return
	}
	if r.URL.Path == "/api/v1/fleets" {
		io.WriteString(w, h.fleets)
		return
	}
	if len(parts) < 3 || parts[1] != "api" {
		http.NotFound(w, r)
		return
	}
	if c, err := r.Cookie(h.prefix + parts[0]); err != nil || c.Value != h.secret[parts[0]] {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"status":401,"code":"unauthorized","message":"no session"}}`)
		return
	}
	fmt.Fprintf(w, `{"version":1,"fleet":{"id":%q,"name":"n"},"data":{}}`, parts[0])
}

func (h *rtHost) seen() []rtCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]rtCall(nil), h.calls...)
}

func (h *rtHost) cookiesSeen() string {
	var all []string
	for _, c := range h.seen() {
		all = append(all, c.cookies...)
	}
	return strings.Join(all, " ")
}

func (h *rtHost) reads() int {
	n := 0
	for _, c := range h.seen() {
		if c.path == "/api/v1/fleets" {
			n++
		}
	}
	return n
}

type rtClients struct {
	mu sync.Mutex
	st map[string]sidecar.ClientStatus
	up []string
}

func (c *rtClients) Start(h remote.Host, _ bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.up = append(c.up, h.ID)
	return nil
}

func (c *rtClients) Status(host string) sidecar.ClientStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st[host]
}

func (c *rtClients) set(host string, st sidecar.ClientStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st[host] = st
}

func upAt(h *rtHost, id string) sidecar.ClientStatus {
	return sidecar.ClientStatus{State: sidecar.Ready, Addr: h.addr(), ID: id}
}

type rtRig struct {
	getenv  func(string) string
	store   *remote.Store
	clients *rtClients
}

func newRTRig(t *testing.T) *rtRig {
	t.Helper()
	root := t.TempDir()
	return &rtRig{
		getenv:  func(k string) string { return map[string]string{"SECONDHAND_HOME": root, "HOME": root}[k] },
		store:   remote.OpenStore(filepath.Join(t.TempDir(), "remote")),
		clients: &rtClients{st: map[string]sidecar.ClientStatus{}},
	}
}

func (g *rtRig) localFleet(t *testing.T, id string, board *rtHost) {
	t.Helper()
	root := g.getenv("SECONDHAND_HOME")
	home := filepath.Join(t.TempDir(), "fleet-"+id)
	for path, body := range map[string]string{
		filepath.Join(home, "hand.db"):     "",
		filepath.Join(home, "board.token"): rtToken + "\n",
		filepath.Join(root, "index", id):   home + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if board != nil {
		if err := os.WriteFile(filepath.Join(root, "board.addr"), []byte(board.addr()+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func (g *rtRig) pair(t *testing.T, host, clientID, fleet, cookie string) {
	t.Helper()
	h := remote.Host{ID: host, ClientID: clientID, PairedAt: time.Now()}
	if err := g.store.Add(h, remote.Fleet{ID: fleet, Name: "remote " + fleet}, cookie, nil); err != nil {
		t.Fatal(err)
	}
}

func (g *rtRig) connect(t *testing.T) desktop.ReadModel {
	t.Helper()
	m, err := desktop.Connect(g.getenv, g.store, g.clients, nil)()
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return m
}

const rtFleetsA = `{"version":1,"data":{"fleets":[{"id":"f0000000000bb","name":"a","needs":{"waiting":3,"worst":"blocked","worst_word":"blocked"}}]}}`
const rtFleetsB = `{"version":1,"data":{"fleets":[{"id":"f0000000000cc","name":"b","needs":{"waiting":1,"worst":"report","worst_word":"report"}}]}}`
const rtFleetsLocal = `{"version":1,"data":{"fleets":[{"id":"f0000000000aa","name":"l","needs":{"waiting":2,"worst":"decision","worst_word":"decision"}}]}}`

func signals(t *testing.T, m desktop.ReadModel) map[string]desktop.Signal {
	t.Helper()
	sigs, err := m.Attention(context.Background())
	if err != nil {
		t.Fatalf("attention: %v", err)
	}
	out := map[string]desktop.Signal{}
	for _, s := range sigs {
		out[s.Fleet] = s.Signal
	}
	return out
}

func TestEachFleetGoesToItsOwnHostWithItsOwnCookieAndNoOtherCredential(t *testing.T) {
	g := newRTRig(t)
	local := newRTHost(t, "hand_board_", map[string]string{rtLocal: rtToken}, rtFleetsLocal)
	a := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	b := newRTHost(t, "hand_phone_", map[string]string{rtB: rtCookieB}, rtFleetsB)
	g.localFleet(t, rtLocal, local)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.pair(t, rtHostB, rtOther, rtB, rtCookieB)
	g.clients.set(rtHostA, upAt(a, rtClient))
	g.clients.set(rtHostB, upAt(b, rtOther))
	m := g.connect(t)
	for _, id := range []string{rtLocal, rtA, rtB} {
		if _, err := m.Status(context.Background(), id); err != nil {
			t.Fatalf("status %s: %v", id, err)
		}
	}
	if got := local.cookiesSeen(); strings.Contains(got, "hand_phone_") || strings.Contains(got, rtCookieA) || strings.Contains(got, rtCookieB) {
		t.Fatalf("the local board saw a remote credential: %s", got)
	}
	if got := a.cookiesSeen(); !strings.Contains(got, "hand_phone_"+rtA+"="+rtCookieA) || strings.Contains(got, rtCookieB) || strings.Contains(got, rtToken) || strings.Contains(got, "hand_board_") {
		t.Fatalf("host A saw %s", got)
	}
	if got := b.cookiesSeen(); !strings.Contains(got, "hand_phone_"+rtB+"="+rtCookieB) || strings.Contains(got, rtCookieA) || strings.Contains(got, rtToken) || strings.Contains(got, "hand_board_") {
		t.Fatalf("host B saw %s", got)
	}
	for _, h := range []*rtHost{a, b} {
		for _, c := range h.seen() {
			if strings.HasSuffix(c.path, "/proof") {
				t.Fatalf("a paired host was asked for a proof: %s", c.path)
			}
			if !c.closed {
				t.Fatalf("a request to a paired host kept its connection alive: %s", c.path)
			}
		}
	}
	proofs := 0
	for _, c := range local.seen() {
		if strings.HasSuffix(c.path, "/proof") {
			proofs++
		}
	}
	if proofs == 0 {
		t.Fatal("the local board was not asked for its proof")
	}
	var held []string
	for _, f := range m.Fleets() {
		held = append(held, f.ID+"@"+f.Host+"|"+f.Home)
	}
	if got := strings.Join(held, " "); !strings.Contains(got, rtA+"@"+rtHostA+"|") || !strings.Contains(got, rtB+"@"+rtHostB+"|") || !strings.Contains(got, rtLocal+"@|") {
		t.Fatalf("fleets = %s", got)
	}
	if len(g.clients.up) != 2 {
		t.Fatalf("started clients = %v", g.clients.up)
	}
}

func TestAFleetNobodyHoldsIsAskedOfNoRemoteHost(t *testing.T) {
	g := newRTRig(t)
	local := newRTHost(t, "hand_board_", map[string]string{rtLocal: rtToken}, rtFleetsLocal)
	a := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	g.localFleet(t, rtLocal, local)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, upAt(a, rtClient))
	m := g.connect(t)
	if _, err := m.Status(context.Background(), rtB); err == nil || !strings.Contains(err.Error(), "board.token") {
		t.Fatalf("an unheld fleet should be refused by the local client, got %v", err)
	}
	if len(a.seen()) != 0 {
		t.Fatalf("host A was asked about a fleet it does not hold: %v", a.seen())
	}
}

const rtFleetsDup = `{"version":1,"data":{"fleets":[{"id":"f0000000000aa","name":"l","needs":{"waiting":9,"worst":"blocked","worst_word":"blocked"}},{"id":"f0000000000bb","name":"a","needs":{"waiting":3,"worst":"blocked","worst_word":"blocked"}}]}}`

func TestAFleetHeldTwiceStaysWithItsFirstHolderAndTheOtherHostIsNeverAsked(t *testing.T) {
	g := newRTRig(t)
	local := newRTHost(t, "hand_board_", map[string]string{rtLocal: rtToken}, rtFleetsLocal)
	a := newRTHost(t, "hand_phone_", map[string]string{rtLocal: rtCookieA}, rtFleetsA)
	g.localFleet(t, rtLocal, local)
	g.pair(t, rtHostA, rtClient, rtLocal, rtCookieA)
	g.clients.set(rtHostA, upAt(a, rtClient))
	m := g.connect(t)
	if n := len(m.Fleets()); n != 1 {
		t.Fatalf("fleets = %+v", m.Fleets())
	}
	if f := m.Fleets()[0]; f.Host != "" || f.ID != rtLocal {
		t.Fatalf("the fleet moved to the remote holder: %+v", f)
	}
	if _, err := m.Status(context.Background(), rtLocal); err != nil {
		t.Fatal(err)
	}
	if got := signals(t, m); len(got) != 1 || got[rtLocal].Waiting != 2 {
		t.Fatalf("signals = %+v", got)
	}
	if len(a.seen()) != 0 {
		t.Fatalf("the second holder was asked: %v", a.seen())
	}
	if got := local.cookiesSeen(); strings.Contains(got, rtCookieA) {
		t.Fatalf("the remote cookie went to the local board: %s", got)
	}
}

func TestAHostHoldingAFleetTwiceIsReadForItsOwnFleetsOnly(t *testing.T) {
	g := newRTRig(t)
	local := newRTHost(t, "hand_board_", map[string]string{rtLocal: rtToken}, rtFleetsLocal)
	a := newRTHost(t, "hand_phone_", map[string]string{rtLocal: rtCookieA, rtA: rtCookieA}, rtFleetsDup)
	g.localFleet(t, rtLocal, local)
	g.pair(t, rtHostA, rtClient, rtLocal, rtCookieA)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, upAt(a, rtClient))
	got := signals(t, g.connect(t))
	if len(got) != 2 || got[rtLocal].Waiting != 2 || got[rtA].Waiting != 3 {
		t.Fatalf("signals = %+v", got)
	}
}

func TestALocalBoardThatFailsMarksOnlyItsFleetsAndARemoteOneThatFailsOnlyItsOwn(t *testing.T) {
	g := newRTRig(t)
	local := newRTHost(t, "hand_board_", map[string]string{rtLocal: rtToken}, rtFleetsLocal)
	a := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	b := newRTHost(t, "hand_phone_", map[string]string{rtB: rtCookieB}, rtFleetsB)
	g.localFleet(t, rtLocal, local)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.pair(t, rtHostB, rtOther, rtB, rtCookieB)
	g.clients.set(rtHostA, upAt(a, rtClient))
	g.clients.set(rtHostB, upAt(b, rtOther))
	b.fail = func(w http.ResponseWriter) bool { w.WriteHeader(http.StatusInternalServerError); return true }
	got := signals(t, g.connect(t))
	if !got[rtLocal].OK || got[rtLocal].Waiting != 2 {
		t.Fatalf("the local fleet did not survive a remote failure: %+v", got[rtLocal])
	}
	if !got[rtA].OK || got[rtA].Waiting != 3 {
		t.Fatalf("host A: %+v", got[rtA])
	}
	if s, ok := got[rtB]; !ok || s.OK {
		t.Fatalf("host B's fleet should be listed unavailable: %+v, %v", s, ok)
	}

	g2 := newRTRig(t)
	local2 := newRTHost(t, "hand_board_", map[string]string{rtLocal: rtToken}, rtFleetsLocal)
	local2.fail = func(w http.ResponseWriter) bool { w.WriteHeader(http.StatusInternalServerError); return true }
	g2.localFleet(t, rtLocal, local2)
	g2.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g2.clients.set(rtHostA, upAt(a, rtClient))
	got = signals(t, g2.connect(t))
	if s, ok := got[rtLocal]; !ok || s.OK {
		t.Fatalf("the local fleet should be unavailable: %+v, %v", s, ok)
	}
	if !got[rtA].OK {
		t.Fatalf("a failing local board marked a remote fleet: %+v", got[rtA])
	}
}

func TestAHostWithNoClientMarksOnlyItsFleetsUnavailableAndSendsNothing(t *testing.T) {
	g := newRTRig(t)
	local := newRTHost(t, "hand_board_", map[string]string{rtLocal: rtToken}, rtFleetsLocal)
	a := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	g.localFleet(t, rtLocal, local)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, sidecar.ClientStatus{State: sidecar.Starting})
	m := g.connect(t)
	got := signals(t, m)
	if !got[rtLocal].OK || got[rtA].OK {
		t.Fatalf("signals = %+v", got)
	}
	if _, err := m.Status(context.Background(), rtA); err == nil || errors.As(err, new(*desktop.APIError)) || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("a host with no client should fail as offline, got %v", err)
	}
	if len(a.seen()) != 0 {
		t.Fatalf("a request went out with no client: %v", a.seen())
	}
}

func TestARemoteOnlyWindowSurvivesNoLocalBoard(t *testing.T) {
	g := newRTRig(t)
	a := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, upAt(a, rtClient))
	m := g.connect(t)
	if f := m.Fleets(); len(f) != 1 || f[0].ID != rtA || f[0].Host != rtHostA || f[0].Home != "" {
		t.Fatalf("fleets = %+v", f)
	}
	if _, err := m.Name(context.Background(), rtA); err != nil {
		t.Fatalf("name: %v", err)
	}
	if got := signals(t, m); !got[rtA].OK || got[rtA].Waiting != 3 {
		t.Fatalf("signals = %+v", got)
	}
}

func TestALocalFleetWithNoBoardStaysListedAndReadsAsNoBoardBesideARemoteHost(t *testing.T) {
	g := newRTRig(t)
	a := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	g.localFleet(t, rtLocal, nil)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, upAt(a, rtClient))
	m := g.connect(t)
	if len(m.Fleets()) != 2 {
		t.Fatalf("fleets = %+v", m.Fleets())
	}
	if _, err := m.Name(context.Background(), rtLocal); !errors.Is(err, desktop.ErrNoBoard) {
		t.Fatalf("local read = %v", err)
	}
	got := signals(t, m)
	if s, ok := got[rtLocal]; !ok || s.OK {
		t.Fatalf("local signal = %+v, %v", s, ok)
	}
	if !got[rtA].OK {
		t.Fatalf("remote signal = %+v", got[rtA])
	}
}

func TestWithNoRemoteHostTheLocalContractHolds(t *testing.T) {
	g := newRTRig(t)
	if _, err := desktop.Connect(g.getenv, g.store, g.clients, nil)(); !errors.Is(err, desktop.ErrNoBoard) {
		t.Fatalf("no board, no host = %v", err)
	}
	local := newRTHost(t, "hand_board_", map[string]string{rtLocal: rtToken}, rtFleetsLocal)
	g.localFleet(t, rtLocal, local)
	m, err := desktop.Connect(g.getenv, nil, nil, nil)()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.(*desktop.HTTPClient); !ok {
		t.Fatalf("a window with no remote host should read the local client directly, got %T", m)
	}
	if f := m.Fleets(); len(f) != 1 || f[0].ID != rtLocal || f[0].Host != "" {
		t.Fatalf("fleets = %+v", f)
	}
}

func TestRemoteNeedsAreReadEvery15SecondsAndLocalOnesEveryCall(t *testing.T) {
	g := newRTRig(t)
	local := newRTHost(t, "hand_board_", map[string]string{rtLocal: rtToken}, rtFleetsLocal)
	a := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	g.localFleet(t, rtLocal, local)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, upAt(a, rtClient))
	m := g.connect(t)
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	desktop.SetRouterClock(m.(*desktop.Router), func() time.Time { return now })
	for _, step := range []struct {
		advance time.Duration
		local   int
		remote  int
	}{{0, 1, 1}, {5 * time.Second, 2, 1}, {9 * time.Second, 3, 1}, {1 * time.Second, 4, 2}, {14 * time.Second, 5, 2}, {time.Second, 6, 3}} {
		now = now.Add(step.advance)
		got := signals(t, m)
		if !got[rtA].OK || got[rtA].Waiting != 3 || !got[rtLocal].OK {
			t.Fatalf("at +%s signals = %+v", step.advance, got)
		}
		if local.reads() != step.local || a.reads() != step.remote {
			t.Fatalf("at +%s local reads %d (want %d), remote %d (want %d)", step.advance, local.reads(), step.local, a.reads(), step.remote)
		}
	}
}

func TestACancelledAttentionReadIsNotKeptAsAnUnavailableHost(t *testing.T) {
	g := newRTRig(t)
	a := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, upAt(a, rtClient))
	m := g.connect(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Attention(ctx); err == nil {
		t.Fatal("a cancelled read returned no error")
	}
	if got := signals(t, m); !got[rtA].OK {
		t.Fatalf("the cancelled read poisoned the next one: %+v", got)
	}
}

func TestAKeyThatIsNotTheOnePairedSendsNothingAndSaysSo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		paired  string
		running string
		want    string
	}{
		{"replaced", rtClient, rtOther, desktop.CodeKeyChanged},
		{"same", rtClient, rtClient, ""},
		{"not recorded", "", rtOther, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newRTRig(t)
			a := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
			g.pair(t, rtHostA, tc.paired, rtA, rtCookieA)
			g.clients.set(rtHostA, upAt(a, tc.running))
			m := g.connect(t)
			_, err := m.Status(context.Background(), rtA)
			var api *desktop.APIError
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("status = %v", err)
			case tc.want != "" && (!errors.As(err, &api) || api.Code != tc.want):
				t.Fatalf("status error = %v", err)
			}
			if tc.want != "" {
				if len(a.seen()) != 0 {
					t.Fatalf("the old cookie went out with a replaced key: %v", a.seen())
				}
				if got := signals(t, m); got[rtA].OK {
					t.Fatalf("a replaced key left the fleet available: %+v", got[rtA])
				}
				if len(a.seen()) != 0 {
					t.Fatalf("attention sent the old cookie: %v", a.seen())
				}
			}
		})
	}
}

func pairedClient(t *testing.T, h *rtHost, cookie string) *desktop.HTTPClient {
	t.Helper()
	return &desktop.HTTPClient{
		Paired: true,
		Base:   h.srv.URL,
		HTTP:   h.srv.Client(),
		Known:  []desktop.Fleet{{ID: rtA, Name: "a", Host: rtHostA}},
		Token:  func(string) (string, error) { return cookie, nil },
	}
}

func TestAPeerRefusalWithTheHeaderIsRevokedAndWithoutItIsNotRecognised(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		header string
		body   string
		code   string
	}{
		{"header", 403, "This%20Companion%20is%20no%20longer%20paired%20with%20this%20fleet.", "This Companion is no longer paired", desktop.CodeRevoked},
		{"header with an envelope", 403, "no%20longer%20paired", `{"error":{"status":403,"code":"forbidden","message":"x"}}`, desktop.CodeRevoked},
		{"no header", 403, "", "Pair this Companion first: open this fleet's Board", desktop.CodeNotRecognised},
		{"no header, no body", 403, "", "", desktop.CodeNotRecognised},
		{"an envelope", 403, "", `{"error":{"status":403,"code":"forbidden","message":"the board's controls are off"}}`, "forbidden"},
		{"another status with the header", 409, "stale", "x", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
			h.fail = func(w http.ResponseWriter) bool {
				if tc.header != "" {
					w.Header().Set("X-Hand-Error", tc.header)
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
				return true
			}
			_, err := pairedClient(t, h, rtCookieA).Status(context.Background(), rtA)
			var api *desktop.APIError
			if tc.code == "" {
				if errors.As(err, &api) {
					t.Fatalf("a %d was read as %q", tc.status, api.Code)
				}
				return
			}
			if !errors.As(err, &api) || api.Code != tc.code || api.Status != 403 || !api.Remote {
				t.Fatalf("error = %#v", err)
			}
			echoes := tc.body != "" && tc.code != "forbidden" && strings.Contains(err.Error(), tc.body)
			if strings.Contains(err.Error(), rtCookieA) || echoes {
				t.Fatalf("the error echoes the cookie or the host's text: %v", err)
			}
		})
	}
}

func TestALocalBoardsHeaderedRefusalIsNeverReadAsRevoked(t *testing.T) {
	h := newRTHost(t, "hand_board_", map[string]string{rtA: rtToken}, rtFleetsA)
	c := &desktop.HTTPClient{Base: h.srv.URL, HTTP: h.srv.Client(), Token: func(string) (string, error) { return rtToken, nil }}
	h.fail = func(w http.ResponseWriter) bool {
		if strings.HasSuffix(h.seen()[len(h.seen())-1].path, "/proof") {
			return false
		}
		w.Header().Set("X-Hand-Error", "no%20longer%20paired")
		w.WriteHeader(http.StatusForbidden)
		return true
	}
	_, err := c.Status(context.Background(), rtA)
	var api *desktop.APIError
	if errors.As(err, &api) {
		t.Fatalf("a local 403 was read as %q", api.Code)
	}
	if err == nil {
		t.Fatal("no error")
	}
}

func TestAnUnreadableFleetIndexNeverMasksTheMissingBoard(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index"), []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string { return map[string]string{"SECONDHAND_HOME": root}[k] }
	if _, err := desktop.NewLocal(getenv); !errors.Is(err, desktop.ErrNoBoard) {
		t.Fatalf("NewLocal = %v", err)
	}
	if _, err := desktop.Connect(getenv, nil, nil, nil)(); !errors.Is(err, desktop.ErrNoBoard) {
		t.Fatalf("Connect = %v", err)
	}
}

type stubModel struct {
	desktop.ReadModel
	fleets []desktop.Fleet
	sigs   []desktop.FleetSignal
	err    error
	status error
}

func (s *stubModel) Fleets() []desktop.Fleet { return s.fleets }

func (s *stubModel) Attention(context.Context) ([]desktop.FleetSignal, error) { return s.sigs, s.err }

func (s *stubModel) Status(context.Context, string) (desktop.Status, error) {
	return desktop.Status{}, s.status
}

func revokedErr() error {
	return &desktop.APIError{Status: 403, Code: desktop.CodeRevoked, Remote: true, Message: "revoked"}
}

func TestARefusalIsClearedOnlyForFleetsTheHostConfirms(t *testing.T) {
	host := &stubModel{fleets: []desktop.Fleet{{ID: rtA, Host: rtHostA}, {ID: rtB, Host: rtHostA}}, status: revokedErr()}
	r := desktop.NewRouter(&stubModel{}, host)
	now := time.Now()
	desktop.SetRouterClock(r, func() time.Time { return now })
	for _, id := range []string{rtA, rtB} {
		if _, err := r.Status(context.Background(), id); err == nil {
			t.Fatal("no refusal from the host")
		}
	}
	poll := func(sigs ...desktop.FleetSignal) {
		t.Helper()
		now = now.Add(desktop.RemoteAttentionRefresh + time.Second)
		host.sigs = sigs
		if _, err := r.Attention(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	poll(desktop.FleetSignal{Fleet: rtA, Signal: desktop.Signal{OK: false}})
	poll()
	if r.Refusal(rtA) != desktop.CodeRevoked || r.Refusal(rtB) != desktop.CodeRevoked {
		t.Fatalf("refusals after an unavailable and an omitted fleet: %q %q", r.Refusal(rtA), r.Refusal(rtB))
	}
	poll(desktop.FleetSignal{Fleet: rtA, Signal: desktop.Signal{OK: true}})
	if r.Refusal(rtA) != "" || r.Refusal(rtB) != desktop.CodeRevoked {
		t.Fatalf("refusals after a confirmed fleet: %q %q", r.Refusal(rtA), r.Refusal(rtB))
	}
}

func TestAHostsRefusalNeverMarksAFleetAnotherHostOwns(t *testing.T) {
	first := &stubModel{fleets: []desktop.Fleet{{ID: rtA, Host: rtHostA}}, err: errors.New("down")}
	second := &stubModel{fleets: []desktop.Fleet{{ID: rtA, Host: rtHostB}, {ID: rtB, Host: rtHostB}}, err: revokedErr()}
	r := desktop.NewRouter(&stubModel{}, first, second)
	if _, err := r.Attention(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Refusal(rtA) != "" || r.Refusal(rtB) != desktop.CodeRevoked {
		t.Fatalf("refusals: %q %q", r.Refusal(rtA), r.Refusal(rtB))
	}
}
