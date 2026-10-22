package board_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
)

func serveJSON(host, path, proxy string) string {
	return `{"TCP":{"443":{"HTTPS":true}},"Web":{"` + host + `":{"Handlers":{"` + path + `":{"Proxy":"` + proxy + `"}}}}}`
}

func TestServeLinkFindsTheBoard(t *testing.T) {
	const name = "laptop.tailnet.example"
	for _, c := range []struct {
		what, json, addr, link, problem string
		funnel                          bool
	}{
		{"root proxy", serveJSON(name+":443", "/", "http://127.0.0.1:7777"), "127.0.0.1:7777", "https://" + name, "", false},
		{"localhost proxy", serveJSON(name+":443", "/", "http://localhost:7777"), "127.0.0.1:7777", "https://" + name, "", false},
		{"trailing slash", serveJSON(name+":443", "/", "http://127.0.0.1:7777/"), "127.0.0.1:7777", "https://" + name, "", false},
		{"another https port", serveJSON(name+":8443", "/", "http://127.0.0.1:7777"), "127.0.0.1:7777", "https://" + name + ":8443", "", false},
		{"ipv6 loopback board", serveJSON(name+":443", "/", "http://[::1]:7777"), "[::1]:7777", "https://" + name, "", false},
		{"a board on a lan address", serveJSON(name+":443", "/", "http://192.168.1.5:7777"), "192.168.1.5:7777", "https://" + name, "", false},
		{"ipv6 proxy to an ipv4 board", serveJSON(name+":443", "/", "http://[::1]:7777"), "127.0.0.1:7777", "", "does not proxy", false},
		{"path mount", serveJSON(name+":443", "/board", "http://127.0.0.1:7777"), "127.0.0.1:7777", "", "under /board", false},
		{"funnel on the root", `{"Web":{"` + name + `:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7777"}}}},"AllowFunnel":{"` + name + `:443":true}}`, "127.0.0.1:7777", "https://" + name, "", true},
		{"funnel on a path mount", `{"Web":{"` + name + `:443":{"Handlers":{"/b":{"Proxy":"http://127.0.0.1:7777"}}}},"AllowFunnel":{"` + name + `:443":true}}`, "127.0.0.1:7777", "", "under /b", true},
		{"funnel off for a listed other name", `{"Web":{"` + name + `:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7777"}}}},"AllowFunnel":{"other.example:443":true}}`, "127.0.0.1:7777", "https://" + name, "", false},
		{"tcp forward", `{"TCP":{"443":{"TCPForward":"127.0.0.1:7777"}}}`, "127.0.0.1:7777", "", "does not proxy", false},
		{"terminated tls forward", `{"TCP":{"443":{"TCPForward":"127.0.0.1:7777","TerminateTLS":"` + name + `"}}}`, "127.0.0.1:7777", "", "does not proxy", false},
		{"other port", serveJSON(name+":443", "/", "http://127.0.0.1:3000"), "127.0.0.1:7777", "", "does not proxy", false},
		{"other host", serveJSON(name+":443", "/", "http://10.0.0.9:7777"), "127.0.0.1:7777", "", "does not proxy", false},
		{"tls backend", serveJSON(name+":443", "/", "https+insecure://127.0.0.1:7777"), "127.0.0.1:7777", "", "does not proxy", false},
		{"backend path", serveJSON(name+":443", "/", "http://127.0.0.1:7777/app"), "127.0.0.1:7777", "", "does not proxy", false},
		{"unix backend", serveJSON(name+":443", "/", "unix:/run/board.sock"), "127.0.0.1:7777", "", "does not proxy", false},
		{"localhost proxy to an lan board", serveJSON(name+":443", "/", "http://localhost:7777"), "192.168.1.5:7777", "", "does not proxy", false},
		{"unusable board address", serveJSON(name+":443", "/", "http://127.0.0.1:7777"), "", "", "does not proxy", false},
		{"two handlers", `{"Web":{"` + name + `:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"},"/board":{"Proxy":"http://127.0.0.1:7777"}}}}}`, "127.0.0.1:7777", "", "under /board", false},
		{"root beside another path", `{"Web":{"` + name + `:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7777"},"/docs":{"Proxy":"http://127.0.0.1:3000"}}}}}`, "127.0.0.1:7777", "https://" + name, "", false},
		{"root and a mount", `{"Web":{"` + name + `:443":{"Handlers":{"/b":{"Proxy":"http://127.0.0.1:7777"},"/":{"Proxy":"http://127.0.0.1:7777"}}}}}`, "127.0.0.1:7777", "https://" + name, "", false},
		{"two names", `{"Web":{"b.example:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7777"}}},"a.example:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7777"}}}}}`, "127.0.0.1:7777", "https://a.example", "", false},
		{"empty config", `{}`, "127.0.0.1:7777", "", "does not proxy", false},
		{"not a config", `{"Web":5}`, "127.0.0.1:7777", "", "did not print a serve config", false},
		{"garbage", `<html>`, "127.0.0.1:7777", "", "did not print a serve config", false},
	} {
		link, funnel, problem := board.ServeLink([]byte(c.json), c.addr)
		if link != c.link || funnel != c.funnel || !strings.Contains(problem, c.problem) || (c.problem == "") != (problem == "") {
			t.Errorf("%s: got %q funnel=%v %q, want %q funnel=%v %q", c.what, link, funnel, problem, c.link, c.funnel, c.problem)
		}
	}
}

