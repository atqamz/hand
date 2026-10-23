package board_test

import (
	"bufio"
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
	"time"
)

var operator = serving("operator@example.com")

func (p *phoneHost) phoneWith(name string, home railFleet, with ...railFleet) map[string]*http.Cookie {
	p.t.Helper()
	all := append([]railFleet{home}, with...)
	code := p.codeOf(p.mint(home, p.laptops(all...), with...))
	rec := p.redeem(home, code, url.Values{"name": {name}}, operator)
	if rec.Code != http.StatusOK && rec.Code != http.StatusSeeOther {
		p.t.Fatalf("pair %s = %d:\n%s", name, rec.Code, rec.Body.String())
	}
	return phoneCookiesOf(rec)
}

func (p *phoneHost) labelOf(f railFleet, name string) string {
	p.t.Helper()
	for _, d := range p.devicesOf(f) {
		if d.Name == name {
			return d.Phone
		}
	}
	p.t.Fatalf("fleet %s holds no phone named %s", f.name, name)
	return ""
}

func (p *phoneHost) everywhere(home railFleet, label string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return p.send("POST", "/"+home.id+"/connect/revoke/everywhere", url.Values{"csrf": {home.token}, "phone": {label}}, cookies, nil)
}

func (p *phoneHost) revokeAt(home, target railFleet, label string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return p.send("POST", "/"+home.id+"/connect/revoke/in", url.Values{"csrf": {home.token}, "phone": {label}, "fleet": {target.id}}, cookies, nil)
}

func (p *phoneHost) phonePage(home railFleet, cookies ...*http.Cookie) string {
	p.t.Helper()
	rec := p.send("GET", "/"+home.id+"/connect", nil, cookies, nil)
	if rec.Code != http.StatusOK {
		p.t.Fatalf("Phone page = %d:\n%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func (p *phoneHost) revoked(f railFleet) []string {
	p.t.Helper()
	events, err := f.st.EventsAfter(context.Background(), 0, []string{"board.revoked"}, 100)
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		out = append(out, e.Detail)
	}
	return out
}

func (p *phoneHost) resetCounts() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lists, p.revokes, p.proofs = map[string]int{}, map[string]int{}, map[string]int{}
}

func (p *phoneHost) counts(m map[string]int, f railFleet) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return m[f.id]
}

func holds(p *phoneHost, f railFleet) []string {
	var out []string
	for _, d := range p.devicesOf(f) {
		out = append(out, d.Name)
	}
	return out
}

func sameResponse(t *testing.T, what string, a, b *httptest.ResponseRecorder) {
	t.Helper()
	if a.Code != b.Code || a.Header().Get("Location") != b.Header().Get("Location") || a.Body.String() != b.Body.String() {
		t.Fatalf("%s differ: %d %q %q and %d %q %q", what, a.Code, a.Header().Get("Location"), a.Body.String(), b.Code, b.Header().Get("Location"), b.Body.String())
	}
}

func TestAPhoneIsListedOnceWithTheFleetsTheLaptopIsSignedInTo(t *testing.T) {
	cases := []struct {
		what   string
		paired []int
		laptop []int
		view   int
		down   []int
		gone   []int
		want   []int
		unread string
	}{
		{"one fleet", []int{0}, []int{0}, 0, nil, nil, nil, ""},
		{"three fleets", []int{0, 1, 2}, []int{0, 1, 2}, 0, nil, nil, []int{0, 1, 2}, ""},
		{"the laptop is signed in to two of three", []int{0, 1, 2}, []int{0, 1}, 0, nil, nil, []int{0, 1}, ""},
		{"the laptop is signed in to the other two only", []int{0, 1, 2}, []int{0, 2}, 0, nil, nil, []int{0, 2}, ""},
		{"read from a fleet the phone is not paired with", []int{1, 2}, []int{0, 1, 2}, 0, nil, nil, []int{1, 2}, ""},
		{"read from a fleet the phone is paired with, beside one it is not", []int{1}, []int{0, 1, 2}, 0, nil, nil, []int{1}, ""},
		{"a fleet that was removed", []int{0, 1, 2}, []int{0, 1, 2}, 0, nil, []int{2}, []int{0, 1}, ""},
		{"a fleet that is down", []int{0, 1, 2}, []int{0, 1, 2}, 0, []int{1}, nil, []int{0, 2}, "beta"},
		{"two fleets that are down", []int{0, 1, 2}, []int{0, 1, 2}, 0, []int{1, 2}, nil, nil, "beta and gamma"},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			p := newPhoneHost(t, true, "alpha", "beta", "gamma", "delta")
			paired := fleetsAt(p, c.paired...)
			p.phoneWith("Pixel", paired[0], paired[1:]...)
			for _, i := range c.down {
				p.setDown(p.fleets[i])
			}
			for _, i := range c.gone {
				p.setGone(p.fleets[i])
			}
			view := p.fleets[c.view]
			p.resetCounts()
			body := p.phonePage(view, p.laptops(fleetsAt(p, c.laptop...)...)...)
			label := p.labelOf(paired[0], "Pixel")
			groups := strings.Count(body, `class="device device-group"`)
			if len(c.want) == 0 {
				if groups != 0 || strings.Contains(body, "dev-fleets") {
					t.Fatalf("a phone in one fleet is listed as a group:\n%s", body)
				}
			} else if groups != 1 {
				t.Fatalf("%d group rows:\n%s", groups, body)
			}
			if len(c.want) == 0 && len(c.paired) == 1 && c.paired[0] == c.view {
				contains(t, "the plain row", body, `aria-label="Revoke Pixel"`)
			}
			var names []string
			for _, i := range c.want {
				f := p.fleets[i]
				names = append(names, f.name)
				contains(t, "the breakdown", body, `aria-label="Revoke Pixel in `+f.name+`"`, `name="fleet" value="`+f.id+`"`)
			}
			if len(c.want) > 0 {
				contains(t, "the group", body, `<span class="dev-note">operator@example.com · paired `)
				for _, n := range names {
					contains(t, "the breakdown", body, `<span>`+n)
				}
			}
			if got := strings.Count(body, `action="/`+view.id+`/connect/revoke/everywhere"`); (len(c.want) > 1) != (got == 1) || got > 1 {
				t.Fatalf("%d Revoke everywhere forms for %d fleets", got, len(c.want))
			}
			forms := 0
			if len(c.want) > 0 {
				forms = len(c.want)
				if forms > 1 {
					forms++
				}
			}
			if n := strings.Count(body, `name="phone" value="`+label+`"`); n != forms {
				t.Fatalf("the label sits in %d forms, want %d", n, forms)
			}
			if c.unread != "" {
				contains(t, "the notice", body, c.unread+" could not be reached, so its phones are not listed here.")
			} else {
				lacks(t, "the page", body, "could not be reached")
			}
			signed := map[int]bool{}
			for _, i := range c.laptop {
				signed[i] = true
			}
			for i, f := range p.fleets {
				if i == c.view {
					continue
				}
				if !signed[i] || slices.Contains(c.gone, i) {
					lacks(t, "the page of a laptop that is not signed in to "+f.name, body, f.name, f.id)
					if got := p.counts(p.lists, f); got != 0 {
						t.Fatalf("fleet %s was asked for its phones %d times", f.name, got)
					}
				} else if got := p.counts(p.lists, f); got > 1 {
					t.Fatalf("fleet %s was asked for its phones %d times", f.name, got)
				}
			}
			lacks(t, "the page", body, "also paired", "other fleets", "other fleet")
		})
	}
}

