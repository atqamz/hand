package desktop_test

import (
	"context"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

func TestALocalClientFramesAFileWithTheBoardCookieOfItsFleet(t *testing.T) {
	srv, _ := docServer(t)
	c := goldenClient(t, srv.URL)
	f, err := c.DocFrame(context.Background(), fleetID, "reports/my notes#1.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if f.Host != desktop.LocalHost {
		t.Errorf("host = %q, want %q", f.Host, desktop.LocalHost)
	}
	if want := srv.URL + "/" + fleetID + "/api/v1/doc/reports/my%20notes%231.pdf?raw"; f.URL != want {
		t.Errorf("url = %q, want %q", f.URL, want)
	}
	if f.Cookie.Name != "hand_board_"+fleetID || f.Cookie.Value != testToken || f.Cookie.Domain != "127.0.0.1" {
		t.Errorf("cookie = %+v", f.Cookie)
	}
}

func TestAFrameOfAFleetTheClientDoesNotHoldIsAnError(t *testing.T) {
	srv, _ := docServer(t)
	c := goldenClient(t, srv.URL)
	if _, err := c.DocFrame(context.Background(), "f0000000000zz", "a.pdf"); err == nil {
		t.Fatal("framed a file of an unknown fleet")
	}
}

func TestAPairedClientFramesAFileWithItsPhoneCookieAndNoProof(t *testing.T) {
	h := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	c := pairedClient(t, h, rtCookieA)
	c.Host = rtHostA
	f, err := c.DocFrame(context.Background(), rtA, "reports/plan.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if f.Host != rtHostA || f.Cookie.Name != "hand_phone_"+rtA || f.Cookie.Value != rtCookieA {
		t.Errorf("frame = %+v", f)
	}
	if !strings.HasPrefix(f.URL, h.srv.URL+"/"+rtA+"/api/v1/doc/reports/plan.pdf?raw") {
		t.Errorf("url = %q", f.URL)
	}
	for _, call := range h.seen() {
		if strings.HasSuffix(call.path, "/proof") {
			t.Errorf("a paired host was asked for a proof: %s", call.path)
		}
	}
}

func TestTheRouterFramesAFileForTheHostThatHoldsItsFleetAndNoOther(t *testing.T) {
	g := newRTRig(t)
	local := newRTHost(t, "hand_board_", map[string]string{rtLocal: rtToken}, rtFleetsLocal)
	a := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	b := newRTHost(t, "hand_phone_", map[string]string{rtB: rtCookieB}, rtFleetsB)
	g.localFleet(t, rtLocal, local)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.pair(t, rtHostB, rtOther, rtB, rtCookieB)
	g.clients.set(rtHostA, upAt(a, rtClient))
	g.clients.set(rtHostB, upAt(b, rtOther))
	m := g.connect(t)
	fr, ok := m.(desktop.DocFramer)
	if !ok {
		t.Fatal("the router cannot frame a file")
	}
	ctx := context.Background()
	for _, tc := range []struct {
		fleet, host, cookie, value string
		srv                        *rtHost
	}{
		{rtLocal, desktop.LocalHost, "hand_board_" + rtLocal, rtToken, local},
		{rtA, rtHostA, "hand_phone_" + rtA, rtCookieA, a},
		{rtB, rtHostB, "hand_phone_" + rtB, rtCookieB, b},
	} {
		f, err := fr.DocFrame(ctx, tc.fleet, "plan.pdf")
		if err != nil {
			t.Fatalf("%s: %v", tc.fleet, err)
		}
		if f.Host != tc.host || f.Cookie.Name != tc.cookie || f.Cookie.Value != tc.value {
			t.Errorf("%s: frame = %+v, want host %q cookie %s", tc.fleet, f, tc.host, tc.cookie)
		}
		if !strings.HasPrefix(f.URL, "http://"+tc.srv.addr()+"/"+tc.fleet+"/api/v1/doc/plan.pdf") {
			t.Errorf("%s: url = %q, want the address of its own host %s", tc.fleet, f.URL, tc.srv.addr())
		}
		if host, _, _ := strings.Cut(tc.srv.addr(), ":"); f.Cookie.Domain != host {
			t.Errorf("%s: cookie domain = %q, want %q", tc.fleet, f.Cookie.Domain, host)
		}
	}
	if _, err := fr.DocFrame(ctx, "f0000000000zz", "plan.pdf"); err == nil {
		t.Error("framed a file of a fleet no host holds")
	}
}
