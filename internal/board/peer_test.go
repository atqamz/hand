package board_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/sidecar"
)

const peerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef"

var (
	peerA     = strings.Repeat("a", 64)
	peerB     = strings.Repeat("b", 64)
	staticRe  = regexp.MustCompile(`/static/[A-Za-z0-9._-]+`)
	composeRe = regexp.MustCompile(`id="composer"`)
)

type peerRig struct {
	*phoneHost
	addr string
}

func newPeerRig(t *testing.T, names ...string) *peerRig {
	t.Helper()
	return servePeerRig(t, newPhoneHost(t, true, names...))
}

func servePeerRig(t *testing.T, p *phoneHost) *peerRig {
	t.Helper()
	peer := board.NewPeer(p.h, peerSecret)
	p.closePeer = peer.CloseEndpoint
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = peer.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = peer.Shutdown(ctx)
	})
	return &peerRig{phoneHost: p, addr: ln.Addr().String()}
}

type peerClient struct {
	t    *testing.T
	conn net.Conn
	br   *bufio.Reader
	line string
}

func (r *peerRig) dial(preface string) *peerClient {
	r.t.Helper()
	conn, err := net.Dial("tcp", r.addr)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = conn.Close() })
	c := &peerClient{t: r.t, conn: conn, br: bufio.NewReader(conn), line: preface}
	return c
}

func (r *peerRig) as(id string) *peerClient {
	return r.dial("HAND-PEER 1 " + id + " " + peerSecret + "\n")
}

func (r *peerRig) anonymous() *peerClient { return r.dial("") }

type peerReply struct {
	Code    int
	Body    string
	Cookies []*http.Cookie
	Header  http.Header
	Close   bool
}

func (c *peerClient) send(method, path string, form url.Values, headers map[string]string, cookies ...*http.Cookie) (peerReply, error) {
	c.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, "http://localhost"+path, body)
	if err != nil {
		c.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	_ = c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if c.line != "" {
		if _, err := io.WriteString(c.conn, c.line); err != nil {
			return peerReply{}, err
		}
		c.line = ""
	}
	if err := req.Write(c.conn); err != nil {
		return peerReply{}, err
	}
	res, err := http.ReadResponse(c.br, req)
	if err != nil {
		return peerReply{}, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return peerReply{}, err
	}
	return peerReply{Code: res.StatusCode, Body: string(b), Cookies: res.Cookies(), Header: res.Header, Close: res.Close}, nil
}

func (c *peerClient) do(method, path string, form url.Values, headers map[string]string, cookies ...*http.Cookie) peerReply {
	c.t.Helper()
	rep, err := c.send(method, path, form, headers, cookies...)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	return rep
}

func (c *peerClient) closed() bool {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := c.br.Read(make([]byte, 1))
	var timeout net.Error
	return n == 0 && err != nil && !(errors.As(err, &timeout) && timeout.Timeout())
}

func (r *peerRig) code(f railFleet) string {
	r.t.Helper()
	return r.codeOf(r.mint(f, []*http.Cookie{r.laptop(f)}))
}

func (r *peerRig) pairOver(c *peerClient, f railFleet, headers map[string]string) (*http.Cookie, peerReply) {
	r.t.Helper()
	rep := c.do("POST", "/"+f.id+"/pair", url.Values{"code": {r.code(f)}, "name": {"Pixel"}}, headers)
	var phone *http.Cookie
	for _, ck := range rep.Cookies {
		if ck.MaxAge > 0 && strings.Contains(ck.Name, "hand_phone") {
			phone = ck
		}
	}
	return phone, rep
}

func (r *peerRig) mustPair(c *peerClient, f railFleet) *http.Cookie {
	r.t.Helper()
	phone, rep := r.pairOver(c, f, nil)
	if rep.Code != http.StatusSeeOther || phone == nil {
		r.t.Fatalf("pair = %d:\n%s", rep.Code, rep.Body)
	}
	return phone
}

func TestAPeerWithTheSecretIsServedAsRemoteAndKeepsItsIdentity(t *testing.T) {
	r := newPeerRig(t, "alpha", "bravo")
	a, b := r.fleets[0], r.fleets[1]
	c := r.as(peerA)
	phone := r.mustPair(c, a)
	if devices := r.devicesOf(a); len(devices) != 1 || devices[0].Login != "iroh:"+peerA {
		t.Fatalf("the device is bound to %+v, want iroh:%s", devices, peerA)
	}
	home := c.do("GET", "/", nil, nil, phone)
	if home.Code != http.StatusOK || !strings.Contains(home.Body, "alpha") || strings.Contains(home.Body, "bravo") {
		t.Fatalf("a paired peer's / = %d and lists the fleets it is not paired with (bravo: %v)", home.Code, strings.Contains(home.Body, "bravo"))
	}
	if rep := c.do("GET", "/"+b.id+"/", nil, nil, phone); rep.Code != http.StatusForbidden {
		t.Fatalf("the phone's cookie opens a fleet it is not paired with: %d", rep.Code)
	}
}

func TestAPairedPeerReachesTheWholeBoardWithControls(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	phone := r.mustPair(c, f)
	for _, path := range []string{"/" + f.id + "/", "/" + f.id + "/connect", "/" + f.id + "/tasks"} {
		if rep := c.do("GET", path, nil, nil, phone); rep.Code != http.StatusOK {
			t.Fatalf("%s = %d", path, rep.Code)
		}
	}
	if rep := c.do("GET", "/"+f.id+"/", nil, nil, phone); !composeRe.MatchString(rep.Body) {
		t.Fatalf("the supervisor composer is off for a paired peer:\n%s", rep.Body)
	}
}

