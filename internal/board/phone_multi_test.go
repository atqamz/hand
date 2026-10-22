package board_test

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

var (
	labelRe    = regexp.MustCompile(`^[0-9a-f]{16}$`)
	checkboxRe = regexp.MustCompile(`<input type="checkbox" name="fleet" value="([^"]+)"`)
)

func (p *phoneHost) setDown(f railFleet) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down[f.id] = true
}

func (p *phoneHost) setGone(f railFleet) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gone[f.id] = true
}

func (p *phoneHost) laptops(fleets ...railFleet) []*http.Cookie {
	var out []*http.Cookie
	for _, f := range fleets {
		out = append(out, p.laptop(f))
	}
	return out
}

func (p *phoneHost) mint(f railFleet, cookies []*http.Cookie, ticked ...railFleet) *httptest.ResponseRecorder {
	p.t.Helper()
	form := url.Values{"csrf": {f.token}, "base": {"https://laptop.example"}}
	for _, t := range ticked {
		form.Add("fleet", t.id)
	}
	return p.send("POST", "/"+f.id+"/connect/code", form, cookies, nil)
}

func (p *phoneHost) codeOf(rec *httptest.ResponseRecorder) string {
	p.t.Helper()
	m := codeRe.FindStringSubmatch(rec.Body.String())
	if rec.Code != http.StatusOK || m == nil {
		p.t.Fatalf("no code = %d:\n%s", rec.Code, rec.Body.String())
	}
	return m[1]
}

func (p *phoneHost) redeem(f railFleet, code string, extra url.Values, headers map[string]string) *httptest.ResponseRecorder {
	p.t.Helper()
	form := url.Values{"code": {code}, "name": {"Pixel"}}
	for k, v := range extra {
		form[k] = v
	}
	return p.send("POST", "/"+f.id+"/pair", form, nil, headers)
}

func phoneCookiesOf(rec *httptest.ResponseRecorder) map[string]*http.Cookie {
	out := map[string]*http.Cookie{}
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge <= 0 {
			continue
		}
		name := strings.TrimPrefix(c.Name, "__Host-")
		if id, ok := strings.CutPrefix(name, "hand_phone_"); ok {
			out[id] = c
		}
	}
	return out
}

func (p *phoneHost) devicesOf(f railFleet) []state.Device {
	p.t.Helper()
	list, err := f.st.Devices(context.Background())
	if err != nil {
		p.t.Fatal(err)
	}
	return list
}

func (p *phoneHost) opens(f railFleet, headers map[string]string, cookies ...*http.Cookie) int {
	return p.send("GET", "/"+f.id+"/", nil, cookies, headers).Code
}

func (p *phoneHost) agree(rec *httptest.ResponseRecorder, op map[string]string) {
	p.t.Helper()
	cookies := phoneCookiesOf(rec)
	for _, f := range p.fleets {
		devices := p.devicesOf(f)
		c := cookies[f.id]
		if (len(devices) > 0) != (c != nil) {
			p.t.Fatalf("fleet %s holds %d records and the response sent cookie %v", f.name, len(devices), c != nil)
		}
		if c == nil {
			continue
		}
		if len(devices) != 1 || !strings.HasPrefix(c.Value, fmt.Sprint(devices[0].ID)+".") {
			p.t.Fatalf("fleet %s: cookie %q does not name its record %+v", f.name, c.Value, devices)
		}
		if got := p.opens(f, op, c); got != http.StatusOK {
			p.t.Fatalf("fleet %s: the cookie it was sent opens %d", f.name, got)
		}
	}
}

func fleetsAt(p *phoneHost, idx ...int) []railFleet {
	var out []railFleet
	for _, i := range idx {
		out = append(out, p.fleets[i])
	}
	return out
}

