package board_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/sidecar"
	"github.com/atqamz/hand/internal/state"
)

const (
	phoneFleet = "f00000000e0aa"
	phoneLabel = "0123456789abcdef"
	phoneHash  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

var (
	readyIroh      = sidecar.Status{State: sidecar.Ready, ID: irohEndpoint, Relay: "https://relay.example"}
	cursorRe       = regexp.MustCompile(`"cursor": "[^"]*"`)
	phoneAppCodeRe = regexp.MustCompile(`c=([0-9A-Z]{10})`)
)

func phoneFixture(t *testing.T, st *sidecar.Status) (*fixture, http.Handler) {
	t.Helper()
	fx := apiFixture(t, func() time.Time { return frozen })
	fx.options.Base = "/" + phoneFleet
	if st != nil {
		fx.options.Iroh = func() sidecar.Status { return *st }
	}
	return fx, fx.handler()
}

func phoneDo(h http.Handler, method, path string, body any, csrf string, cookie bool) *httptest.ResponseRecorder {
	var rd io.Reader = http.NoBody
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-Hand-CSRF", csrf)
	}
	if cookie {
		req.AddCookie(&http.Cookie{Name: "hand_board_" + phoneFleet, Value: token})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func seedPhones(t *testing.T, fx *fixture) {
	t.Helper()
	ctx := context.Background()
	if _, err := fx.st.AddDevice(ctx, "Old phone", "", phoneHash, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.AddDevice(ctx, "Pixel 9", "iroh:"+irohEndpoint, phoneHash, phoneLabel); err != nil {
		t.Fatal(err)
	}
}

func phoneGolden(t *testing.T, name string, rec *httptest.ResponseRecorder, secrets ...string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s = %d:\n%s", name, rec.Code, rec.Body)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, rec.Body.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	text := cursorRe.ReplaceAllString(out.String(), `"cursor": "CURSOR"`)
	for _, s := range secrets {
		text = strings.ReplaceAll(text, strings.ReplaceAll(s, "-", ""), "K7QM3XRD2P")
		text = strings.ReplaceAll(text, s, "K7QM3-XRD2P")
	}
	text += "\n"
	file := filepath.Join("testdata", "api", name+".json")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1 to write it)", err)
	}
	if text != string(want) {
		t.Fatalf("%s does not match %s:\n%s", name, file, text)
	}
}

func TestAPIPhoneGolden(t *testing.T) {
	fx, h := phoneFixture(t, &readyIroh)
	seedPhones(t, fx)
	phoneGolden(t, "phone", phoneDo(h, "GET", "/api/v1/phone", nil, "", true))
	rec := phoneDo(h, "POST", "/api/v1/phone/code", map[string]any{"base": "https://host.example"}, token, true)
	_, shown := apiBody[board.APIPhone](t, rec)
	phoneGolden(t, "phone-code", rec, shown.Code.Code)
	rec = phoneDo(h, "POST", "/api/v1/phone/app", nil, token, true)
	_, shown = apiBody[board.APIPhone](t, rec)
	phoneGolden(t, "phone-app", rec, phoneAppCodeRe.FindStringSubmatch(shown.App.Link)[1])
	fx, h = phoneFixture(t, &readyIroh)
	seedPhones(t, fx)
	phoneGolden(t, "phone-revoked", phoneDo(h, "POST", "/api/v1/phone/revoke", map[string]any{"phone": phoneLabel}, token, true))
}

func TestAPIPhoneRoutes(t *testing.T) {
	want := map[string]bool{"GET /phone": true, "POST /phone/code": true, "POST /phone/app": true, "POST /phone/revoke": true}
	for _, rt := range board.APIRoutes() {
		delete(want, rt.Method+" "+rt.Path)
	}
	if len(want) > 0 {
		t.Fatalf("the API table lacks %v", want)
	}
	_, h := phoneFixture(t, nil)
	for path, c := range map[string]struct{ method, allow string }{
		"/api/v1/phone":        {http.MethodPost, "GET, HEAD"},
		"/api/v1/phone/code":   {http.MethodPut, "POST"},
		"/api/v1/phone/app":    {http.MethodPut, "POST"},
		"/api/v1/phone/revoke": {http.MethodPut, "POST"},
	} {
		rec := phoneDo(h, c.method, path, nil, token, true)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != c.allow {
			t.Errorf("%s %s = %d, Allow %q, want 405 %q", c.method, path, rec.Code, rec.Header().Get("Allow"), c.allow)
		}
	}
	for _, path := range []string{"/api/v1/phone/code", "/api/v1/phone/app", "/api/v1/phone/revoke"} {
		if rec := phoneDo(h, "GET", path, nil, "", true); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404: an action path has no read", path, rec.Code)
		}
	}
}

func TestAPIPhoneNeedsTheSessionTheTokenAndALoopbackBoard(t *testing.T) {
	posts := map[string]any{"/api/v1/phone/code": map[string]any{"base": "https://host.example"}, "/api/v1/phone/app": nil, "/api/v1/phone/revoke": map[string]any{"phone": phoneLabel}}
	t.Run("no session", func(t *testing.T) {
		fx, h := phoneFixture(t, &readyIroh)
		seedPhones(t, fx)
		testAPIError(t, phoneDo(h, "GET", "/api/v1/phone", nil, "", false), http.StatusUnauthorized, "unauthorized")
		for path, body := range posts {
			testAPIError(t, phoneDo(h, "POST", path, body, token, false), http.StatusUnauthorized, "unauthorized")
		}
	})
	t.Run("csrf", func(t *testing.T) {
		fx, h := phoneFixture(t, &readyIroh)
		seedPhones(t, fx)
		for path, body := range posts {
			for _, csrf := range []string{"", "wrong"} {
				testAPIError(t, phoneDo(h, "POST", path, body, csrf, true), http.StatusForbidden, "csrf_required")
			}
		}
		_, shown := apiBody[board.APIPhone](t, phoneDo(h, "GET", "/api/v1/phone", nil, "", true))
		if shown.Code != nil || shown.App != nil || len(shown.Devices) != 2 {
			t.Fatalf("a refused action changed the phone data: %+v", shown)
		}
	})
	t.Run("network board", func(t *testing.T) {
		fx, h := phoneFixture(t, &readyIroh)
		seedPhones(t, fx)
		fx.options.Controls = false
		h = fx.handler()
		testAPIError(t, phoneDo(h, "GET", "/api/v1/phone", nil, "", true), http.StatusForbidden, "forbidden")
		for path, body := range posts {
			testAPIError(t, phoneDo(h, "POST", path, body, token, true), http.StatusForbidden, "forbidden")
		}
	})
	t.Run("proxy", func(t *testing.T) {
		fx, h := phoneFixture(t, &readyIroh)
		seedPhones(t, fx)
		for _, header := range []string{"X-Forwarded-For", "Forwarded"} {
			req := httptest.NewRequest("GET", "/api/v1/phone", nil)
			req.AddCookie(&http.Cookie{Name: "hand_board_" + phoneFleet, Value: token})
			req.Header.Set(header, "203.0.113.9")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			testAPIError(t, rec, http.StatusForbidden, "forbidden")
			if strings.Contains(rec.Body.String(), "Pixel") {
				t.Fatalf("a proxied request got the phones:\n%s", rec.Body)
			}
		}
	})
}

