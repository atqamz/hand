package board_test

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAPhoneListsExactlyTheFleetsItIsPairedWith(t *testing.T) {
	op := serving("operator@example.com")
	forged := "1." + strings.Repeat("a", 64)
	cases := []struct {
		what    string
		network bool
		on      int
		status  int
		setup   func(p *phoneHost) ([]*http.Cookie, map[string]string)
		rail    []int
		home    []int
	}{
		{"no cookie", false, 0, 403, func(p *phoneHost) ([]*http.Cookie, map[string]string) { return nil, op }, nil, nil},
		{"laptop session only", false, 0, 200, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			return []*http.Cookie{p.laptop(p.fleets[0]), p.laptop(p.fleets[1])}, op
		}, []int{0, 1}, nil},
		{"phone paired with one fleet", false, 0, 200, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			return []*http.Cookie{p.pair(p.fleets[0], op)}, op
		}, []int{0}, []int{0}},
		{"phone paired with two of three", false, 0, 200, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			return []*http.Cookie{p.pair(p.fleets[0], op), p.pair(p.fleets[1], op)}, op
		}, []int{0, 1}, []int{0, 1}},
		{"the second of two, opened from its own page", false, 1, 200, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			return []*http.Cookie{p.pair(p.fleets[0], op), p.pair(p.fleets[1], op)}, op
		}, []int{0, 1}, []int{0, 1}},
		{"the third fleet, never paired", false, 2, 403, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			return []*http.Cookie{p.pair(p.fleets[0], op), p.pair(p.fleets[1], op)}, op
		}, nil, []int{0, 1}},
		{"one paired fleet revoked", false, 0, 200, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			a, b := p.pair(p.fleets[0], op), p.pair(p.fleets[1], op)
			p.revoke(p.fleets[1], b)
			return []*http.Cookie{a, b}, op
		}, []int{0}, []int{0}},
		{"the revoked fleet's own page", false, 1, 403, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			a, b := p.pair(p.fleets[0], op), p.pair(p.fleets[1], op)
			p.revoke(p.fleets[1], b)
			return []*http.Cookie{a, b}, op
		}, nil, []int{0}},
		{"one paired fleet lapsed", false, 0, 200, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			b := p.pair(p.fleets[1], op)
			p.advance(60 * 24 * time.Hour)
			a := p.pair(p.fleets[0], op)
			p.advance(40 * 24 * time.Hour)
			return []*http.Cookie{a, b}, op
		}, []int{0}, []int{0}},
		{"the lapsed fleet's own page", false, 1, 403, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			b := p.pair(p.fleets[1], op)
			p.advance(60 * 24 * time.Hour)
			a := p.pair(p.fleets[0], op)
			p.advance(40 * 24 * time.Hour)
			return []*http.Cookie{a, b}, op
		}, nil, []int{0}},
		{"one fleet paired for another login", false, 0, 200, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			a, b := p.pair(p.fleets[0], serving("a@example.com")), p.pair(p.fleets[1], serving("b@example.com"))
			return []*http.Cookie{a, b}, serving("a@example.com")
		}, []int{0}, []int{0}},
		{"every fleet paired for another login", false, 0, 403, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			a, b := p.pair(p.fleets[0], serving("a@example.com")), p.pair(p.fleets[1], serving("a@example.com"))
			return []*http.Cookie{a, b}, serving("b@example.com")
		}, nil, nil},
		{"a forged cookie beside a real one", false, 0, 200, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			return []*http.Cookie{p.pair(p.fleets[0], op), {Name: "__Host-hand_phone_" + p.fleets[1].id, Value: forged}}, op
		}, []int{0}, []int{0}},
		{"a forged cookie alone", false, 1, 403, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			return []*http.Cookie{{Name: "__Host-hand_phone_" + p.fleets[1].id, Value: forged}}, op
		}, nil, nil},
		{"fleet A's cookie under fleet B's name", false, 0, 200, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			a := p.pair(p.fleets[0], op)
			return []*http.Cookie{a, phoneNamed("__Host-hand_phone_"+p.fleets[1].id, a)}, op
		}, []int{0}, []int{0}},
		{"fleet B's page with fleet A's cookie under B's name", false, 1, 403, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			a := p.pair(p.fleets[0], op)
			return []*http.Cookie{a, phoneNamed("__Host-hand_phone_"+p.fleets[1].id, a)}, op
		}, nil, []int{0}},
		{"fleet B's page with fleet A's cookie as it is", false, 1, 403, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			return []*http.Cookie{p.pair(p.fleets[0], op)}, op
		}, nil, []int{0}},
		{"a read-only board", true, 0, 200, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			return []*http.Cookie{p.pair(p.fleets[0], op), p.pair(p.fleets[1], op)}, op
		}, []int{0}, nil},
		{"plain HTTP", false, 0, 200, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			plain := map[string]string{"X-Forwarded-For": "100.64.0.7"}
			a, b := p.pair(p.fleets[0], plain), p.pair(p.fleets[1], plain)
			if a.Name != "hand_phone_"+p.fleets[0].id {
				t.Fatalf("plain HTTP cookie = %q", a.Name)
			}
			return []*http.Cookie{a, b}, plain
		}, []int{0, 1}, []int{0, 1}},
		{"a secure cookie over plain HTTP", false, 0, 403, func(p *phoneHost) ([]*http.Cookie, map[string]string) {
			return []*http.Cookie{p.pair(p.fleets[0], op), p.pair(p.fleets[1], op)}, map[string]string{"X-Forwarded-For": "100.64.0.7", "Tailscale-User-Login": "operator@example.com"}
		}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			p := newPhoneHost(t, !c.network, "alpha", "bravo", "charlie")
			cookies, headers := c.setup(p)
			on := p.fleets[c.on]
			rec := p.send("GET", "/"+on.id+"/", nil, cookies, headers)
			body := rec.Body.String()
			if rec.Code != c.status {
				t.Fatalf("GET /%s/ = %d, want %d:\n%s", on.id, rec.Code, c.status, body)
			}
			if c.status == http.StatusOK {
				rail := railOf(t, body)
				for i, f := range p.fleets {
					if slices.Contains(c.rail, i) {
						contains(t, c.what, rail, listed(f), `<span class="label">`+f.name+`</span>`)
					} else {
						lacks(t, c.what, body, f.name, f.id)
					}
				}
				if p.loopback {
					contains(t, c.what, rail, `<span class="label">All fleets</span>`)
				} else {
					lacks(t, c.what, body, "All fleets")
				}
			} else {
				for _, f := range p.fleets {
					lacks(t, c.what, body, f.name)
					if f.id != on.id {
						lacks(t, c.what, body, f.id)
					}
				}
			}
			rec = p.send("GET", "/", nil, cookies, headers)
			body = rec.Body.String()
			if rec.Code != http.StatusOK {
				t.Fatalf("GET / = %d:\n%s", rec.Code, body)
			}
			if c.home == nil {
				contains(t, c.what, body, "pair it on the fleet's Phone page")
			} else {
				lacks(t, c.what, body, "pair it on the fleet's Phone page", "No fleets yet")
			}
			for i, f := range p.fleets {
				if slices.Contains(c.home, i) {
					contains(t, c.what, body, `<a class="fleet-name" title="`+f.name+`" href="/`+f.id+`/">`+f.name+`</a>`)
				} else {
					lacks(t, c.what, body, f.name, f.id)
				}
			}
		})
	}
}

