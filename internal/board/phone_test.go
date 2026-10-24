package board_test

import (
	"bufio"
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

var (
	codeRe   = regexp.MustCompile(`class="code-big"[^>]*>([0-9A-Z]{5}-[0-9A-Z]{5})<`)
	csrfRe   = regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`)
	laptop   = &http.Cookie{Name: "hand_board", Value: token}
	viaServe = map[string]string{"X-Forwarded-For": "100.64.0.7", "X-Forwarded-Proto": "https", "Tailscale-User-Login": "operator@example.com"}
)

type phoneRig struct {
	t   *testing.T
	fx  *fixture
	h   http.Handler
	mu  sync.Mutex
	now time.Time
}

func newPhoneRig(t *testing.T) *phoneRig {
	t.Helper()
	p := &phoneRig{t: t, fx: newFixture(t), now: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	p.fx.options.Now = func() time.Time {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.now
	}
	p.fx.options.Tick = 10 * time.Millisecond
	p.h = p.fx.handler()
	return p
}

func (p *phoneRig) advance(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.now = p.now.Add(d)
}

func (p *phoneRig) do(method, path string, form url.Values, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	p.t.Helper()
	body := strings.NewReader("")
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	p.h.ServeHTTP(rec, req)
	return rec
}

func (p *phoneRig) code() string {
	p.t.Helper()
	rec := p.do("POST", "/connect/code", url.Values{"csrf": {token}, "base": {"https://laptop.tailnet.example"}}, laptop, nil)
	m := codeRe.FindStringSubmatch(rec.Body.String())
	if rec.Code != http.StatusOK || m == nil {
		p.t.Fatalf("connect/code = %d:\n%s", rec.Code, rec.Body.String())
	}
	return m[1]
}

func (p *phoneRig) tryPair(code, from string) *httptest.ResponseRecorder {
	p.t.Helper()
	h := map[string]string{"X-Forwarded-For": from, "X-Forwarded-Proto": "https", "Tailscale-User-Login": "operator@example.com"}
	return p.do("POST", "/pair", url.Values{"code": {code}, "name": {"Pixel"}}, nil, h)
}

func phoneCookieOf(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if strings.HasSuffix(c.Name, "hand_phone") && c.MaxAge > 0 {
			return c
		}
	}
	return nil
}

func (p *phoneRig) pair() *http.Cookie {
	p.t.Helper()
	rec := p.tryPair(p.code(), "100.64.0.7")
	c := phoneCookieOf(rec)
	if rec.Code != http.StatusSeeOther || c == nil {
		p.t.Fatalf("pair = %d:\n%s", rec.Code, rec.Body.String())
	}
	return c
}

func (p *phoneRig) deviceID(c *http.Cookie) string { return strings.SplitN(c.Value, ".", 2)[0] }

func (p *phoneRig) events(kinds ...string) []string {
	p.t.Helper()
	events, err := p.fx.st.EventsAfter(context.Background(), 0, kinds, 100)
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		out = append(out, e.Kind+" "+e.Detail)
	}
	return out
}

func TestAPairedPhoneGetsItsOwnSessionNotTheToken(t *testing.T) {
	p := newPhoneRig(t)
	p.fx.supervisor(t, state.AttemptRunning, "gen-1")
	seed(t, p.fx.st)
	phone := p.pair()

	home := p.do("GET", "/", nil, phone, viaServe)
	if home.Code != http.StatusOK {
		t.Fatalf("phone home = %d", home.Code)
	}
	own := csrfRe.FindStringSubmatch(home.Body.String())
	if own == nil || own[1] == token {
		t.Fatalf("the phone's csrf field = %v, want its own value", own)
	}
	if mine := csrfRe.FindStringSubmatch(p.do("GET", "/", nil, laptop, nil).Body.String()); mine == nil || mine[1] != token {
		t.Fatalf("the laptop's csrf field = %v, want today's token", mine)
	}

	if rec := p.do("POST", "/supervisor/send", url.Values{"csrf": {token}, "text": {"hi"}}, phone, viaServe); rec.Code != http.StatusForbidden {
		t.Fatalf("phone POST with the fleet token as csrf = %d", rec.Code)
	}
	if rec := p.do("POST", "/supervisor/send", url.Values{"csrf": {own[1]}, "text": {"hi from the phone"}}, phone, viaServe); rec.Code == http.StatusForbidden {
		t.Fatalf("phone POST with its own csrf = %d", rec.Code)
	}
	if rec := p.do("POST", "/connect/code", url.Values{"csrf": {own[1]}, "base": {"https://laptop.tailnet.example"}}, phone, viaServe); rec.Code != http.StatusForbidden || codeRe.MatchString(rec.Body.String()) {
		t.Fatalf("a phone minted a pairing code: %d", rec.Code)
	}
	if rec := p.do("POST", "/connect/code", url.Values{"csrf": {token}, "base": {"https://laptop.tailnet.example"}}, phone, viaServe); rec.Code != http.StatusForbidden {
		t.Fatalf("a phone minted a pairing code with the token as csrf: %d", rec.Code)
	}
}

func TestNoPageAPhoneLoadsHoldsTheFleetToken(t *testing.T) {
	p := newPhoneRig(t)
	p.fx.supervisor(t, state.AttemptRunning, "gen-1")
	seed(t, p.fx.st)
	phone := p.pair()
	own := csrfRe.FindStringSubmatch(p.do("GET", "/", nil, phone, viaServe).Body.String())[1]
	paths := []string{"/", "/?all=1", "/?pick=1", "/tasks", "/tasks?q=fix", "/task/t1", "/task/t2", "/attempt/a1", "/ref/a1", "/decision/d1", "/ref/t1", "/ref/d1", "/supervisor/log", "/connect", "/pair"}
	laptopSaw := 0
	for _, path := range paths {
		rec := p.do("GET", path, nil, phone, viaServe)
		if rec.Code >= 500 {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, token) {
			t.Fatalf("GET %s holds the fleet token:\n%s", path, body)
		}
		for _, h := range rec.Result().Header {
			for _, v := range h {
				if strings.Contains(v, token) {
					t.Fatalf("GET %s sends the fleet token in a header", path)
				}
			}
		}
		for _, m := range csrfRe.FindAllStringSubmatch(body, -1) {
			if m[1] != own {
				t.Fatalf("GET %s: csrf %q is not the phone's own", path, m[1])
			}
		}
		if strings.Contains(p.do("GET", path, nil, laptop, nil).Body.String(), `name="csrf" value="`+token+`"`) {
			laptopSaw++
		}
	}
	if laptopSaw == 0 {
		t.Fatal("no page of the laptop carries its csrf field, so the comparison proves nothing")
	}
	base := serve(t, p.h)
	req, _ := http.NewRequest("GET", base+"/events", nil)
	req.AddCookie(phone)
	for k, v := range viaServe {
		req.Header.Set(k, v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("phone events = %v, %v", res, err)
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	var stream strings.Builder
	for sc.Scan() && !strings.Contains(stream.String(), "event: tasks") {
		stream.WriteString(sc.Text() + "\n")
	}
	if strings.Contains(stream.String(), token) || !strings.Contains(stream.String(), `name="csrf" value="`+own+`"`) {
		t.Fatalf("the phone's event stream holds the token or lacks its own csrf:\n%s", stream.String())
	}
}

func TestAPairingCodeLooksAndWorksAsDesigned(t *testing.T) {
	p := newPhoneRig(t)
	seen := map[string]bool{}
	for range 20 {
		code := p.code()
		if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{5}-[0-9A-HJKMNP-TV-Z]{5}$`).MatchString(code) || seen[code] {
			t.Fatalf("code %q is not a fresh 10-character Crockford code", code)
		}
		seen[code] = true
	}
	code := p.code()
	typed := strings.NewReplacer("0", "o", "1", "l").Replace(strings.ToLower(strings.ReplaceAll(code, "-", " ")))
	if rec := p.tryPair(typed, "100.64.0.7"); rec.Code != http.StatusSeeOther {
		t.Fatalf("code %q typed as %q = %d", code, typed, rec.Code)
	}
}