func TestAFleetsLegacyRecordsStayThisFleetOnly(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	hash := strings.Repeat("c", 64)
	for _, f := range p.fleets {
		if _, err := f.st.AddDevice(context.Background(), "Old "+f.name, "", hash, ""); err != nil {
			t.Fatal(err)
		}
	}
	p.phoneWith("Pixel", a, b)
	body := p.phonePage(a, p.laptops(a, b)...)
	if got := strings.Count(body, "this fleet only ·"); got != 1 {
		t.Fatalf("%d legacy rows read 'this fleet only':\n%s", got, body)
	}
	contains(t, "the page", body, `aria-label="Revoke Old alpha"`, `<span class="dev-name">Pixel</span>`)
	lacks(t, "the page", body, "Old beta")
	label := p.labelOf(a, "Pixel")
	if rec := p.everywhere(a, label, p.laptops(a, b)...); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke everywhere = %d", rec.Code)
	}
	for _, f := range p.fleets {
		if got := holds(p, f); !slices.Equal(got, []string{"Old " + f.name}) {
			t.Fatalf("fleet %s holds %v", f.name, got)
		}
	}
	for what, label := range map[string]string{"an empty label": "", "no label": "x", "a short one": "abc", "an upper-case one": "ABCDEF0123456789"} {
		rec := p.everywhere(a, label, p.laptops(a, b)...)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("revoke everywhere with %s = %d", what, rec.Code)
		}
		if rec := p.revokeAt(a, b, label, p.laptops(a, b)...); rec.Code != http.StatusBadRequest {
			t.Fatalf("revoke in one fleet with %s = %d", what, rec.Code)
		}
	}
	if rec := p.everywhere(a, "abcdef0123456789", p.laptops(a, b)...); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke everywhere with the lower-case form = %d", rec.Code)
	}
	for _, f := range p.fleets {
		if got := holds(p, f); !slices.Equal(got, []string{"Old " + f.name}) || len(p.revoked(f)) != 1 {
			t.Fatalf("fleet %s holds %v after refused labels, events %v", f.name, got, p.revoked(f))
		}
	}
}

