package board_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
)

type pairReply struct {
	Error struct{ Code string }
	Data  board.APIPaired
}

func pairBody(code, name string) string {
	b, _ := json.Marshal(map[string]string{"code": code, "name": name})
	return string(b)
}

func (r *peerRig) pairAPI(c *peerClient, f railFleet, body string) (peerReply, pairReply) {
	r.t.Helper()
	rep := r.post(c, f, "pair", body, "")
	var got pairReply
	if err := json.Unmarshal([]byte(rep.Body), &got); err != nil {
		r.t.Fatalf("pair answer is not JSON: %v\n%s", err, rep.Body)
	}
	return rep, got
}

func (r *peerRig) refused(c *peerClient, f railFleet, body string, status int, code string) {
	r.t.Helper()
	rep, got := r.pairAPI(c, f, body)
	if rep.Code != status || got.Error.Code != code {
		r.t.Fatalf("pair = %d %q, want %d %q:\n%s", rep.Code, got.Error.Code, status, code, rep.Body)
	}
}

func TestAnUnpairedPeerPairsThroughTheAPIAndItsSecretOpensTheAPI(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	code := r.code(f)
	rep, got := r.pairAPI(c, f, pairBody(code, "Pixel"))
	if rep.Code != http.StatusOK || len(got.Data.Fleets) != 1 || rep.Header.Get("Cache-Control") != "no-store" || len(rep.Cookies) != 0 {
		t.Fatalf("pair = %d, cache %q, %d cookies:\n%s", rep.Code, rep.Header.Get("Cache-Control"), len(rep.Cookies), rep.Body)
	}
	pf := got.Data.Fleets[0]
	if pf.ID != f.id || pf.Name != "alpha" || pf.CookieName != "hand_phone_"+f.id || !regexp.MustCompile(`^[1-9][0-9]*\.[0-9a-f]{64}$`).MatchString(pf.CookieValue) {
		t.Fatalf("answer = %+v", pf)
	}
	devices := r.devicesOf(f)
	if len(devices) != 1 || devices[0].Login != "iroh:"+peerA || devices[0].Name != "Pixel" {
		t.Fatalf("devices = %+v", devices)
	}
	companion := &http.Cookie{Name: pf.CookieName, Value: pf.CookieValue}
	if rep := r.apiGet(c, f, "status", nil, companion); rep.Code != http.StatusOK {
		t.Fatalf("status with the answered secret = %d:\n%s", rep.Code, rep.Body)
	}
	if rep := r.apiGet(r.as(peerB), f, "status", nil, companion); rep.Code != http.StatusForbidden {
		t.Fatalf("the secret from another endpoint = %d", rep.Code)
	}
	r.refused(r.as(peerB), f, pairBody(code, "Pixel"), http.StatusForbidden, "code_used")
	if n := len(r.devicesOf(f)); n != 1 {
		t.Fatalf("a second redeem left %d devices", n)
	}
}

func TestThePairAPIRefusesAWrongAnExpiredAndAUsedCodeEachByItsOwnCode(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	stale := r.code(f)
	r.advance(6 * time.Minute)
	r.refused(c, f, pairBody(stale, "Pixel"), http.StatusForbidden, "code_expired")
	live := r.code(f)
	r.refused(c, f, pairBody("ZZZZZ-ZZZZZ", "Pixel"), http.StatusForbidden, "wrong_code")
	r.refused(c, f, pairBody("", "Pixel"), http.StatusForbidden, "wrong_code")
	if rep, _ := r.pairAPI(c, f, pairBody(live, "Pixel")); rep.Code != http.StatusOK {
		t.Fatalf("the live code = %d:\n%s", rep.Code, rep.Body)
	}
	r.refused(c, f, pairBody(live, "Pixel"), http.StatusForbidden, "code_used")
}

func TestThePairAPIRateLimitsAPeerAndCancelsEveryCodeAfterTenWrongTries(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	code := r.code(f)
	a, b := r.as(peerA), r.as(peerB)
	for range 5 {
		r.refused(a, f, pairBody("ZZZZZ-ZZZZZ", "Pixel"), http.StatusForbidden, "wrong_code")
	}
	r.refused(a, f, pairBody(code, "Pixel"), http.StatusTooManyRequests, "rate_limited")
	for range 4 {
		r.refused(b, f, pairBody("ZZZZZ-ZZZZZ", "Pixel"), http.StatusForbidden, "wrong_code")
	}
	r.refused(b, f, pairBody("ZZZZZ-ZZZZZ", "Pixel"), http.StatusForbidden, "codes_cancelled")
	r.refused(b, f, pairBody(code, "Pixel"), http.StatusForbidden, "wrong_code")
	if n := len(r.devicesOf(f)); n != 0 {
		t.Fatalf("a refused pairing left %d devices", n)
	}
}

