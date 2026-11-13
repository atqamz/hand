package board_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestThePhonePagesStyleEveryPartTheyAdd(t *testing.T) {
	css := asset(t, "board.css")
	for _, rule := range []string{
		".connect-part h3{margin:0}",
		".part-body{display:grid;gap:var(--space-10);padding:var(--space-12)}",
		".part-body>p{margin:0}",
		".net{display:grid;gap:var(--space-10);margin:0;padding:0;list-style:none}",
		".net>li{display:flex;align-items:baseline;gap:var(--space-10);min-width:0}",
		".net>li>.badge{flex:none}",
		".net>li>span:last-child{min-width:0;overflow-wrap:anywhere}",
		".steps{display:grid;gap:var(--space-6);margin:0;padding-left:var(--space-20)}",
		".pair-code{display:flex;flex-wrap:wrap;align-items:flex-start;gap:var(--space-16)}",
		".pair-copy{display:grid;flex:1 1 260px;gap:var(--space-8);min-width:0}",
		".pair-copy>p{margin:0}",
		".qr{display:block;flex:none;width:min(100%,280px);border:1px solid var(--line)}",
		".pair-link{display:flex;flex-wrap:wrap;align-items:center;gap:var(--space-8)}",
		".connect-part code{white-space:normal;overflow-wrap:anywhere}",
		".code-big{font-family:var(--mono);font-size:22px;line-height:30px;font-weight:600;letter-spacing:.06em}",
		".pair-form{display:grid;gap:var(--space-8);max-width:440px}",
		".pair-form input{width:100%}",
		".pair-form button{justify-self:start}",
		".field-error{margin:0;color:var(--fail);font-size:14px}",
		".devices{margin:0;padding:0;list-style:none}",
		".device{display:flex;align-items:center;gap:var(--space-12);padding:var(--space-8) 0;border-bottom:1px solid var(--line)}",
		".dev-text{display:grid;flex:1;gap:var(--space-2);min-width:0;overflow-wrap:anywhere}",
		".dev-note{color:var(--muted);font-size:12px}",
	} {
		contains(t, "board.css", css, "\n"+rule+"\n")
	}
	block := css[strings.Index(css, "\n.connect-part h3{margin:0}"):strings.Index(css, "\n.pair-fleets{")]
	lacks(t, "the phone rules", block, "user-select", "min-height", "\nheight:", ";height:", "{height:", "#fff", "#000", "!important", "@media")
	for _, m := range regexp.MustCompile(`font-size:(\d+)px`).FindAllStringSubmatch(block, -1) {
		switch m[1] {
		case "12", "14", "15", "18", "22":
		default:
			t.Errorf("the phone rules use a %spx font size", m[0])
		}
	}
	if strings.Count(block, "\n") > 27 {
		t.Fatalf("the phone rules grew past the pinned set:\n%s", block)
	}
}

func TestThePairPageStylesTheFleetChoices(t *testing.T) {
	css := asset(t, "board.css")
	rules := []string{
		".pair-fleets{display:grid;gap:var(--space-2);min-width:0;margin:0;padding:0;border:0}",
		".pair-fleets legend{padding:0;margin-bottom:var(--space-4)}",
		".pair-fleet{display:flex;align-items:center;gap:var(--space-10);padding-block:calc((var(--control) - var(--space-20))/2);line-height:20px;cursor:pointer}",
		".pair-fleets .pair-fleet>input{flex:none;width:18px;height:18px;margin:0;padding:0;accent-color:var(--accent)}",
	}
	for _, rule := range rules {
		contains(t, "board.css", css, "\n"+rule+"\n")
	}
	block := css[strings.Index(css, "\n.pair-fleets{"):strings.Index(css, "\n.device-group{")]
	lacks(t, "the fleet choice rules", block, "user-select", "min-height", "#fff", "#000", "!important", "@media")
	if strings.Count(block, "\n") != len(rules) {
		t.Fatalf("the fleet choice rules grew past the pinned set:\n%s", block)
	}
}

func TestThePhonePageStylesAPhoneListedWithItsFleets(t *testing.T) {
	css := asset(t, "board.css")
	rules := []string{
		".device-group{flex-wrap:wrap}",
		".dev-fleets{display:grid;flex:1 0 100%;gap:var(--space-2);margin:0;padding:0 0 0 var(--space-12);border-left:2px solid var(--line);list-style:none}",
		".dev-fleet{display:flex;align-items:center;justify-content:space-between;gap:var(--space-12)}",
		".dev-fleet>span{min-width:0;overflow-wrap:anywhere}",
		".revoke-note{display:grid;gap:var(--space-10);justify-items:start}",
	}
	for _, rule := range rules {
		contains(t, "board.css", css, "\n"+rule+"\n")
	}
	block := css[strings.Index(css, "\n.device-group{"):]
	lacks(t, "the grouped phone rules", block, "user-select", "min-height", "height:", "#fff", "#000", "!important", "@media")
	if strings.Count(block, "\n") != len(rules)+1 {
		t.Fatalf("the grouped phone rules grew past the pinned set:\n%s", block)
	}
}