func TestRevokingEverywhereRevokesEveryFleetTheLaptopIsSignedInTo(t *testing.T) {
	cases := []struct {
		what    string
		paired  []int
		laptop  []int
		down    []int
		gone    []int
		bare    []int
		closed  []int
		status  int
		said    string
		revoked []int
		kept    []int
	}{
		{"one fleet", []int{0}, []int{0}, nil, nil, nil, nil, http.StatusSeeOther, "", []int{0}, nil},
		{"three fleets", []int{0, 1, 2}, []int{0, 1, 2}, nil, nil, nil, nil, http.StatusSeeOther, "", []int{0, 1, 2}, nil},
		{"the laptop is signed in to two of three", []int{0, 1, 2}, []int{0, 1}, nil, nil, nil, nil, http.StatusSeeOther, "", []int{0, 1}, []int{2}},
		{"a fleet the laptop holds a token for and the phone is not in", []int{0, 1}, []int{0, 1, 2}, nil, nil, nil, nil, http.StatusSeeOther, "", []int{0, 1}, nil},
		{"one fleet is down", []int{0, 1, 2}, []int{0, 1, 2}, []int{1}, nil, nil, nil, http.StatusOK, "Revoked in alpha and gamma. beta could not be reached: its record of this phone, if it has one, is still active. Press Revoke everywhere again when it is back, and also remove the phone from your tailnet if it is lost.", []int{0, 2}, []int{1}},
		{"one fleet's store fails", []int{0, 1, 2}, []int{0, 1, 2}, nil, nil, nil, []int{2}, http.StatusOK, "Revoked in alpha and beta. gamma could not be reached: its record of this phone, if it has one, is still active. Press Revoke everywhere again when it is back, and also remove the phone from your tailnet if it is lost.", []int{0, 1}, []int{2}},
		{"one fleet cannot revoke", []int{0, 1, 2}, []int{0, 1, 2}, nil, nil, []int{1}, nil, http.StatusOK, "Revoked in alpha and gamma. beta could not be reached: its record of this phone, if it has one, is still active. Press Revoke everywhere again when it is back, and also remove the phone from your tailnet if it is lost.", []int{0, 2}, []int{1}},
		{"two fleets are down", []int{0, 1, 2}, []int{0, 1, 2}, []int{1, 2}, nil, nil, nil, http.StatusOK, "Revoked in alpha. beta and gamma could not be reached: their records of this phone, if they have any, are still active. Press Revoke everywhere again when they are back, and also remove the phone from your tailnet if it is lost.", []int{0}, []int{1, 2}},
		{"every fleet that holds it is down", []int{1, 2}, []int{0, 1, 2}, []int{1, 2}, nil, nil, nil, http.StatusOK, "Nothing was revoked. beta and gamma could not be reached: their records of this phone, if they have any, are still active. Press Revoke everywhere again when they are back, and also remove the phone from your tailnet if it is lost.", nil, []int{1, 2}},
		{"a fleet was removed", []int{0, 1, 2}, []int{0, 1, 2}, nil, []int{2}, nil, nil, http.StatusSeeOther, "", []int{0, 1}, nil},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			p := newPhoneHost(t, true, "alpha", "beta", "gamma", "delta")
			paired := fleetsAt(p, c.paired...)
			cookies := p.phoneWith("Pixel", paired[0], paired[1:]...)
			label := p.labelOf(paired[0], "Pixel")
			home := p.fleets[0]
			for _, i := range c.down {
				p.setDown(p.fleets[i])
			}
			for _, i := range c.gone {
				p.setGone(p.fleets[i])
			}
			for _, i := range c.bare {
				p.bare[p.fleets[i].id] = true
			}
			p.restart()
			for _, i := range c.closed {
				if err := p.fleets[i].st.Close(); err != nil {
					t.Fatal(err)
				}
			}
			rec := p.everywhere(home, label, p.laptops(fleetsAt(p, c.laptop...)...)...)
			if rec.Code != c.status {
				t.Fatalf("revoke everywhere = %d:\n%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if c.status == http.StatusSeeOther {
				if loc := rec.Header().Get("Location"); loc != "/"+home.id+"/connect" {
					t.Fatalf("redirect to %q", loc)
				}
			} else {
				contains(t, "the result", body, c.said, `action="/`+home.id+`/connect/revoke/everywhere"`, `name="phone" value="`+label+`"`, "<button>Revoke everywhere</button>")
			}
			for i, f := range p.fleets {
				if slices.Contains(c.closed, i) {
					continue
				}
				got := holds(p, f)
				switch {
				case slices.Contains(c.revoked, i):
					if len(got) != 0 || len(p.revoked(f)) != 1 || p.revoked(f)[0] != fmt.Sprint(1)+" Pixel" {
						t.Fatalf("fleet %s holds %v and logged %v", f.name, got, p.revoked(f))
					}
				case slices.Contains(c.paired, i):
					if len(got) != 1 || len(p.revoked(f)) != 0 {
						t.Fatalf("fleet %s should still hold the phone: holds %v and logged %v", f.name, got, p.revoked(f))
					}
				}
			}
			for i, f := range p.fleets {
				if !slices.Contains(c.laptop, i) || slices.Contains(c.gone, i) {
					if got := p.counts(p.revokes, f); got != 0 {
						t.Fatalf("fleet %s the laptop is not signed in to was asked to revoke %d times", f.name, got)
					}
					lacks(t, "the result", body, f.name)
				}
			}
			for _, i := range c.revoked {
				f := p.fleets[i]
				if got := p.opens(f, operator, cookies[f.id]); got != http.StatusForbidden {
					t.Fatalf("a revoked phone opens fleet %s = %d", f.name, got)
				}
			}
			for _, i := range c.kept {
				f := p.fleets[i]
				if slices.Contains(c.closed, i) || slices.Contains(c.down, i) {
					continue
				}
				if got := p.opens(f, operator, cookies[f.id]); got != http.StatusOK {
					t.Fatalf("a phone nobody revoked in fleet %s = %d", f.name, got)
				}
			}
			if c.status == http.StatusOK && len(c.down) > 0 && len(c.closed) == 0 && len(c.bare) == 0 {
				for _, i := range c.down {
					p.mu.Lock()
					delete(p.down, p.fleets[i].id)
					p.mu.Unlock()
				}
				again := p.everywhere(home, label, p.laptops(fleetsAt(p, c.laptop...)...)...)
				if again.Code != http.StatusSeeOther {
					t.Fatalf("the repeat once the fleets are back = %d:\n%s", again.Code, again.Body.String())
				}
				for _, i := range c.paired {
					f := p.fleets[i]
					if got := holds(p, f); len(got) != 0 || len(p.revoked(f)) != 1 {
						t.Fatalf("after the repeat fleet %s holds %v and logged %v", f.name, got, p.revoked(f))
					}
				}
			}
		})
	}
}