func TestOneCodePairsAPhoneWithTheFleetsTheLaptopChose(t *testing.T) {
	op := serving("operator@example.com")
	cases := []struct {
		what    string
		laptops []int
		ticked  []int
		down    []int
		gone    []int
		status  int
		paired  []int
		missed  string
		said    string
	}{
		{"one fleet", []int{0}, nil, nil, nil, http.StatusSeeOther, []int{0}, "", ""},
		{"the laptop is proven for more but ticked none", []int{0, 1, 2}, nil, nil, nil, http.StatusSeeOther, []int{0}, "", ""},
		{"two fleets", []int{0, 1}, []int{1}, nil, nil, http.StatusOK, []int{0, 1}, "", "Paired with alpha and beta."},
		{"three fleets", []int{0, 1, 2}, []int{1, 2}, nil, nil, http.StatusOK, []int{0, 1, 2}, "", "Paired with alpha, beta and gamma."},
		{"one of three unticked", []int{0, 1, 2}, []int{2}, nil, nil, http.StatusOK, []int{0, 2}, "", "Paired with alpha and gamma."},
		{"one fleet down at redeem", []int{0, 1, 2}, []int{1, 2}, []int{1}, nil, http.StatusOK, []int{0, 2}, "1 fleet was not reachable. Pair it from its own Phone page on your laptop.", "Paired with alpha and gamma."},
		{"one fleet removed after the code was shown", []int{0, 1, 2}, []int{1, 2}, nil, []int{2}, http.StatusOK, []int{0, 1}, "1 fleet was not reachable. Pair it from its own Phone page on your laptop.", "Paired with alpha and beta."},
		{"every other fleet unreachable", []int{0, 1, 2}, []int{1, 2}, []int{1}, []int{2}, http.StatusOK, []int{0}, "2 fleets were not reachable. Pair them from their own Phone page on your laptop.", "Paired with alpha."},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			p := newPhoneHost(t, true, "alpha", "beta", "gamma", "delta")
			a := p.fleets[0]
			code := p.codeOf(p.mint(a, p.laptops(fleetsAt(p, c.laptops...)...), fleetsAt(p, c.ticked...)...))
			for _, i := range c.down {
				p.setDown(p.fleets[i])
			}
			for _, i := range c.gone {
				p.setGone(p.fleets[i])
			}
			rec := p.redeem(a, code, nil, op)
			if rec.Code != c.status {
				t.Fatalf("redeem = %d:\n%s", rec.Code, rec.Body.String())
			}
			cookies := phoneCookiesOf(rec)
			if len(cookies) != len(c.paired) {
				t.Fatalf("cookies for %d fleets, want %d", len(cookies), len(c.paired))
			}
			var label string
			for _, i := range c.paired {
				f := p.fleets[i]
				devices := p.devicesOf(f)
				if len(devices) != 1 || devices[0].Name != "Pixel" || devices[0].Login != "operator@example.com" || !labelRe.MatchString(devices[0].Phone) {
					t.Fatalf("fleet %s holds %+v", f.name, devices)
				}
				if label != "" && devices[0].Phone != label {
					t.Fatalf("the records of one pairing carry two labels: %q and %q", label, devices[0].Phone)
				}
				label = devices[0].Phone
				if got := p.opens(f, op, cookies[f.id]); got != http.StatusOK {
					t.Fatalf("fleet %s opens %d with its own cookie", f.name, got)
				}
				events, err := f.st.EventsAfter(context.Background(), 0, []string{"board.paired"}, 10)
				if err != nil || len(events) != 1 || events[0].Detail != fmt.Sprint(devices[0].ID)+" Pixel" {
					t.Fatalf("fleet %s events = %+v, %v", f.name, events, err)
				}
			}
			p.agree(rec, op)
			if c.status == http.StatusSeeOther {
				if loc := rec.Header().Get("Location"); loc != "/"+a.id+"/" {
					t.Fatalf("redirect to %q", loc)
				}
				return
			}
			body := rec.Body.String()
			contains(t, "result page", body, c.said, `<h2 class="thread-title">Phone paired</h2>`)
			if c.missed != "" {
				contains(t, "result page", body, c.missed)
			} else {
				lacks(t, "result page", body, "not reachable")
			}
			for i, f := range p.fleets {
				if !slices.Contains(c.paired, i) && strings.Contains(body, f.name) {
					t.Fatalf("the result page names %s, which the phone is not paired with:\n%s", f.name, body)
				}
				if slices.Contains(c.paired, i) {
					contains(t, "result page", body, `action="/`+f.id+`/"`, `aria-label="Open `+f.name+`"`)
				}
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestTheRecordsOfTwoPairingsCarryTwoLabels(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	op := serving("operator@example.com")
	for range 2 {
		code := p.codeOf(p.mint(a, p.laptops(a, b), b))
		if rec := p.redeem(a, code, nil, op); rec.Code != http.StatusOK {
			t.Fatalf("redeem = %d", rec.Code)
		}
	}
	for _, f := range p.fleets {
		if list := p.devicesOf(f); len(list) != 2 || list[0].Phone == list[1].Phone {
			t.Fatalf("fleet %s labels = %+v", f.name, list)
		}
	}
	first, second := p.devicesOf(a), p.devicesOf(b)
	if first[0].Phone != second[0].Phone || first[1].Phone != second[1].Phone {
		t.Fatalf("labels do not match across fleets: %+v and %+v", first, second)
	}
}

func TestAChosenFleetTheLaptopIsNotProvenForGetsNoCode(t *testing.T) {
	op := serving("operator@example.com")
	wrong := func(f railFleet) *http.Cookie {
		return &http.Cookie{Name: "hand_board_" + f.id, Value: strings.Repeat("0", 32)}
	}
	cases := []struct {
		what   string
		cookie func(p *phoneHost) []*http.Cookie
		ticked func(p *phoneHost) []string
	}{
		{"a fleet with no cookie", func(p *phoneHost) []*http.Cookie { return p.laptops(p.fleets[0]) }, func(p *phoneHost) []string { return []string{p.fleets[1].id} }},
		{"a fleet whose cookie holds another token", func(p *phoneHost) []*http.Cookie {
			return []*http.Cookie{p.laptop(p.fleets[0]), wrong(p.fleets[1])}
		}, func(p *phoneHost) []string { return []string{p.fleets[1].id} }},
		{"one proven and one not", func(p *phoneHost) []*http.Cookie { return p.laptops(p.fleets[0], p.fleets[1]) }, func(p *phoneHost) []string { return []string{p.fleets[1].id, p.fleets[2].id} }},
		{"an id nobody registered", func(p *phoneHost) []*http.Cookie {
			return []*http.Cookie{p.laptop(p.fleets[0]), {Name: "hand_board_f00000000dead", Value: p.fleets[0].token}}
		}, func(p *phoneHost) []string { return []string{"f00000000dead"} }},
		{"an id that is not a fleet id", func(p *phoneHost) []*http.Cookie { return p.laptops(p.fleets[0]) }, func(p *phoneHost) []string { return []string{"../" + p.fleets[1].id, "", " ", "%00"} }},
		{"a fleet the phone is paired with, not the laptop", func(p *phoneHost) []*http.Cookie {
			return []*http.Cookie{p.laptop(p.fleets[0]), p.pair(p.fleets[1], op)}
		}, func(p *phoneHost) []string { return []string{p.fleets[1].id} }},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			p := newPhoneHost(t, true, "alpha", "beta", "gamma")
			a := p.fleets[0]
			cookies := c.cookie(p)
			before := len(p.devicesOf(p.fleets[1]))
			form := url.Values{"csrf": {a.token}, "base": {"https://laptop.example"}, "fleet": c.ticked(p)}
			rec := p.send("POST", "/"+a.id+"/connect/code", form, cookies, op)
			if rec.Code != http.StatusForbidden || codeRe.MatchString(rec.Body.String()) {
				t.Fatalf("mint = %d:\n%s", rec.Code, rec.Body.String())
			}
			contains(t, "refusal", rec.Body.String(), "not signed in to every fleet you chose")
			for _, f := range p.fleets[1:] {
				lacks(t, "refusal", rec.Body.String(), f.name, f.id)
			}
			if got := p.redeem(a, "AAAAA-AAAAA", nil, op); got.Code != http.StatusForbidden || len(phoneCookiesOf(got)) != 0 {
				t.Fatalf("a redeem after the refused mint = %d", got.Code)
			}
			for _, f := range p.fleets {
				want := 0
				if f.id == p.fleets[1].id {
					want = before
				}
				if got := len(p.devicesOf(f)); got != want {
					t.Fatalf("fleet %s holds %d records after a refused mint", f.name, got)
				}
			}
		})
	}
}

func TestOnlyTheFleetsTheLaptopHoldsATokenForAreOffered(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta", "gamma", "delta")
	a, b, c, d := p.fleets[0], p.fleets[1], p.fleets[2], p.fleets[3]
	page := func(cookies ...*http.Cookie) string {
		rec := p.send("GET", "/"+a.id+"/connect", nil, cookies, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("connect = %d", rec.Code)
		}
		return rec.Body.String()
	}
	alone := page(p.laptop(a))
	lacks(t, "the page of a laptop signed in to one fleet", alone, `name="fleet"`, "Fleets this phone may open", b.name, c.name, d.name)

	both := page(p.laptop(b), p.laptop(a))
	if got := checkboxRe.FindAllStringSubmatch(both, -1); len(got) != 2 {
		t.Fatalf("checkboxes = %v", got)
	}
	contains(t, "the page for two proven fleets", both,
		`<label class="pair-fleet"><input type="checkbox" name="fleet" value="`+a.id+`" checked disabled><span>alpha (this fleet)</span></label>`,
		`<label class="pair-fleet"><input type="checkbox" name="fleet" value="`+b.id+`"><span>beta</span></label>`)
	lacks(t, "the page for two proven fleets", both, c.name, d.name, c.id, d.id)

	phoneC := p.pair(c, op)
	withPhone := p.send("GET", "/"+a.id+"/connect", nil, []*http.Cookie{p.laptop(a), p.laptop(b), phoneC}, op).Body.String()
	contains(t, "the rail of a laptop that also holds a phone's cookie", withPhone, `href="/`+c.id+`/"`)
	var values []string
	for _, m := range checkboxRe.FindAllStringSubmatch(withPhone, -1) {
		values = append(values, m[1])
	}
	if !slices.Equal(values, []string{a.id, b.id}) {
		t.Fatalf("a phone's cookie for gamma changed the choices to %v", values)
	}

	stale := page(p.laptop(a), p.laptop(b), &http.Cookie{Name: "hand_board_" + c.id, Value: strings.Repeat("1", 32)})
	lacks(t, "a stale token", stale, c.name, c.id)
	p.setGone(b)
	lacks(t, "a removed fleet", page(p.laptop(a), p.laptop(b)), b.name, b.id, `name="fleet"`)

	form := p.send("POST", "/"+a.id+"/connect/code", url.Values{"csrf": {a.token}, "base": {"https://laptop.example"}, "fleet": {c.id}}, p.laptops(a, c), nil)
	contains(t, "the code page", form.Body.String(), `name="fleet" value="`+c.id+`" checked>`, "It pairs the phone with alpha and gamma.")
	lacks(t, "the code page", form.Body.String(), `value="`+c.id+`" checked disabled`)
	if got := checkboxRe.FindAllStringSubmatch(form.Body.String(), -1); len(got) != 2 {
		t.Fatalf("checkboxes on the code page = %v", got)
	}
}

func TestAFleetListedOnTheCodePageKeepsItsTickWhenTheAddressIsWrong(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	form := url.Values{"csrf": {a.token}, "base": {"laptop"}, "fleet": {b.id}}
	rec := p.send("POST", "/"+a.id+"/connect/code", form, p.laptops(a, b), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad address = %d", rec.Code)
	}
	contains(t, "the page", rec.Body.String(), `name="fleet" value="`+b.id+`" checked>`, "type the address your phone opens")
	lacks(t, "the page", rec.Body.String(), "pairs the phone with")
	if list := p.devicesOf(a); len(list) != 0 {
		t.Fatalf("a bad address left %v", list)
	}
}

func TestAPhoneCannotMintACodeOrAddAFleet(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	phoneA, phoneB := p.pair(a, op), p.pair(b, op)
	csrfA := csrfRe.FindStringSubmatch(p.send("GET", "/"+a.id+"/connect", nil, []*http.Cookie{phoneA, phoneB}, op).Body.String())[1]
	for what, form := range map[string]url.Values{
		"its own csrf":                   {"csrf": {csrfA}, "base": {"https://laptop.example"}},
		"a fleet it is paired with":      {"csrf": {csrfA}, "base": {"https://laptop.example"}, "fleet": {b.id}},
		"a fleet it is not paired with":  {"csrf": {csrfA}, "base": {"https://laptop.example"}, "fleet": {c.id}},
		"the fleet token as its csrf":    {"csrf": {a.token}, "base": {"https://laptop.example"}, "fleet": {b.id}},
		"another fleet's token as csrf":  {"csrf": {b.token}, "base": {"https://laptop.example"}, "fleet": {b.id}},
		"every fleet at once, bad csrf":  {"csrf": {"x"}, "base": {"https://laptop.example"}, "fleet": {b.id, c.id}},
		"a body with only the fleet ids": {"fleet": {a.id, b.id, c.id}},
	} {
		rec := p.send("POST", "/"+a.id+"/connect/code", form, []*http.Cookie{phoneA, phoneB}, op)
		if rec.Code != http.StatusForbidden || codeRe.MatchString(rec.Body.String()) {
			t.Fatalf("%s: mint by a phone = %d:\n%s", what, rec.Code, rec.Body.String())
		}
	}
	page := p.send("GET", "/"+a.id+"/connect", nil, []*http.Cookie{phoneA, phoneB}, op).Body.String()
	lacks(t, "a phone's Phone page", page, `name="fleet"`, "Fleets this phone may open", `action="/`+a.id+`/connect/code"`)
	if rec := p.redeem(a, "AAAAA-AAAAA", nil, op); rec.Code != http.StatusForbidden {
		t.Fatalf("redeem after the refused mints = %d", rec.Code)
	}
}

func TestAPhoneWithAFleetsCookieAndTheLaptopForAnotherCannotAddThatFleet(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	phoneB := p.pair(b, op)
	rec := p.send("POST", "/"+a.id+"/connect/code", url.Values{"csrf": {a.token}, "base": {"https://laptop.example"}, "fleet": {b.id}}, []*http.Cookie{p.laptop(a), phoneB}, op)
	if rec.Code != http.StatusForbidden || codeRe.MatchString(rec.Body.String()) {
		t.Fatalf("mint with a phone's proof for beta = %d:\n%s", rec.Code, rec.Body.String())
	}
}

func TestThePhoneCannotChangeTheFleetsOfACode(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	code := p.codeOf(p.mint(a, p.laptops(a, b, c), b))
	extra := url.Values{"fleet": {c.id}, "fleets": {c.id, b.id}, "Fleet": {c.id}, "id": {c.id}, "base": {"/" + c.id}}
	rec := p.redeem(a, code, extra, op)
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem = %d", rec.Code)
	}
	if got := p.devicesOf(c); len(got) != 0 {
		t.Fatalf("a form field added fleet gamma: %+v", got)
	}
	if cookies := phoneCookiesOf(rec); len(cookies) != 2 || cookies[c.id] != nil {
		t.Fatalf("cookies = %v", cookies)
	}
	lacks(t, "result page", rec.Body.String(), c.name)
	p.agree(rec, op)
}