func TestAPIPhoneAPairedPhoneCannotManagePhones(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	owner := p.pair(b, nil)
	phone := p.pair(a, nil)
	rec := p.papi(a, "GET", "/session", nil, []*http.Cookie{phone})
	_, sess := apiBody[board.APISession](t, rec)
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var rd io.Reader = http.NoBody
		if body != nil {
			raw, _ := json.Marshal(body)
			rd = bytes.NewReader(raw)
		}
		req := httptest.NewRequest(method, "/"+a.id+"/api/v1"+path, rd)
		req.Header.Set("X-Hand-CSRF", sess.CSRF)
		req.AddCookie(phone)
		out := httptest.NewRecorder()
		p.h.ServeHTTP(out, req)
		return out
	}
	id := strings.SplitN(owner.Value, ".", 2)[0]
	for _, c := range []*httptest.ResponseRecorder{
		call("GET", "/phone", nil),
		call("POST", "/phone/code", map[string]any{"base": "https://host.example"}),
		call("POST", "/phone/app", nil),
		call("POST", "/phone/revoke", map[string]any{"id": 1}),
		call("POST", "/phone/revoke", map[string]any{"id": json.Number(id)}),
	} {
		testAPIError(t, c, http.StatusForbidden, "forbidden")
	}
	if n := len(p.devicesOf(a)); n != 1 {
		t.Fatalf("a phone's revoke changed the fleet's devices: %d left", n)
	}
	_, host := apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, p.hosts(a, b)))
	if host.Code != nil {
		t.Fatalf("a paired phone's refused code was minted: %+v", host.Code)
	}
}

func (p *companionHost) papi(f railFleet, method, path string, body any, cookies []*http.Cookie) *httptest.ResponseRecorder {
	p.t.Helper()
	var rd io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			p.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "/"+f.id+"/api/v1"+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost {
		req.Header.Set("X-Hand-CSRF", f.token)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	p.h.ServeHTTP(rec, req)
	return rec
}

func (p *companionHost) apiCode(f railFleet, base string, ticked ...railFleet) (string, board.APIPhone) {
	p.t.Helper()
	ids := []string{}
	for _, t := range ticked {
		ids = append(ids, t.id)
	}
	_, got := apiBody[board.APIPhone](p.t, p.papi(f, "POST", "/phone/code", map[string]any{"base": base, "fleets": ids}, p.hosts(p.fleets...)))
	if got.Code == nil {
		p.t.Fatalf("no code: %+v", got)
	}
	return got.Code.Code, got
}

func TestAPIPhoneCodeIsTheBoardsCode(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	code, shown := p.apiCode(a, "https://host.example", b, c)
	if !regexp.MustCompile(`^[0-9A-Z]{5}-[0-9A-Z]{5}$`).MatchString(code) {
		t.Fatalf("code = %q", code)
	}
	want := "https://host.example/" + a.id + "/pair"
	if shown.Code.URL != want || shown.Code.Link != want+"#"+code || shown.Code.Address != "https://host.example" || shown.Code.PlainHTTP {
		t.Fatalf("code = %+v", shown.Code)
	}
	if shown.Code.ExpiresAt != companionEpoch.Add(5*time.Minute).Format(time.RFC3339) {
		t.Fatalf("expires_at = %q", shown.Code.ExpiresAt)
	}
	ids := []string{}
	for _, f := range shown.Code.Fleets {
		ids = append(ids, f.ID)
	}
	if !slices.Equal(ids, []string{a.id, b.id, c.id}) || shown.Code.Fleets[0].Name != "alpha" {
		t.Fatalf("code fleets = %+v", shown.Code.Fleets)
	}
	selected := map[string]bool{}
	for _, f := range shown.Fleets {
		selected[f.ID] = f.Selected
	}
	if len(shown.Fleets) != 3 || shown.Fleets[0].ID != a.id || !shown.Fleets[0].Current || !selected[a.id] || !selected[b.id] || !selected[c.id] {
		t.Fatalf("fleets = %+v", shown.Fleets)
	}
	rec := p.redeem(a, code, nil, nil)
	if got := companionCookiesOf(rec); len(got) != 3 {
		t.Fatalf("the board's pair form paired %d fleets with the API's code:\n%s", len(got), rec.Body)
	}
	for _, f := range p.fleets {
		if n := len(p.devicesOf(f)); n != 1 {
			t.Errorf("%s holds %d devices", f.name, n)
		}
	}
	_, after := apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, p.hosts(a, b, c)))
	if after.Code != nil || len(after.Devices) != 1 || len(after.Devices[0].Fleets) != 3 || after.Devices[0].Phone == "" || after.Devices[0].ID != 0 {
		t.Fatalf("after the phone paired: %+v", after)
	}
	if rec := p.redeem(a, code, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("a second redeem = %d", rec.Code)
	}
}

func TestAPIPhoneANewCodeCancelsTheLastLikeTheBoards(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	first, _ := p.apiCode(a, "https://host.example")
	second, _ := p.apiCode(a, "https://other.example", b)
	if first == second {
		t.Fatal("two codes are the same")
	}
	if rec := p.redeem(a, first, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("the cancelled code redeemed = %d", rec.Code)
	}
	_, got := apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, p.hosts(a, b)))
	if got.Code == nil || got.Code.Code != second || got.Code.Address != "https://other.example" {
		t.Fatalf("the phone data shows %+v", got.Code)
	}
	page := p.send("POST", "/"+a.id+"/connect/code", url.Values{"csrf": {a.token}, "base": {"http://192.0.2.7:7777"}}, p.hosts(a, b), nil)
	pageCode := p.codeOf(page)
	_, got = apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, p.hosts(a, b)))
	if got.Code == nil || got.Code.Code != pageCode || !got.Code.PlainHTTP || got.Code.URL != "http://192.0.2.7:7777/"+a.id+"/pair" {
		t.Fatalf("a code the page showed is not the one the phone data shows: %+v", got.Code)
	}
}

