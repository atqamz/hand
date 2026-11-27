package board_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
)

func (p *companionHost) selfCSRF(f railFleet, phone *http.Cookie) string {
	p.t.Helper()
	_, sess := apiBody[board.APISession](p.t, p.papi(f, "GET", "/session", nil, []*http.Cookie{phone}))
	return sess.CSRF
}

func (p *companionHost) selfRevoke(f railFleet, method, csrf string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	p.t.Helper()
	req := httptest.NewRequest(method, "/"+f.id+"/api/v1/session/revoke", http.NoBody)
	if csrf != "" {
		req.Header.Set("X-Hand-CSRF", csrf)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	p.h.ServeHTTP(rec, req)
	return rec
}

func TestAPISessionRevokeEndsOnlyTheCallingPhoneInThatFleet(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	label, cookies := pairedAcross(t, p, b)
	cA, cB := cookies[a.id], cookies[b.id]
	idA := strings.SplitN(cA.Value, ".", 2)[0]
	rec := p.selfRevoke(a, "POST", p.selfCSRF(a, cA), nil, cA)
	env, got := apiBody[board.APIAccepted](t, rec)
	if !got.Accepted || env.Fleet.ID != a.id {
		t.Fatalf("answer = %+v, fleet %+v", got, env.Fleet)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatalf("the answer sets cookies: %v", rec.Result().Cookies())
	}
	if n := len(p.devicesOf(a)); n != 0 {
		t.Fatalf("alpha holds %d devices, want 0", n)
	}
	if n := len(p.devicesOf(b)); n != 1 {
		t.Fatalf("beta holds %d devices, want 1", n)
	}
	if ev := p.revoked(a); len(ev) != 1 || !strings.HasPrefix(ev[0], idA+" ") {
		t.Fatalf("alpha revoked events = %q", ev)
	}
	if ev := p.revoked(b); len(ev) != 0 {
		t.Fatalf("beta revoked events = %q", ev)
	}
	if code := p.opens(a, nil, cA); code == http.StatusOK {
		t.Fatalf("the signed-out phone still opens alpha = %d", code)
	}
	if code := p.opens(b, nil, cB); code != http.StatusOK {
		t.Fatalf("beta after alpha's sign out = %d", code)
	}
	_, list := apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, p.hosts(a, b)))
	for _, d := range list.Devices {
		if d.Phone == label && len(d.Fleets) != 1 {
			t.Fatalf("the phone's fleets = %+v, want only beta", d.Fleets)
		}
	}
	if _, sess := apiBody[board.APISession](t, p.papi(b, "GET", "/session", nil, []*http.Cookie{cB})); sess.CSRF == "" {
		t.Fatal("beta's session is gone")
	}
}

func TestAPISessionRevokeNeverTouchesAnotherDevice(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	one := p.pair(a, nil)
	two := p.pair(a, nil)
	idTwo := strings.SplitN(two.Value, ".", 2)[0]
	if rec := p.selfRevoke(a, "POST", p.selfCSRF(a, two), nil, two); rec.Code != http.StatusOK {
		t.Fatalf("second phone's call = %d:\n%s", rec.Code, rec.Body)
	}
	left := p.devicesOf(a)
	if len(left) != 1 || strings.SplitN(one.Value, ".", 2)[0] == idTwo || p.opens(a, nil, one) != http.StatusOK {
		t.Fatalf("devices after the second phone signed out = %+v", left)
	}
	if code := p.opens(a, nil, two); code == http.StatusOK {
		t.Fatalf("the signed-out second phone opens alpha")
	}
	if ev := p.revoked(a); len(ev) != 1 || !strings.HasPrefix(ev[0], idTwo+" ") {
		t.Fatalf("revoked events = %q", ev)
	}
}

func TestAPISessionRevokeNamesNoTarget(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	one := p.pair(a, nil)
	two := p.pair(a, nil)
	idOne := strings.SplitN(one.Value, ".", 2)[0]
	body := `{"id": ` + idOne + `, "phone": "0123456789abcdef"}`
	req := httptest.NewRequest("POST", "/"+a.id+"/api/v1/session/revoke", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hand-CSRF", p.selfCSRF(a, two))
	req.AddCookie(two)
	rec := httptest.NewRecorder()
	p.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("call = %d:\n%s", rec.Code, rec.Body)
	}
	if p.opens(a, nil, one) != http.StatusOK || p.opens(a, nil, two) == http.StatusOK {
		t.Fatal("a body named another device and the call followed it")
	}
}