func TestACodeStaysOneUseAndGrantsNothingTwice(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	code := p.codeOf(p.mint(a, p.laptops(a, b, c), b, c))
	first := p.redeem(a, code, nil, op)
	if first.Code != http.StatusOK {
		t.Fatalf("first = %d", first.Code)
	}
	for range 3 {
		again := p.redeem(a, code, nil, serving("operator@example.com"))
		if again.Code != http.StatusForbidden || len(phoneCookiesOf(again)) != 0 {
			t.Fatalf("replay = %d with %d cookies", again.Code, len(phoneCookiesOf(again)))
		}
		contains(t, "replay", again.Body.String(), "That code was already used.")
	}
	for _, f := range p.fleets {
		if got := len(p.devicesOf(f)); got != 1 {
			t.Fatalf("fleet %s holds %d records after replays", f.name, got)
		}
	}
	p.agree(first, op)
}

func TestACodeMintedHereDoesNotPairAtAnotherFleet(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	code := p.codeOf(p.mint(a, p.laptops(a, b), b))
	rec := p.redeem(b, code, nil, op)
	if rec.Code != http.StatusForbidden || len(phoneCookiesOf(rec)) != 0 {
		t.Fatalf("a code of alpha redeemed at beta = %d", rec.Code)
	}
	for _, f := range p.fleets {
		if got := p.devicesOf(f); len(got) != 0 {
			t.Fatalf("fleet %s holds %+v", f.name, got)
		}
	}
	if rec := p.redeem(a, code, nil, op); rec.Code != http.StatusOK {
		t.Fatalf("the code still pairs at alpha = %d", rec.Code)
	}
}