func TestAPairingCodeWorksOnceAndSaysWhyItFailed(t *testing.T) {
	p := newPhoneRig(t)
	used := p.code()
	if rec := p.tryPair(used, "100.64.0.7"); rec.Code != http.StatusSeeOther {
		t.Fatalf("first use = %d", rec.Code)
	}
	rec := p.tryPair(used, "100.64.0.7")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "already used") || phoneCookieOf(rec) != nil {
		t.Fatalf("replayed code = %d:\n%s", rec.Code, rec.Body.String())
	}
	late := p.code()
	p.advance(5*time.Minute + time.Second)
	rec = p.tryPair(late, "100.64.0.7")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "has expired") || phoneCookieOf(rec) != nil {
		t.Fatalf("expired code = %d:\n%s", rec.Code, rec.Body.String())
	}
	edge := p.code()
	p.advance(5 * time.Minute)
	if rec := p.tryPair(edge, "100.64.0.7"); rec.Code != http.StatusSeeOther {
		t.Fatalf("a code at exactly 5 minutes = %d", rec.Code)
	}
	rec = p.tryPair("AAAAA-AAAAA", "100.64.0.7")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "not right") {
		t.Fatalf("wrong code = %d:\n%s", rec.Code, rec.Body.String())
	}
	if n := len(p.events("board.paired")); n != 2 {
		t.Fatalf("%d phones paired, want 2", n)
	}
}

func TestEveryFailureKeepsTheFormAndWaysBack(t *testing.T) {
	p := newPhoneRig(t)
	p.code()
	rec := p.tryPair("AAAAA-AAAAA", "100.64.0.7")
	body := rec.Body.String()
	contains(t, "failure", body, `data-pair`, `name="code"`, `name="name"`, `value="Pixel"`, `id="code-error"`, `role="alert"`, "show a new code there")
	lacks(t, "failure", body, `class="rail"`, `id="rail"`, `nav-menu`)
}

func TestANewCodeCancelsTheOneBefore(t *testing.T) {
	p := newPhoneRig(t)
	first, second := p.code(), p.code()
	rec := p.tryPair(first, "100.64.0.7")
	if rec.Code != http.StatusForbidden || phoneCookieOf(rec) != nil || !strings.Contains(rec.Body.String(), "not right") {
		t.Fatalf("the earlier code after a new one = %d:\n%s", rec.Code, rec.Body.String())
	}
	if rec := p.tryPair(second, "100.64.0.7"); rec.Code != http.StatusSeeOther {
		t.Fatalf("the newest code = %d", rec.Code)
	}
	spent := p.code()
	p.tryPair(spent, "100.64.0.7")
	p.code()
	if rec := p.tryPair(spent, "100.64.0.7"); !strings.Contains(rec.Body.String(), "already used") {
		t.Fatalf("a used code after a new one = %d:\n%s", rec.Code, rec.Body.String())
	}
}

func TestAPhoneStaysSignedInForNinetyDaysAndNoLonger(t *testing.T) {
	p := newPhoneRig(t)
	phone := p.pair()
	devices, err := p.fx.st.Devices(context.Background())
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices = %v, %v", devices, err)
	}
	paired := devices[0].PairedAt
	p.mu.Lock()
	p.now = paired.Add(90*24*time.Hour - time.Second)
	p.mu.Unlock()
	if rec := p.do("GET", "/", nil, phone, viaServe); rec.Code != http.StatusOK {
		t.Fatalf("a second before 90 days = %d", rec.Code)
	}
	p.advance(2 * time.Second)
	rec := p.do("GET", "/", nil, phone, viaServe)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "no longer paired") {
		t.Fatalf("a second after 90 days = %d", rec.Code)
	}
	contains(t, "list", p.do("GET", "/connect", nil, laptop, nil).Body.String(), "signed out after 90 days", `aria-label="Revoke Pixel"`)
	if rec := p.do("POST", "/connect/revoke", url.Values{"csrf": {token}, "id": {p.deviceID(phone)}}, laptop, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoking an expired phone = %d", rec.Code)
	}
}

func TestTenWrongCodesCancelTheLiveCode(t *testing.T) {
	p := newPhoneRig(t)
	live := p.code()
	for i := range 9 {
		if rec := p.tryPair("AAAAA-AAAAA", "100.64.0."+strconv.Itoa(10+i/3)); rec.Code != http.StatusForbidden {
			t.Fatalf("wrong try %d = %d", i+1, rec.Code)
		}
	}
	rec := p.tryPair("AAAAA-AAAAA", "100.64.0.20")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Every code was cancelled") {
		t.Fatalf("the tenth wrong try = %d:\n%s", rec.Code, rec.Body.String())
	}
	if rec := p.tryPair(live, "100.64.0.7"); rec.Code != http.StatusForbidden || phoneCookieOf(rec) != nil {
		t.Fatalf("a live code survived 10 wrong tries: %d", rec.Code)
	}
	if rec := p.tryPair(p.code(), "100.64.0.7"); rec.Code != http.StatusSeeOther {
		t.Fatalf("a code minted after the burn = %d", rec.Code)
	}
}