func TestAPIPhoneCodeRefusals(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	p.setGone(b)
	cookies := p.hosts(a, b)
	for name, c := range map[string]struct {
		body   string
		status int
		code   string
	}{
		"no address":     {`{}`, 400, "bad_request"},
		"not a url":      {`{"base": "host.example"}`, 400, "bad_request"},
		"wrong scheme":   {`{"base": "ftp://host.example"}`, 400, "bad_request"},
		"has a path":     {`{"base": "https://host.example/x"}`, 400, "bad_request"},
		"has a login":    {`{"base": "https://me@host.example"}`, 400, "bad_request"},
		"has a query":    {`{"base": "https://host.example?x=1"}`, 400, "bad_request"},
		"not an object":  {`["https://host.example"]`, 400, "bad_request"},
		"two objects":    {`{"base": "https://host.example"}{}`, 400, "bad_request"},
		"fleets a list":  {`{"base": "https://host.example", "fleets": "x"}`, 400, "bad_request"},
		"unknown fleet":  {`{"base": "https://host.example", "fleets": ["f0000000000ff"]}`, 403, "forbidden"},
		"a gone fleet":   {`{"base": "https://host.example", "fleets": ["` + b.id + `"]}`, 403, "forbidden"},
		"a malformed id": {`{"base": "https://host.example", "fleets": ["../x"]}`, 403, "forbidden"},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/"+a.id+"/api/v1/phone/code", strings.NewReader(c.body))
			req.Header.Set("X-Hand-CSRF", a.token)
			for _, ck := range cookies {
				req.AddCookie(ck)
			}
			rec := httptest.NewRecorder()
			p.h.ServeHTTP(rec, req)
			testAPIError(t, rec, c.status, c.code)
			_, got := apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, cookies))
			if got.Code != nil {
				t.Fatalf("a refused request minted a code: %+v", got.Code)
			}
		})
	}
}

func TestAPIPhoneCodeKeepsTheOwnFleetAndDeduplicates(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	_, got := p.apiCode(a, "https://host.example", a, b, b)
	if len(got.Code.Fleets) != 2 {
		t.Fatalf("fleets = %+v", got.Code.Fleets)
	}
	_, got = p.apiCode(a, "https://host.example")
	if len(got.Code.Fleets) != 1 || got.Code.Fleets[0].ID != a.id {
		t.Fatalf("a code that ticks nothing must cover its own fleet: %+v", got.Code.Fleets)
	}
}

func TestAPIPhoneCodeExpiresWithTheBoardsFiveMinutes(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	code, _ := p.apiCode(a, "https://host.example")
	read := func() board.APIPhone {
		_, got := apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, p.hosts(a)))
		return got
	}
	p.advance(5 * time.Minute)
	if got := read(); got.Code == nil || got.Code.Code != code {
		t.Fatalf("a code at its last second is gone: %+v", got.Code)
	}
	p.advance(time.Second)
	if got := read(); got.Code != nil {
		t.Fatalf("an expired code is still shown: %+v", got.Code)
	}
	rec := p.redeem(a, code, nil, nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "expired") {
		t.Fatalf("the board's pair form took the expired code = %d", rec.Code)
	}
}

func TestAPIPhoneUsedCodeLeavesThePhoneData(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	code, _ := p.apiCode(a, "https://host.example")
	if rec := p.redeem(a, code, nil, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("redeem = %d", rec.Code)
	}
	_, got := apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, p.hosts(a)))
	if got.Code != nil || len(got.Devices) != 1 {
		t.Fatalf("a spent code is still shown, or the phone is not listed: %+v", got)
	}
}

func TestAPIPhoneAppLink(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	iroh := readyIroh
	p.iroh = func() sidecar.Status { return iroh }
	p.restart()
	_, got := apiBody[board.APIPhone](t, p.papi(a, "POST", "/phone/app", nil, p.hosts(a)))
	if got.App == nil || got.Code != nil {
		t.Fatalf("app answer = %+v", got)
	}
	m := regexp.MustCompile(`^handboard://c#v=1&e=` + irohEndpoint + `&f=` + a.id + `&c=([0-9A-Z]{10})&r=https%3A%2F%2Frelay\.example$`).FindStringSubmatch(got.App.Link)
	if m == nil {
		t.Fatalf("link = %q", got.App.Link)
	}
	if got.App.ExpiresAt != companionEpoch.Add(5*time.Minute).Format(time.RFC3339) {
		t.Fatalf("expires_at = %q", got.App.ExpiresAt)
	}
	if got.Iroh == nil || got.Iroh.State != "ready" || got.Iroh.ID != irohEndpoint {
		t.Fatalf("iroh = %+v", got.Iroh)
	}
	typed := m[1][:5] + "-" + m[1][5:]
	if rec := p.redeem(a, typed, nil, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("the board's pair form refused the app's code = %d", rec.Code)
	}
	_, got = apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, p.hosts(a)))
	if got.App != nil {
		t.Fatalf("a spent app link is still shown: %+v", got.App)
	}
	if rec := p.papi(a, "POST", "/phone/app", nil, p.hosts(a)); rec.Code != http.StatusOK {
		t.Fatalf("a second app link = %d", rec.Code)
	}
	iroh = sidecar.Status{State: sidecar.Failed, Reason: "exit status 1"}
	_, got = apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, p.hosts(a)))
	if got.App != nil {
		t.Fatalf("a link is shown while the sidecar is down: %+v", got.App)
	}
}

func TestAPIPhoneAppNeedsTheSidecarReady(t *testing.T) {
	for name, st := range map[string]*sidecar.Status{
		"without iroh": nil,
		"starting":     {State: sidecar.Starting},
		"failed":       {State: sidecar.Failed, Reason: "hand-iroh not found"},
		"no relay":     {State: sidecar.Ready, ID: irohEndpoint, Relay: "none"},
		"empty relay":  {State: sidecar.Ready, ID: irohEndpoint},
	} {
		t.Run(name, func(t *testing.T) {
			_, h := phoneFixture(t, st)
			e := testAPIError(t, phoneDo(h, "POST", "/api/v1/phone/app", nil, token, true), http.StatusConflict, "conflict")
			if !strings.Contains(e.Message, "iroh") {
				t.Fatalf("message = %q", e.Message)
			}
			_, got := apiBody[board.APIPhone](t, phoneDo(h, "GET", "/api/v1/phone", nil, "", true))
			if got.App != nil || got.Code != nil {
				t.Fatalf("a refused app request minted %+v", got)
			}
		})
	}
}

func TestAPIPhoneIrohStateIsThePagesWords(t *testing.T) {
	for name, c := range map[string]struct {
		st          sidecar.Status
		state, tone string
		text        string
	}{
		"ready":    {readyIroh, "ready", "pass", "Companion access over iroh is online. The Companion can pair with this host."},
		"no relay": {sidecar.Status{State: sidecar.Ready, ID: irohEndpoint, Relay: "none"}, "no relay", "wait", "Companion access over iroh is running, but it reached no relay, so a Companion elsewhere cannot dial it."},
		"starting": {sidecar.Status{State: sidecar.Starting, Reason: "exit status 1: cannot open the endpoint"}, "starting", "wait", "Companion access over iroh is restarting after: exit status 1: cannot open the endpoint."},
		"failed":   {sidecar.Status{State: sidecar.Failed, Reason: "hand-iroh not found: it is not next to hand (/home/me/bin) or on PATH"}, "failed", "fail", "Companion access over iroh is hand-iroh not found: it is not next to hand (bin) or on PATH."},
	} {
		t.Run(name, func(t *testing.T) {
			_, h := phoneFixture(t, &c.st)
			_, got := apiBody[board.APIPhone](t, phoneDo(h, "GET", "/api/v1/phone", nil, "", true))
			if got.Iroh == nil || got.Iroh.State != c.state || got.Iroh.Tone != c.tone || got.Iroh.Text != c.text {
				t.Fatalf("iroh = %+v", got.Iroh)
			}
		})
	}
	_, h := phoneFixture(t, nil)
	if raw := phoneDo(h, "GET", "/api/v1/phone", nil, "", true).Body.String(); strings.Contains(raw, `"iroh"`) {
		t.Fatalf("a board without --iroh answers an iroh state:\n%s", raw)
	}
}