func TestACookieOnlyWorksFromThePeerItWasPairedFor(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	phone := r.mustPair(r.as(peerA), f)
	if rep := r.as(peerA).do("GET", "/"+f.id+"/", nil, nil, phone); rep.Code != http.StatusOK {
		t.Fatalf("the same peer on a new connection = %d", rep.Code)
	}
	for name, c := range map[string]*peerClient{"another peer": r.as(peerB), "no identity": r.anonymous()} {
		if rep := c.do("GET", "/"+f.id+"/", nil, nil, phone); rep.Code != http.StatusForbidden {
			t.Fatalf("the cookie opened the board for %s: %d", name, rep.Code)
		}
	}
	if rec := r.send("GET", "/"+f.id+"/", nil, []*http.Cookie{phone}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("the cookie opened the board over the main listener: %d", rec.Code)
	}
	if rec := r.send("GET", "/"+f.id+"/", nil, []*http.Cookie{phone}, map[string]string{"Tailscale-User-Login": "iroh:" + peerA, "X-Forwarded-For": "100.64.0.7"}); rec.Code != http.StatusForbidden {
		t.Fatalf("a forged iroh login header on the main listener opened the board: %d", rec.Code)
	}
}

func TestAForgedIrohLoginHeaderPairsWithNoLogin(t *testing.T) {
	p := newPhoneHost(t, true, "alpha")
	f := p.fleets[0]
	p.pair(f, serving())
	if devices := p.devicesOf(f); len(devices) != 1 || devices[0].Login != "" {
		t.Fatalf("a header login of iroh:ID was recorded as %+v", devices)
	}
}

func TestARevokedPeerIsRefusedOnTheConnectionItAlreadyHolds(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	phone := r.mustPair(c, f)
	if rep := c.do("GET", "/"+f.id+"/", nil, nil, phone); rep.Code != http.StatusOK {
		t.Fatalf("paired = %d", rep.Code)
	}
	r.revoke(f, phone)
	if !c.closed() {
		t.Fatal("revoke left the connection open")
	}
	again := r.as(peerA)
	if rep := again.do("GET", "/"+f.id+"/", nil, nil, phone); rep.Code != http.StatusForbidden {
		t.Fatalf("a revoked peer on a new connection = %d", rep.Code)
	}
}

func TestAPairedConnectionStillNeedsAPhoneCookieForEveryRequest(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	phone := r.mustPair(c, f)
	if rep := c.do("GET", "/"+f.id+"/", nil, nil, phone); rep.Code != http.StatusOK {
		t.Fatalf("paired = %d", rep.Code)
	}
	for _, path := range []string{"/" + f.id + "/proof?nonce=x", "/" + f.id + "/", "/"} {
		if rep := c.do("GET", path, nil, nil); rep.Code == http.StatusOK {
			t.Fatalf("%s answered a request with no cookie on a connection that had paired", path)
		}
	}
}

func TestAnUnpairedPeerReachesOnlyThePairPageAndItsAssets(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	for name, c := range map[string]*peerClient{"identified": r.as(peerA), "anonymous": r.anonymous()} {
		page := c.do("GET", "/"+f.id+"/pair", nil, nil)
		if page.Code != http.StatusOK || !strings.Contains(page.Body, "Pair this phone") {
			t.Fatalf("%s: the pair page = %d", name, page.Code)
		}
		asset := staticRe.FindString(page.Body)
		if asset == "" {
			t.Fatalf("%s: the pair page names no asset", name)
		}
		if rep := c.do("GET", asset, nil, nil); rep.Code != http.StatusOK {
			t.Fatalf("%s: %s = %d", name, asset, rep.Code)
		}
		for _, path := range []string{"/", "/" + f.id + "/", "/" + f.id + "/connect", "/" + f.id + "/events", "/" + f.id + "/proof?nonce=x", "/" + f.id + "/pair/", "/manifest.webmanifest", "/sw.js", "//static/x"} {
			if rep := c.do("GET", path, nil, nil); rep.Code == http.StatusOK || strings.Contains(rep.Body, "alpha") {
				t.Fatalf("%s: %s = %d for an unpaired peer", name, path, rep.Code)
			}
		}
		if rep := c.do("POST", "/"+f.id+"/supervisor/send", url.Values{"csrf": {"x"}, "text": {"hi"}}, nil); rep.Code != http.StatusForbidden {
			t.Fatalf("%s: an unpaired peer posted to the supervisor: %d", name, rep.Code)
		}
	}
}

func TestThePeerListenerNeverTakesTheLaptopToken(t *testing.T) {
	r := newPeerRig(t, "alpha", "bravo")
	a, b := r.fleets[0], r.fleets[1]
	c := r.as(peerA)
	if rep := c.do("GET", "/"+a.id+"/?token="+a.token, nil, nil); rep.Code != http.StatusForbidden || len(rep.Cookies) != 0 {
		t.Fatalf("a token link on the peer listener = %d with %d cookies", rep.Code, len(rep.Cookies))
	}
	if rep := c.do("GET", "/"+a.id+"/", nil, nil, r.laptop(a)); rep.Code != http.StatusForbidden {
		t.Fatalf("the laptop cookie opened the board over the peer listener: %d", rep.Code)
	}
	if rep := c.do("GET", "/"+a.id+"/pair?token="+a.token, nil, nil); len(rep.Cookies) != 0 {
		t.Fatalf("the pair path took a token and set %d cookies", len(rep.Cookies))
	}
	phone := r.mustPair(c, a)
	if rep := c.do("GET", "/"+b.id+"/?token="+b.token, nil, nil, phone); rep.Code != http.StatusForbidden || len(rep.Cookies) != 0 {
		t.Fatalf("a paired peer logged in to another fleet with its token: %d, %d cookies", rep.Code, len(rep.Cookies))
	}
	if rep := c.do("GET", "/"+b.id+"/connect", nil, nil, phone, r.laptop(b)); rep.Code != http.StatusForbidden {
		t.Fatalf("a paired peer opened another fleet with its laptop cookie: %d", rep.Code)
	}
	if rep := c.do("GET", "/"+a.id+"/connect", nil, nil, phone, r.laptop(a)); rep.Code != http.StatusOK || strings.Contains(rep.Body, "Show pairing code") {
		t.Fatalf("a laptop cookie beside the phone's gave the laptop's Phone page: %d", rep.Code)
	}
}