func TestOneAddressCannotGuessAtSpeed(t *testing.T) {
	p := newPhoneRig(t)
	code := p.code()
	for i := range 5 {
		if rec := p.tryPair("BBBBB-BBBBB", "100.64.0.99"); rec.Code != http.StatusForbidden {
			t.Fatalf("wrong try %d = %d", i+1, rec.Code)
		}
	}
	rec := p.tryPair(code, "100.64.0.99")
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "Wait a minute") || phoneCookieOf(rec) != nil {
		t.Fatalf("the right code from a blocked address = %d:\n%s", rec.Code, rec.Body.String())
	}
	spoofed := map[string]string{"X-Forwarded-For": "1.2.3.4, 100.64.0.99", "X-Forwarded-Proto": "https"}
	if rec := p.do("POST", "/pair", url.Values{"code": {code}}, nil, spoofed); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("an address that hides behind a forged first entry = %d", rec.Code)
	}
	p.advance(time.Minute)
	if rec := p.tryPair(code, "100.64.0.99"); rec.Code != http.StatusSeeOther {
		t.Fatalf("the same address a minute later = %d", rec.Code)
	}
	again := p.code()
	if rec := p.tryPair(again, "100.64.0.7"); rec.Code != http.StatusSeeOther {
		t.Fatalf("another address after a block = %d", rec.Code)
	}
}

func TestOpeningThePairLinkNeverSpendsTheCode(t *testing.T) {
	p := newPhoneRig(t)
	code := p.code()
	for _, path := range []string{"/pair", "/pair?code=" + code} {
		rec := p.do("GET", path, nil, nil, viaServe)
		if rec.Code != http.StatusOK || len(rec.Result().Cookies()) != 0 {
			t.Fatalf("GET %s = %d with cookies %v", path, rec.Code, rec.Result().Cookies())
		}
		lacks(t, path, rec.Body.String(), code)
	}
	if rec := p.do("HEAD", "/pair", nil, nil, viaServe); len(rec.Result().Cookies()) != 0 {
		t.Fatal("HEAD /pair set a cookie")
	}
	contains(t, "pair page", p.do("GET", "/pair", nil, nil, viaServe).Body.String(), `<form class="card pair-card" method="post" data-pair>`, `autocomplete="one-time-code"`, `name="code"`, `maxlength="13"`)
	if rec := p.tryPair(code, "100.64.0.7"); rec.Code != http.StatusSeeOther {
		t.Fatalf("the code after four GETs = %d", rec.Code)
	}
}

func TestOnlyThePostOfAFormPairs(t *testing.T) {
	p := newPhoneRig(t)
	code := p.code()
	for _, m := range []string{"PUT", "DELETE", "PATCH"} {
		if rec := p.do(m, "/pair", url.Values{"code": {code}}, nil, viaServe); rec.Code == http.StatusSeeOther || phoneCookieOf(rec) != nil {
			t.Fatalf("%s /pair = %d", m, rec.Code)
		}
	}
	if rec := p.do("POST", "/pair?code="+code, nil, nil, viaServe); phoneCookieOf(rec) != nil {
		t.Fatal("a code in the query string paired a phone")
	}
	if rec := p.tryPair(code, "100.64.0.7"); rec.Code != http.StatusSeeOther {
		t.Fatalf("the code after refused methods = %d", rec.Code)
	}
	oversized := url.Values{"code": {strings.Repeat("A", 8192)}}
	if rec := p.do("POST", "/pair", oversized, nil, viaServe); rec.Code != http.StatusBadRequest {
		t.Fatalf("an oversized pair form = %d", rec.Code)
	}
}

func TestThePhoneCookieIsHardened(t *testing.T) {
	p := newPhoneRig(t)
	secure := phoneCookieOf(p.tryPair(p.code(), "100.64.0.7"))
	if secure.Name != "__Host-hand_phone" || !secure.Secure || !secure.HttpOnly || secure.SameSite != http.SameSiteLaxMode || secure.Path != "/" || secure.Domain != "" || secure.MaxAge != 90*24*3600 {
		t.Fatalf("cookie through https = %+v", secure)
	}
	rec := p.do("POST", "/pair", url.Values{"code": {p.code()}}, nil, map[string]string{"X-Forwarded-For": "127.0.0.1"})
	plain := phoneCookieOf(rec)
	if plain == nil || plain.Name != "hand_phone" || plain.Secure || !plain.HttpOnly || plain.SameSite != http.SameSiteLaxMode || plain.Path != "/" || plain.MaxAge != 90*24*3600 {
		t.Fatalf("cookie over plain http = %+v", plain)
	}
	if strings.Contains(secure.Value, token) || strings.Contains(plain.Value, token) {
		t.Fatal("a phone cookie holds the fleet token")
	}
	if p.do("GET", "/", nil, secure, viaServe).Code != http.StatusOK || p.do("GET", "/", nil, plain, nil).Code != http.StatusOK {
		t.Fatal("a phone cookie does not open its own page")
	}
}

func TestACookieTossedFromASiblingNeverSignsAPhoneIn(t *testing.T) {
	p := newPhoneRig(t)
	secure := phoneCookieOf(p.tryPair(p.code(), "100.64.0.7"))
	plainName := &http.Cookie{Name: "hand_phone", Value: secure.Value}
	if rec := p.do("GET", "/", nil, plainName, viaServe); rec.Code != http.StatusForbidden {
		t.Fatalf("a plain-named cookie over https = %d", rec.Code)
	}
	plain := phoneCookieOf(p.do("POST", "/pair", url.Values{"code": {p.code()}}, nil, map[string]string{"X-Forwarded-For": "127.0.0.1"}))
	hostNamed := &http.Cookie{Name: "__Host-hand_phone", Value: plain.Value}
	if rec := p.do("GET", "/", nil, hostNamed, map[string]string{"X-Forwarded-For": "127.0.0.1"}); rec.Code != http.StatusForbidden {
		t.Fatalf("a __Host- cookie over plain http = %d", rec.Code)
	}
	if rec := p.do("GET", "/", nil, &http.Cookie{Name: "hand_board", Value: secure.Value}, viaServe); rec.Code != http.StatusForbidden {
		t.Fatalf("a phone secret in the token cookie = %d", rec.Code)
	}
}