func TestTenWrongTriesCancelAMultiFleetCodeAndPairNothing(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	code := p.codeOf(p.mint(a, p.laptops(a, b, c), b, c))
	other := p.codeOf(p.mint(b, p.laptops(b)))
	var last *httptest.ResponseRecorder
	for i := range 10 {
		from := map[string]string{"X-Forwarded-For": fmt.Sprintf("100.64.1.%d", i+1), "X-Forwarded-Proto": "https"}
		last = p.redeem(a, "AAAAA-AAAAA", nil, from)
		if last.Code != http.StatusForbidden {
			t.Fatalf("wrong try %d = %d", i, last.Code)
		}
	}
	contains(t, "the tenth wrong try", last.Body.String(), "Too many wrong codes. Every code was cancelled.")
	rec := p.redeem(a, code, nil, map[string]string{"X-Forwarded-For": "100.64.2.1", "X-Forwarded-Proto": "https"})
	if rec.Code != http.StatusForbidden || len(phoneCookiesOf(rec)) != 0 {
		t.Fatalf("the cancelled code = %d", rec.Code)
	}
	for _, f := range p.fleets {
		if got := p.devicesOf(f); len(got) != 0 {
			t.Fatalf("fleet %s holds %+v after a burned code", f.name, got)
		}
	}
	if rec := p.redeem(b, other, nil, map[string]string{"X-Forwarded-For": "100.64.2.2", "X-Forwarded-Proto": "https"}); rec.Code != http.StatusSeeOther {
		t.Fatalf("beta's own code, never guessed at, = %d: each fleet keeps its own limit", rec.Code)
	}
}