func TestAPeerGetsNoLaptopLoginEvenWithKeep(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	rec := r.send("GET", "/"+f.id+"/pair?token="+f.token+"&keep=1", nil, nil, nil)
	if got := rec.Result().Cookies(); len(got) != 1 || got[0].MaxAge != 400*24*3600 {
		t.Fatalf("the main listener keeps the login for %v", got)
	}
	rep := r.as(peerA).do("GET", "/"+f.id+"/pair?token="+f.token+"&keep=1", nil, nil)
	if len(rep.Cookies) != 0 {
		t.Fatalf("the peer listener sets %+v for a token link with keep=1", rep.Cookies)
	}
}

func TestClientSentProxyHeadersDoNothingOnThePeerListener(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	spoof := map[string]string{
		"X-Forwarded-For":        "203.0.113.9",
		"X-Forwarded-Proto":      "https",
		"X-Forwarded-Host":       "evil.example",
		"Forwarded":              "for=203.0.113.9;proto=https",
		"Tailscale-User-Login":   "iroh:" + peerB,
		"Tailscale-User-Name":    "Victim",
		"Tailscale-Headers-Info": "x",
	}
	phone, rep := r.pairOver(r.as(peerA), f, spoof)
	if rep.Code != http.StatusSeeOther || phone == nil {
		t.Fatalf("pair = %d", rep.Code)
	}
	if strings.HasPrefix(phone.Name, "__Host-") || phone.Secure {
		t.Fatalf("a client-sent X-Forwarded-Proto made the cookie %q secure=%v", phone.Name, phone.Secure)
	}
	if devices := r.devicesOf(f); len(devices) != 1 || devices[0].Login != "iroh:"+peerA {
		t.Fatalf("the device is bound to %+v after a spoofed login header", devices)
	}
	if rep := r.as(peerB).do("GET", "/"+f.id+"/", nil, spoof, phone); rep.Code != http.StatusForbidden {
		t.Fatalf("a spoofed login header opened the board for another peer: %d", rep.Code)
	}
	if rep := r.anonymous().do("GET", "/"+f.id+"/", nil, spoof, phone); rep.Code != http.StatusForbidden {
		t.Fatalf("a spoofed login header opened the board with no identity: %d", rep.Code)
	}
}

func TestAnAnonymousPeerPairsWithNoBindingAndNeverAsTheLaptop(t *testing.T) {
	r := newPeerRig(t, "alpha", "bravo")
	f := r.fleets[0]
	c := r.anonymous()
	phone := r.mustPair(c, f)
	if devices := r.devicesOf(f); len(devices) != 1 || devices[0].Login != "" {
		t.Fatalf("an anonymous pairing recorded %+v, want no login", devices)
	}
	if home := c.do("GET", "/", nil, nil, phone); strings.Contains(home.Body, "bravo") {
		t.Fatalf("an anonymous peer saw a fleet it is not paired with:\n%s", home.Body)
	}
}

func TestEachPeerHasItsOwnPairingTries(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	code := r.code(f)
	wrong := func(c *peerClient) int {
		return c.do("POST", "/"+f.id+"/pair", url.Values{"code": {"ZZZZZ-ZZZZZ"}, "name": {"x"}}, nil).Code
	}
	a := r.as(peerA)
	for i := 0; i < 5; i++ {
		if got := wrong(a); got != http.StatusForbidden {
			t.Fatalf("wrong try %d = %d", i+1, got)
		}
	}
	if got := wrong(r.as(peerA)); got != http.StatusTooManyRequests {
		t.Fatalf("a fresh connection of a blocked peer = %d, want 429", got)
	}
	if rep := r.as(peerB).do("POST", "/"+f.id+"/pair", url.Values{"code": {code}, "name": {"x"}}, nil); rep.Code != http.StatusSeeOther {
		t.Fatalf("another peer is blocked by the first one's tries: %d", rep.Code)
	}
}