func TestOnlyAProxyHidesTheFleetListAndAPhoneBringsItBack(t *testing.T) {
	proxies := []map[string]string{{"X-Forwarded-For": "100.64.0.7"}, {"Forwarded": "for=100.64.0.7"}, {"Tailscale-User-Login": "operator@example.com"}}
	for _, headers := range proxies {
		p := newPhoneHost(t, true, "alpha", "bravo", "charlie")
		phones := []*http.Cookie{p.pair(p.fleets[0], headers), p.pair(p.fleets[1], headers)}
		bare := p.send("GET", "/", nil, nil, headers).Body.String()
		contains(t, fmt.Sprint(headers), bare, "pair it on the fleet's Phone page")
		lacks(t, fmt.Sprint(headers), bare, "alpha", "bravo", "charlie")
		listed := p.send("GET", "/", nil, phones, headers).Body.String()
		contains(t, fmt.Sprint(headers), listed, `href="/`+p.fleets[0].id+`/"`, `href="/`+p.fleets[1].id+`/"`)
		lacks(t, fmt.Sprint(headers), listed, "charlie", p.fleets[2].id, "pair it on the fleet's Phone page")
		direct := p.send("GET", "/", nil, phones, nil).Body.String()
		contains(t, "direct", direct, "alpha", "bravo", "charlie")
	}
}

func TestALaptopSessionNeverOpensTheListForAProxy(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "bravo", "charlie")
	phone := p.pair(p.fleets[0], op)
	body := p.send("GET", "/", nil, []*http.Cookie{p.laptop(p.fleets[2])}, op).Body.String()
	lacks(t, "laptop only", body, "alpha", "bravo", "charlie")
	body = p.send("GET", "/", nil, []*http.Cookie{phone, p.laptop(p.fleets[2])}, op).Body.String()
	contains(t, "both", body, "alpha")
	lacks(t, "both", body, "bravo", "charlie")
}

func TestAFunnelRequestWithPhoneSessionsGetsOnlyTheRefusal(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "bravo")
	cookies := []*http.Cookie{p.pair(p.fleets[0], op), p.pair(p.fleets[1], op)}
	funnel := map[string]string{"Tailscale-Funnel-Request": "?1", "X-Forwarded-For": "203.0.113.9", "X-Forwarded-Proto": "https", "Tailscale-User-Login": "operator@example.com"}
	for _, path := range []string{"/", "/" + p.fleets[0].id + "/", "/" + p.fleets[1].id + "/connect", "/" + p.fleets[0].id + "/pair"} {
		rec := p.send("GET", path, nil, cookies, funnel)
		body := rec.Body.String()
		if rec.Code != http.StatusForbidden || !strings.Contains(body, "does not answer the public internet") {
			t.Fatalf("GET %s through Funnel = %d:\n%s", path, rec.Code, body)
		}
		lacks(t, path, body, "alpha", "bravo", p.fleets[1].id)
	}
}