func TestOneAddressStillCannotGuessAtAMultiFleetCode(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	code := p.codeOf(p.mint(a, p.laptops(a, b), b))
	from := map[string]string{"X-Forwarded-For": "100.64.3.1", "X-Forwarded-Proto": "https"}
	for i := range 5 {
		if rec := p.redeem(a, "AAAAA-AAAAA", nil, from); rec.Code != http.StatusForbidden {
			t.Fatalf("try %d = %d", i, rec.Code)
		}
	}
	rec := p.redeem(a, code, nil, from)
	if rec.Code != http.StatusTooManyRequests || len(phoneCookiesOf(rec)) != 0 {
		t.Fatalf("the sixth try from one address = %d", rec.Code)
	}
	for _, f := range p.fleets {
		if got := p.devicesOf(f); len(got) != 0 {
			t.Fatalf("fleet %s holds %+v", f.name, got)
		}
	}
}

func TestAFleetWhoseStoreFailsLeavesNoRecordAndNoCookie(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	code := p.codeOf(p.mint(a, p.laptops(a, b, c), b, c))
	if err := b.st.Close(); err != nil {
		t.Fatal(err)
	}
	rec := p.redeem(a, code, nil, op)
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem = %d:\n%s", rec.Code, rec.Body.String())
	}
	cookies := phoneCookiesOf(rec)
	if len(cookies) != 2 || cookies[b.id] != nil || cookies[a.id] == nil || cookies[c.id] == nil {
		t.Fatalf("cookies = %v", cookies)
	}
	contains(t, "result page", rec.Body.String(), "Paired with alpha and gamma.", "1 fleet was not reachable.")
	lacks(t, "result page", rec.Body.String(), b.name)
	reopened, err := state.Open(p.paths[b.id], p.clock)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if list, err := reopened.Devices(context.Background()); err != nil || len(list) != 0 {
		t.Fatalf("the failed fleet holds %v, %v", list, err)
	}
	for _, f := range []railFleet{a, c} {
		if got := p.opens(f, op, cookies[f.id]); got != http.StatusOK {
			t.Fatalf("fleet %s = %d", f.name, got)
		}
	}
}

func TestAFleetOutsideTheCodeIsNeverPairedByAnyBoard(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta")
	a := p.fleets[0]
	direct := board.New(a.st, a.token, board.Options{Base: "/" + a.id, Now: p.clock}).(board.Pairer)
	for what, rd := range map[string]*board.Redeemed{"a nil grant": nil, "an empty grant": new(board.Redeemed)} {
		if got, err := direct.PairPhone(context.Background(), rd); err == nil || got != (board.Paired{}) {
			t.Fatalf("%s paired %+v", what, got)
		}
	}
	if list := p.devicesOf(a); len(list) != 0 {
		t.Fatalf("a grant nobody redeemed left %v", list)
	}
}