func TestPeerTriesNeverBlockTheLoopbackAddress(t *testing.T) {
	for name, dial := range map[string]func(*peerRig) *peerClient{
		"anonymous":  func(r *peerRig) *peerClient { return r.anonymous() },
		"identified": func(r *peerRig) *peerClient { return r.as(peerA) },
	} {
		r := newPeerRig(t, "alpha")
		f := r.fleets[0]
		code := r.code(f)
		c := dial(r)
		for i := 0; i < 5; i++ {
			c.do("POST", "/"+f.id+"/pair", url.Values{"code": {"ZZZZZ-ZZZZZ"}, "name": {"x"}}, nil)
		}
		req := httptest.NewRequest("POST", "/"+f.id+"/pair", strings.NewReader(url.Values{"code": {code}, "name": {"x"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "127.0.0.1:40000"
		rec := httptest.NewRecorder()
		r.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s: wrong tries over the peer listener blocked the loopback address: %d", name, rec.Code)
		}
	}
}

func TestAnonymousPeersShareOnePairingBucket(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	r.code(f)
	for i := 0; i < 5; i++ {
		r.anonymous().do("POST", "/"+f.id+"/pair", url.Values{"code": {"ZZZZZ-ZZZZZ"}, "name": {"x"}}, nil)
	}
	if got := r.anonymous().do("POST", "/"+f.id+"/pair", url.Values{"code": {"ZZZZZ-ZZZZZ"}, "name": {"x"}}, nil).Code; got != http.StatusTooManyRequests {
		t.Fatalf("new anonymous connections get fresh tries: %d", got)
	}
}

func TestAPeerListenerRefusesAWrongOrMalformedPreface(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	good := peerSecret
	for name, preface := range map[string]string{
		"wrong secret":     "HAND-PEER 1 " + peerA + " " + strings.Repeat("0", 48) + "\n",
		"shorter secret":   "HAND-PEER 1 " + peerA + " " + good[:47] + "\n",
		"empty secret":     "HAND-PEER 1 " + peerA + " \n",
		"no secret field":  "HAND-PEER 1 " + peerA + "\n",
		"version 2":        "HAND-PEER 2 " + peerA + " " + good + "\n",
		"upper-case id":    "HAND-PEER 1 " + strings.Repeat("A", 64) + " " + good + "\n",
		"short id":         "HAND-PEER 1 abc " + good + "\n",
		"long id":          "HAND-PEER 1 " + strings.Repeat("a", 65) + " " + good + "\n",
		"id with a symbol": "HAND-PEER 1 " + strings.Repeat("a", 15) + "/ " + good + "\n",
		"extra field":      "HAND-PEER 1 " + peerA + " " + good + " x\n",
		"double space":     "HAND-PEER 1  " + peerA + " " + good + "\n",
		"carriage return":  "HAND-PEER 1 " + peerA + " " + good + "\r\n",
		"over 256 bytes":   "HAND-PEER 1 " + peerA + " " + good + strings.Repeat("a", 300) + "\n",
		"no newline":       "HAND-PEER 1 " + peerA + " " + good + strings.Repeat("a", 300),
	} {
		c := r.dial(preface)
		if rep, err := c.send("GET", "/"+f.id+"/pair", nil, nil); err == nil {
			t.Fatalf("%s: the board answered %d", name, rep.Code)
		}
		if !c.closed() {
			t.Fatalf("%s: the connection stayed open", name)
		}
	}
}

func TestAPeerWithAnEmptyBoardSecretGetsNoIdentity(t *testing.T) {
	p := newPhoneHost(t, true, "alpha")
	peer := board.NewPeer(p.h, "")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = peer.Serve(ln) }()
	t.Cleanup(func() { _ = peer.Shutdown(context.Background()) })
	r := &peerRig{phoneHost: p, addr: ln.Addr().String()}
	c := r.dial("HAND-PEER 1 " + peerA + " \n")
	if _, err := c.send("GET", "/"+p.fleets[0].id+"/pair", nil, nil); err == nil {
		t.Fatal("a board with no secret admitted a preface that carries none")
	}
}

func TestOnlyTheFirstBytesOfAConnectionCanCarryAPreface(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	if rep := c.do("GET", "/"+f.id+"/pair", nil, nil); rep.Code != http.StatusOK {
		t.Fatalf("first request = %d", rep.Code)
	}
	if _, err := io.WriteString(c.conn, "HAND-PEER 1 "+peerB+" "+peerSecret+"\n"); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(c.br, &http.Request{Method: "GET"})
	if err != nil || res.StatusCode != http.StatusBadRequest {
		t.Fatalf("a second preface = %v, %v, want 400", res, err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	if !c.closed() {
		t.Fatal("the connection stayed open after a second preface")
	}
}

func TestAPlainRequestWithNoPrefaceIsAnonymousNotRefused(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	if rep := r.anonymous().do("GET", "/"+f.id+"/pair", nil, nil); rep.Code != http.StatusOK {
		t.Fatalf("a header-less forward = %d", rep.Code)
	}
	if _, err := r.anonymous().send("HEAD", "/"+f.id+"/pair", nil, nil); err != nil {
		t.Fatalf("a request that starts with the letter H was refused: %v", err)
	}
}

func TestTwoPeersOnOneListenerKeepTheirOwnIdentity(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	a, b := r.as(peerA), r.as(peerB)
	a.do("GET", "/"+f.id+"/pair", nil, nil)
	b.do("GET", "/"+f.id+"/pair", nil, nil)
	pa := r.mustPair(a, f)
	pb := r.mustPair(b, f)
	logins := map[string]bool{}
	for _, d := range r.devicesOf(f) {
		logins[d.Login] = true
	}
	if len(logins) != 2 || !logins["iroh:"+peerA] || !logins["iroh:"+peerB] {
		t.Fatalf("the two pairings recorded %v", logins)
	}
	if a.do("GET", "/"+f.id+"/", nil, nil, pa).Code != http.StatusOK || b.do("GET", "/"+f.id+"/", nil, nil, pb).Code != http.StatusOK {
		t.Fatal("a peer cannot open the board with its own cookie")
	}
	if a.do("GET", "/"+f.id+"/", nil, nil, pb).Code != http.StatusForbidden || b.do("GET", "/"+f.id+"/", nil, nil, pa).Code != http.StatusForbidden {
		t.Fatal("a peer opened the board with the other's cookie")
	}
}

func TestAnUnpairedConnectionIsCutAfter32Requests(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	for i := 1; i <= 32; i++ {
		if rep := c.do("GET", "/"+f.id+"/pair", nil, nil); rep.Code != http.StatusOK {
			t.Fatalf("request %d = %d", i, rep.Code)
		}
	}
	if rep := c.do("GET", "/"+f.id+"/pair", nil, nil); rep.Code != http.StatusTooManyRequests || !rep.Close {
		t.Fatalf("request 33 = %d, closing %v", rep.Code, rep.Close)
	}
	if !c.closed() {
		t.Fatal("the connection stayed open")
	}
}

func TestAnUnpairedConnectionIsCutAfter64KiBOfRequests(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	pad := map[string]string{"X-Pad": strings.Repeat("p", 8000)}
	served := 0
	for i := 0; i < 20; i++ {
		if _, err := c.send("GET", "/"+f.id+"/pair", nil, pad); err != nil {
			break
		}
		served++
	}
	if served < 7 || served > 9 {
		t.Fatalf("served %d requests of about 8 KB, want the connection cut near 64 KiB", served)
	}
}

func TestAPairedConnectionHasNoCap(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	phone := r.mustPair(c, f)
	pad := map[string]string{"X-Pad": strings.Repeat("p", 8000)}
	for i := 0; i < 100; i++ {
		if rep, err := c.send("GET", "/"+f.id+"/tasks", nil, pad, phone); err != nil || rep.Code != http.StatusOK {
			t.Fatalf("request %d = %v, %v", i+1, rep.Code, err)
		}
	}
}

func TestAPeerHeadOver16KiBIsRefused(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	if rep, err := r.as(peerA).send("GET", "/"+f.id+"/pair", nil, map[string]string{"X-Pad": strings.Repeat("p", 15<<10)}); err != nil || rep.Code != http.StatusOK {
		t.Fatalf("a head of 15 KiB = %d, %v", rep.Code, err)
	}
	for _, kb := range []int{17, 20} {
		if rep, err := r.as(peerA).send("GET", "/"+f.id+"/pair", nil, map[string]string{"X-Pad": strings.Repeat("p", kb<<10)}); err == nil && rep.Code != http.StatusRequestHeaderFieldsTooLarge {
			t.Fatalf("a head of %d KiB = %d, want it refused", kb, rep.Code)
		}
	}
}

func TestFunnelRefusalsCountAgainstTheUnpairedCap(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	for i := 1; i <= 32; i++ {
		if rep := c.do("GET", "/"+f.id+"/pair", nil, map[string]string{"Tailscale-Funnel-Request": "?1"}); rep.Code != http.StatusForbidden {
			t.Fatalf("funnel request %d = %d", i, rep.Code)
		}
	}
	if rep := c.do("GET", "/"+f.id+"/pair", nil, map[string]string{"Tailscale-Funnel-Request": "?1"}); rep.Code != http.StatusTooManyRequests || !rep.Close {
		t.Fatalf("funnel request 33 = %d, closing %v", rep.Code, rep.Close)
	}
}

func TestAPairedBrowserLiftsTheCapOnItsFirstRequestEvenForAnAsset(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	phone := r.mustPair(r.as(peerA), f)
	page := r.as(peerA).do("GET", "/"+f.id+"/pair", nil, nil)
	asset := staticRe.FindString(page.Body)
	c := r.as(peerA)
	for i := 1; i <= 60; i++ {
		if rep := c.do("GET", asset, nil, nil, phone); rep.Code != http.StatusOK {
			t.Fatalf("asset request %d with the phone's cookie = %d", i, rep.Code)
		}
	}
}

func TestACookieOfARevokedOrOtherPeerLiftsNoCap(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	phone := r.mustPair(r.as(peerA), f)
	page := r.as(peerA).do("GET", "/"+f.id+"/pair", nil, nil)
	asset := staticRe.FindString(page.Body)
	c := r.as(peerB)
	for i := 1; i <= 32; i++ {
		if rep := c.do("GET", asset, nil, nil, phone); rep.Code != http.StatusOK {
			t.Fatalf("asset request %d = %d", i, rep.Code)
		}
	}
	if rep := c.do("GET", asset, nil, nil, phone); rep.Code != http.StatusTooManyRequests {
		t.Fatalf("a cookie bound to another peer lifted the cap: %d", rep.Code)
	}
}

func TestThePeerListenerStillRefusesFunnelTraffic(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	for name, c := range map[string]*peerClient{"identified": r.as(peerA), "anonymous": r.anonymous()} {
		rep := c.do("GET", "/"+f.id+"/pair", nil, map[string]string{"Tailscale-Funnel-Request": "?1"})
		if rep.Code != http.StatusForbidden || !strings.Contains(rep.Body, "Funnel") {
			t.Fatalf("%s: a Funnel request = %d", name, rep.Code)
		}
	}
}

func TestPeerRefusalsCarryTheBoardsSecurityHeaders(t *testing.T) {
	r := newPeerRig(t, "alpha")
	rep := r.as(peerA).do("GET", "/", nil, nil)
	if rep.Code != http.StatusForbidden || rep.Header.Get("Content-Security-Policy") == "" || rep.Header.Get("X-Frame-Options") != "DENY" || rep.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("a refusal = %d with headers %v", rep.Code, rep.Header)
	}
}

func TestTheMainHandlerIgnoresThePeerRules(t *testing.T) {
	r := newPeerRig(t, "alpha")
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "alpha") {
		t.Fatalf("the loopback list = %d:\n%s", rec.Code, rec.Body)
	}
}

func TestTheBoardNeverSeesAClientSentProxyOrTailscaleHeaderOnThePeerListener(t *testing.T) {
	var seen http.Header
	h := board.NewHost(board.HostOptions{
		Loopback: true,
		Resolve:  func(string) (string, error) { return "/home", nil },
		Open: func(id, _ string) (http.Handler, io.Closer, error) {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.Header.Clone()
				_, _ = io.WriteString(w, "ok")
			}), closer(func() error { return nil }), nil
		},
	})
	peer := board.NewPeer(h, peerSecret)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = peer.Serve(ln) }()
	t.Cleanup(func() { _ = peer.Shutdown(context.Background()) })
	r := &peerRig{addr: ln.Addr().String(), phoneHost: &phoneHost{t: t}}
	headers := map[string]string{
		"X-Forwarded-For":            "203.0.113.9",
		"X-Forwarded-Proto":          "https",
		"X-Forwarded-Host":           "evil.example",
		"X-Forwarded-Anything":       "1",
		"Forwarded":                  "for=203.0.113.9",
		"Tailscale-User-Login":       "alice@example.com",
		"Tailscale-User-Name":        "Alice",
		"Tailscale-App-Capabilities": "{}",
		"X-Keep":                     "kept",
		"Accept-Language":            "en",
	}
	if rep := r.as(peerA).do("GET", "/f0123456789ab/pair", nil, headers); rep.Code != http.StatusOK {
		t.Fatalf("probe = %d", rep.Code)
	}
	for k := range seen {
		if k == "Forwarded" || strings.HasPrefix(k, "X-Forwarded-") || strings.HasPrefix(k, "Tailscale-") {
			t.Fatalf("the board saw %s: %v", k, seen)
		}
	}
	if seen.Get("X-Keep") != "kept" || seen.Get("Accept-Language") != "en" {
		t.Fatalf("the peer listener dropped headers that are not proxy headers: %v", seen)
	}
}