func TestRepeatingARevokeEverywhereIsHarmless(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	p.phoneWith("Pixel", a, b, c)
	keep := p.phoneWith("Tablet", a, b, c)
	label := p.labelOf(a, "Pixel")
	var first *httptest.ResponseRecorder
	for i := range 4 {
		rec := p.everywhere(a, label, p.laptops(a, b, c)...)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/"+a.id+"/connect" {
			t.Fatalf("revoke %d = %d", i, rec.Code)
		}
		if first == nil {
			first = rec
		}
		sameResponse(t, "a revoke and its repeat", first, rec)
	}
	for _, f := range p.fleets {
		if got := holds(p, f); !slices.Equal(got, []string{"Tablet"}) {
			t.Fatalf("fleet %s holds %v", f.name, got)
		}
		if got := p.revoked(f); len(got) != 1 {
			t.Fatalf("fleet %s logged %v", f.name, got)
		}
		if got := p.opens(f, operator, keep[f.id]); got != http.StatusOK {
			t.Fatalf("another phone in fleet %s = %d", f.name, got)
		}
	}
}

func TestRevokingInOneFleetTouchesThatFleetAlone(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	cookies := p.phoneWith("Pixel", a, b, c)
	label := p.labelOf(a, "Pixel")
	if rec := p.revokeAt(a, b, label, p.laptops(a, b, c)...); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/"+a.id+"/connect" {
		t.Fatalf("revoke in beta = %d", rec.Code)
	}
	for i, f := range p.fleets {
		want := map[int]int{0: 1, 1: 0, 2: 1}[i]
		if got := len(p.devicesOf(f)); got != want || len(p.revoked(f)) != 1-want {
			t.Fatalf("fleet %s holds %d and logged %v", f.name, got, p.revoked(f))
		}
	}
	if got := p.opens(b, operator, cookies[b.id]); got != http.StatusForbidden {
		t.Fatalf("beta after its revoke = %d", got)
	}
	for _, f := range []railFleet{a, c} {
		if got := p.opens(f, operator, cookies[f.id]); got != http.StatusOK {
			t.Fatalf("fleet %s = %d", f.name, got)
		}
	}
	if rec := p.revokeAt(a, a, label, p.laptops(a, b, c)...); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke in the current fleet = %d", rec.Code)
	}
	if got := len(p.devicesOf(a)); got != 0 || len(p.devicesOf(c)) != 1 {
		t.Fatalf("alpha holds %d, gamma %d", got, len(p.devicesOf(c)))
	}
	if rec := p.revokeAt(a, b, label, p.laptops(a, b, c)...); rec.Code != http.StatusSeeOther {
		t.Fatalf("a repeated revoke in beta = %d", rec.Code)
	}
	if got := p.revoked(b); len(got) != 1 {
		t.Fatalf("beta logged %v", got)
	}
}

func TestRevokingInAFleetThatIsDownSaysSoAndOffersTheSamePress(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	cookies := p.phoneWith("Pixel", a, b)
	label := p.labelOf(a, "Pixel")
	p.setDown(b)
	rec := p.revokeAt(a, b, label, p.laptops(a, b)...)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke in a fleet that is down = %d", rec.Code)
	}
	contains(t, "the result", rec.Body.String(),
		"Nothing was revoked. beta could not be reached: its record of this phone, if it has one, is still active. Press Revoke again when it is back, and also remove the phone from your tailnet if it is lost.",
		`action="/`+a.id+`/connect/revoke/in"`, `name="fleet" value="`+b.id+`"`, `name="phone" value="`+label+`"`, "<button>Revoke</button>")
	if len(p.devicesOf(a)) != 1 || len(p.revoked(a)) != 0 {
		t.Fatal("a revoke in beta touched alpha")
	}
	p.mu.Lock()
	delete(p.down, b.id)
	p.mu.Unlock()
	if rec := p.revokeAt(a, b, label, p.laptops(a, b)...); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke once beta is back = %d", rec.Code)
	}
	if got := p.opens(b, operator, cookies[b.id]); got != http.StatusForbidden || len(p.devicesOf(a)) != 1 {
		t.Fatalf("beta = %d, alpha holds %d", got, len(p.devicesOf(a)))
	}
}