func TestEveryFleetOfAPairedPhoneSeesOnlyItsOwnSessionAndNeverAToken(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta", "gamma", "delta")
	a, b, c, d := p.fleets[0], p.fleets[1], p.fleets[2], p.fleets[3]
	code := p.codeOf(p.mint(a, p.laptops(a, b, c, d), b, c))
	rec := p.redeem(a, code, nil, op)
	cookies := phoneCookiesOf(rec)
	var sent []*http.Cookie
	secret := strings.SplitN(cookies[a.id].Value, ".", 2)[1]
	for _, f := range []railFleet{a, b, c} {
		sent = append(sent, cookies[f.id])
		if got := strings.SplitN(cookies[f.id].Value, ".", 2)[1]; got != secret {
			t.Fatalf("fleet %s holds another secret", f.name)
		}
	}
	tokens := []string{a.token, b.token, c.token, d.token}
	own := map[string]string{}
	for _, f := range []railFleet{a, b, c} {
		own[f.id] = csrfRe.FindStringSubmatch(p.send("GET", "/"+f.id+"/connect", nil, sent, op).Body.String())[1]
	}
	if own[a.id] == own[b.id] || own[b.id] == own[c.id] || own[a.id] == own[c.id] {
		t.Fatalf("two fleets give one phone the same form token: %v", own)
	}
	paths := []string{"/", "/?all=1", "/?pick=1", "/tasks", "/tasks?q=fix", "/task/t1", "/decision/d1", "/ref/t1", "/supervisor/log", "/connect", "/pair"}
	check := func(what string, r *httptest.ResponseRecorder, csrf string) {
		t.Helper()
		if r.Code >= 500 {
			t.Fatalf("%s = %d", what, r.Code)
		}
		body := r.Body.String()
		for _, tk := range tokens {
			if strings.Contains(body, tk) {
				t.Fatalf("%s holds a fleet token:\n%s", what, body)
			}
			for k, v := range r.Result().Header {
				for _, line := range v {
					if strings.Contains(line, tk) {
						t.Fatalf("%s sends a fleet token in %s", what, k)
					}
				}
			}
		}
		if strings.Contains(body, secret) {
			t.Fatalf("%s holds the phone's secret", what)
		}
		for _, m := range csrfRe.FindAllStringSubmatch(body, -1) {
			if m[1] != csrf {
				t.Fatalf("%s carries csrf %q, want its own %q", what, m[1], csrf)
			}
		}
	}
	check("the result page", rec, "")
	check("the host list", p.send("GET", "/", nil, sent, op), "")
	rails := map[string]string{}
	for _, f := range []railFleet{a, b, c} {
		for _, path := range paths {
			r := p.send("GET", "/"+f.id+path, nil, sent, op)
			check("GET /"+f.name+path, r, own[f.id])
			if path == "/" {
				rails[f.name] = r.Body.String()
			}
		}
	}
	for name, body := range rails {
		contains(t, name, body, `href="/`+a.id+`/"`, `href="/`+b.id+`/"`, `href="/`+c.id+`/"`)
		lacks(t, name, body, d.name, d.id)
	}
	list := p.send("GET", "/", nil, sent, op).Body.String()
	contains(t, "the host list", list, a.name, b.name, c.name)
	lacks(t, "the host list", list, d.name, d.id)
}

func TestOneFleetsFormTokenOpensNothingInAnotherFleet(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	code := p.codeOf(p.mint(a, p.laptops(a, b), b))
	rec := p.redeem(a, code, nil, op)
	cookies := phoneCookiesOf(rec)
	sent := []*http.Cookie{cookies[a.id], cookies[b.id]}
	csrfA := csrfRe.FindStringSubmatch(p.send("GET", "/"+a.id+"/connect", nil, sent, op).Body.String())[1]
	csrfB := csrfRe.FindStringSubmatch(p.send("GET", "/"+b.id+"/connect", nil, sent, op).Body.String())[1]
	idB := strings.SplitN(cookies[b.id].Value, ".", 2)[0]
	for what, token := range map[string]string{"alpha's token": csrfA, "no token": "", "the laptop's token": b.token, "alpha's laptop token": a.token} {
		got := p.send("POST", "/"+b.id+"/connect/revoke", url.Values{"csrf": {token}, "id": {idB}}, sent, op)
		if got.Code != http.StatusForbidden {
			t.Fatalf("%s posted to beta = %d", what, got.Code)
		}
	}
	if list := p.devicesOf(b); len(list) != 1 {
		t.Fatalf("beta holds %v after the refused posts", list)
	}
	if got := p.send("POST", "/"+b.id+"/connect/revoke", url.Values{"csrf": {csrfB}, "id": {idB}}, sent, op); got.Code != http.StatusOK {
		t.Fatalf("beta's own token = %d", got.Code)
	}
	if list := p.devicesOf(b); len(list) != 0 {
		t.Fatalf("beta holds %v after its own sign-out", list)
	}
	if got := p.opens(a, op, sent...); got != http.StatusOK {
		t.Fatalf("signing out of beta closed alpha = %d", got)
	}
}

func TestRevokingInOneFleetLeavesTheOtherFleetsOfThePhoneAlone(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	code := p.codeOf(p.mint(a, p.laptops(a, b, c), b, c))
	rec := p.redeem(a, code, nil, op)
	cookies := phoneCookiesOf(rec)
	sent := []*http.Cookie{cookies[a.id], cookies[b.id], cookies[c.id]}
	p.revoke(b, sent[1])
	if got := p.opens(b, op, sent...); got != http.StatusForbidden {
		t.Fatalf("revoked beta = %d", got)
	}
	for _, f := range []railFleet{a, c} {
		if got := p.opens(f, op, sent...); got != http.StatusOK {
			t.Fatalf("fleet %s = %d after beta was revoked", f.name, got)
		}
		events, _ := f.st.EventsAfter(context.Background(), 0, []string{"board.revoked"}, 10)
		if len(p.devicesOf(f)) != 1 || len(events) != 0 {
			t.Fatalf("fleet %s lost a record or logged a revoke: %v", f.name, events)
		}
	}
	list := p.send("GET", "/", nil, sent, op).Body.String()
	contains(t, "the host list", list, a.name, c.name)
	lacks(t, "the host list", list, b.name)
}

func TestRotatingOneFleetsTokenSignsOutThePhoneOfThatFleetOnly(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	code := p.codeOf(p.mint(a, p.laptops(a, b, c), b, c))
	cookies := phoneCookiesOf(p.redeem(a, code, nil, op))
	sent := []*http.Cookie{cookies[a.id], cookies[b.id], cookies[c.id]}
	p.fleets[1].token = strings.Repeat("9", 32)
	p.restart()
	if got := p.opens(b, op, sent...); got != http.StatusForbidden {
		t.Fatalf("beta after its token rotated = %d", got)
	}
	for _, f := range []railFleet{a, c} {
		if got := p.opens(f, op, sent...); got != http.StatusOK {
			t.Fatalf("fleet %s = %d after beta's token rotated", f.name, got)
		}
	}
	list := p.send("GET", "/", nil, sent, op).Body.String()
	lacks(t, "the host list", list, b.name)
}