func TestThePairAPIRefusesABadNameWithoutSpendingTheCode(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	code := r.code(f)
	for _, name := range []string{strings.Repeat("x", 41), "Pi\nxel", "Pi\x00xel", "Pixel\n", "\tPixel"} {
		r.refused(c, f, pairBody(code, name), http.StatusBadRequest, "bad_name")
	}
	rep, got := r.pairAPI(c, f, pairBody(code, strings.Repeat("é", 40)))
	if rep.Code != http.StatusOK || len(got.Data.Fleets) != 1 {
		t.Fatalf("the code after bad names = %d:\n%s", rep.Code, rep.Body)
	}
}

func TestThePairAPIReadsOnlyOneSmallJSONObject(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	code := r.code(f)
	for name, body := range map[string]string{
		"empty":       "",
		"text":        "code=" + code,
		"array":       "[]",
		"null":        "null",
		"no keys":     "{}",
		"no name":     `{"code":"` + code + `"}`,
		"null name":   `{"code":"` + code + `","name":null}`,
		"null code":   `{"code":null,"name":"Pixel"}`,
		"number name": `{"code":"` + code + `","name":7}`,
		"unknown key": `{"code":"` + code + `","name":"Pixel","extra":"x"}`,
		"cased keys":  `{"Code":"` + code + `","Name":"Pixel"}`,
		"trailing":    pairBody(code, "Pixel") + pairBody(code, "Pixel"),
		"large":       pairBody(code, strings.Repeat("x", 5000)),
	} {
		for range 3 {
			rep, got := r.pairAPI(r.as(peerA), f, body)
			if rep.Code != http.StatusBadRequest || got.Error.Code != "bad_request" {
				t.Fatalf("%s = %d:\n%s", name, rep.Code, rep.Body)
			}
		}
	}
	req, _ := http.NewRequest("POST", "http://localhost/"+f.id+"/api/v1/pair", strings.NewReader(pairBody(code, "Pixel")))
	req.Header.Set("Content-Type", "text/plain")
	if rep, err := c.exchange(req); err != nil || rep.Code != http.StatusBadRequest || !strings.Contains(rep.Body, `"bad_request"`) {
		t.Fatalf("a form-like content type = %d, %v:\n%s", rep.Code, err, rep.Body)
	}
	if rep, _ := r.pairAPI(c, f, pairBody(code, "Pixel")); rep.Code != http.StatusOK {
		t.Fatalf("the code after 39 unreadable bodies, which must not count as wrong codes = %d:\n%s", rep.Code, rep.Body)
	}
}

func TestThePairAPIKeepsTheAPIsRefusalsForForwardedFunnelAndNetworkAddressRequests(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	f := p.fleets[0]
	code := codeOf(p, f)
	do := func(headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/"+f.id+"/api/v1/pair", strings.NewReader(pairBody(code, "Pixel")))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		p.h.ServeHTTP(rec, req)
		return rec
	}
	for _, h := range []map[string]string{
		{"X-Forwarded-For": "100.64.0.7"},
		{"Forwarded": "for=100.64.0.7"},
		{"Tailscale-Funnel-Request": "?1"},
	} {
		if rec := do(h); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"forbidden"`) {
			t.Fatalf("pair with %v = %d:\n%s", h, rec.Code, rec.Body.String())
		}
	}
	if len(p.devicesOf(f)) != 0 {
		t.Fatal("a refused request paired a device")
	}
	if rec := do(nil); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"forbidden"`) || strings.Contains(rec.Body.String(), "hand_phone") {
		t.Fatalf("a direct loopback request = %d:\n%s", rec.Code, rec.Body.String())
	}
	if len(p.devicesOf(f)) != 0 {
		t.Fatal("the loopback listener paired a device")
	}

	r := newPeerRig(t, "alpha")
	rf := r.fleets[0]
	r.refused(r.anonymous(), rf, pairBody(r.code(rf), "Pixel"), http.StatusForbidden, "forbidden")
	if len(r.devicesOf(rf)) != 0 {
		t.Fatal("a peer with no key paired a device through the API")
	}

	n := servePeerRig(t, newCompanionHost(t, false, "alpha"))
	nf := n.fleets[0]
	n.refused(n.as(peerA), nf, pairBody(n.code(nf), "Pixel"), http.StatusForbidden, "forbidden")
}

func codeOf(p *companionHost, f railFleet) string {
	return p.codeOf(p.mint(f, []*http.Cookie{p.host(f)}))
}

