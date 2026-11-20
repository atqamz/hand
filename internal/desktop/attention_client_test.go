package desktop_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

type hostCall struct {
	path    string
	cookies []string
	csrf    bool
}

type hostBoard struct {
	t        *testing.T
	mu       sync.Mutex
	calls    []hostCall
	tokens   map[string]string
	liar     string
	refuse   map[string]int
	bounce   string
	hangup   string
	redirect bool
	status   int
	body     string
}

func (h *hostBoard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	call := hostCall{path: r.URL.Path, csrf: r.Header.Get("X-Hand-CSRF") != ""}
	for _, c := range r.Cookies() {
		call.cookies = append(call.cookies, c.Name+"="+c.Value)
	}
	h.mu.Lock()
	h.calls = append(h.calls, call)
	h.mu.Unlock()
	if id, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/proof"); ok {
		if code := h.refuse[id]; code != 0 {
			http.Error(w, "refused", code)
			return
		}
		if id == h.bounce {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		if id == h.hangup {
			panic(http.ErrAbortHandler)
		}
		token := h.tokens[id]
		if id == h.liar {
			token = "not the token"
		}
		mac := hmac.New(sha256.New, []byte(token))
		mac.Write([]byte(r.URL.Query().Get("nonce")))
		io.WriteString(w, hex.EncodeToString(mac.Sum(nil)))
		return
	}
	if r.URL.Path != "/api/v1/fleets" {
		http.NotFound(w, r)
		return
	}
	if h.redirect {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
		return
	}
	if h.status != 0 {
		w.WriteHeader(h.status)
		io.WriteString(w, `{"error":{"status":401,"code":"unauthorized","message":"this request needs the board's session cookie"}}`)
		return
	}
	io.WriteString(w, h.body)
}

func (h *hostBoard) snapshot() []hostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]hostCall(nil), h.calls...)
}

func (h *hostBoard) paths() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, c := range h.calls {
		out = append(out, c.path)
	}
	return out
}

const fleetList = `{"version":1,"generated_at":"2026-11-01T05:00:00Z","data":{"fleets":[
{"id":"f00000000e0aa","name":"golden","pill":"working","line":"working","needs":{"waiting":2,"worst":"blocked","worst_word":"blocked","worst_count":1}},
{"id":"f00000000e00d","name":"second","pill":"ready","line":"x","needs":{"waiting":0,"worst_count":0,"summary":"all clear"}},
{"id":"f00000000e0cc","name":"api","unavailable":true},
{"id":"f00000000e0ee","name":"stranger","needs":{"waiting":9,"worst":"blocked","worst_word":"blocked","worst_count":9}}
]}}`

func hostClient(t *testing.T, h *hostBoard, known ...string) *desktop.HTTPClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	h.t = t
	var fleets []desktop.Fleet
	for _, id := range known {
		fleets = append(fleets, desktop.Fleet{ID: id, Name: id})
	}
	return &desktop.HTTPClient{
		Base: srv.URL, Known: fleets, HTTP: srv.Client(),
		Token: func(id string) (string, error) {
			tok, ok := h.tokens[id]
			if !ok {
				return "", errors.New("no token")
			}
			return tok, nil
		},
	}
}

func newHost() *hostBoard {
	return &hostBoard{
		tokens: map[string]string{fleetID: "token-one", secondID: "token-two", "f00000000e0cc": "token-three"},
		body:   fleetList,
	}
}

func TestAttentionProvesEveryFleetThenSendsAllCookiesInOneRead(t *testing.T) {
	h := newHost()
	c := hostClient(t, h, fleetID, secondID, "f00000000e0cc")
	got, err := c.Attention(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/" + fleetID + "/proof", "/" + secondID + "/proof", "/f00000000e0cc/proof", "/api/v1/fleets"}
	if paths := h.paths(); strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Fatalf("requests = %q, want %q", paths, want)
	}
	for _, call := range h.calls[:3] {
		if len(call.cookies) != 0 {
			t.Fatalf("a proof request to %s carried %q", call.path, call.cookies)
		}
	}
	last := h.calls[3]
	if last.csrf {
		t.Fatal("the read sent X-Hand-CSRF")
	}
	wantCookies := "hand_board_" + fleetID + "=token-one hand_board_" + secondID + "=token-two hand_board_f00000000e0cc=token-three"
	if strings.Join(last.cookies, " ") != wantCookies {
		t.Fatalf("cookies = %q, want %q", last.cookies, wantCookies)
	}
	if len(got) != 3 {
		t.Fatalf("signals = %+v, want the three proven fleets and not the stranger", got)
	}
	if s := got[0]; s.Fleet != fleetID || !s.OK || s.Waiting != 2 || s.Worst != "blocked" || s.WorstWord != "blocked" {
		t.Fatalf("first signal = %+v", s)
	}
	if s := got[1]; s.Fleet != secondID || !s.OK || s.Waiting != 0 {
		t.Fatalf("second signal = %+v", s)
	}
	if s := got[2]; s.Fleet != "f00000000e0cc" || s.OK {
		t.Fatalf("an unavailable fleet = %+v, want OK false", s)
	}
}