func TestALabelOnlyAnUnprovenFleetHoldsIsNeverRevealed(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	p.phoneWith("Visible", a, b)
	hidden := p.phoneWith("Hidden", c)
	secret := p.labelOf(c, "Hidden")
	laptop := p.laptops(a, b)
	p.resetCounts()
	page := p.phonePage(a, laptop...)
	lacks(t, "the page", page, "Hidden", secret, c.name, c.id)
	nowhere := "0123456789abcdef"
	sameResponse(t, "a label held only by an unproven fleet and a label nobody holds", p.everywhere(a, secret, laptop...), p.everywhere(a, nowhere, laptop...))
	sameResponse(t, "the same, revoking in one proven fleet", p.revokeAt(a, b, secret, laptop...), p.revokeAt(a, b, nowhere, laptop...))
	sameResponse(t, "an unproven fleet and an unregistered one", p.revokeAt(a, c, secret, laptop...), p.revokeAt(a, railFleet{id: "f00000000dead"}, secret, laptop...))
	if rec := p.revokeAt(a, c, secret, laptop...); rec.Code != http.StatusForbidden {
		t.Fatalf("revoke in an unproven fleet = %d", rec.Code)
	} else {
		lacks(t, "the refusal", rec.Body.String(), "Hidden", secret, c.name, c.id)
	}
	if got := holds(p, c); !slices.Equal(got, []string{"Hidden"}) || len(p.revoked(c)) != 0 {
		t.Fatalf("gamma holds %v and logged %v", got, p.revoked(c))
	}
	if p.counts(p.lists, c) != 0 || p.counts(p.revokes, c) != 0 || p.counts(p.proofs, c) != 0 {
		t.Fatal("the laptop's request reached a fleet it is not signed in to")
	}
	if got := p.opens(c, operator, hidden[c.id]); got != http.StatusOK {
		t.Fatalf("the hidden phone = %d", got)
	}
}

func TestAPhoneSessionCannotListRevokeOrLearnOfAnotherPhone(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	mine := p.phoneWith("Pixel", a, b, c)
	other := p.phoneWith("Tablet", a, b, c)
	own, theirs := p.labelOf(a, "Pixel"), p.labelOf(a, "Tablet")
	sent := []*http.Cookie{mine[a.id], mine[b.id], mine[c.id]}
	token := func(f railFleet) string {
		return csrfRe.FindStringSubmatch(p.send("GET", "/"+f.id+"/connect", nil, sent, operator).Body.String())[1]
	}
	theirID := strings.SplitN(other[a.id].Value, ".", 2)[0]
	p.resetCounts()
	page := p.send("GET", "/"+a.id+"/connect", nil, sent, operator).Body.String()
	lacks(t, "a phone's page", page, "Tablet", own, theirs, "Revoke everywhere", "/connect/revoke/", "dev-fleets", "Paired phones")
	for _, f := range p.fleets {
		if got := p.counts(p.lists, f); got != 0 {
			t.Fatalf("a phone's page listed fleet %s", f.name)
		}
	}
	for _, route := range []string{"everywhere", "in"} {
		for _, f := range p.fleets {
			for what, form := range map[string]url.Values{
				"its own label":           {"phone": {own}, "fleet": {a.id}},
				"the other phone":         {"phone": {theirs}, "fleet": {b.id}},
				"another of its fleets":   {"phone": {own}, "fleet": {c.id}},
				"a label nobody holds":    {"phone": {"0123456789abcdef"}, "fleet": {a.id}},
				"no label":                {"fleet": {a.id}},
				"an unregistered fleet":   {"phone": {own}, "fleet": {"f00000000dead"}},
				"no fleet":                {"phone": {own}},
				"a label and every fleet": {"phone": {own}, "fleet": {a.id, b.id, c.id}},
			} {
				for _, csrf := range []string{token(f), f.token, "", "x"} {
					form.Set("csrf", csrf)
					rec := p.send("POST", "/"+f.id+"/connect/revoke/"+route, form, sent, operator)
					if rec.Code != http.StatusForbidden {
						t.Fatalf("a phone posted %s to /%s/connect/revoke/%s with csrf %q = %d:\n%s", what, f.name, route, csrf, rec.Code, rec.Body.String())
					}
					lacks(t, "the refusal", rec.Body.String(), "Tablet", theirs, own, "could not be reached")
				}
			}
		}
	}
	for what, form := range map[string]url.Values{
		"the other phone's number": {"id": {theirID}},
		"a number nobody holds":    {"id": {"99"}},
	} {
		form.Set("csrf", token(a))
		if rec := p.send("POST", "/"+a.id+"/connect/revoke", form, sent, operator); rec.Code != http.StatusForbidden {
			t.Fatalf("a phone posted %s = %d", what, rec.Code)
		}
	}
	cleared := p.send("POST", "/"+b.id+"/connect/revoke", url.Values{"csrf": {token(b)}, "id": {strings.SplitN(mine[b.id].Value, ".", 2)[0]}}, sent, operator)
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body.String(), "This phone is signed out") {
		t.Fatalf("a phone signing itself out of beta = %d:\n%s", cleared.Code, cleared.Body.String())
	}
	for i, f := range p.fleets {
		want := []string{"Pixel", "Tablet"}
		if i == 1 {
			want = []string{"Tablet"}
		}
		if got := holds(p, f); !slices.Equal(got, want) {
			t.Fatalf("fleet %s holds %v after a phone tried every action", f.name, got)
		}
		wantEvents := 0
		if i == 1 {
			wantEvents = 1
		}
		if got := p.revoked(f); len(got) != wantEvents {
			t.Fatalf("fleet %s logged %v", f.name, got)
		}
		if got := p.counts(p.revokes, f); got != 0 {
			t.Fatalf("fleet %s was asked to revoke by a phone %d times", f.name, got)
		}
		if got := p.opens(f, operator, other[f.id]); got != http.StatusOK {
			t.Fatalf("the other phone in fleet %s = %d", f.name, got)
		}
	}
}