func TestThePhoneScriptPinsItsFlow(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`const pairForm = document.querySelector("form[data-pair]");`,
		`code.value = decodeURIComponent(location.hash.slice(1));`,
		`history.replaceState(null, "", location.pathname);`,
		`(code.value ? pairForm.querySelector("#name") : code).focus();`,
		`const addressForm = document.querySelector("form[data-address]");`,
		"const addressKey = `hand.phone.address${body.dataset.base}`;",
		`field.value = localStorage.getItem(addressKey) || field.value`,
		`if (typed && typed !== detected) localStorage.setItem(addressKey, typed);`,
		`else localStorage.removeItem(addressKey);`,
		`body.dataset.session === "phone" ? "Signed out: pair again from the host's Board" : "Log in again: hand open --print"`,
		`{ path: "Path", link: "Link" }[b.dataset.copy]`)
	lacks(t, "app.js", js, `location.hash = `, `location.search`, `eval(`, "document.write")
	if n := strings.Count(js, "const pairForm ="); n != 1 {
		t.Fatalf("%d declarations of pairForm", n)
	}
}

func TestThePhonePagesAreStaticMarkupWithNoInlineCode(t *testing.T) {
	p := newPhoneRig(t)
	code := p.code()
	phone := p.pair()
	pages := map[string]string{
		"pair":     p.do("GET", "/pair", nil, nil, viaServe).Body.String(),
		"pair bad": p.tryPair("AAAAA-AAAAA", "100.64.0.7").Body.String(),
		"connect":  p.do("GET", "/connect", nil, host, nil).Body.String(),
		"code":     p.do("POST", "/connect/code", url.Values{"csrf": {token}, "base": {"https://host.tailnet.example"}}, host, nil).Body.String(),
		"phone":    p.do("GET", "/connect", nil, phone, viaServe).Body.String(),
		"revoked":  p.do("GET", "/", nil, &http.Cookie{Name: phone.Name, Value: "1." + strings.Repeat("0", 64)}, viaServe).Body.String(),
		"funnel":   p.do("GET", "/", nil, host, map[string]string{"Tailscale-Funnel-Request": "?1"}).Body.String(),
	}
	_ = code
	inline := regexp.MustCompile(`(?i)\sstyle=|<style|<script[^>]*>[^<]|\son[a-z]+=|javascript:`)
	for name, body := range pages {
		if m := inline.FindString(body); m != "" {
			t.Errorf("%s page holds inline code %q", name, m)
		}
		lacks(t, name+" page", body, "Desk", "<img ")
		if name != "code" {
			lacks(t, name+" page", body, `<svg class="qr"`)
		}
		if n := strings.Count(body, "<script"); n != 1 {
			t.Errorf("%s page has %d script tags, want the one app.js", name, n)
		}
	}
	contains(t, "pair", pages["pair"], `<input id="code" name="code" required autocomplete="one-time-code" autocapitalize="characters"`, `<input id="name" name="name" maxlength="40" autocomplete="off" value="Phone">`, `<button class="primary">Pair</button>`)
	lacks(t, "pair", pages["pair"], `<input type="hidden"`, `name="csrf"`, `name="base"`)
	contains(t, "code", pages["code"], `<div class="pair-code">`+"\n"+`<svg class="qr" viewBox="0 0 41 41" role="img" aria-label="QR code for the pairing link"`, `<div class="pair-copy">`, `<p class="pair-link"><code class="ref">`, `<p class="code-big"`, `<button type="button" class="doc-copy" data-copy="link"`)
	for _, name := range []string{"connect", "code"} {
		contains(t, name, pages[name], `<label for="base">Address the phone opens</label>`, `<input id="base" name="base" type="url" inputmode="url"`, `class="pair-form" method="post" action="/connect/code" data-address`)
	}
	contains(t, "connect", pages["connect"], `<button aria-label="Revoke Pixel">Revoke</button>`, `class="device"`)
	for _, name := range []string{"pair", "pair bad", "phone", "revoked", "funnel"} {
		lacks(t, name+" page", pages[name], token, phone.Value, strings.SplitN(phone.Value, ".", 2)[1])
	}
}