func TestAnOversizedPrefaceIsCutWithoutWaitingForItsNewline(t *testing.T) {
	r := newPeerRig(t, "alpha")
	c := r.dial("")
	if _, err := io.WriteString(c.conn, "HAND-PEER 1 "+strings.Repeat("a", 400)); err != nil {
		t.Fatal(err)
	}
	if !c.closed() {
		t.Fatal("the board kept waiting for the end of a preface longer than 256 bytes")
	}
}

var proofRe = regexp.MustCompile(`[0-9a-f]{64}`)

func TestAPairedPeerIsNotServedTheProofCheckOfAnyFleet(t *testing.T) {
	r := newPeerRig(t, "alpha", "bravo")
	a, b := r.fleets[0], r.fleets[1]
	if rec := r.send("GET", "/"+a.id+"/proof?nonce=x", nil, nil, nil); rec.Code != http.StatusOK || !proofRe.MatchString(rec.Body.String()) {
		t.Fatalf("the main listener's proof = %d", rec.Code)
	}
	c := r.as(peerA)
	phone := r.mustPair(c, a)
	for _, f := range []railFleet{a, b} {
		if rep := c.do("GET", "/"+f.id+"/proof?nonce=x", nil, nil, phone); rep.Code != http.StatusForbidden || proofRe.MatchString(rep.Body) {
			t.Fatalf("a paired peer asked %s for its proof: %d %q", f.name, rep.Code, rep.Body)
		}
	}
}