func TestAPhoneSwitchesBetweenItsFleetsWithoutANewLogin(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "bravo", "charlie")
	a, b := p.pair(p.fleets[0], op), p.pair(p.fleets[1], op)
	cookies := []*http.Cookie{a, b}
	first := p.send("GET", "/"+p.fleets[0].id+"/", nil, cookies, op)
	contains(t, "alpha page", first.Body.String(), `href="/`+p.fleets[1].id+`/"`)
	second := p.send("GET", "/"+p.fleets[1].id+"/", nil, cookies, op)
	if second.Code != http.StatusOK {
		t.Fatalf("switching to bravo = %d:\n%s", second.Code, second.Body.String())
	}
	contains(t, "bravo page", second.Body.String(), `<title>board · bravo</title>`, `href="/`+p.fleets[0].id+`/"`)
	lacks(t, "bravo page", second.Body.String(), "charlie", p.fleets[2].id)
	p.revoke(p.fleets[1], b)
	gone := p.send("GET", "/"+p.fleets[1].id+"/", nil, cookies, op)
	if gone.Code != http.StatusForbidden {
		t.Fatalf("a fleet revoked between the list and the click = %d", gone.Code)
	}
	contains(t, "revoked click", gone.Body.String(), "This phone is no longer paired with this fleet", "<title>403 · this Board</title>")
	lacks(t, "revoked click", gone.Body.String(), "bravo", "alpha")
	p.advance(91 * 24 * time.Hour)
	late := p.send("GET", "/"+p.fleets[0].id+"/", nil, cookies, op)
	if late.Code != http.StatusForbidden {
		t.Fatalf("a lapsed fleet = %d", late.Code)
	}
	contains(t, "lapsed click", late.Body.String(), "no longer paired")
}

func TestTwoFleetsPhoneCookiesCoexist(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "bravo")
	a, b := p.pair(p.fleets[0], op), p.pair(p.fleets[1], op)
	for i, c := range []*http.Cookie{a, b} {
		if c.Name != "__Host-hand_phone_"+p.fleets[i].id || c.Path != "/" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
			t.Fatalf("cookie %d = %+v", i, c)
		}
	}
	if a.Name == b.Name || a.Value == b.Value {
		t.Fatalf("two fleets share a cookie: %+v %+v", a, b)
	}
	for i, f := range p.fleets {
		if rec := p.send("GET", "/"+f.id+"/", nil, []*http.Cookie{a, b}, op); rec.Code != http.StatusOK {
			t.Fatalf("fleet %d with both cookies = %d", i, rec.Code)
		}
	}
	if rec := p.send("GET", "/"+p.fleets[1].id+"/", nil, []*http.Cookie{a}, op); rec.Code != http.StatusForbidden {
		t.Fatalf("bravo with alpha's cookie = %d", rec.Code)
	}
	if rec := p.send("POST", "/"+p.fleets[1].id+"/supervisor/send", url.Values{"csrf": {"x"}, "text": {"hi"}}, []*http.Cookie{phoneNamed(b.Name, a)}, op); rec.Code != http.StatusForbidden {
		t.Fatalf("bravo with alpha's secret = %d", rec.Code)
	}
}

func TestOnlyTheFleetsACookieNamesAreOpenedAndEachIsCheckedOnce(t *testing.T) {
	op := serving("operator@example.com")
	p := newPhoneHost(t, true, "alpha", "bravo", "charlie", "delta", "echo")
	b, d := p.pair(p.fleets[1], op), p.pair(p.fleets[3], op)
	p.restart()
	unknown := &http.Cookie{Name: "__Host-hand_phone_f0000000000ff", Value: b.Value}
	body := p.send("GET", "/", nil, []*http.Cookie{b, d, unknown, {Name: "__Host-hand_phone_f1", Value: "x"}, {Name: "hand_phone_" + p.fleets[2].id, Value: b.Value}}, op).Body.String()
	contains(t, "home", body, "bravo", "delta")
	lacks(t, "home", body, "alpha", "charlie", "echo")
	if !slices.Equal(opened(p), slices.Sorted(slices.Values([]string{p.fleets[1].id, p.fleets[3].id}))) {
		t.Fatalf("opened %v", p.opened)
	}
	if p.proofs[p.fleets[1].id] != 1 || p.proofs[p.fleets[3].id] != 1 || len(p.proofs) != 2 {
		t.Fatalf("checks %v", p.proofs)
	}
	p.send("GET", "/"+p.fleets[1].id+"/", nil, []*http.Cookie{b, d}, op)
	if p.proofs[p.fleets[1].id] != 1 || p.proofs[p.fleets[3].id] != 2 || len(p.opened) != 2 {
		t.Fatalf("the current fleet was checked twice or one was opened again: %v %v", p.proofs, p.opened)
	}
}

func opened(p *phoneHost) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var ids []string
	for id := range p.opened {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