func TestAPISessionRevokeIgnoresATargetInTheQuery(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	one := p.pair(a, nil)
	two := p.pair(a, nil)
	idOne := strings.SplitN(one.Value, ".", 2)[0]
	req := httptest.NewRequest("POST", "/"+a.id+"/api/v1/session/revoke?id="+idOne+"&phone=0123456789abcdef&fleet="+a.id, http.NoBody)
	req.Header.Set("X-Hand-CSRF", p.selfCSRF(a, two))
	req.AddCookie(two)
	rec := httptest.NewRecorder()
	p.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("call = %d:\n%s", rec.Code, rec.Body)
	}
	if p.opens(a, nil, one) != http.StatusOK || p.opens(a, nil, two) == http.StatusOK || len(p.devicesOf(a)) != 1 {
		t.Fatal("a target in the query changed which device was revoked")
	}
}

func TestAPISessionRevokeRefusals(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	phone := p.pair(a, nil)
	csrf := p.selfCSRF(a, phone)
	keep := func(what string) {
		t.Helper()
		if n := len(p.devicesOf(a)); n != 1 || p.opens(a, nil, phone) != http.StatusOK || len(p.revoked(a)) != 0 {
			t.Fatalf("%s changed the fleet: %d devices", what, n)
		}
	}
	for _, csrf := range []string{"", "wrong", a.token} {
		testAPIError(t, p.selfRevoke(a, "POST", csrf, nil, phone), http.StatusForbidden, "csrf_required")
		keep("csrf " + csrf)
	}
	host := p.host(a)
	testAPIError(t, p.selfRevoke(a, "POST", a.token, nil, host), http.StatusForbidden, "forbidden")
	keep("the host session")
	testAPIError(t, p.selfRevoke(a, "POST", csrf, nil), http.StatusUnauthorized, "unauthorized")
	keep("no cookie")
	testAPIError(t, p.selfRevoke(a, "POST", csrf, nil, &http.Cookie{Name: phone.Name, Value: "1." + strings.Repeat("a", 64)}), http.StatusUnauthorized, "unauthorized")
	keep("a forged cookie")
	for _, m := range []string{"PUT", "DELETE", "PATCH"} {
		rec := p.selfRevoke(a, m, csrf, nil, phone)
		testAPIError(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
		if rec.Header().Get("Allow") != "POST" {
			t.Fatalf("%s Allow = %q", m, rec.Header().Get("Allow"))
		}
		keep(m)
	}
	for _, m := range []string{"GET", "HEAD"} {
		if rec := p.selfRevoke(a, m, csrf, nil, phone); rec.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", m, rec.Code)
		}
		keep(m)
	}
	testAPIError(t, p.selfRevoke(a, "POST", csrf, map[string]string{"X-Forwarded-For": "203.0.113.9"}, phone), http.StatusForbidden, "forbidden")
	keep("a proxied request")
}

func TestAPISessionRevokeAnotherFleetsCookieProvesNothing(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	_, cookies := pairedAcross(t, p, b)
	cB := cookies[b.id]
	testAPIError(t, p.selfRevoke(a, "POST", p.selfCSRF(b, cB), nil, cB), http.StatusUnauthorized, "unauthorized")
	if len(p.devicesOf(a)) != 1 || len(p.devicesOf(b)) != 1 {
		t.Fatal("a cookie of one fleet revoked in another")
	}
}

func TestAPISessionRevokeTwiceAnswersGoneNotAccepted(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	phone := p.pair(a, nil)
	csrf := p.selfCSRF(a, phone)
	if rec := p.selfRevoke(a, "POST", csrf, nil, phone); rec.Code != http.StatusOK {
		t.Fatalf("first = %d", rec.Code)
	}
	testAPIError(t, p.selfRevoke(a, "POST", csrf, nil, phone), http.StatusUnauthorized, "unauthorized")
	if ev := p.revoked(a); len(ev) != 1 {
		t.Fatalf("revoked events = %q", ev)
	}
}

func TestAPISessionRevokeRacingCallsLeaveOneEvent(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	phone := p.pair(a, nil)
	csrf := p.selfCSRF(a, phone)
	codes := make(chan int, 2)
	for range 2 {
		go func() { codes <- p.selfRevoke(a, "POST", csrf, nil, phone).Code }()
	}
	got := []int{<-codes, <-codes}
	for _, c := range got {
		if c != http.StatusOK && c != http.StatusUnauthorized {
			t.Fatalf("racing codes = %v", got)
		}
	}
	if len(p.devicesOf(a)) != 0 || len(p.revoked(a)) != 1 {
		t.Fatalf("devices %d, events %q", len(p.devicesOf(a)), p.revoked(a))
	}
}

func TestAPISessionRevokeAnExpiredPhoneCannotSignOut(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	phone := p.pair(a, nil)
	csrf := p.selfCSRF(a, phone)
	p.advance(91 * 24 * time.Hour)
	testAPIError(t, p.selfRevoke(a, "POST", csrf, nil, phone), http.StatusUnauthorized, "unauthorized")
	if len(p.devicesOf(a)) != 1 {
		t.Fatal("a refused call removed the row")
	}
}