func TestEightFleetsFitOneCodeAndANinthIsNotOffered(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10")
	a := p.fleets[0]
	page := p.send("GET", "/"+a.id+"/connect", nil, p.laptops(p.fleets...), nil).Body.String()
	if got := checkboxRe.FindAllStringSubmatch(page, -1); len(got) != 8 {
		t.Fatalf("a laptop proven for ten fleets is offered %d, want this one and 7 others", len(got))
	}
	over := p.fleets[8]
	rec := p.mint(a, p.laptops(p.fleets...), p.fleets[1:9]...)
	if rec.Code != http.StatusForbidden || codeRe.MatchString(rec.Body.String()) {
		t.Fatalf("a code for nine fleets = %d", rec.Code)
	}
	if rec := p.mint(a, p.laptops(p.fleets...), over); rec.Code != http.StatusForbidden {
		t.Fatalf("a fleet beyond the 7 offered = %d", rec.Code)
	}
	code := p.codeOf(p.mint(a, p.laptops(p.fleets...), p.fleets[1:8]...))
	rec = p.redeem(a, code, nil, op)
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem = %d:\n%s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	var sent []string
	longest := 0
	for _, c := range cookies {
		if c.MaxAge > 0 {
			sent = append(sent, c.Name+"="+c.Value)
			longest = max(longest, len(c.String()))
		}
	}
	if len(sent) != 8 {
		t.Fatalf("%d cookies for 8 fleets", len(sent))
	}
	header := strings.Join(sent, "; ")
	if len(header) > 1024 || longest > 256 {
		t.Fatalf("the cookie header is %d bytes and the longest Set-Cookie %d", len(header), longest)
	}
	setCookies := 0
	for _, v := range rec.Header().Values("Set-Cookie") {
		setCookies += len(v)
	}
	if setCookies > 2048 {
		t.Fatalf("the Set-Cookie headers hold %d bytes", setCookies)
	}
	req := httptest.NewRequest("GET", "/"+a.id+"/", nil)
	req.Header.Set("Cookie", header)
	for k, v := range op {
		req.Header.Set(k, v)
	}
	got := httptest.NewRecorder()
	p.h.ServeHTTP(got, req)
	if got.Code != http.StatusOK {
		t.Fatalf("a request carrying the header of 8 cookies = %d", got.Code)
	}
	for _, f := range p.fleets[:8] {
		if n := len(p.devicesOf(f)); n != 1 {
			t.Fatalf("fleet %s holds %d records", f.name, n)
		}
	}
	for _, f := range p.fleets[8:] {
		if n := len(p.devicesOf(f)); n != 0 {
			t.Fatalf("fleet %s holds %d records", f.name, n)
		}
	}
}

func TestAFunnelRequestPairsNothingAndSpendsNoCode(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	funnel := map[string]string{"Tailscale-Funnel-Request": "?1", "X-Forwarded-For": "203.0.113.9", "X-Forwarded-Proto": "https"}
	form := url.Values{"csrf": {a.token}, "base": {"https://laptop.example"}, "fleet": {b.id}}
	rec := p.send("POST", "/"+a.id+"/connect/code", form, p.laptops(a, b), funnel)
	if rec.Code != http.StatusForbidden || codeRe.MatchString(rec.Body.String()) {
		t.Fatalf("a code through Funnel = %d", rec.Code)
	}
	code := p.codeOf(p.mint(a, p.laptops(a, b), b))
	if rec := p.redeem(a, code, nil, funnel); rec.Code != http.StatusForbidden || len(phoneCookiesOf(rec)) != 0 {
		t.Fatalf("a redeem through Funnel = %d", rec.Code)
	}
	for _, f := range p.fleets {
		if got := p.devicesOf(f); len(got) != 0 {
			t.Fatalf("fleet %s holds %+v after Funnel", f.name, got)
		}
	}
	if rec := p.redeem(a, code, nil, op); rec.Code != http.StatusOK {
		t.Fatalf("the code after the Funnel refusal = %d", rec.Code)
	}
}