func TestAPhoneHoldingALaptopsLoginForAnotherFleetStillRevokesNothing(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	mine := p.phoneWith("Pixel", a, b)
	label := p.labelOf(a, "Pixel")
	csrf := csrfRe.FindStringSubmatch(p.send("GET", "/"+a.id+"/connect", nil, []*http.Cookie{mine[a.id]}, operator).Body.String())[1]
	sent := []*http.Cookie{mine[a.id], mine[b.id], p.laptop(b)}
	for _, route := range []string{"in", "everywhere"} {
		rec := p.send("POST", "/"+a.id+"/connect/revoke/"+route, url.Values{"csrf": {csrf}, "phone": {label}, "fleet": {b.id}}, sent, operator)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s = %d", route, rec.Code)
		}
	}
	if len(p.devicesOf(a)) != 1 || len(p.devicesOf(b)) != 1 || p.counts(p.revokes, b) != 0 {
		t.Fatal("a phone revoked through a laptop login it carried")
	}
}

func TestACrossFleetRevokeNeedsTheLaptopsProofForEveryFleet(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	cookies := p.phoneWith("Pixel", a, b, c)
	label := p.labelOf(a, "Pixel")
	stale := func(f railFleet, token string) *http.Cookie {
		return &http.Cookie{Name: "hand_board_" + f.id, Value: token}
	}
	untouched := func(what string) {
		t.Helper()
		for _, f := range p.fleets {
			if len(p.devicesOf(f)) != 1 || len(p.revoked(f)) != 0 {
				t.Fatalf("%s: fleet %s holds %v and logged %v", what, f.name, holds(p, f), p.revoked(f))
			}
		}
	}
	send := func(home railFleet, route, csrf string, target railFleet, jar ...*http.Cookie) *httptest.ResponseRecorder {
		form := url.Values{"csrf": {csrf}, "phone": {label}}
		if target.id != "" {
			form.Set("fleet", target.id)
		}
		return p.send("POST", "/"+home.id+"/connect/revoke/"+route, form, jar, nil)
	}
	for what, c := range map[string]*httptest.ResponseRecorder{
		"alpha's form token at beta's route":            send(b, "everywhere", a.token, railFleet{}, p.laptops(a, b)...),
		"alpha's form token at beta's route, one fleet": send(b, "in", a.token, b, p.laptops(a, b)...),
		"beta's form token at alpha's route":            send(a, "in", b.token, b, p.laptops(a, b)...),
		"no form token":                                 send(a, "everywhere", "", railFleet{}, p.laptops(a, b, c)...),
		"alpha's token with no login for beta":          send(a, "in", a.token, b, p.laptops(a)...),
		"a stale login for beta":                        send(a, "in", a.token, b, p.laptop(a), stale(b, strings.Repeat("0", 32))),
		"gamma's login under beta's name":               send(a, "in", a.token, b, p.laptop(a), stale(b, c.token)),
		"a phone's own cookie for beta":                 send(a, "in", a.token, b, p.laptop(a), cookies[b.id]),
		"an unregistered fleet":                         send(a, "in", a.token, railFleet{id: "f00000000dead"}, p.laptops(a, b)...),
		"a fleet id that is a path":                     send(a, "in", a.token, railFleet{id: "../" + b.id}, p.laptops(a, b)...),
	} {
		if c.Code != http.StatusForbidden {
			t.Fatalf("%s = %d:\n%s", what, c.Code, c.Body.String())
		}
		untouched(what)
	}
	if got := p.counts(p.revokes, b) + p.counts(p.revokes, c); got != 0 {
		t.Fatalf("beta and gamma were asked to revoke %d times", got)
	}
	if rec := send(a, "in", a.token, b, p.laptop(a), p.laptop(b), cookies[c.id]); rec.Code != http.StatusSeeOther {
		t.Fatalf("alpha's form token with a login for beta = %d", rec.Code)
	}
	if len(p.devicesOf(b)) != 0 || len(p.devicesOf(a)) != 1 || len(p.devicesOf(c)) != 1 {
		t.Fatalf("holds %v, %v, %v", holds(p, a), holds(p, b), holds(p, c))
	}
	if got := p.counts(p.proofs, a) + p.counts(p.proofs, b); got != 0 {
		t.Fatalf("a laptop's revoke asked for %d phone proofs", got)
	}
}

func TestEachFleetsLoginIsCheckedAgainForEachFleetOfARevoke(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	p.phoneWith("Pixel", a, b, c)
	label := p.labelOf(a, "Pixel")
	rotated := &http.Cookie{Name: "hand_board_" + c.id, Value: strings.Repeat("7", 32)}
	rec := p.everywhere(a, label, p.laptop(a), p.laptop(b), rotated)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke everywhere = %d", rec.Code)
	}
	if got := holds(p, c); len(got) != 1 || p.counts(p.revokes, c) != 0 {
		t.Fatalf("gamma, whose login the laptop no longer holds, = %v, asked %d times", got, p.counts(p.revokes, c))
	}
	for _, f := range []railFleet{a, b} {
		if len(p.devicesOf(f)) != 0 {
			t.Fatalf("fleet %s still holds the phone", f.name)
		}
	}
}