func TestAttentionSendsNoCookieWhenAnyProofFails(t *testing.T) {
	h := newHost()
	h.liar = secondID
	c := hostClient(t, h, fleetID, secondID)
	if _, err := c.Attention(context.Background()); err == nil {
		t.Fatal("a service that failed one fleet's proof was read")
	}
	for _, call := range h.calls {
		if len(call.cookies) != 0 {
			t.Fatalf("%s carried %q after a failed proof", call.path, call.cookies)
		}
	}
	if paths := h.paths(); len(paths) != 2 || paths[len(paths)-1] != "/"+secondID+"/proof" {
		t.Fatalf("requests = %q, want two proofs and no list read", paths)
	}
}

func TestAttentionSendsNoCookieWhenAProofIsRedirectedOrUnanswered(t *testing.T) {
	for name, set := range map[string]func(h *hostBoard){
		"redirect":  func(h *hostBoard) { h.bounce = secondID },
		"transport": func(h *hostBoard) { h.hangup = secondID },
		"teapot":    func(h *hostBoard) { h.refuse = map[string]int{secondID: http.StatusTeapot} },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost()
			set(h)
			c := hostClient(t, h, fleetID, secondID, "f00000000e0cc")
			if _, err := c.Attention(context.Background()); err == nil {
				t.Fatal("the read went on after a proof that was not a clean refusal")
			}
			for _, call := range h.snapshot() {
				if len(call.cookies) != 0 {
					t.Fatalf("%s carried %q", call.path, call.cookies)
				}
			}
			for _, p := range h.paths() {
				if !strings.HasSuffix(p, "/proof") || strings.Contains(p, "f00000000e0cc") {
					t.Fatalf("requests = %q, want proofs of the first two fleets only", h.paths())
				}
			}
		})
	}
}

func TestAttentionMarksOnlyAFleetTheBoardCannotOpenUnavailable(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			h := newHost()
			h.refuse = map[string]int{secondID: code}
			c := hostClient(t, h, fleetID, secondID, "f00000000e0cc")
			got, err := c.Attention(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			byID := map[string]desktop.Signal{}
			for _, s := range got {
				byID[s.Fleet] = s.Signal
			}
			if len(got) != 3 || byID[secondID].OK || !byID[fleetID].OK || byID[fleetID].Worst != "blocked" || byID[fleetID].Waiting != 2 {
				t.Fatalf("signals = %+v", got)
			}
			last := h.calls[len(h.calls)-1]
			if last.path != "/api/v1/fleets" {
				t.Fatalf("requests = %q, want a list read last", h.paths())
			}
			want := "hand_board_" + fleetID + "=token-one hand_board_f00000000e0cc=token-three"
			if strings.Join(last.cookies, " ") != want {
				t.Fatalf("list cookies = %q, want %q", last.cookies, want)
			}
			for _, call := range h.calls[:len(h.calls)-1] {
				if len(call.cookies) != 0 {
					t.Fatalf("%s carried %q", call.path, call.cookies)
				}
			}
		})
	}
}

func TestAttentionWithEveryFleetRefusedReadsNothingMore(t *testing.T) {
	h := newHost()
	h.refuse = map[string]int{fleetID: http.StatusNotFound, secondID: http.StatusInternalServerError}
	c := hostClient(t, h, fleetID, secondID)
	got, err := c.Attention(context.Background())
	if err != nil || len(got) != 2 || got[0].OK || got[1].OK {
		t.Fatalf("signals = %+v, err = %v", got, err)
	}
	if n := len(h.paths()); n != 2 {
		t.Fatalf("%d requests, want two proofs: %q", n, h.paths())
	}
}

func TestAttentionSkipsAFleetWhoseTokenIsUnreadable(t *testing.T) {
	h := newHost()
	c := hostClient(t, h, fleetID, "f00000000e0bb", secondID)
	got, err := c.Attention(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Fleet != fleetID || got[1].Fleet != secondID {
		t.Fatalf("signals = %+v", got)
	}
	if n := len(h.paths()); n != 3 {
		t.Fatalf("%d requests, want two proofs and one read: %q", n, h.paths())
	}
}

func TestAttentionWithNoTokenAtAllMakesNoRequest(t *testing.T) {
	h := newHost()
	c := hostClient(t, h, "f00000000e0bb")
	if _, err := c.Attention(context.Background()); !errors.Is(err, desktop.ErrNoBoard) {
		t.Fatalf("err = %v, want ErrNoBoard", err)
	}
	if n := len(h.paths()); n != 0 {
		t.Fatalf("%d requests with no fleet to prove", n)
	}
}

func TestAttentionNeverFollowsARedirect(t *testing.T) {
	h := newHost()
	h.redirect = true
	c := hostClient(t, h, fleetID)
	if _, err := c.Attention(context.Background()); err == nil {
		t.Fatal("a redirect was read as an answer")
	}
	if paths := h.paths(); paths[len(paths)-1] != "/api/v1/fleets" {
		t.Fatalf("requests = %q: the redirect was followed", paths)
	}
}

func TestAttentionReportsARefusalAndAForeignVersion(t *testing.T) {
	h := newHost()
	h.status = http.StatusUnauthorized
	c := hostClient(t, h, fleetID)
	_, err := c.Attention(context.Background())
	var api *desktop.APIError
	if !errors.As(err, &api) || api.Status != 401 {
		t.Fatalf("err = %v, want a 401 APIError", err)
	}
	h = newHost()
	h.body = `{"version":2,"data":{"fleets":[]}}`
	c = hostClient(t, h, fleetID)
	if _, err := c.Attention(context.Background()); err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("err = %v, want a version error", err)
	}
}