func TestAPIPhoneListsWhatThePageLists(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	code, _ := p.apiCode(a, "https://host.example", b)
	if len(companionCookiesOf(p.redeem(a, code, nil, nil))) != 2 {
		t.Fatal("the code did not pair two fleets")
	}
	if _, err := a.st.AddDevice(context.Background(), "Old phone", "", phoneHash, ""); err != nil {
		t.Fatal(err)
	}
	p.advance(time.Minute)
	_, got := apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, p.hosts(a, b, c)))
	if len(got.Devices) != 2 {
		t.Fatalf("devices = %+v", got.Devices)
	}
	pixel, old := got.Devices[0], got.Devices[1]
	if pixel.Name != "Pixel" || !labelRe.MatchString(pixel.Phone) || pixel.ID != 0 || len(pixel.Fleets) != 2 ||
		pixel.Fleets[0] != (board.APIPhoneAt{ID: a.id, Name: "alpha", Current: true}) || pixel.Fleets[1] != (board.APIPhoneAt{ID: b.id, Name: "beta"}) {
		t.Fatalf("a phone paired with two fleets = %+v", pixel)
	}
	if old.Name != "Old phone" || old.Phone != "" || old.ID == 0 || old.Fleets != nil {
		t.Fatalf("a phone paired before one code could pair several = %+v", old)
	}
	page := p.send("GET", "/"+a.id+"/connect", nil, p.hosts(a, b, c), nil).Body.String()
	for _, name := range []string{pixel.Name, old.Name, "alpha (this fleet)", "beta"} {
		if !strings.Contains(page, name) {
			t.Fatalf("the page lacks %q that the API lists", name)
		}
	}
	p.setDown(b)
	_, got = apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, p.hosts(a, b, c)))
	if !slices.Equal(got.Unreachable, []string{"beta"}) {
		t.Fatalf("unreachable = %q", got.Unreachable)
	}
	for _, d := range got.Devices {
		for _, at := range d.Fleets {
			if at.ID == b.id {
				t.Fatalf("a fleet that could not be read is listed: %+v", d)
			}
		}
	}
}

func TestAPIPhoneSeenAndExpired(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	phone := p.pair(a, nil)
	p.advance(time.Hour)
	if rec := p.papi(a, "GET", "/status", nil, []*http.Cookie{phone}); rec.Code != http.StatusOK {
		t.Fatalf("the phone's read = %d", rec.Code)
	}
	_, got := apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, p.hosts(a)))
	if len(got.Devices) != 1 || got.Devices[0].SeenAt != companionEpoch.Add(time.Hour).Format(time.RFC3339) || got.Devices[0].Expired {
		t.Fatalf("devices = %+v", got.Devices)
	}
	p.advance(91 * 24 * time.Hour)
	_, got = apiBody[board.APIPhone](t, p.papi(a, "GET", "/phone", nil, p.hosts(a)))
	if !got.Devices[0].Expired {
		t.Fatalf("a phone past 90 days is not expired: %+v", got.Devices[0])
	}
}

func pairedAcross(t *testing.T, p *companionHost, ticked ...railFleet) (string, map[string]*http.Cookie) {
	t.Helper()
	code, _ := p.apiCode(p.fleets[0], "https://host.example", ticked...)
	cookies := companionCookiesOf(p.redeem(p.fleets[0], code, nil, nil))
	if len(cookies) != len(ticked)+1 {
		t.Fatalf("paired %d fleets", len(cookies))
	}
	_, got := apiBody[board.APIPhone](t, p.papi(p.fleets[0], "GET", "/phone", nil, p.hosts(p.fleets...)))
	return got.Devices[0].Phone, cookies
}

func phoneIDs(fs []board.APIFleet) []string {
	out := []string{}
	for _, f := range fs {
		out = append(out, f.ID)
	}
	return out
}

func TestAPIPhoneRevokeInOneFleetAndEverywhere(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	label, cookies := pairedAcross(t, p, b, c)
	all := p.hosts(a, b, c)
	rec := p.papi(a, "POST", "/phone/revoke", map[string]any{"phone": label, "fleet": b.id}, all)
	env, got := apiBody[board.APIPhone](t, rec)
	if got.Revoke == nil || !slices.Equal(phoneIDs(got.Revoke.Revoked), []string{b.id}) || len(got.Revoke.Failed) != 0 {
		t.Fatalf("revoke = %+v", got.Revoke)
	}
	if env.Receipt != "Revoked in beta" || env.Warn != "" || rec.Header().Get("X-Hand-Receipt") != "Revoked%20in%20beta" {
		t.Fatalf("receipt %q warn %q", env.Receipt, env.Warn)
	}
	for f, want := range map[railFleet]int{a: 1, b: 0, c: 1} {
		if n := len(p.devicesOf(f)); n != want {
			t.Errorf("%s holds %d devices, want %d", f.name, n, want)
		}
	}
	if code := p.opens(b, nil, cookies[b.id]); code == http.StatusOK {
		t.Fatalf("a revoked phone still opens beta = %d", code)
	}
	if code := p.opens(a, nil, cookies[a.id]); code != http.StatusOK {
		t.Fatalf("a phone revoked in beta no longer opens alpha = %d", code)
	}
	if len(got.Devices) != 1 || len(got.Devices[0].Fleets) != 2 {
		t.Fatalf("the answer lists %+v", got.Devices)
	}
	rec = p.papi(a, "POST", "/phone/revoke", map[string]any{"phone": label}, all)
	env, got = apiBody[board.APIPhone](t, rec)
	if !slices.Equal(phoneIDs(got.Revoke.Revoked), []string{a.id, c.id}) || len(got.Revoke.Failed) != 0 || env.Receipt != "Revoked in alpha and gamma" {
		t.Fatalf("revoke everywhere = %+v, receipt %q", got.Revoke, env.Receipt)
	}
	for _, f := range p.fleets {
		if n := len(p.devicesOf(f)); n != 0 {
			t.Errorf("%s still holds %d devices", f.name, n)
		}
	}
	if len(got.Devices) != 0 {
		t.Fatalf("the answer still lists %+v", got.Devices)
	}
	rec = p.papi(a, "POST", "/phone/revoke", map[string]any{"phone": label}, all)
	env, got = apiBody[board.APIPhone](t, rec)
	if len(got.Revoke.Revoked) != 0 || len(got.Revoke.Failed) != 0 || env.Receipt != "" || env.Warn != "" {
		t.Fatalf("revoking a phone that is gone reports %+v, receipt %q, warn %q", got.Revoke, env.Receipt, env.Warn)
	}
}