func TestARevokedPhoneIsSignedOutOfEveryFleetAndItsStreamsClose(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	cookies := p.phoneWith("Pixel", a, b, c)
	keep := p.phoneWith("Tablet", a, b, c)
	base := serve(t, p.h)
	open := func(f railFleet) *bufio.Scanner {
		req, _ := http.NewRequest("GET", base+"/"+f.id+"/events", nil)
		req.AddCookie(cookies[f.id])
		for k, v := range operator {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("events of %s = %v, %v", f.name, res, err)
		}
		t.Cleanup(func() { res.Body.Close() })
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		if !sc.Scan() {
			t.Fatal("the stream said nothing")
		}
		return sc
	}
	streams := []*bufio.Scanner{open(a), open(c)}
	rec := p.everywhere(a, p.labelOf(a, "Pixel"), p.laptops(a, b, c)...)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke everywhere = %d", rec.Code)
	}
	for _, f := range p.fleets {
		got := p.send("GET", "/"+f.id+"/", nil, []*http.Cookie{cookies[f.id]}, operator)
		if got.Code != http.StatusForbidden {
			t.Fatalf("a revoked phone opens fleet %s = %d", f.name, got.Code)
		}
		contains(t, "the page of fleet "+f.name, got.Body.String(), "This phone is no longer paired with this fleet", "Phone, in its menu")
		lacks(t, "the page of fleet "+f.name, got.Body.String(), f.name, a.name+" ")
		if got := p.opens(f, operator, keep[f.id]); got != http.StatusOK {
			t.Fatalf("another phone in fleet %s = %d", f.name, got)
		}
		post := p.send("POST", "/"+f.id+"/supervisor/send", url.Values{"csrf": {"x"}, "text": {"x"}}, []*http.Cookie{cookies[f.id]}, operator)
		if post.Code != http.StatusForbidden {
			t.Fatalf("a revoked phone posts to fleet %s = %d", f.name, post.Code)
		}
	}
	list := p.send("GET", "/", nil, []*http.Cookie{cookies[a.id], cookies[b.id], cookies[c.id]}, operator).Body.String()
	lacks(t, "the host list of a revoked phone", list, a.name, b.name, c.name, a.id, b.id, c.id)
	for i, sc := range streams {
		done := make(chan struct{})
		go func() {
			defer close(done)
			for sc.Scan() {
			}
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("stream %d of a revoked phone stayed open", i)
		}
	}
}

func TestARevokeEverywhereOnAReadOnlyBoardReachesNoOtherFleet(t *testing.T) {
	p := newPhoneHost(t, false, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	for _, f := range p.fleets {
		if _, err := f.st.AddDevice(context.Background(), "Pixel", "", strings.Repeat("d", 64), "0123456789abcdef"); err != nil {
			t.Fatal(err)
		}
	}
	laptop := p.laptops(a, b)
	page := p.phonePage(a, laptop...)
	lacks(t, "the page", page, b.name, b.id, "dev-fleets", "revoke/everywhere", "Revoke everywhere")
	contains(t, "the page", page, `aria-label="Revoke Pixel"`)
	if rec := p.revokeAt(a, b, "0123456789abcdef", laptop...); rec.Code != http.StatusForbidden {
		t.Fatalf("revoke in another fleet = %d", rec.Code)
	}
	if rec := p.everywhere(a, "0123456789abcdef", laptop...); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke everywhere = %d", rec.Code)
	}
	if len(p.devicesOf(a)) != 0 || len(p.devicesOf(b)) != 1 || p.counts(p.revokes, b)+p.counts(p.lists, b) != 0 {
		t.Fatalf("alpha holds %v, beta %v", holds(p, a), holds(p, b))
	}
}

func TestARevokeThroughFunnelRevokesNothing(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	p.phoneWith("Pixel", a, b)
	label := p.labelOf(a, "Pixel")
	funnel := map[string]string{"Tailscale-Funnel-Request": "?1", "X-Forwarded-For": "203.0.113.9", "X-Forwarded-Proto": "https"}
	for _, route := range []string{"in", "everywhere"} {
		form := url.Values{"csrf": {a.token}, "phone": {label}, "fleet": {b.id}}
		rec := p.send("POST", "/"+a.id+"/connect/revoke/"+route, form, p.laptops(a, b), funnel)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "does not answer the public internet") {
			t.Fatalf("%s through Funnel = %d", route, rec.Code)
		}
	}
	if len(p.devicesOf(a)) != 1 || len(p.devicesOf(b)) != 1 {
		t.Fatal("a Funnel request revoked a phone")
	}
}

func TestARecordOlderThanNinetyDaysIsListedAndRevokedLikeAnyOther(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	p.phoneWith("Pixel", a, b)
	label := p.labelOf(a, "Pixel")
	p.advance(91 * 24 * time.Hour)
	body := p.phonePage(a, p.laptops(a, b)...)
	if got := strings.Count(body, "signed out after 90 days"); got != 2 {
		t.Fatalf("%d fleets read 'signed out after 90 days':\n%s", got, body)
	}
	if rec := p.everywhere(a, label, p.laptops(a, b)...); rec.Code != http.StatusSeeOther || len(p.devicesOf(a))+len(p.devicesOf(b)) != 0 {
		t.Fatalf("revoke = %d", rec.Code)
	}
}

