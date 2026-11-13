package board_test

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/sidecar"
)

const irohEndpoint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func irohRig(t *testing.T, st *sidecar.Status) *phoneRig {
	t.Helper()
	p := &phoneRig{t: t, fx: newFixture(t)}
	if st != nil {
		p.fx.options.Iroh = func() sidecar.Status { return *st }
	}
	p.h = p.fx.handler()
	return p
}

func TestThePhonePageShowsTheSidecarsState(t *testing.T) {
	for name, tc := range map[string]struct {
		st   sidecar.Status
		want []string
	}{
		"ready": {sidecar.Status{State: sidecar.Ready, ID: irohEndpoint, Relay: "https://relay.test./"}, []string{
			`<span class="badge" data-tone="pass">ready</span>`, "Phone access over iroh is online. The Hand app can pair with this host.",
			`Endpoint ID <code class="ref">` + irohEndpoint + `</code>.`}},
		"no relay": {sidecar.Status{State: sidecar.Ready, ID: irohEndpoint, Relay: "none"}, []string{
			`<span class="badge" data-tone="wait">no relay</span>`, "Phone access over iroh is running, but it reached no relay, so a phone elsewhere cannot dial it.",
			`Endpoint ID <code class="ref">` + irohEndpoint + `</code>.`}},
		"starting": {sidecar.Status{State: sidecar.Starting}, []string{
			`<span class="badge" data-tone="wait">starting</span>`, "Phone access over iroh is starting."}},
		"restarting": {sidecar.Status{State: sidecar.Starting, Reason: "exit status 1: cannot open the endpoint"}, []string{
			`data-tone="wait">starting</span>`, "Phone access over iroh is restarting after: exit status 1: cannot open the endpoint."}},
		"failed": {sidecar.Status{State: sidecar.Failed, Reason: "hand-iroh not found: it is not next to hand (/home/me/bin) or on PATH"}, []string{
			`<span class="badge" data-tone="fail">failed</span>`, "Phone access over iroh is hand-iroh not found: it is not next to hand (bin) or on PATH."}},
	} {
		page := irohRig(t, &tc.st).do("GET", "/connect", nil, host, nil).Body.String()
		for _, want := range tc.want {
			if !strings.Contains(page, want) {
				t.Errorf("%s: the Phone page lacks %q", name, want)
			}
		}
		if strings.Contains(page, "/home/me") {
			t.Errorf("%s: the Phone page shows a path", name)
		}
	}
}

func TestThePhonePageSaysNothingAboutIrohWhileItIsOffOrStopped(t *testing.T) {
	for name, st := range map[string]*sidecar.Status{"no sidecar": nil, "stopped": {State: sidecar.Stopped}, "unset": {}} {
		if page := irohRig(t, st).do("GET", "/connect", nil, host, nil).Body.String(); strings.Contains(page, "iroh") {
			t.Errorf("%s: the Phone page mentions iroh", name)
		}
	}
}

func TestTheSidecarsReasonIsEscapedAndShownToTheHostOnly(t *testing.T) {
	st := &sidecar.Status{State: sidecar.Failed, Reason: `<script>alert(1)</script> "quoted"`}
	p := irohRig(t, st)
	page := p.do("GET", "/connect", nil, host, nil).Body.String()
	if strings.Contains(page, "<script>alert(1)") || !strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("the reason is not escaped:\n%s", page)
	}
	cookie := p.pair()
	if page := p.do("GET", "/connect", nil, cookie, viaServe).Body.String(); strings.Contains(page, "iroh") {
		t.Fatalf("a paired phone sees the sidecar's state:\n%s", page)
	}
}