func TestAForgedOrBorrowedCookieOpensNothing(t *testing.T) {
	p := newPhoneRig(t)
	phone := p.pair()
	id, secret, _ := strings.Cut(phone.Value, ".")
	flipped := secret[:len(secret)-1] + "0"
	if strings.HasSuffix(secret, "0") {
		flipped = secret[:len(secret)-1] + "1"
	}
	for name, value := range map[string]string{
		"a changed secret":  id + "." + flipped,
		"another id":        "99." + secret,
		"no secret":         id + ".",
		"no id":             "." + secret,
		"the token":         token,
		"the id alone":      id,
		"a negative id":     "-1." + secret,
		"a long secret":     id + "." + secret + secret,
		"an empty value":    "x",
		"id with a suffix":  id + "x." + secret,
		"two dots":          id + "." + secret + ".",
		"leading space":     " " + id + "." + secret,
		"a zero id":         "0." + secret,
		"a very large id":   "99999999999999999999." + secret,
		"the csrf as value": id + "." + csrfRe.FindStringSubmatch(p.do("GET", "/", nil, phone, viaServe).Body.String())[1],
	} {
		rec := p.do("GET", "/", nil, &http.Cookie{Name: phone.Name, Value: value}, viaServe)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s opened the Board: %d", name, rec.Code)
		}
	}
}

func TestACookieWorksOnlyForTheLoginItWasPairedFor(t *testing.T) {
	p := newPhoneRig(t)
	phone := p.pair()
	other := map[string]string{"X-Forwarded-For": "100.64.0.9", "X-Forwarded-Proto": "https", "Tailscale-User-Login": "someone@example.com"}
	rec := p.do("GET", "/", nil, phone, other)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "another tailnet login") {
		t.Fatalf("phone cookie under another login = %d", rec.Code)
	}
	missing := map[string]string{"X-Forwarded-Proto": "https"}
	if rec := p.do("GET", "/", nil, phone, missing); rec.Code != http.StatusForbidden {
		t.Fatalf("phone cookie with no login at all = %d", rec.Code)
	}
	if rec := p.do("GET", "/", nil, phone, viaServe); rec.Code != http.StatusOK {
		t.Fatalf("phone cookie under its own login = %d", rec.Code)
	}
	spaced := map[string]string{"X-Forwarded-Proto": "https", "Tailscale-User-Login": "Op  Er@example.com"}
	rec = p.do("POST", "/pair", url.Values{"code": {p.code()}}, nil, spaced)
	if c := phoneCookieOf(rec); c == nil || p.do("GET", "/", nil, c, spaced).Code != http.StatusOK {
		t.Fatalf("a login the store tidies could not come back: %d", rec.Code)
	}
	encoded := map[string]string{"X-Forwarded-Proto": "https", "Tailscale-User-Login": "=?utf-8?q?op=C3=A9rateur@example.com?="}
	rec = p.do("POST", "/pair", url.Values{"code": {p.code()}}, nil, encoded)
	bound := phoneCookieOf(rec)
	if bound == nil {
		t.Fatalf("pairing with an encoded login = %d", rec.Code)
	}
	if rec := p.do("GET", "/", nil, bound, encoded); rec.Code != http.StatusOK {
		t.Fatalf("the encoded login again = %d", rec.Code)
	}
	if list := p.do("GET", "/connect", nil, laptop, nil).Body.String(); !strings.Contains(list, "opérateur@example.com") {
		t.Fatalf("the list does not show the decoded login:\n%s", list)
	}
}

func TestAnIdentityHeaderAloneGrantsNothing(t *testing.T) {
	p := newPhoneRig(t)
	for _, path := range []string{"/", "/connect", "/tasks", "/events", "/supervisor/log", "/task/t1", "/attempt/a1", "/doc/inbox"} {
		rec := p.do("GET", path, nil, nil, viaServe)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Pair this phone first") {
			t.Fatalf("GET %s with an identity header and no session = %d:\n%s", path, rec.Code, rec.Body.String())
		}
	}
	for _, h := range []map[string]string{
		{"Tailscale-User-Login": "operator@example.com"},
		{"Tailscale-User-Login": "operator@example.com", "Tailscale-User-Name": "Operator"},
		{"X-Forwarded-For": "100.64.0.7"},
		{"Forwarded": "for=100.64.0.7;proto=https"},
	} {
		if rec := p.do("GET", "/", nil, nil, h); rec.Code != http.StatusForbidden {
			t.Fatalf("headers %v = %d", h, rec.Code)
		}
		if rec := p.do("POST", "/supervisor/send", url.Values{"csrf": {token}, "text": {"x"}}, nil, h); rec.Code != http.StatusForbidden {
			t.Fatalf("POST with headers %v = %d", h, rec.Code)
		}
	}
	if rec := p.do("GET", "/", nil, nil, nil); !strings.Contains(rec.Body.String(), "hand open") {
		t.Fatal("a plain request lost its hand open hint")
	}
}