func TestOnlyThePairPathIsOpenToAnUnpairedPeerOnTheAPI(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	for _, path := range []string{"status", "session", "events", "timeline", "tasks", "task/t1", "pair/x", "pair/"} {
		if rep := r.apiGet(c, f, path, nil); rep.Code != http.StatusForbidden || !strings.Contains(rep.Body, "Pair this phone first") {
			t.Fatalf("GET %s = %d:\n%s", path, rep.Code, rep.Body)
		}
	}
	for _, path := range []string{"message", "pair/x", "supervisor/stop"} {
		if rep := r.post(c, f, path, "{}", "x"); rep.Code != http.StatusForbidden || !strings.Contains(rep.Body, "Pair this phone first") {
			t.Fatalf("POST %s = %d:\n%s", path, rep.Code, rep.Body)
		}
	}
	if rep := r.apiGet(c, f, "pair", nil); rep.Code != http.StatusUnauthorized {
		t.Fatalf("GET pair = %d", rep.Code)
	}
}

func TestTheCompanionVectorsForPairingAreWhatTheBoardAnswers(t *testing.T) {
	raw, err := os.ReadFile("../../docs/companion-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Pair struct {
			CookieValue string `json:"cookie_value"`
			Exchanges   []struct {
				Name    string
				Given   string
				Request struct{ Code, Name string }
				Status  int
				Error   string
				Fleets  []struct {
					ID, Name   string
					CookieName string `json:"cookie_name"`
				}
			}
		}
	}
	if err := json.Unmarshal(raw, &v); err != nil || len(v.Pair.Exchanges) < 7 {
		t.Fatalf("%d exchanges, %v", len(v.Pair.Exchanges), err)
	}
	value := regexp.MustCompile(v.Pair.CookieValue)
	for _, x := range v.Pair.Exchanges {
		r := newPeerRig(t, "hand")
		f := r.fleets[0]
		c := r.as(peerA)
		code := r.code(f)
		switch x.Given {
		case "used":
			r.pairAPI(r.as(peerB), f, pairBody(code, "Other"))
		case "expired":
			r.advance(6 * time.Minute)
		case "five wrong tries":
			for range 5 {
				r.pairAPI(c, f, pairBody("ZZZZZ-ZZZZZ", "Pixel"))
			}
		case "fresh", "wrong":
		default:
			t.Fatalf("%s: unknown given %q", x.Name, x.Given)
		}
		if x.Request.Code == "<code>" {
			x.Request.Code = code
		}
		rep, got := r.pairAPI(c, f, pairBody(x.Request.Code, x.Request.Name))
		if rep.Code != x.Status || got.Error.Code != x.Error {
			t.Errorf("%s: %d %q, want %d %q:\n%s", x.Name, rep.Code, got.Error.Code, x.Status, x.Error, rep.Body)
			continue
		}
		if x.Status != http.StatusOK {
			continue
		}
		if len(got.Data.Fleets) != len(x.Fleets) {
			t.Fatalf("%s: %d fleets, want %d", x.Name, len(got.Data.Fleets), len(x.Fleets))
		}
		for i, want := range x.Fleets {
			g := got.Data.Fleets[i]
			if g.ID != strings.ReplaceAll(want.ID, "<fleet id>", f.id) || g.Name != want.Name || g.CookieName != strings.ReplaceAll(want.CookieName, "<fleet id>", f.id) || !value.MatchString(g.CookieValue) {
				t.Errorf("%s: fleet %+v, want %+v", x.Name, g, want)
			}
		}
	}
}

func TestAnUnpairedPeerPairingWithAFleetThatDoesNotOpenGetsNotFoundNotUnauthorized(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	code := r.code(f)
	r.setDown(f)
	c := r.as(peerA)
	r.refused(c, f, pairBody(code, "Pixel"), http.StatusNotFound, "not_found")
	if rep := r.apiGet(c, f, "status", nil); rep.Code != http.StatusForbidden {
		t.Fatalf("an unpaired peer's GET status on a fleet that does not open = %d", rep.Code)
	}
	if rep := c.do("POST", "/"+f.id+"/pair", url.Values{"code": {code}, "name": {"Pixel"}}, nil); rep.Code != http.StatusNotFound {
		t.Fatalf("the form on a fleet that does not open = %d", rep.Code)
	}
	for _, method := range []string{"GET", "HEAD", "PUT", "DELETE", "OPTIONS"} {
		if rep := c.do(method, "/"+f.id+"/api/v1/pair", nil, nil); rep.Code != http.StatusNotFound {
			t.Fatalf("%s pair on a fleet that does not open = %d", method, rep.Code)
		}
	}
}

func TestAWrongMethodOnThePairPathOfAnOpenFleetStaysUnauthorized(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		if rep := r.as(peerA).do(method, "/"+f.id+"/api/v1/pair", nil, nil); rep.Code != http.StatusUnauthorized || !strings.Contains(rep.Body, `"unauthorized"`) {
			t.Fatalf("%s pair on an open fleet = %d:\n%s", method, rep.Code, rep.Body)
		}
	}
}