func TestAPISessionRevokeThatCannotDeleteAnswers500AndKeepsTheRow(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	phone := p.pair(a, nil)
	csrf := p.selfCSRF(a, phone)
	db, err := sql.Open("sqlite", "file:"+p.paths[a.id])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TRIGGER keep_device BEFORE DELETE ON device BEGIN SELECT RAISE(ABORT, 'kept'); END`); err != nil {
		t.Fatal(err)
	}
	testAPIError(t, p.selfRevoke(a, "POST", csrf, nil, phone), http.StatusInternalServerError, "internal")
	if len(p.devicesOf(a)) != 1 || len(p.revoked(a)) != 0 || p.opens(a, nil, phone) != http.StatusOK {
		t.Fatal("a failed delete changed the fleet")
	}
}

func TestAPISessionRevokeOverThePeerKeepsTheConnectionForTheOtherFleets(t *testing.T) {
	r := newPeerRig(t, "alpha", "bravo")
	a, b := r.fleets[0], r.fleets[1]
	c := r.as(peerA)
	cA, cB := r.mustPair(c, a), r.mustPair(c, b)
	call := func(f railFleet, ck *http.Cookie) peerReply {
		sess := c.do("GET", "/"+f.id+"/api/v1/session", nil, nil, ck)
		var env struct {
			Data struct {
				CSRF string `json:"csrf"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(sess.Body), &env); err != nil || env.Data.CSRF == "" {
			t.Fatalf("session = %d %v:\n%s", sess.Code, err, sess.Body)
		}
		return c.do("POST", "/"+f.id+"/api/v1/session/revoke", nil, map[string]string{"X-Hand-CSRF": env.Data.CSRF}, ck)
	}
	for _, f := range []struct {
		f  railFleet
		ck *http.Cookie
	}{{a, cA}, {b, cB}} {
		if rep := call(f.f, f.ck); rep.Code != http.StatusOK || !strings.Contains(rep.Body, `"accepted":true`) {
			t.Fatalf("sign out of %s = %d:\n%s", f.f.name, rep.Code, rep.Body)
		}
		if c.closed() {
			t.Fatalf("signing out of %s closed the connection", f.f.name)
		}
	}
	if len(r.devicesOf(a))+len(r.devicesOf(b)) != 0 {
		t.Fatal("a device is left")
	}
	again := c.do("POST", "/"+a.id+"/api/v1/session/revoke", nil, map[string]string{"X-Hand-CSRF": "x"}, cA)
	if again.Code != http.StatusForbidden || !strings.Contains(goneNotice(again), "no longer paired") {
		t.Fatalf("repeat over the peer = %d, notice %q", again.Code, goneNotice(again))
	}
	both := c.do("POST", "/"+a.id+"/api/v1/session/revoke", nil, map[string]string{"X-Hand-CSRF": "x"}, cA, cB)
	if both.Code != http.StatusForbidden {
		t.Fatalf("repeat beside a signed-out cookie = %d", both.Code)
	}
}

func TestAPISessionRevokeOverThePeerRefusesALapsedPhoneAndKeepsItsRow(t *testing.T) {
	r := newPeerRig(t, "alpha")
	a := r.fleets[0]
	c := r.as(peerA)
	ck := r.mustPair(c, a)
	sess := c.do("GET", "/"+a.id+"/api/v1/session", nil, nil, ck)
	var env struct {
		Data struct {
			CSRF string `json:"csrf"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(sess.Body), &env); err != nil || env.Data.CSRF == "" {
		t.Fatalf("session = %d %v", sess.Code, err)
	}
	r.advance(91 * 24 * time.Hour)
	rep := c.do("POST", "/"+a.id+"/api/v1/session/revoke", nil, map[string]string{"X-Hand-CSRF": env.Data.CSRF}, ck)
	if rep.Code != http.StatusForbidden || !strings.Contains(goneNotice(rep), "no longer paired") {
		t.Fatalf("lapsed over the peer = %d, notice %q", rep.Code, goneNotice(rep))
	}
	if len(r.devicesOf(a)) != 1 {
		t.Fatal("a lapsed call removed the row")
	}
}

func TestAPISessionRevokeAgainBesideALiveCookieIs401OverThePeer(t *testing.T) {
	r := newPeerRig(t, "alpha", "bravo")
	a, b := r.fleets[0], r.fleets[1]
	c := r.as(peerA)
	cA, cB := r.mustPair(c, a), r.mustPair(c, b)
	r.revoke(a, cA)
	rep := r.as(peerA).do("POST", "/"+a.id+"/api/v1/session/revoke", nil, map[string]string{"X-Hand-CSRF": "x"}, cA, cB)
	if rep.Code != http.StatusUnauthorized || goneNotice(rep) != "" {
		t.Fatalf("repeat beside a live cookie = %d, notice %q", rep.Code, goneNotice(rep))
	}
}