func TestRevokingAPhoneSignsItOutAtOnce(t *testing.T) {
	p := newPhoneRig(t)
	p.fx.supervisor(t, state.AttemptRunning, "gen-1")
	phone, keep := p.pair(), p.pair()
	if rec := p.do("GET", "/", nil, phone, viaServe); rec.Code != http.StatusOK {
		t.Fatalf("before the revoke = %d", rec.Code)
	}
	list := p.do("GET", "/connect", nil, laptop, nil)
	contains(t, "list", list.Body.String(), "Pixel", `value="`+p.deviceID(phone)+`"`, `aria-label="Revoke Pixel"`, "operator@example.com")
	rec := p.do("POST", "/connect/revoke", url.Values{"csrf": {token}, "id": {p.deviceID(phone)}}, laptop, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/connect" {
		t.Fatalf("revoke = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = p.do("GET", "/", nil, phone, viaServe)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "This phone is no longer paired with this fleet") || !strings.Contains(rec.Body.String(), "Phone, in its menu") {
		t.Fatalf("revoked phone = %d:\n%s", rec.Code, rec.Body.String())
	}
	if rec := p.do("POST", "/supervisor/send", url.Values{"csrf": {"x"}, "text": {"x"}}, phone, viaServe); rec.Code != http.StatusForbidden {
		t.Fatalf("revoked phone POST = %d", rec.Code)
	}
	if rec := p.do("GET", "/", nil, keep, viaServe); rec.Code != http.StatusOK {
		t.Fatalf("revoking one phone signed out another = %d", rec.Code)
	}
	if rec := p.do("POST", "/connect/revoke", url.Values{"csrf": {token}, "id": {p.deviceID(phone)}}, laptop, nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/connect" {
		t.Fatalf("revoking a phone twice = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := p.do("POST", "/connect/revoke", url.Values{"csrf": {token}, "id": {"x"}}, laptop, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("revoking no phone = %d", rec.Code)
	}
	if rec := p.do("POST", "/connect/revoke", url.Values{"id": {p.deviceID(keep)}}, laptop, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("revoke without csrf = %d", rec.Code)
	}
}

func TestRevokingClosesThePhonesOpenStream(t *testing.T) {
	p := newPhoneRig(t)
	p.fx.supervisor(t, state.AttemptRunning, "gen-1")
	phone := p.pair()
	base := serve(t, p.h)
	req, _ := http.NewRequest("GET", base+"/events", nil)
	req.AddCookie(phone)
	for k, v := range viaServe {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("events = %v, %v", res, err)
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	if !sc.Scan() {
		t.Fatal("the stream said nothing")
	}
	if rec := p.do("POST", "/connect/revoke", url.Values{"csrf": {token}, "id": {p.deviceID(phone)}}, laptop, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke = %d", rec.Code)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for sc.Scan() {
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a revoked phone's stream stayed open")
	}
}

func TestAPhoneSignsItselfOutAndNobodyElse(t *testing.T) {
	p := newPhoneRig(t)
	phone, other := p.pair(), p.pair()
	own := csrfRe.FindStringSubmatch(p.do("GET", "/connect", nil, phone, viaServe).Body.String())[1]
	page := p.do("GET", "/connect", nil, phone, viaServe).Body.String()
	contains(t, "This phone", page, "This phone", "Paired as <b>Pixel</b>", "It stays signed in for 90 days from pairing, or until it is revoked.", "Sign out this phone", `action="/connect/revoke"`)
	lacks(t, "This phone", page, "Paired phones", "Show pairing code", `name="base"`, "operator@example.com", `value="`+p.deviceID(other)+`"`)
	if rec := p.do("POST", "/connect/revoke", url.Values{"csrf": {own}, "id": {p.deviceID(other)}}, phone, viaServe); rec.Code != http.StatusForbidden {
		t.Fatalf("a phone revoked another phone = %d", rec.Code)
	}
	if rec := p.do("GET", "/", nil, other, viaServe); rec.Code != http.StatusOK {
		t.Fatalf("the other phone was signed out = %d", rec.Code)
	}
	rec := p.do("POST", "/connect/revoke", url.Values{"csrf": {own}, "id": {p.deviceID(phone)}}, phone, viaServe)
	cleared := false
	for _, c := range rec.Result().Cookies() {
		cleared = cleared || (c.Name == "__Host-hand_phone" && c.MaxAge < 0 && c.Secure && c.Path == "/" && c.Value == "")
	}
	if rec.Code != http.StatusOK || !cleared || !strings.Contains(rec.Body.String(), "This phone is signed out") {
		t.Fatalf("sign out = %d, cleared %v:\n%s", rec.Code, cleared, rec.Body.String())
	}
	if rec := p.do("GET", "/", nil, phone, viaServe); rec.Code != http.StatusForbidden {
		t.Fatalf("the signed-out phone = %d", rec.Code)
	}
	if rec := p.do("POST", "/connect/revoke", url.Values{"csrf": {own}, "id": {p.deviceID(phone)}}, phone, viaServe); rec.Code != http.StatusForbidden {
		t.Fatalf("a signed-out phone revoked again = %d", rec.Code)
	}
	if got := p.events("board.revoked"); len(got) != 1 || got[0] != "board.revoked "+p.deviceID(phone)+" Pixel" {
		t.Fatalf("revoked events = %q", got)
	}
}

func TestRotatingTheTokenSignsEveryPhoneOut(t *testing.T) {
	p := newPhoneRig(t)
	phone := p.pair()
	if p.do("GET", "/", nil, phone, viaServe).Code != http.StatusOK {
		t.Fatal("the phone cannot open before the rotation")
	}
	rotated := board.New(p.fx.st, "fedcba9876543210fedcba9876543210", p.fx.options)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(phone)
	for k, v := range viaServe {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	rotated.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "no longer paired") {
		t.Fatalf("phone after the token rotated = %d", rec.Code)
	}
	if list := p.do("GET", "/connect", nil, laptop, nil).Body.String(); !strings.Contains(list, "Pixel") {
		t.Fatalf("the old Board lost its list:\n%s", list)
	}
}

func TestThePairedPhonesAndTheirEventsHoldNoSecret(t *testing.T) {
	p := newPhoneRig(t)
	phone := p.pair()
	_, secret, _ := strings.Cut(phone.Value, ".")
	code := p.code()
	list := p.do("GET", "/connect", nil, laptop, nil).Body.String()
	own := csrfRe.FindStringSubmatch(p.do("GET", "/", nil, phone, viaServe).Body.String())[1]
	for _, leak := range []string{secret, own, phone.Value} {
		lacks(t, "the laptop's Phone page", list, leak)
	}
	contains(t, "events", strings.Join(p.events("board.paired"), "\n"), "board.paired "+p.deviceID(phone)+" Pixel")
	all, err := p.fx.st.RecentEvents(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		for _, leak := range []string{secret, own, token, code, "operator@example.com", "100.64.0.7"} {
			if strings.Contains(e.Detail, leak) {
				t.Fatalf("event %s %q holds %q", e.Kind, e.Detail, leak)
			}
		}
	}
	devices, _ := p.fx.st.Devices(context.Background())
	if len(devices) != 1 || devices[0].Login != "operator@example.com" {
		t.Fatalf("devices = %+v", devices)
	}
	if _, ok, _ := p.fx.st.DeviceWithSecret(context.Background(), devices[0].ID, secret); ok {
		t.Fatal("the raw cookie secret is what the store keeps")
	}
}

func TestTheLaptopSessionAloneMintsAndListsCodes(t *testing.T) {
	p := newPhoneRig(t)
	for _, path := range []string{"/connect/code", "/connect/revoke"} {
		if rec := p.do("POST", path, url.Values{"csrf": {token}, "base": {"https://x.example"}, "id": {"1"}}, nil, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("POST %s with no session = %d", path, rec.Code)
		}
		if rec := p.do("POST", path, url.Values{"base": {"https://x.example"}}, laptop, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("POST %s with no csrf = %d", path, rec.Code)
		}
	}
	for _, base := range []string{"", "laptop", "ftp://laptop.example", "https://", "javascript:alert(1)", "https://u:p@laptop.example", "https://laptop.example/board", "https://laptop.example?x=1", "https://laptop.example#x", "https://laptop .example"} {
		rec := p.do("POST", "/connect/code", url.Values{"csrf": {token}, "base": {base}}, laptop, nil)
		if rec.Code != http.StatusBadRequest || codeRe.MatchString(rec.Body.String()) || !strings.Contains(rec.Body.String(), `id="base-error"`) {
			t.Fatalf("address %q = %d", base, rec.Code)
		}
	}
	rec := p.do("POST", "/connect/code", url.Values{"csrf": {token}, "base": {" https://Laptop.Tailnet.Example/ "}}, laptop, nil)
	body := rec.Body.String()
	m := codeRe.FindStringSubmatch(body)
	if rec.Code != http.StatusOK || m == nil || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("code = %d, cache %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	contains(t, "code page", body,
		`<code class="ref">https://Laptop.Tailnet.Example/pair#`+m[1]+`</code>`,
		`<code class="ref">https://Laptop.Tailnet.Example/pair</code>`,
		`data-copy="link" data-text="https://Laptop.Tailnet.Example/pair#`+m[1]+`"`,
		`>Copy link</button>`, `<button class="primary">New code</button>`, `value="https://Laptop.Tailnet.Example"`,
		"It works once, until", "kept in memory only")
	lacks(t, "code page", body, "doc-notice")
	plain := p.do("POST", "/connect/code", url.Values{"csrf": {token}, "base": {"http://laptop.lan:7777"}}, laptop, nil).Body.String()
	contains(t, "plain http", plain, `class="doc-notice"`, "unencrypted")
	loop := p.do("POST", "/connect/code", url.Values{"csrf": {token}, "base": {"http://127.0.0.1:7777"}}, laptop, nil).Body.String()
	lacks(t, "loopback", loop, `class="doc-notice"`)
	named := p.do("POST", "/connect/code", url.Values{"csrf": {token}, "base": {"http://localhost:7777"}}, laptop, nil).Body.String()
	lacks(t, "localhost", named, `class="doc-notice"`)
}

func TestThePhonePageIsOnTheRailAndDoesNotKeepTheCode(t *testing.T) {
	p := newPhoneRig(t)
	code := p.code()
	page := p.do("GET", "/connect", nil, laptop, nil)
	body := page.Body.String()
	contains(t, "Phone page", body, `<a href="/connect" aria-current="page">`, `<span class="label">Phone</span>`, "Connect your phone", "Show pairing code", `name="base"`, `data-address`, "No phone is paired.", "Network", "tailscale serve")
	lacks(t, "Phone page", body, code, `style=`, "Desk", `data-region=`, "Tailscale detection")
	if page.Header().Get("Cache-Control") == "" && strings.Contains(body, code) {
		t.Fatal("a page with a code is cacheable")
	}
	if rec := p.do("GET", "/connect", nil, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("Phone page with no session = %d", rec.Code)
	}
}

func TestAFunnelRequestGetsARefusalAndNothingElse(t *testing.T) {
	p := newPhoneRig(t)
	phone := p.pair()
	code := p.code()
	funnel := func(h map[string]string) map[string]string {
		out := map[string]string{"Tailscale-Funnel-Request": "?1"}
		for k, v := range h {
			out[k] = v
		}
		return out
	}
	for _, c := range []struct {
		method, path string
		cookie       *http.Cookie
		form         url.Values
	}{
		{"GET", "/", laptop, nil}, {"GET", "/", phone, nil}, {"GET", "/", nil, nil},
		{"GET", "/pair", nil, nil}, {"POST", "/pair", nil, url.Values{"code": {code}}},
		{"GET", "/attempt/a1", laptop, nil}, {"GET", "/attempt/a1", phone, nil},
		{"GET", "/connect", laptop, nil}, {"POST", "/connect/code", laptop, url.Values{"csrf": {token}, "base": {"https://x.example"}}},
		{"GET", "/events", laptop, nil}, {"GET", "/proof?nonce=x", nil, nil},
		{"GET", "/?token=" + token, nil, nil},
	} {
		rec := p.do(c.method, c.path, c.form, c.cookie, funnel(viaServe))
		body := rec.Body.String()
		if rec.Code != http.StatusForbidden || !strings.Contains(body, "does not answer the public internet") || !strings.Contains(body, "tailscale funnel reset") {
			t.Fatalf("%s %s through Funnel = %d:\n%s", c.method, c.path, rec.Code, body)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("%s %s through Funnel set a cookie", c.method, c.path)
		}
		if strings.Contains(body, token) || strings.Contains(body, code) {
			t.Fatalf("%s %s through Funnel leaked", c.method, c.path)
		}
	}
	refusal := p.do("GET", "/", nil, nil, funnel(nil)).Body.String()
	sheet := regexp.MustCompile(`href="(/static/board\.[0-9a-f]+\.css)"`).FindStringSubmatch(refusal)
	if sheet == nil {
		t.Fatal("the refusal page links no stylesheet")
	}
	if rec := p.do("GET", sheet[1], nil, nil, funnel(nil)); rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("the styles through Funnel = %d", rec.Code)
	}
	if rec := p.tryPair(code, "100.64.0.7"); rec.Code != http.StatusSeeOther {
		t.Fatalf("the code after refused Funnel posts = %d", rec.Code)
	}
	if n := len(p.events("board.paired")); n != 2 {
		t.Fatalf("%d phones paired, want 2", n)
	}
}

func TestEveryResponseTellsTheBrowserNotToSendAReferrer(t *testing.T) {
	p := newPhoneRig(t)
	phone := p.pair()
	for _, c := range []struct {
		path   string
		cookie *http.Cookie
		h      map[string]string
	}{
		{"/", laptop, nil}, {"/", phone, viaServe}, {"/", nil, nil}, {"/pair", nil, viaServe}, {"/connect", laptop, nil},
		{"/tasks", laptop, nil}, {"/static/board.css", nil, nil}, {"/proof?nonce=x", nil, nil}, {"/", laptop, map[string]string{"Tailscale-Funnel-Request": "?1"}},
		{"/doc/inbox", laptop, nil}, {"/nope", laptop, nil},
	} {
		if got := p.do("GET", c.path, nil, c.cookie, c.h).Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("GET %s: Referrer-Policy = %q", c.path, got)
		}
	}
}

func TestAReadOnlyBoardKeepsEveryPhoneSessionReadOnly(t *testing.T) {
	p := newPhoneRig(t)
	p.fx.options.Controls = false
	p.h = p.fx.handler()
	phone := p.pair()
	home := p.do("GET", "/", nil, phone, viaServe)
	if home.Code != http.StatusOK {
		t.Fatalf("read-only phone home = %d", home.Code)
	}
	contains(t, "read-only", home.Body.String(), "Read-only: this board listens on a network address")
	lacks(t, "read-only", home.Body.String(), `id="composer"`, `action="/supervisor/send"`, `action="/supervisor/start"`)
	own := csrfRe.FindStringSubmatch(p.do("GET", "/connect", nil, phone, viaServe).Body.String())[1]
	for _, path := range []string{"/supervisor/send", "/supervisor/start", "/supervisor/resume", "/supervisor/stop", "/supervisor/keys", "/supervisor/interrupt", "/supervisor/force", "/supervisor/switch", "/supervisor/image"} {
		rec := p.do("POST", path, url.Values{"csrf": {own}, "text": {"hi"}}, phone, viaServe)
		if rec.Code < 400 {
			t.Errorf("read-only phone POST %s = %d", path, rec.Code)
		}
	}
	if len(p.fx.srv.Calls("agent.send")) != 0 {
		t.Fatal("a read-only phone reached the supervisor")
	}
}

func TestAPhoneOnALoopbackBoardKeepsTheControlsItsCsrfAllows(t *testing.T) {
	p := newPhoneRig(t)
	p.fx.supervisor(t, state.AttemptRunning, "gen-1")
	phone := p.pair()
	home := p.do("GET", "/", nil, phone, viaServe).Body.String()
	contains(t, "phone home", home, `id="composer"`, `action="/supervisor/send"`, `data-session="phone"`)
	if strings.Contains(p.do("GET", "/", nil, laptop, nil).Body.String(), `data-session=`) {
		t.Fatal("the laptop's page says it is a phone")
	}
}

func TestTheFleetListStaysHiddenFromAProxy(t *testing.T) {
	f := railFleets(t, "alpha", "bravo")
	h := railHost(f, true, nil)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "alpha") || !strings.Contains(rec.Body.String(), "bravo") {
		t.Fatalf("a direct loopback request lost the list:\n%s", rec.Body.String())
	}
	for _, header := range []map[string]string{{"X-Forwarded-For": "100.64.0.7"}, {"Forwarded": "for=100.64.0.7"}, {"Tailscale-User-Login": "operator@example.com"}} {
		req := httptest.NewRequest("GET", "/", nil)
		for k, v := range header {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		body := rec.Body.String()
		if strings.Contains(body, "alpha") || strings.Contains(body, "bravo") || strings.Contains(body, f[0].id) || !strings.Contains(body, "pair it on the fleet's Phone page") {
			t.Fatalf("a proxied request (%v) sees the fleet list:\n%s", header, body)
		}
	}
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Tailscale-Funnel-Request", "?1")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "alpha") || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("Funnel on the fleet list = %d", rec.Code)
	}
}