func TestAPIPhoneRevokeReportsAPartialFailureWithoutAHostPath(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	label, _ := pairedAcross(t, p, b, c)
	p.mu.Lock()
	p.failWith = map[string]error{c.id: errors.New("open /home/me/.secondhand/gamma/hand.db: permission denied")}
	p.mu.Unlock()
	p.restart()
	all := p.hosts(a, b, c)
	rec := p.papi(a, "POST", "/phone/revoke", map[string]any{"phone": label}, all)
	env, got := apiBody[board.APIPhone](t, rec)
	if got.Revoke == nil || !slices.Equal(phoneIDs(got.Revoke.Revoked), []string{a.id, b.id}) {
		t.Fatalf("revoked = %+v", got.Revoke)
	}
	if len(got.Revoke.Failed) != 1 || got.Revoke.Failed[0] != (board.APIPhoneFailed{ID: c.id, Name: "gamma", Reason: "the fleet did not answer"}) {
		t.Fatalf("failed = %+v", got.Revoke.Failed)
	}
	const warn = "Revoked in alpha and beta. gamma could not be reached: its record of this Companion, if it has one, is still active. Press Revoke everywhere again when it is back."
	if env.Receipt != "" || env.Warn != warn || rec.Header().Get("X-Hand-Warn") != url.PathEscape(warn) {
		t.Fatalf("receipt %q warn %q", env.Receipt, env.Warn)
	}
	body := rec.Body.String() + fmt.Sprint(rec.Header())
	for _, leak := range []string{"permission denied", "/home/me", ".secondhand", "hand.db", "/homes/", p.paths[c.id], p.paths[a.id]} {
		if strings.Contains(body, leak) {
			t.Fatalf("the answer carries %q:\n%s", leak, body)
		}
	}
	if n := len(p.devicesOf(c)); n != 1 {
		t.Fatalf("the fleet that failed holds %d devices, want its record kept", n)
	}
	if !slices.Equal(got.Unreachable, []string{"gamma"}) {
		t.Fatalf("unreachable = %q", got.Unreachable)
	}
	p.mu.Lock()
	p.failWith = nil
	p.mu.Unlock()
	p.restart()
	env, got = apiBody[board.APIPhone](t, p.papi(a, "POST", "/phone/revoke", map[string]any{"phone": label}, all))
	if !slices.Equal(phoneIDs(got.Revoke.Revoked), []string{c.id}) || len(got.Revoke.Failed) != 0 || env.Receipt != "Revoked in gamma" {
		t.Fatalf("the retry = %+v, receipt %q", got.Revoke, env.Receipt)
	}
}

func TestAPIPhoneRevokeThatReachesNoFleetSaysNothingWasRevoked(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	label, _ := pairedAcross(t, p, b)
	p.setGone(b)
	p.mu.Lock()
	p.failWith = map[string]error{b.id: errors.New("fleet is down")}
	p.gone = map[string]bool{}
	p.mu.Unlock()
	p.restart()
	rec := p.papi(a, "POST", "/phone/revoke", map[string]any{"phone": label, "fleet": b.id}, p.hosts(a, b))
	env, got := apiBody[board.APIPhone](t, rec)
	if len(got.Revoke.Revoked) != 0 || len(got.Revoke.Failed) != 1 || env.Receipt != "" {
		t.Fatalf("revoke = %+v, receipt %q", got.Revoke, env.Receipt)
	}
	if !strings.HasPrefix(env.Warn, "Nothing was revoked. beta could not be reached") {
		t.Fatalf("warn = %q", env.Warn)
	}
	if n := len(p.devicesOf(b)); n != 1 {
		t.Fatalf("beta holds %d devices", n)
	}
}

func TestAPIPhoneRevokeLeavesAFleetTheSessionDoesNotProve(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	label, _ := pairedAcross(t, p, b, c)
	_, got := apiBody[board.APIPhone](t, p.papi(a, "POST", "/phone/revoke", map[string]any{"phone": label}, p.hosts(a, b)))
	if !slices.Equal(phoneIDs(got.Revoke.Revoked), []string{a.id, b.id}) || len(got.Revoke.Failed) != 0 {
		t.Fatalf("revoke = %+v", got.Revoke)
	}
	if n := len(p.devicesOf(c)); n != 1 {
		t.Fatalf("a fleet this session does not prove lost its device: %d", n)
	}
	testAPIError(t, p.papi(a, "POST", "/phone/revoke", map[string]any{"phone": label, "fleet": c.id}, p.hosts(a, b)), http.StatusForbidden, "forbidden")
	testAPIError(t, p.papi(a, "POST", "/phone/revoke", map[string]any{"phone": label, "fleet": "f0000000000ff"}, p.hosts(a, b, c)), http.StatusForbidden, "forbidden")
	if n := len(p.devicesOf(c)); n != 1 {
		t.Fatalf("a refused revoke removed a device: %d", n)
	}
}

