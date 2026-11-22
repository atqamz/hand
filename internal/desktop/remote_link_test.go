package desktop_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/sidecar"
)

type refuser interface{ Refusal(string) string }

func TestTheRouterRemembersARefusalPerFleetAndClearsItOnTheNextGoodRead(t *testing.T) {
	g := newRTRig(t)
	host := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, upAt(host, rtClient))
	m := g.connect(t)
	r := m.(refuser)
	ctx := context.Background()
	host.fail = func(w http.ResponseWriter) bool {
		w.Header().Set("X-Hand-Error", "no longer paired")
		w.WriteHeader(http.StatusForbidden)
		return true
	}
	if _, err := m.Status(ctx, rtA); err == nil {
		t.Fatal("no error")
	}
	if got := r.Refusal(rtA); got != desktop.CodeRevoked {
		t.Fatalf("refusal = %q", got)
	}
	host.fail = nil
	if _, err := m.Status(ctx, rtA); err != nil {
		t.Fatal(err)
	}
	if got := r.Refusal(rtA); got != "" {
		t.Fatalf("refusal after a good read = %q", got)
	}
}

func TestAnAttentionReadRecordsARefusalForEveryFleetOfTheHost(t *testing.T) {
	g := newRTRig(t)
	host := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, upAt(host, rtOther))
	m := g.connect(t)
	if _, err := m.Attention(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.(refuser).Refusal(rtA); got != desktop.CodeKeyChanged {
		t.Fatalf("refusal = %q", got)
	}
}

func TestALostLinkReadsAsTheDocumentedSentenceNotATransportError(t *testing.T) {
	for link, want := range map[string]string{
		sidecar.BoardDown:   "Board not running on the host. Start it there.",
		sidecar.Busy:        "Host busy. Trying again.",
		sidecar.Unreachable: "Can't reach host.",
	} {
		g := newRTRig(t)
		host := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
		host.fail = func(http.ResponseWriter) bool { panic(http.ErrAbortHandler) }
		g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
		st := upAt(host, rtClient)
		st.Link = link
		g.clients.set(rtHostA, st)
		m := g.connect(t)
		_, err := m.Status(context.Background(), rtA)
		var api *desktop.APIError
		if !errors.As(err, &api) || !api.Remote || !strings.HasPrefix(api.Message, want) {
			t.Fatalf("%s: err = %v", link, err)
		}
		if strings.Contains(err.Error(), "127.0.0.1") {
			t.Fatalf("%s: the loopback address is in %q", link, err)
		}
		if _, err := m.Attention(context.Background()); err != nil {
			var a *desktop.APIError
			if !errors.As(err, &a) || a.Message != api.Message {
				t.Fatalf("%s: attention err = %v", link, err)
			}
		}
	}
}

func TestAHostThatClosesWithNoLinkStateReadsAsUnreachableWithoutAnAddress(t *testing.T) {
	g := newRTRig(t)
	host := newRTHost(t, "hand_phone_", map[string]string{rtA: rtCookieA}, rtFleetsA)
	host.fail = func(http.ResponseWriter) bool { panic(http.ErrAbortHandler) }
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	st := upAt(host, rtClient)
	st.Link = sidecar.Connected
	g.clients.set(rtHostA, st)
	_, err := g.connect(t).Status(context.Background(), rtA)
	var api *desktop.APIError
	if !errors.As(err, &api) || api.Code != "unreachable" || !api.Remote || !strings.HasPrefix(api.Message, "Can't reach host.") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "localhost") {
		t.Fatalf("the loopback address is in %q", err)
	}
}

func TestAPairedHostsPlain401IsNotRecognisedNeverALocalAuthFailure(t *testing.T) {
	g := newRTRig(t)
	host := newRTHost(t, "hand_phone_", map[string]string{rtA: "other"}, rtFleetsA)
	g.pair(t, rtHostA, rtClient, rtA, rtCookieA)
	g.clients.set(rtHostA, upAt(host, rtClient))
	_, err := g.connect(t).Status(context.Background(), rtA)
	var api *desktop.APIError
	if !errors.As(err, &api) || api.Status != 403 || api.Code != desktop.CodeNotRecognised || !api.Remote {
		t.Fatalf("err = %v", err)
	}
}