func TestAPeerPairedWithOneFleetSeesNothingOfAnotherThroughItsLaptopCookie(t *testing.T) {
	r := newPeerRig(t, "alpha", "bravo")
	a, b := r.fleets[0], r.fleets[1]
	c := r.as(peerA)
	phone := r.mustPair(c, a)
	for _, path := range []string{"/" + a.id + "/", "/" + a.id + "/connect", "/" + a.id + "/tasks", "/"} {
		rep := c.do("GET", path, nil, nil, phone, r.laptop(b))
		if rep.Code != http.StatusOK || strings.Contains(rep.Body, "bravo") || strings.Contains(rep.Body, b.id) {
			t.Fatalf("%s with a laptop cookie of another fleet = %d and names it: %v", path, rep.Code, strings.Contains(rep.Body, "bravo"))
		}
	}
}

func TestALaptopCookieOverAPeerCannotRevokeAnotherFleetsPhones(t *testing.T) {
	r := newPeerRig(t, "alpha", "bravo")
	a, b := r.fleets[0], r.fleets[1]
	c := r.as(peerA)
	phone := r.mustPair(c, a)
	r.phoneWith("Pixel B", b)
	label := r.labelOf(b, "Pixel B")
	rep := c.do("POST", "/"+a.id+"/connect/revoke/in", url.Values{"csrf": {a.token}, "phone": {label}, "fleet": {b.id}}, nil, phone, r.laptop(a), r.laptop(b))
	if got := holds(r.phoneHost, b); len(got) != 1 {
		t.Fatalf("a peer revoked a phone of another fleet: %d, %v", rep.Code, got)
	}
}

func TestAMainListenerClientCannotSpendAPeersPairingTries(t *testing.T) {
	for who, peer := range map[string]string{peerA: peerA, "iroh:" + peerA: peerA, "peer:" + peerA: peerA, "anonymous": "", "peer:anonymous": ""} {
		r := newPeerRig(t, "alpha")
		f := r.fleets[0]
		code := r.code(f)
		for i := 0; i < 5; i++ {
			r.send("POST", "/"+f.id+"/pair", url.Values{"code": {"ZZZZZ-ZZZZZ"}, "name": {"x"}}, nil, map[string]string{"X-Forwarded-For": who})
		}
		c := r.anonymous()
		if peer != "" {
			c = r.as(peer)
		}
		if rep := c.do("POST", "/"+f.id+"/pair", url.Values{"code": {code}, "name": {"x"}}, nil); rep.Code != http.StatusSeeOther {
			t.Fatalf("five wrong tries on the main listener from X-Forwarded-For %q blocked the peer: %d", who, rep.Code)
		}
	}
}