func TestAPhoneSessionOnTheHostSeesOnlyItsOwnFleet(t *testing.T) {
	f := railFleets(t, "alpha", "bravo")
	h := railHost(f, true, nil)
	req := httptest.NewRequest("POST", "/"+f[0].id+"/connect/code", strings.NewReader(url.Values{"csrf": {f[0].token}, "base": {"https://laptop.example"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "hand_board_" + f[0].id, Value: f[0].token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	code := codeRe.FindStringSubmatch(rec.Body.String())
	if code == nil {
		t.Fatalf("no code:\n%s", rec.Body.String())
	}
	pairReq := httptest.NewRequest("POST", "/"+f[0].id+"/pair", strings.NewReader(url.Values{"code": {code[1]}, "name": {"Pixel"}}.Encode()))
	pairReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range viaServe {
		pairReq.Header.Set(k, v)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, pairReq)
	var phone *http.Cookie
	for _, c := range rec.Result().Cookies() {
		phone = c
	}
	if rec.Code != http.StatusSeeOther || phone == nil || phone.Name != "__Host-hand_phone_"+f[0].id || rec.Header().Get("Location") != "/"+f[0].id+"/" {
		t.Fatalf("pair on the host = %d %+v %q", rec.Code, phone, rec.Header().Get("Location"))
	}
	view := httptest.NewRequest("GET", "/"+f[0].id+"/", nil)
	view.AddCookie(phone)
	for k, v := range viaServe {
		view.Header.Set(k, v)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, view)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || strings.Contains(body, "bravo") || strings.Contains(body, f[1].id) || strings.Contains(body, f[0].token) {
		t.Fatalf("phone on the host = %d:\n%s", rec.Code, body)
	}
	other := httptest.NewRequest("GET", "/"+f[1].id+"/", nil)
	other.AddCookie(phone)
	for k, v := range viaServe {
		other.Header.Set(k, v)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, other)
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "alpha") {
		t.Fatalf("a phone paired with one fleet opened another = %d", rec.Code)
	}
}

func TestAFunnelRequestToTheHostGetsOnlyTheStyles(t *testing.T) {
	f := railFleets(t, "alpha", "bravo")
	h := railHost(f, true, nil)
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Tailscale-Funnel-Request", "?1")
		req.AddCookie(&http.Cookie{Name: "hand_board_" + f[0].id, Value: f[0].token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, path := range []string{"/", "/" + f[0].id + "/", "/" + f[0].id + "/pair", "/" + f[0].id + "/connect", "/" + f[0].id + "/events", "/" + f[1].id + "/", "/nope"} {
		rec := get(path)
		body := rec.Body.String()
		if rec.Code != http.StatusForbidden || !strings.Contains(body, "does not answer the public internet") {
			t.Fatalf("GET %s through Funnel = %d:\n%s", path, rec.Code, body)
		}
		for _, name := range []string{"alpha", "bravo", f[0].id, f[1].id, f[0].token} {
			if strings.Contains(body, name) {
				t.Fatalf("GET %s through Funnel names %q", path, name)
			}
		}
	}
	sheet := regexp.MustCompile(`href="(/static/board\.[0-9a-f]+\.css)"`).FindStringSubmatch(get("/").Body.String())
	if sheet == nil {
		t.Fatal("the refusal page links no stylesheet")
	}
	if rec := get(sheet[1]); rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("the styles through Funnel = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestOnePhonesFormTokenOpensNothingOnAnotherSession(t *testing.T) {
	p := newPhoneRig(t)
	a, b := p.pair(), p.pair()
	tokenOf := func(c *http.Cookie) string {
		return csrfRe.FindStringSubmatch(p.do("GET", "/", nil, c, viaServe).Body.String())[1]
	}
	ta, tb := tokenOf(a), tokenOf(b)
	if ta == tb || ta == token || tb == token {
		t.Fatal("two sessions share a form token")
	}
	for name, c := range map[string]struct {
		cookie *http.Cookie
		csrf   string
	}{"A's token on B": {b, ta}, "B's token on A": {a, tb}, "A's token on the laptop": {laptop, ta}, "no token": {a, ""}, "the token on a phone": {a, token}} {
		rec := p.do("POST", "/supervisor/send", url.Values{"csrf": {c.csrf}, "text": {"x"}}, c.cookie, viaServe)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}
	if rec := p.do("POST", "/supervisor/send", url.Values{"csrf": {ta}, "text": {"x"}}, a, viaServe); rec.Code == http.StatusForbidden {
		t.Fatalf("a phone's own token = %d", rec.Code)
	}
}

func TestEveryNewPageRefusesFramingAndKeepsTheStrictPolicy(t *testing.T) {
	p := newPhoneRig(t)
	phone := p.pair()
	for _, c := range []struct {
		method, path string
		cookie       *http.Cookie
		h            map[string]string
		form         url.Values
	}{
		{"GET", "/pair", nil, viaServe, nil}, {"POST", "/pair", nil, viaServe, url.Values{"code": {"AAAAA-AAAAA"}}}, {"GET", "/connect", laptop, nil, nil},
		{"GET", "/connect", phone, viaServe, nil}, {"GET", "/", nil, viaServe, nil}, {"GET", "/", laptop, map[string]string{"Tailscale-Funnel-Request": "?1"}, nil},
		{"POST", "/connect/revoke", phone, viaServe, url.Values{"csrf": {csrfRe.FindStringSubmatch(p.do("GET", "/", nil, phone, viaServe).Body.String())[1]}, "id": {p.deviceID(phone)}}},
	} {
		h := p.do(c.method, c.path, c.form, c.cookie, c.h).Header()
		if h.Get("X-Frame-Options") != "DENY" || !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") || !strings.Contains(h.Get("Content-Security-Policy"), "script-src 'self'; style-src 'self'") || !strings.Contains(h.Get("Content-Security-Policy"), "form-action 'self'") {
			t.Errorf("%s %s sends X-Frame-Options %q and CSP %q", c.method, c.path, h.Get("X-Frame-Options"), h.Get("Content-Security-Policy"))
		}
	}
}

func TestPairingWritesNothingToTheLog(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	p := newPhoneRig(t)
	code := p.code()
	phone := p.pair()
	p.tryPair(code, "100.64.0.7")
	p.tryPair("AAAAA-AAAAA", "100.64.0.7")
	p.do("GET", "/", nil, phone, viaServe)
	p.do("GET", "/", nil, nil, map[string]string{"Tailscale-Funnel-Request": "?1"})
	if buf.Len() != 0 {
		t.Fatalf("the board logged %q", buf.String())
	}
}

func TestANewPhoneDoesNotInheritARevokedPhonesLastSeen(t *testing.T) {
	p := newPhoneRig(t)
	old := p.pair()
	p.do("GET", "/", nil, old, viaServe)
	if !strings.Contains(p.do("GET", "/connect", nil, laptop, nil).Body.String(), ", seen ") {
		t.Fatal("the list shows no last seen time for a phone that just loaded a page")
	}
	p.do("POST", "/connect/revoke", url.Values{"csrf": {token}, "id": {p.deviceID(old)}}, laptop, nil)
	fresh := p.pair()
	if p.deviceID(fresh) == p.deviceID(old) {
		t.Fatalf("a new phone got the revoked phone's number %s", p.deviceID(fresh))
	}
	if strings.Contains(p.do("GET", "/connect", nil, laptop, nil).Body.String(), ", seen ") {
		t.Fatal("a new phone shows the last seen time of a revoked one")
	}
}

func TestTheCodePageShowsThePairingLinkAsAQRCode(t *testing.T) {
	p := newPhoneRig(t)
	show := func() (*httptest.ResponseRecorder, string, string) {
		rec := p.do("POST", "/connect/code", url.Values{"csrf": {token}, "base": {"https://laptop.tailnet.example"}}, laptop, nil)
		body := rec.Body.String()
		m := codeRe.FindStringSubmatch(body)
		if rec.Code != http.StatusOK || m == nil {
			t.Fatalf("connect/code = %d", rec.Code)
		}
		return rec, body, m[1]
	}
	rec, body, code := show()
	want, err := board.QRSVG("https://laptop.tailnet.example/pair#"+code, "QR code for the pairing link")
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "code page", body, string(want), `aria-label="QR code for the pairing link"`, "Scan the code with the phone's camera, or open this link on the phone:",
		`<code class="ref">https://laptop.tailnet.example/pair#`+code+`</code>`, `class="code-big" aria-label="Pairing code `+code+`"`)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if strings.Count(body, `<svg class="qr"`) != 1 || strings.Index(body, `<svg class="qr"`) > strings.Index(body, `class="code-big"`) {
		t.Fatal("the page holds more than one picture, or the code comes before it")
	}
	_, again, next := show()
	if next == code || strings.Contains(again, string(want)) {
		t.Fatal("a new code shows the old picture")
	}
	if page := p.do("GET", "/connect", nil, laptop, nil).Body.String(); strings.Contains(page, `<svg class="qr"`) {
		t.Fatal("the Phone page keeps a QR code once the code is gone")
	}
}

func TestALinkTooLongForAQRCodeFallsBackToText(t *testing.T) {
	p := newPhoneRig(t)
	for _, c := range []struct {
		host    int
		picture bool
	}{{188, true}, {189, false}} {
		base := "https://" + strings.Repeat("a", c.host)
		rec := p.do("POST", "/connect/code", url.Values{"csrf": {token}, "base": {base}}, laptop, nil)
		body := rec.Body.String()
		m := codeRe.FindStringSubmatch(body)
		if rec.Code != http.StatusOK || m == nil || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%d host bytes: code = %d", c.host, rec.Code)
		}
		if got := strings.Contains(body, `<svg class="qr"`); got != c.picture {
			t.Errorf("%d host bytes: a QR code shows is %v, want %v", c.host, got, c.picture)
		}
		contains(t, "code page", body, `<code class="ref">`+base+"/pair#"+m[1]+"</code>", `<button type="button" class="doc-copy" data-copy="link"`)
		if !c.picture {
			contains(t, "code page", body, "On the phone, open this link:")
			lacks(t, "code page", body, "Scan the code", "QR code")
		}
	}
}