func TestTwoPhonesWithOneNameAndLoginNeverMerge(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	const n = 12
	for range n {
		p.phoneWith("Pixel", a, b, c)
		p.advance(time.Second)
	}
	labels := map[string]bool{}
	for _, d := range p.devicesOf(a) {
		labels[d.Phone] = true
	}
	if len(labels) != n {
		t.Fatalf("%d labels for %d pairings", len(labels), n)
	}
	body := p.phonePage(a, p.laptops(a, b, c)...)
	if got := strings.Count(body, `class="device device-group"`); got != n {
		t.Fatalf("%d group rows for %d phones", got, n)
	}
	first := p.devicesOf(a)[0].Phone
	if rec := p.everywhere(a, first, p.laptops(a, b, c)...); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke = %d", rec.Code)
	}
	for _, f := range p.fleets {
		list := p.devicesOf(f)
		if len(list) != n-1 {
			t.Fatalf("fleet %s holds %d records after revoking one of %d phones", f.name, len(list), n)
		}
		for _, d := range list {
			if d.Phone == first {
				t.Fatalf("fleet %s still holds a record of the revoked phone", f.name)
			}
		}
		if len(p.revoked(f)) != 1 {
			t.Fatalf("fleet %s logged %v", f.name, p.revoked(f))
		}
	}
}

func TestRevokingWritesNoLabelCodeSecretOrTokenToTheLogOrTheEvents(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	code := p.codeOf(p.mint(a, p.laptops(a, b, c), b, c))
	rec := p.redeem(a, code, nil, operator)
	cookies := phoneCookiesOf(rec)
	secret := strings.SplitN(cookies[a.id].Value, ".", 2)[1]
	label := p.labelOf(a, "Pixel")
	p.setDown(b)
	if err := c.st.Close(); err != nil {
		t.Fatal(err)
	}
	p.everywhere(a, label, p.laptops(a, b, c)...)
	p.revokeAt(a, b, label, p.laptops(a, b, c)...)
	p.phonePage(a, p.laptops(a, b, c)...)
	hidden := []string{label, secret, code, strings.ReplaceAll(code, "-", ""), a.token, b.token, c.token}
	for _, bad := range hidden {
		if strings.Contains(buf.String(), bad) {
			t.Fatalf("the board logged %q in %q", bad, buf.String())
		}
	}
	for _, want := range []string{"revoking a phone could not reach fleet " + b.id, "revoking a phone in fleet " + c.id + " failed"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("the log lacks %q: %q", want, buf.String())
		}
	}
	events, err := a.st.RecentEvents(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		for _, bad := range hidden {
			if strings.Contains(e.Detail, bad) {
				t.Fatalf("event %s %q holds %q", e.Kind, e.Detail, bad)
			}
		}
	}
	if got := p.revoked(a); len(got) != 1 || got[0] != "1 Pixel" {
		t.Fatalf("alpha's revoked events = %v", got)
	}
}

func TestEveryNewFormOnThePhonePageHoldsNoInlineCodeAndKeepsTheCopyableText(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	p.phoneWith("Pixel", a, b, c)
	p.phoneWith("Tablet", b, c)
	label := p.labelOf(a, "Pixel")
	p.setDown(c)
	partial := p.everywhere(a, label, p.laptops(a, b, c)...)
	codePage := p.mint(a, p.laptops(a, b, c), b)
	pages := map[string]string{
		"list":    p.phonePage(a, p.laptops(a, b)...),
		"partial": partial.Body.String(),
		"code":    codePage.Body.String(),
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
		for _, f := range p.fleets {
			lacks(t, name+" page", body, ">"+f.token)
		}
		for _, m := range csrfRe.FindAllStringSubmatch(body, -1) {
			if m[1] != a.token {
				t.Errorf("%s page carries csrf %q", name, m[1])
			}
		}
	}
	code := p.codeOf(codePage)
	contains(t, "the code page", pages["code"], `data-copy="link" data-text="https://laptop.example/`+a.id+`/pair#`+code+`"`, `<code class="ref">https://laptop.example/`+a.id+`/pair#`+code+`</code>`, `<p class="code-big" aria-label="Pairing code `+code+`">`+code+`</p>`, `class="device device-group"`)
	lacks(t, "the code page", pages["code"], `class="revoke-note"`)
	contains(t, "the partial page", pages["partial"], `class="doc-notice revoke-note" role="status"`)
	if strings.Count(pages["list"], "<button") != strings.Count(pages["list"], "</button>") {
		t.Fatal("a button is not closed")
	}
	for _, m := range regexp.MustCompile(`<button[^>]*>`).FindAllString(pages["list"], -1) {
		if strings.Contains(m, "height") || strings.Contains(m, "style") {
			t.Fatalf("a control sets its own size: %s", m)
		}
	}
}

func TestTheLabelsOfSeveralPhonesAreNeverShownToAPhone(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	cookies := p.phoneWith("Pixel", a, b)
	label := p.labelOf(a, "Pixel")
	sent := []*http.Cookie{cookies[a.id], cookies[b.id]}
	for _, f := range p.fleets {
		for _, path := range []string{"/", "/connect", "/tasks", "/supervisor/log", "/pair"} {
			rec := p.send("GET", "/"+f.id+path, nil, sent, operator)
			lacks(t, "GET /"+f.name+path, rec.Body.String(), label)
			for k, v := range rec.Result().Header {
				if strings.Contains(strings.Join(v, ""), label) {
					t.Fatalf("the label is in the %s header", k)
				}
			}
		}
	}
	for _, c := range sent {
		if strings.Contains(c.Value, label) || strings.Contains(c.Name, label) {
			t.Fatalf("the label is in the cookie %s", c.Name)
		}
	}
}