func TestARevokedPeerIsCappedAgainOnANewConnection(t *testing.T) {
	for name, path := range map[string]string{"a private page": "/%s/", "the pair page": "/%s/pair"} {
		r := newPeerRig(t, "alpha")
		f := r.fleets[0]
		c := r.as(peerA)
		phone := r.mustPair(c, f)
		for i := 0; i < 40; i++ {
			if rep := c.do("GET", "/"+f.id+"/pair", nil, nil, phone); rep.Code != http.StatusOK {
				t.Fatalf("%s: paired request %d = %d", name, i+1, rep.Code)
			}
		}
		r.revoke(f, phone)
		if !c.closed() {
			t.Fatalf("%s: revoke left the connection open", name)
		}
		again := r.as(peerA)
		if rep := again.do("GET", strings.Replace(path, "%s", f.id, 1), nil, nil, phone); rep.Code == http.StatusTooManyRequests {
			t.Fatalf("%s: the first request on the new connection was already capped", name)
		}
		capped := false
		for i := 0; i < 40 && !capped; i++ {
			rep, err := again.send("GET", "/"+f.id+"/pair", nil, nil)
			capped = err != nil || rep.Code == http.StatusTooManyRequests
		}
		if !capped {
			t.Fatalf("%s: a revoked peer kept an uncapped connection", name)
		}
	}
}

func TestARequestWithNoCookieDoesNotUnpairAConnection(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	phone := r.mustPair(c, f)
	pad := map[string]string{"X-Pad": strings.Repeat("p", 8000)}
	for i := 0; i < 20; i++ {
		if rep, err := c.send("GET", "/"+f.id+"/tasks", nil, pad, phone); err != nil || rep.Code != http.StatusOK {
			t.Fatalf("request %d = %v, %v", i+1, rep.Code, err)
		}
	}
	if rep := c.do("GET", "/manifest.webmanifest", nil, nil); rep.Code != http.StatusForbidden {
		t.Fatalf("a cookie-less manifest request = %d", rep.Code)
	}
	if rep, err := c.send("GET", "/"+f.id+"/tasks", nil, nil, phone); err != nil || rep.Code != http.StatusOK {
		t.Fatalf("the request after a cookie-less one = %v, %v", rep.Code, err)
	}
}

func TestTheRequestThatProvesAPhoneIsServedPastTheCap(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	phone := r.mustPair(r.as(peerA), f)
	c := r.as(peerA)
	for i := 1; i <= 32; i++ {
		if rep := c.do("GET", "/"+f.id+"/pair", nil, nil); rep.Code != http.StatusOK {
			t.Fatalf("request %d = %d", i, rep.Code)
		}
	}
	for i := 33; i <= 35; i++ {
		if rep := c.do("GET", "/"+f.id+"/tasks", nil, nil, phone); rep.Code != http.StatusOK {
			t.Fatalf("request %d with the phone's cookie = %d", i, rep.Code)
		}
	}
}

func TestAFunnelRequestWithAValidCookieLiftsNoCap(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	phone := r.mustPair(r.as(peerA), f)
	funnel := map[string]string{"Tailscale-Funnel-Request": "?1"}
	c := r.as(peerA)
	for i := 1; i <= 32; i++ {
		if rep := c.do("GET", "/"+f.id+"/pair", nil, funnel, phone); rep.Code != http.StatusForbidden {
			t.Fatalf("funnel request %d = %d", i, rep.Code)
		}
	}
	if rep := c.do("GET", "/"+f.id+"/pair", nil, funnel, phone); rep.Code != http.StatusTooManyRequests {
		t.Fatalf("a Funnel request with a valid cookie lifted the cap: %d", rep.Code)
	}
}

func TestOnlyARealFleetsPairPageIsOpenToAnUnpairedPeer(t *testing.T) {
	r := newPeerRig(t, "alpha")
	for _, path := range []string{"/nope/pair", "/F0123456789AB/pair", "/f0123456789a/pair"} {
		if rep := r.as(peerA).do("GET", path, nil, nil); rep.Code != http.StatusForbidden {
			t.Fatalf("%s = %d, want the same refusal as any private path", path, rep.Code)
		}
	}
}

func TestRevokeClosesTheEndpointsConnectionsAndLeavesOnlyThePairPage(t *testing.T) {
	for name, revoke := range map[string]struct {
		path string
		form func(f railFleet, label string) url.Values
	}{
		"revoke": {"/connect/revoke/in", func(f railFleet, label string) url.Values {
			return url.Values{"csrf": {f.token}, "phone": {label}, "fleet": {f.id}}
		}},
		"revoke everywhere": {"/connect/revoke/everywhere", func(f railFleet, label string) url.Values {
			return url.Values{"csrf": {f.token}, "phone": {label}}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newPeerRig(t, "alpha")
			f := r.fleets[0]
			c := r.as(peerA)
			phone := r.mustPair(c, f)
			if rep := c.do("GET", "/"+f.id+"/", nil, nil, phone); rep.Code != http.StatusOK {
				t.Fatalf("a paired connection = %d", rep.Code)
			}
			devices := r.devicesOf(f)
			if len(devices) != 1 {
				t.Fatalf("devices = %+v", devices)
			}
			label := devices[0].Phone
			other := r.as(peerB)
			otherPhone := r.mustPair(other, f)
			rec := r.send("POST", "/"+f.id+revoke.path, revoke.form(f, label), []*http.Cookie{r.laptop(f)}, nil)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("%s = %d:\n%s", revoke.path, rec.Code, rec.Body.String())
			}
			if !c.closed() {
				t.Fatal("revoke left the endpoint's connection open")
			}
			if rep := other.do("GET", "/"+f.id+"/", nil, nil, otherPhone); rep.Code != http.StatusOK {
				t.Fatalf("revoking one endpoint closed another: %d", rep.Code)
			}
			again := r.as(peerA)
			if rep := again.do("GET", "/"+f.id+"/", nil, nil, phone); rep.Code != http.StatusForbidden {
				t.Fatalf("a revoked endpoint reached the board: %d", rep.Code)
			}
			if rep := again.do("GET", "/"+f.id+"/tasks", nil, nil, phone); rep.Code != http.StatusForbidden {
				t.Fatalf("a revoked endpoint reached the tasks: %d", rep.Code)
			}
			pair := again.do("GET", "/"+f.id+"/pair", nil, nil)
			if pair.Code != http.StatusOK {
				t.Fatalf("a revoked endpoint could not reach the pair page: %d", pair.Code)
			}
			asset := staticRe.FindString(pair.Body)
			if asset == "" {
				t.Fatal("the pair page loads no asset")
			}
			if rep := again.do("GET", asset, nil, nil); rep.Code != http.StatusOK {
				t.Fatalf("a revoked endpoint could not reach %s: %d", asset, rep.Code)
			}
			fresh, rep := r.pairOver(again, f, nil)
			if rep.Code != http.StatusSeeOther || fresh == nil {
				t.Fatalf("a revoked endpoint could not pair again: %d", rep.Code)
			}
			if rep := again.do("GET", "/"+f.id+"/", nil, nil, fresh); rep.Code != http.StatusOK {
				t.Fatalf("a repaired endpoint = %d", rep.Code)
			}
		})
	}
}