func TestABoardOnANetworkAddressPairsOnlyItsOwnFleet(t *testing.T) {
	p := newPhoneHost(t, false, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	lacks(t, "the Phone page", p.send("GET", "/"+a.id+"/connect", nil, p.laptops(a, b), nil).Body.String(), `name="fleet"`, b.name)
	if rec := p.mint(a, p.laptops(a, b), b); rec.Code != http.StatusForbidden || codeRe.MatchString(rec.Body.String()) {
		t.Fatalf("a second fleet on a network address board = %d", rec.Code)
	}
	code := p.codeOf(p.mint(a, p.laptops(a, b)))
	rec := p.redeem(a, code, nil, serving("operator@example.com"))
	if rec.Code != http.StatusSeeOther || len(phoneCookiesOf(rec)) != 1 {
		t.Fatalf("redeem = %d", rec.Code)
	}
	if got := p.devicesOf(b); len(got) != 0 {
		t.Fatalf("beta holds %+v", got)
	}
}

func TestPairingSeveralFleetsWritesNoCodeSecretOrTokenToTheLog(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	code := p.codeOf(p.mint(a, p.laptops(a, b, c), b, c))
	p.setDown(c)
	rec := p.redeem(a, code, nil, op)
	cookies := phoneCookiesOf(rec)
	secret := strings.SplitN(cookies[a.id].Value, ".", 2)[1]
	p.redeem(a, code, nil, op)
	p.redeem(a, "AAAAA-AAAAA", nil, op)
	for _, bad := range append([]string{code, secret, strings.ReplaceAll(code, "-", "")}, a.token, b.token, c.token) {
		if strings.Contains(buf.String(), bad) {
			t.Fatalf("the board logged %q in %q", bad, buf.String())
		}
	}
	if !strings.Contains(buf.String(), "pairing could not reach fleet "+c.id) {
		t.Fatalf("the unreachable fleet left no trace in the log: %q", buf.String())
	}
}

func TestAPhoneListsItsRecordsAsLabelledOrFleetOnly(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	hash := strings.Repeat("c", 64)
	if _, err := a.st.AddDevice(context.Background(), "Old phone", "", hash, ""); err != nil {
		t.Fatal(err)
	}
	page := func() string {
		return p.send("GET", "/"+a.id+"/connect", nil, p.laptops(a), nil).Body.String()
	}
	if got := strings.Count(page(), "this fleet only ·"); got != 1 {
		t.Fatalf("one legacy row reads %d times 'this fleet only'", got)
	}
	code := p.codeOf(p.mint(a, p.laptops(a, b), b))
	p.redeem(a, code, nil, op)
	body := page()
	if got := strings.Count(body, "this fleet only ·"); got != 1 {
		t.Fatalf("a legacy and a labelled row read %d times 'this fleet only'", got)
	}
	contains(t, "the list", body, `aria-label="Revoke Old phone"`, `aria-label="Revoke Pixel"`)
	lacks(t, "the list", body, p.devicesOf(a)[1].Phone)
	for _, d := range p.devicesOf(a) {
		if d.Name == "Old phone" && d.Phone != "" {
			t.Fatalf("the legacy row gained a label: %+v", d)
		}
	}
}

func TestThePairPageBeforeRedeemingNamesNoFleetAndTheMarkupHoldsNoInlineCode(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	codePage := p.mint(a, p.laptops(a, b, c), b)
	code := p.codeOf(codePage)
	pages := map[string]string{
		"pair":     p.send("GET", "/"+a.id+"/pair", nil, nil, op).Body.String(),
		"pair bad": p.redeem(a, "AAAAA-AAAAA", nil, op).Body.String(),
		"connect":  p.send("GET", "/"+a.id+"/connect", nil, p.laptops(a, b, c), nil).Body.String(),
		"code":     codePage.Body.String(),
		"result":   p.redeem(a, code, nil, op).Body.String(),
	}
	inline := regexp.MustCompile(`(?i)\sstyle=|<style|<script[^>]*>[^<]|\son[a-z]+=|javascript:`)
	for name, body := range pages {
		if m := inline.FindString(body); m != "" {
			t.Errorf("%s page holds inline code %q", name, m)
		}
		if n := strings.Count(body, "<script"); n != 1 {
			t.Errorf("%s page has %d script tags", name, n)
		}
		lacks(t, name+" page", body, "Desk", "<img ")
	}
	for _, name := range []string{"pair", "pair bad", "result"} {
		for _, f := range p.fleets {
			lacks(t, name+" page", pages[name], f.token)
		}
	}
	for _, name := range []string{"pair", "pair bad"} {
		lacks(t, name+" page", pages[name], a.name, b.name, c.name, b.id, c.id)
	}
	contains(t, "connect", pages["connect"], `<fieldset class="pair-fleets"><legend>Fleets this phone may open</legend>`)
	boxes := checkboxRe.FindAllStringSubmatch(pages["connect"], -1)
	if len(boxes) != 3 {
		t.Fatalf("checkboxes = %v", boxes)
	}
	for _, m := range boxes {
		contains(t, "connect", pages["connect"], `<label class="pair-fleet"><input type="checkbox" name="fleet" value="`+m[1]+`"`)
	}
	contains(t, "result", pages["result"], "Paired with alpha and beta.")
	lacks(t, "result", pages["result"], `name="csrf"`, `type="hidden"`, c.name)
}

func TestEveryRecordOfAPairingKeepsTheLoginItWasPairedFor(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	code := p.codeOf(p.mint(a, p.laptops(a, b, c), b, c))
	rec := p.redeem(a, code, nil, serving("a@example.com"))
	cookies := phoneCookiesOf(rec)
	if len(cookies) != 3 {
		t.Fatalf("cookies = %v", cookies)
	}
	for _, f := range []railFleet{a, b, c} {
		if got := p.opens(f, serving("a@example.com"), cookies[f.id]); got != http.StatusOK {
			t.Fatalf("fleet %s for the login it was paired for = %d", f.name, got)
		}
		for what, headers := range map[string]map[string]string{"another login": serving("b@example.com"), "no login": {"X-Forwarded-For": "100.64.0.7", "X-Forwarded-Proto": "https"}} {
			if got := p.opens(f, headers, cookies[f.id]); got != http.StatusForbidden {
				t.Fatalf("fleet %s with %s = %d", f.name, what, got)
			}
		}
	}
}