func TestAPIPhoneRevokeByID(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	old, err := a.st.AddDevice(context.Background(), "Old phone", "", phoneHash, "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := b.st.AddDevice(context.Background(), "Other", "", phoneHash, "")
	if err != nil {
		t.Fatal(err)
	}
	all := p.hosts(a, b)
	env, got := apiBody[board.APIPhone](t, p.papi(a, "POST", "/phone/revoke", map[string]any{"id": old.ID}, all))
	if !slices.Equal(phoneIDs(got.Revoke.Revoked), []string{a.id}) || env.Receipt != "Revoked in alpha" || len(got.Devices) != 0 {
		t.Fatalf("revoke = %+v, receipt %q, devices %+v", got.Revoke, env.Receipt, got.Devices)
	}
	if n := len(p.devicesOf(a)); n != 0 {
		t.Fatalf("alpha holds %d devices", n)
	}
	if other.ID != old.ID {
		t.Fatalf("the two fleets' first devices have ids %d and %d", old.ID, other.ID)
	}
	_, got = apiBody[board.APIPhone](t, p.papi(a, "POST", "/phone/revoke", map[string]any{"id": other.ID}, all))
	if len(got.Revoke.Revoked) != 0 || len(p.devicesOf(b)) != 1 {
		t.Fatalf("an id is this fleet's own row, never another fleet's: %+v, beta holds %d", got.Revoke, len(p.devicesOf(b)))
	}
	_, got = apiBody[board.APIPhone](t, p.papi(a, "POST", "/phone/revoke", map[string]any{"id": 4096}, all))
	if len(got.Revoke.Revoked) != 0 || len(got.Revoke.Failed) != 0 {
		t.Fatalf("an unknown id = %+v", got.Revoke)
	}
}

func TestAPIPhoneRevokeRefusals(t *testing.T) {
	p := newCompanionHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	label, _ := pairedAcross(t, p, b)
	all := p.hosts(a, b)
	for name, body := range map[string]string{
		"empty":             `{}`,
		"not an object":     `[]`,
		"two objects":       `{"phone": "` + label + `"}{}`,
		"short label":       `{"phone": "abc"}`,
		"upper case label":  `{"phone": "0123456789ABCDEF"}`,
		"label and id":      `{"phone": "` + label + `", "id": 1}`,
		"id and fleet":      `{"id": 1, "fleet": "` + a.id + `"}`,
		"fleet alone":       `{"fleet": "` + a.id + `"}`,
		"negative id":       `{"id": -1}`,
		"zero id":           `{"id": 0}`,
		"id a word":         `{"id": "one"}`,
		"phone not text":    `{"phone": 7}`,
		"label with a path": `{"phone": "../../../x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/"+a.id+"/api/v1/phone/revoke", strings.NewReader(body))
			req.Header.Set("X-Hand-CSRF", a.token)
			for _, c := range all {
				req.AddCookie(c)
			}
			rec := httptest.NewRecorder()
			p.h.ServeHTTP(rec, req)
			testAPIError(t, rec, http.StatusBadRequest, "bad_request")
			for _, f := range p.fleets {
				if n := len(p.devicesOf(f)); n != 1 {
					t.Fatalf("a refused revoke left %s with %d devices", f.name, n)
				}
			}
		})
	}
}

func TestAPIPhoneCursor(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	read := func(since string) *httptest.ResponseRecorder {
		path := "/phone"
		if since != "" {
			path += "?since=" + since
		}
		return p.papi(a, "GET", path, nil, p.hosts(a))
	}
	first := read("")
	cursor := first.Header().Get("X-Hand-Cursor")
	if cursor == "" {
		t.Fatal("no cursor")
	}
	if env, _ := apiBody[board.APIPhone](t, first); env.Cursor != cursor {
		t.Fatalf("envelope cursor %q, header %q", env.Cursor, cursor)
	}
	if rec := read(cursor); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 || rec.Header().Get("X-Hand-Cursor") != cursor {
		t.Fatalf("a poll with the current cursor = %d %q", rec.Code, rec.Body)
	}
	code1, _ := p.apiCode(a, "https://host.example")
	rec := read(cursor)
	cursor1 := rec.Header().Get("X-Hand-Cursor")
	if rec.Code != http.StatusOK || cursor1 == cursor {
		t.Fatalf("a new code did not move the cursor: %d", rec.Code)
	}
	code2, _ := p.apiCode(a, "https://host.example")
	rec = read(cursor1)
	cursor2 := rec.Header().Get("X-Hand-Cursor")
	if rec.Code != http.StatusOK || cursor2 == cursor1 || !strings.Contains(rec.Body.String(), code2) {
		t.Fatalf("a second code with the same expiry did not move the cursor: %d", rec.Code)
	}
	for _, c := range []string{cursor1, cursor2} {
		for _, code := range []string{code1, code2, strings.ReplaceAll(code1, "-", ""), strings.ReplaceAll(code2, "-", "")} {
			if strings.Contains(c, code) {
				t.Fatalf("cursor %q carries a code", c)
			}
		}
	}
	if rec := read(cursor2); rec.Code != http.StatusNotModified {
		t.Fatalf("a poll while the code shows = %d", rec.Code)
	}
	p.advance(6 * time.Minute)
	if rec := read(cursor2); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), code2) {
		t.Fatalf("a poll after the code expired = %d:\n%s", rec.Code, rec.Body)
	}
	testAPIError(t, read("junk"), http.StatusBadRequest, "bad_cursor")
	post := p.papi(a, "POST", "/phone/code?since="+cursor2, map[string]any{"base": "https://host.example"}, p.hosts(a))
	if post.Code != http.StatusOK {
		t.Fatalf("a mutation with ?since= = %d", post.Code)
	}
}

func TestAPIPhoneCursorMovesWithADevice(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	cursor := p.papi(a, "GET", "/phone", nil, p.hosts(a)).Header().Get("X-Hand-Cursor")
	p.pair(a, nil)
	rec := p.papi(a, "GET", "/phone?since="+cursor, nil, p.hosts(a))
	if rec.Code != http.StatusOK || rec.Header().Get("X-Hand-Cursor") == cursor {
		t.Fatalf("a paired phone did not move the cursor: %d", rec.Code)
	}
}

func scanFor(t *testing.T, what string, text string, secrets []string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(text, s) {
			t.Errorf("%s carries the pairing credential %q:\n%.600s", what, s, text)
		}
	}
}

func scanResponse(t *testing.T, what string, rec *httptest.ResponseRecorder, secrets []string) {
	t.Helper()
	scanFor(t, what+" body", rec.Body.String(), secrets)
	for k, vs := range rec.Header() {
		scanFor(t, what+" header "+k, strings.Join(vs, "\n"), secrets)
		if u, err := url.PathUnescape(strings.Join(vs, "\n")); err == nil {
			scanFor(t, what+" header "+k, u, secrets)
		}
	}
}

func withoutOwnFields(t *testing.T, raw []byte) string {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	data, _ := root["data"].(map[string]any)
	for _, k := range []string{"code", "app"} {
		if m, ok := data[k].(map[string]any); ok {
			delete(m, "code")
			delete(m, "link")
		}
	}
	out, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func secretsOf(code string) []string {
	return []string{code, strings.ReplaceAll(code, "-", "")}
}

func TestAPINeverReturnsAPairingCodeOutsideItsOwnFields(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	t.Run("one fleet", func(t *testing.T) {
		fx := apiFixture(t, func() time.Time { return frozen })
		fx.options.Beat = 20 * time.Millisecond
		h := fx.handler()
		rec := apiPost(t, h, "/api/v1/phone/code", map[string]any{"base": "https://host.example"}, token, true)
		_, shown := apiBody[board.APIPhone](t, rec)
		secrets := secretsOf(shown.Code.Code)
		if !strings.Contains(shown.Code.Link, shown.Code.Code) {
			t.Fatalf("link = %q", shown.Code.Link)
		}
		scanFor(t, "the answer's own fields", withoutOwnFields(t, rec.Body.Bytes()), secrets)
		scanResponse(t, "POST /phone/code, its headers", &httptest.ResponseRecorder{Body: new(bytes.Buffer), HeaderMap: rec.Header()}, secrets)
		get := request(h, "GET", "/api/v1/phone", nil, true)
		scanFor(t, "GET /phone", withoutOwnFields(t, get.Body.Bytes()), secrets)
		scanResponse(t, "GET /phone, its headers", &httptest.ResponseRecorder{Body: new(bytes.Buffer), HeaderMap: get.Header()}, secrets)
		if !strings.Contains(get.Body.String(), shown.Code.Code) {
			t.Fatal("GET /phone does not show the code, so the scan below proves nothing")
		}
		paths := []string{"/api/v1", "/api/v1/status", "/api/v1/models", "/api/v1/timeline", "/api/v1/tasks", "/api/v1/tasks/search", "/api/v1/tasks/search?q=cookie", "/api/v1/projects", "/api/v1/history/search", "/api/v1/history/search?q=cookie", "/api/v1/history/r1", "/api/v1/links", "/api/v1/links?path=reports/x",
			"/api/v1/task/t1", "/api/v1/attempt/a1", "/api/v1/decision/d1", "/api/v1/report/r1", "/api/v1/session", "/api/v1/doc/reports", "/api/v1/inbox/x",
			"/", "/connect", "/alerts", "/alerts/feed", "/tasks", "/supervisor", "/task/t1", "/attempt/a1", "/decision/d1"}
		for _, rt := range board.APIRoutes() {
			if rt.Method == "GET" && !rt.Fallback && rt.Path != "/phone" && rt.Path != "/events" && !slices.Contains(paths, "/api/v1"+rt.Path) && !strings.Contains(rt.Path, "{") {
				t.Errorf("GET /api/v1%s is not in the credential scan", rt.Path)
			}
		}
		for _, path := range paths {
			scanResponse(t, "GET "+path, request(h, "GET", path, nil, true), secrets)
		}
		for name, c := range map[string]struct {
			path string
			body any
			csrf string
		}{
			"a refused address":  {"/api/v1/phone/code", map[string]any{"base": "nope"}, token},
			"a refused fleet":    {"/api/v1/phone/code", map[string]any{"base": "https://host.example", "fleets": []string{"f0000000000ff"}}, token},
			"a missing token":    {"/api/v1/phone/code", map[string]any{"base": "https://host.example"}, ""},
			"an app refusal":     {"/api/v1/phone/app", nil, token},
			"a refused revoke":   {"/api/v1/phone/revoke", map[string]any{"phone": "x"}, token},
			"an unknown revoke":  {"/api/v1/phone/revoke", map[string]any{"id": 77}, token},
			"another action":     {"/api/v1/message", map[string]any{"text": "hello"}, token},
			"a wrong route body": {"/api/v1/report/r1/ack", nil, token},
		} {
			rec := apiPost(t, h, c.path, c.body, c.csrf, true)
			if strings.HasPrefix(c.path, "/api/v1/phone") && rec.Code == http.StatusOK {
				scanFor(t, name+" body", withoutOwnFields(t, rec.Body.Bytes()), secrets)
				rec.Body.Reset()
			}
			scanResponse(t, name, rec, secrets)
		}
		ch, _ := stream(t, serve(t, board.New(fx.st, token, fx.options))+eventsPath)
		for _, e := range first(t, ch, 6) {
			scanFor(t, "the stream's "+e.name+" event", e.name+e.data, secrets)
		}
		events, err := fx.st.RecentEvents(context.Background(), 1000)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			scanFor(t, "event "+e.Kind, e.Kind+" "+e.Detail, secrets)
		}
		scanFor(t, "the log", logs.String(), secrets)
	})
	t.Run("a host with iroh", func(t *testing.T) {
		p := newCompanionHost(t, true, "alpha", "beta")
		a, b := p.fleets[0], p.fleets[1]
		p.iroh = func() sidecar.Status { return readyIroh }
		p.restart()
		all := p.hosts(a, b)
		_, web := apiBody[board.APIPhone](t, p.papi(a, "POST", "/phone/code", map[string]any{"base": "https://host.example", "fleets": []string{b.id}}, all))
		secrets := secretsOf(web.Code.Code)
		scan := func(rounds string) {
			t.Helper()
			for _, f := range p.fleets {
				for _, path := range []string{"/status", "/timeline", "/tasks", "/models", "/session", "/events-not-streamed"} {
					scanResponse(t, rounds+" "+f.name+path, p.papi(f, "GET", path, nil, all), secrets)
				}
				for _, page := range []string{"/", "/connect", "/alerts", "/alerts/feed", "/tasks"} {
					scanResponse(t, rounds+" "+f.name+" page "+page, p.send("GET", "/"+f.id+page, nil, all, nil), secrets)
				}
				for _, suffix := range []string{"", "-wal", "-shm"} {
					raw, err := os.ReadFile(p.paths[f.id] + suffix)
					if err != nil {
						continue
					}
					if bytes.Contains(raw, []byte(secrets[0])) || bytes.Contains(raw, []byte(secrets[1])) {
						t.Errorf("%s: %s%s holds the code", rounds, filepath.Base(p.paths[f.id]), suffix)
					}
				}
				evs, err := f.st.RecentEvents(context.Background(), 1000)
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range evs {
					scanFor(t, rounds+" event "+e.Kind, e.Kind+" "+e.Detail, secrets)
				}
			}
			for _, path := range []string{"/api/v1/fleets", "/api/v1/meta"} {
				scanResponse(t, rounds+" "+path, p.send("GET", path, nil, all, nil), secrets)
			}
		}
		scan("web code")
		_, app := apiBody[board.APIPhone](t, p.papi(a, "POST", "/phone/app", nil, all))
		secrets = append(secrets, app.App.Link, phoneAppCodeRe.FindStringSubmatch(app.App.Link)[1])
		scan("app link")
		scanFor(t, "the log", logs.String(), secrets)
		rec := p.papi(a, "GET", "/phone", nil, all)
		scanFor(t, "GET /phone", withoutOwnFields(t, rec.Body.Bytes()), secrets)
	})
}

func TestThePhoneDecodesThePhoneAnswersWithoutAChange(t *testing.T) {
	envelope := kotlinParamsIn(t, "BoardClient.kt", "class Envelope<T>(")
	var required []string
	for _, p := range envelope {
		if p = strings.TrimSpace(p); p != "" && !strings.Contains(p, " = ") {
			required = append(required, kotlinValName.FindStringSubmatch(p)[1])
		}
	}
	status := phoneRequired(t, "Status")
	for _, name := range []string{"phone", "phone-code", "phone-app", "phone-revoked"} {
		raw, err := os.ReadFile(filepath.Join("testdata", "api", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var root map[string]json.RawMessage
		if err := json.Unmarshal(raw, &root); err != nil {
			t.Fatal(err)
		}
		for _, k := range required {
			if _, ok := root[k]; !ok {
				t.Errorf("%s has no %s, which the phone's Envelope requires", name, k)
			}
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(root["data"], &data); err != nil {
			t.Fatalf("%s: data is not one object: %v", name, err)
		}
		for _, k := range status {
			if _, ok := data[k]; ok {
				t.Errorf("%s carries %s, a field of the phone's Status", name, k)
			}
		}
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "api", "message-sent.json"))
	if err != nil {
		t.Fatal(err)
	}
	phone, err := os.ReadFile(filepath.Join("..", "..", "android", "src-tauri", "gen", "android", "app", "src", "test", "resources", "api", "message-sent.json"))
	if err != nil || !bytes.Equal(golden, phone) {
		t.Fatalf("the phone's copy of message-sent.json differs from the golden (%v)", err)
	}
}

func TestAPIDocsNameThePhoneRoutes(t *testing.T) {
	doc, err := os.ReadFile("../../docs/api.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, rt := range []string{"GET /api/v1/phone", "POST /api/v1/phone/code", "POST /api/v1/phone/app", "POST /api/v1/phone/revoke"} {
		if !strings.Contains(string(doc), "| `"+rt+"` |") {
			t.Errorf("docs/api.md has no row for %s", rt)
		}
	}
}

func TestAPIPhoneCursorIsNotAPlainHashOfTheCode(t *testing.T) {
	p := newCompanionHost(t, true, "alpha")
	a := p.fleets[0]
	p.apiCode(a, "https://host.example")
	rec := p.papi(a, "GET", "/phone", nil, p.hosts(a))
	env, _ := apiBody[board.APIPhone](t, rec)
	head, _, _ := strings.Cut(env.Cursor, ".")
	payload, err := base64.RawURLEncoding.DecodeString(head)
	if err != nil {
		t.Fatal(err)
	}
	_, sum, _ := strings.Cut(string(payload), "\x00")
	var raw struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{string(raw.Data), "alpha\x00" + string(raw.Data)} {
		h := fnv.New64a()
		_, _ = h.Write([]byte(plain))
		if sum == strconv.FormatUint(h.Sum64(), 16) {
			t.Fatalf("the cursor is a plain hash of the answer that holds the code: %s", sum)
		}
	}
}

func TestAPIPhoneListsAndRevokesEveryFleetButACodeNamesEight(t *testing.T) {
	p := newCompanionHost(t, true, "a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "ninthfleet")
	home := p.fleets[0]
	for _, f := range p.fleets {
		if _, err := f.st.AddDevice(context.Background(), "Pixel", "", phoneHash, phoneLabel); err != nil {
			t.Fatal(err)
		}
	}
	all := p.hosts(p.fleets...)
	_, got := apiBody[board.APIPhone](t, p.papi(home, "GET", "/phone", nil, all))
	if len(got.Devices) != 1 || len(got.Devices[0].Fleets) != 9 || len(got.Unreachable) != 0 {
		t.Fatalf("phone = %d devices, %+v, unreachable %v", len(got.Devices), got.Devices, got.Unreachable)
	}
	if page := p.phonePage(home, all...); !strings.Contains(page, "ninthfleet") || len(checkboxRe.FindAllString(page, -1)) != 8 {
		t.Fatalf("the page does not list the ninth fleet's phone, or offers a code more than this fleet and 7 others")
	}
	if len(got.Fleets) != 8 || !got.Fleets[0].Current {
		t.Fatalf("a code is offered %d fleets, want this one and 7 others: %+v", len(got.Fleets), got.Fleets)
	}
	for _, c := range []struct {
		ticked []railFleet
		status int
	}{{p.fleets[1:], http.StatusForbidden}, {p.fleets[1:8], http.StatusOK}, {p.fleets[8:], http.StatusForbidden}} {
		ids := []string{}
		for _, f := range c.ticked {
			ids = append(ids, f.id)
		}
		rec := p.papi(home, "POST", "/phone/code", map[string]any{"base": "https://host.example", "fleets": ids}, all)
		if rec.Code != c.status {
			t.Fatalf("a code for %d other fleets = %d, want %d:\n%s", len(c.ticked), rec.Code, c.status, rec.Body)
		}
		if c.status == http.StatusForbidden {
			testAPIError(t, rec, http.StatusForbidden, "forbidden")
			continue
		}
		_, shown := apiBody[board.APIPhone](t, rec)
		if shown.Code == nil || len(shown.Code.Fleets) != 8 {
			t.Fatalf("code = %+v", shown.Code)
		}
	}
	_, got = apiBody[board.APIPhone](t, p.papi(home, "POST", "/phone/revoke", map[string]any{"phone": phoneLabel}, all))
	if len(got.Revoke.Revoked) != 9 || len(got.Revoke.Failed) != 0 {
		t.Fatalf("revoke = %+v", got.Revoke)
	}
	for _, f := range p.fleets {
		if n := len(p.devicesOf(f)); n != 0 {
			t.Errorf("%s still holds %d devices", f.name, n)
		}
	}
}

func phoneBrokenDB(t *testing.T) (http.Handler, *state.Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hand.db")
	st, err := state.Open(path, func() time.Time { return frozen })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO fleet(only, id, name) VALUES (1, ?, 'golden')`, phoneFleet); err != nil {
		t.Fatal(err)
	}
	fx := &fixture{st: st, options: board.Options{Controls: true, Now: func() time.Time { return frozen }, Base: "/" + phoneFleet, Tick: apiTick, Iroh: func() sidecar.Status { return readyIroh }}}
	return fx.handler(), st, db
}

func TestAPIPhoneAnActionThatCannotBeAnsweredChangesNothing(t *testing.T) {
	h, _, db := phoneBrokenDB(t)
	body := map[string]any{"base": "https://host.example"}
	_, first := apiBody[board.APIPhone](t, phoneDo(h, "POST", "/api/v1/phone/code", body, token, true))
	if first.Code == nil {
		t.Fatalf("no first code: %+v", first)
	}
	if _, err := db.Exec(`ALTER TABLE device RENAME TO device_away`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		body any
	}{{"/phone/code", body}, {"/phone/app", nil}} {
		testAPIError(t, phoneDo(h, "POST", "/api/v1"+c.path, c.body, token, true), http.StatusInternalServerError, "internal")
	}
	if _, err := db.Exec(`ALTER TABLE device_away RENAME TO device`); err != nil {
		t.Fatal(err)
	}
	_, got := apiBody[board.APIPhone](t, phoneDo(h, "GET", "/api/v1/phone", nil, "", true))
	if got.Code == nil || got.Code.Code != first.Code.Code || got.App != nil {
		t.Fatalf("a failed action changed the code: %+v app %+v", got.Code, got.App)
	}
}

func TestAPIPhoneARevokeByIDThatCannotBeAnsweredRevokesNothing(t *testing.T) {
	h, st, db := phoneBrokenDB(t)
	d, err := st.AddDevice(context.Background(), "Old phone", "", phoneHash, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE fleet RENAME TO fleet_away`); err != nil {
		t.Fatal(err)
	}
	testAPIError(t, phoneDo(h, "POST", "/api/v1/phone/revoke", map[string]any{"id": d.ID}, token, true), http.StatusInternalServerError, "internal")
	if _, err := db.Exec(`ALTER TABLE fleet_away RENAME TO fleet`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM device`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("a refused revoke left %d devices, err %v, want 1", n, err)
	}
}