func TestTailnetStatusCountsOnlineUserPhones(t *testing.T) {
	https, phones := board.TailnetStatus([]byte(`{"CertDomains":["laptop.tailnet.example"],"Peer":{"a":{"OS":"android","Online":true},"b":{"OS":"iOS","Online":true,"Tags":null},"c":{"OS":"android","Online":false},"d":{"OS":"linux","Online":true},"e":{"OS":"android","Online":true,"Tags":["tag:ci"]}}}`))
	if !https || phones != 2 {
		t.Fatalf("https=%v phones=%d", https, phones)
	}
	for _, in := range []string{``, `<html>`, `{"CertDomains":null}`, `{"Peer":5}`} {
		if https, phones := board.TailnetStatus([]byte(in)); https || phones != 0 {
			t.Errorf("%q: https=%v phones=%d", in, https, phones)
		}
	}
}

func phoneWith(t *testing.T, link board.PhoneLink) *phoneRig {
	t.Helper()
	p := newPhoneRig(t)
	p.fx.options.Phone = func(context.Context) board.PhoneLink { return link }
	p.h = p.fx.handler()
	return p
}

func TestThePhonePageSaysWhatTailscaleFoundAndWhatToDoNext(t *testing.T) {
	const name = "https://laptop.tailnet.example"
	steps := []string{"Run <code>tailscale serve --bg 7777</code>", "sudo tailscale set --operator=$USER", "Install Tailscale on the phone"}
	for _, c := range []struct {
		what    string
		link    board.PhoneLink
		has     []string
		lacks   []string
		address string
	}{
		{"ready", board.PhoneLink{Tailscale: true, HTTPS: true, Port: "7777", URL: name, Phones: 1},
			[]string{`data-tone="pass">ready`, `<code class="ref">` + name + `</code> through <code>tailscale serve</code>`, "1 online", "A phone on your tailnet is online.", "A different address typed below is used instead."},
			append([]string{">not yet</span>", ">public</span>", "Funnel", "certificate transparency"}, steps...), name},
		{"ready, no phone online", board.PhoneLink{Tailscale: true, HTTPS: true, Port: "7777", URL: name},
			[]string{">ready</span>", `data-tone="wait">0 online`, "No phone on your tailnet is online."}, steps, name},
		{"not yet, certificates off", board.PhoneLink{Tailscale: true, Port: "7777", Problem: "tailscale serve does not proxy this Board yet"},
			append([]string{`data-tone="wait">not yet`, "tailscale serve does not proxy this Board yet.", "HTTPS Certificates", "certificate transparency logs"}, steps...), []string{">ready</span>", ">public</span>"}, ""},
		{"not yet, certificates on", board.PhoneLink{Tailscale: true, HTTPS: true, Port: "8123", Problem: "tailscale serve does not proxy this Board yet"},
			[]string{">not yet</span>", "Run <code>tailscale serve --bg 8123</code>", "Install Tailscale on the phone"}, []string{"HTTPS Certificates", "certificate transparency", ">ready</span>"}, ""},
		{"public", board.PhoneLink{Tailscale: true, HTTPS: true, Port: "7777", URL: name, Funnel: true},
			[]string{`data-tone="fail">public`, "Tailscale Funnel is on for this Board at <code class=\"ref\">" + name + "</code>", "The Board refuses those requests.", "<code>tailscale funnel reset</code>"},
			[]string{">ready</span>", ">not yet</span>", "online"}, ""},
		{"path mount", board.PhoneLink{Tailscale: true, HTTPS: true, Port: "7777", Problem: "tailscale serve proxies this Board under /board; the Board needs the root"},
			[]string{">not yet</span>", "under /board; the Board needs the root.", "Run <code>tailscale serve --bg 7777</code>"}, []string{">ready</span>", ">public</span>"}, ""},
		{"no tailscale", board.PhoneLink{Port: "7777"},
			[]string{"This Board listens on this computer only.", "an SSH tunnel or WireGuard"}, append([]string{">not yet</span>", ">ready</span>", ">public</span>"}, steps...), ""},
	} {
		body := phoneWith(t, c.link).do("GET", "/connect", nil, laptop, nil).Body.String()
		contains(t, c.what, body, c.has...)
		lacks(t, c.what, body, c.lacks...)
		if got := strings.Contains(body, `value="`+name+`"`); got != (c.address != "") {
			t.Errorf("%s: the address field prefilled = %v", c.what, got)
		}
		if c.address != "" {
			contains(t, c.what, body, `data-detected="`+name+`"`)
		} else {
			contains(t, c.what, body, `data-detected=""`)
		}
	}
}

func TestATypedAddressOverridesTheDetectedOne(t *testing.T) {
	p := phoneWith(t, board.PhoneLink{Tailscale: true, HTTPS: true, Port: "7777", URL: "https://laptop.tailnet.example"})
	rec := p.do("POST", "/connect/code", url.Values{"csrf": {token}, "base": {"https://tunnel.example"}}, laptop, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("connect/code = %d", rec.Code)
	}
	contains(t, "typed address", body, `value="https://tunnel.example"`, `<code class="ref">https://tunnel.example/pair#`)
	lacks(t, "typed address", body, `<code class="ref">https://laptop.tailnet.example/pair#`)
	bad := p.do("POST", "/connect/code", url.Values{"csrf": {token}, "base": {"not an address"}}, laptop, nil).Body.String()
	contains(t, "typed address", bad, `value="not an address"`, `data-detected="https://laptop.tailnet.example"`)
}

func TestAPairedPhoneNeverRunsTheDetection(t *testing.T) {
	p := newPhoneRig(t)
	runs := 0
	p.fx.options.Phone = func(context.Context) board.PhoneLink { runs++; return board.PhoneLink{} }
	p.h = p.fx.handler()
	phone := p.pair()
	before := runs
	body := p.do("GET", "/connect", nil, phone, viaServe).Body.String()
	lacks(t, "paired phone", body, "tailscale serve", "Network")
	if runs != before {
		t.Fatalf("the detection ran %d times for a paired phone", runs-before)
	}
}