func TestRevokeClosesEveryConnectionOfAnEndpoint(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	first := r.as(peerA)
	phone := r.mustPair(first, f)
	second := r.as(peerA)
	if rep := second.do("GET", "/"+f.id+"/", nil, nil, phone); rep.Code != http.StatusOK {
		t.Fatalf("a second connection of the paired endpoint = %d", rep.Code)
	}
	other := r.as(peerB)
	otherPhone := r.mustPair(other, f)
	label := ""
	for _, d := range r.devicesOf(f) {
		if d.Login == "iroh:"+peerA {
			label = d.Phone
		}
	}
	if label == "" {
		t.Fatal("the paired endpoint holds no device label")
	}
	rec := r.send("POST", "/"+f.id+"/connect/revoke/in", url.Values{"csrf": {f.token}, "phone": {label}, "fleet": {f.id}}, []*http.Cookie{r.laptop(f)}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke = %d:\n%s", rec.Code, rec.Body.String())
	}
	if !first.closed() || !second.closed() {
		t.Fatal("revoke left a connection of the endpoint open")
	}
	if rep := other.do("GET", "/"+f.id+"/", nil, nil, otherPhone); rep.Code != http.StatusOK {
		t.Fatalf("revoking one endpoint closed another: %d", rep.Code)
	}
}

func TestASignOutOverAPeerConnectionIsAcknowledged(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	phone := r.mustPair(c, f)
	page := c.do("GET", "/"+f.id+"/connect", nil, nil, phone)
	csrf := csrfRe.FindStringSubmatch(page.Body)
	if csrf == nil {
		t.Fatalf("the phone's own page holds no form token:\n%s", page.Body)
	}
	id := strings.SplitN(phone.Value, ".", 2)[0]
	rep, err := c.send("POST", "/"+f.id+"/connect/revoke", url.Values{"csrf": {csrf[1]}, "id": {id}}, nil, phone)
	if err != nil {
		t.Fatalf("signing out over the peer connection: %v", err)
	}
	if rep.Code != http.StatusOK || !strings.Contains(rep.Body, "This phone is signed out") {
		t.Fatalf("the sign-out = %d:\n%s", rep.Code, rep.Body)
	}
	if !c.closed() {
		t.Fatal("the sign-out left the phone's connection open")
	}
	if rep := r.as(peerA).do("GET", "/"+f.id+"/", nil, nil, phone); rep.Code != http.StatusForbidden {
		t.Fatalf("a signed-out phone reopened the board: %d", rep.Code)
	}
}

func TestAHandAppCodePairsOverAPeerConnection(t *testing.T) {
	p := newPhoneHost(t, true, "alpha")
	f := p.fleets[0]
	p.iroh = func() sidecar.Status {
		return sidecar.Status{State: sidecar.Ready, ID: appEndpoint, Relay: appRelay}
	}
	p.restart()
	r := servePeerRig(t, p)
	rec := r.send("POST", "/"+f.id+"/connect/app", url.Values{"csrf": {f.token}}, []*http.Cookie{r.laptop(f)}, nil)
	m := appCodeRe.FindStringSubmatch(rec.Body.String())
	if rec.Code != http.StatusOK || m == nil {
		t.Fatalf("connect/app = %d:\n%s", rec.Code, redactAppPage(rec.Body.String()))
	}
	c := r.as(peerA)
	rep := c.do("POST", "/"+f.id+"/pair", url.Values{"code": {m[1]}, "name": {"Pixel"}}, nil)
	var phone *http.Cookie
	for _, ck := range rep.Cookies {
		if ck.MaxAge > 0 && strings.Contains(ck.Name, "hand_phone") {
			phone = ck
		}
	}
	if rep.Code != http.StatusSeeOther || phone == nil {
		t.Fatalf("pairing the minted app code = %d:\n%s", rep.Code, rep.Body)
	}
	if devices := r.devicesOf(f); len(devices) != 1 || devices[0].Login != "iroh:"+peerA {
		t.Fatalf("the app pairing recorded %+v, want iroh:%s", devices, peerA)
	}
	if rep := c.do("GET", "/"+f.id+"/", nil, nil, phone); rep.Code != http.StatusOK {
		t.Fatalf("the app's paired connection = %d", rep.Code)
	}
}

func TestAPairPageOpenedByTheAppNamesTheApp(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	page := r.as(peerA).do("GET", "/"+f.id+"/pair", nil, nil)
	if !strings.Contains(page.Body, "Pairing over the Hand app as iroh:"+peerA+".") || strings.Contains(page.Body, "tailnet login") {
		t.Fatalf("the app's pair page reads:\n%s", page.Body)
	}
}
