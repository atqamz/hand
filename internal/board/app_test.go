package board_test

import (
	"bytes"
	"context"
	"html"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/sidecar"
)

const (
	appEndpoint = "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6"
	appRelay    = "https://relay.example.test:7842/"
)

var appCodeRe = regexp.MustCompile(`(?:&amp;|&)c=([0-9A-Z]{10})`)

func appRig(t *testing.T, st *sidecar.Status) (*phoneRig, string, func() string) {
	t.Helper()
	p := &phoneRig{t: t, fx: newFixture(t), now: phoneEpoch}
	p.fx.options.Now = func() time.Time {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.now
	}
	f, err := p.fx.st.Fleet(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p.fx.options.Base = "/" + f.ID
	if st != nil {
		p.fx.options.Iroh = func() sidecar.Status { return *st }
	}
	p.h = p.fx.handler()
	return p, f.ID, func() string { return p.do("GET", "/connect", nil, p.laptopAt(f.ID), nil).Body.String() }
}

func (p *phoneRig) laptopAt(id string) *http.Cookie {
	return &http.Cookie{Name: "hand_board_" + id, Value: token}
}

func (p *phoneRig) appCode(id string) (string, string) {
	p.t.Helper()
	rec := p.do("POST", "/connect/app", url.Values{"csrf": {token}}, p.laptopAt(id), nil)
	body := rec.Body.String()
	m := appCodeRe.FindStringSubmatch(body)
	if rec.Code != http.StatusOK || m == nil {
		p.t.Fatalf("connect/app = %d:\n%s", rec.Code, body)
	}
	return body, m[1]
}

func TestTheHandAppLinkIsShownAsTextAndAQRCode(t *testing.T) {
	p, id, _ := appRig(t, &sidecar.Status{State: sidecar.Ready, ID: appEndpoint, Relay: appRelay})
	body, code := p.appCode(id)
	want := "handboard://c#v=1&e=" + appEndpoint + "&f=" + id + "&c=" + code + "&r=https%3A%2F%2Frelay.example.test%3A7842%2F"
	qr, err := board.QRSVG(want, "QR code for the Hand app pairing link")
	if err != nil {
		t.Fatal(err)
	}
	esc := html.EscapeString(want)
	contains(t, "app page", body, string(qr), `<code class="ref">`+esc+`</code>`,
		`data-copy="link" data-text="`+esc+`"`,
		"worth revoking after the phone pairs")
	if rec := p.do("POST", "/connect/app", url.Values{"csrf": {token}}, p.laptopAt(id), nil); rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if len(want) > 213 {
		t.Fatalf("the link is %d bytes", len(want))
	}
}

func TestANewHandAppCodeCancelsTheOneBefore(t *testing.T) {
	p, id, _ := appRig(t, &sidecar.Status{State: sidecar.Ready, ID: appEndpoint, Relay: appRelay})
	_, first := p.appCode(id)
	_, second := p.appCode(id)
	if first == second {
		t.Fatal("a new Hand app code reused the old one")
	}
	if rec := p.tryPairAt(id, first); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "That code is not right") {
		t.Fatalf("the cancelled code = %d:\n%s", rec.Code, rec.Body.String())
	}
	if rec := p.tryPairAt(id, second); rec.Code != http.StatusSeeOther {
		t.Fatalf("the new code = %d:\n%s", rec.Code, rec.Body.String())
	}
	if rec := p.tryPairAt(id, second); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "already used") {
		t.Fatalf("a used code = %d:\n%s", rec.Code, rec.Body.String())
	}
}

func TestAHandAppCodeExpiresAfterFiveMinutes(t *testing.T) {
	p, id, _ := appRig(t, &sidecar.Status{State: sidecar.Ready, ID: appEndpoint, Relay: appRelay})
	_, code := p.appCode(id)
	p.advance(5*time.Minute + time.Second)
	if rec := p.tryPairAt(id, code); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "expired") {
		t.Fatalf("an expired Hand app code = %d:\n%s", rec.Code, rec.Body.String())
	}
}

func (p *phoneRig) tryPairAt(id, code string) *httptest.ResponseRecorder {
	p.t.Helper()
	return p.do("POST", "/pair", url.Values{"code": {code}, "name": {"Pixel"}}, nil, nil)
}

func TestThePhonePageNamesTheDevicePairedOverIroh(t *testing.T) {
	p, _, page := appRig(t, &sidecar.Status{State: sidecar.Ready, ID: appEndpoint, Relay: appRelay})
	if strings.Contains(page(), "paired: Pixel") {
		t.Fatal("the page names a phone before one is paired")
	}
	if _, err := p.fx.st.AddDevice(context.Background(), "Pixel", "iroh:"+appEndpoint, strings.Repeat("a", 64), "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	contains(t, "Phone page", page(), "paired: Pixel")
	if strings.Contains(page(), "&c=") {
		t.Fatal("the Phone page shows a link before one is asked for")
	}
}

func TestTheHandAppLinkNeedsASidecarWithARelay(t *testing.T) {
	for name, st := range map[string]*sidecar.Status{
		"no sidecar": nil,
		"starting":   {State: sidecar.Starting},
		"no relay":   {State: sidecar.Ready, ID: appEndpoint, Relay: "none"},
		"failed":     {State: sidecar.Failed, Reason: "x"},
	} {
		p, id, page := appRig(t, st)
		if strings.Contains(page(), "handboard://") || strings.Contains(page(), "/connect/app") {
			t.Errorf("%s: the page offers a Hand app link", name)
		}
		rec := p.do("POST", "/connect/app", url.Values{"csrf": {token}}, p.laptopAt(id), nil)
		if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "handboard://") {
			t.Errorf("%s: connect/app = %d and shows a link", name, rec.Code)
		}
	}
}

func TestTheHandAppFlowLogsNoCodeOrLink(t *testing.T) {
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	p, id, _ := appRig(t, &sidecar.Status{State: sidecar.Ready, ID: appEndpoint, Relay: appRelay})
	_, code := p.appCode(id)
	p.tryPairAt(id, code)
	p.tryPairAt(id, "AAAAA-AAAAA")
	if got := buf.String(); strings.Contains(got, code) || strings.Contains(got, "handboard://") {
		t.Fatalf("the board logged %q", got)
	}
}
